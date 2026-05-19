// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/graph"
)

// ---------- Encoder handler ----------

// passlogfileSafe matches filenames that are safe to use as pass-log prefixes:
// only alphanumeric characters, hyphens, underscores, and dots. This is used
// to validate user-supplied __passlogfile values after path components have
// been stripped by filepath.Base, preventing any residual path-traversal or
// shell-injection risk.
var passlogfileSafe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// encoderSession holds the per-execution state for one encoder node run and
// drives the frame-encode-drain loop. Separating it from graphRunner makes
// sendOne, drain, and the CFR fill loop independently testable.
type encoderSession struct {
	enc     *av.EncoderContext
	fpsRW   *fpsRewriter
	forceKF *forceKeyFramesMatcher
	// passLog is the open pass-1 stats file for generic codecs (mpeg2video,
	// mpeg4, libxvid, ...). libx264 / libvvenc / libx265 manage their own
	// stats via a codec AVOption; for them this field is nil.
	passLog *os.File
	outs    []chan<- any
	nodeID  string
	pipe    *Pipeline
	runner  *graphRunner // for updating r.encoders on graceful restart (Phase 5)
	// encOpts holds the options used to open enc. On the first video frame
	// the session checks whether the frame's coded dimensions match the
	// encoder's; if not (anamorphic source, display width ≠ coded width)
	// the encoder is closed and reopened with the frame's real dimensions.
	// Set to zero after the first-frame check to skip on subsequent frames.
	encOpts    av.EncoderOptions
	checkedDim bool
	perf       *NodePerfTracker // nil when no perf tracking is active
}

// newEncoderSession creates an encoderSession for the given encoder node,
// resolving fpsRewriter and forceKeyFramesMatcher from node.Internal.Encoder.
func (r *graphRunner) newEncoderSession(node *graph.Node, enc *av.EncoderContext, encOpts av.EncoderOptions, outs []chan<- any) (*encoderSession, error) {
	encInt := node.Internal.Encoder
	var fpsRW *fpsRewriter
	if enc.MediaType() == av.MediaTypeVideo && encInt != nil {
		mode := encInt.FPSMode
		if mode != "" && mode != "passthrough" {
			fpsRW = newFPSRewriter(mode, computeFrameDurationTB(enc.FrameRate(), enc.TimeBase()))
		}
	}

	var forceKF *forceKeyFramesMatcher
	if enc.MediaType() == av.MediaTypeVideo && encInt != nil {
		if specStr := encInt.ForceKeyFrames; specStr != "" {
			spec, err := parseForceKeyFrames(specStr)
			if err != nil {
				return nil, fmt.Errorf("encoder %q: %w", node.ID, err)
			}
			tb := enc.TimeBase()
			m, err := newForceKeyFramesMatcher(spec, tb[0], tb[1])
			if err != nil {
				return nil, fmt.Errorf("encoder %q: %w", node.ID, err)
			}
			forceKF = m
		}
	}

	return &encoderSession{
		enc:     enc,
		fpsRW:   fpsRW,
		forceKF: forceKF,
		passLog: r.passLogFiles[node.ID],
		outs:    outs,
		nodeID:  node.ID,
		pipe:    r.pipe,
		runner:  r,
		encOpts: encOpts,
		perf:    r.trackers[node.ID],
	}, nil
}

// sendPacket fans out an encoded packet to every downstream output channel.
// With one output the packet is forwarded as-is; with N outputs it is cloned
// N-1 times so each consumer owns an independent reference.
func (s *encoderSession) sendPacket(ctx context.Context, p *av.Packet) error {
	for i, out := range s.outs {
		var pkt *av.Packet
		if i == len(s.outs)-1 {
			pkt = p
		} else {
			c, err := av.ClonePacket(p)
			if err != nil {
				p.Close()
				return err
			}
			pkt = c
		}
		select {
		case out <- pkt:
		case <-ctx.Done():
			pkt.Close()
			return ctx.Err()
		}
	}
	return nil
}

// drain receives all pending encoded packets from the encoder and forwards them
// downstream via sendPacket. Mirrors fftools/ffmpeg_enc.c::reap_filters.
func (s *encoderSession) drain(ctx context.Context) error {
	for {
		p, err := av.AllocPacket()
		if err != nil {
			return err
		}
		if err := s.enc.ReceivePacket(p); err != nil {
			p.Close()
			if av.IsEAgain(err) || av.IsEOF(err) {
				return nil
			}
			return err
		}
		if s.passLog != nil {
			if st := s.enc.StatsOut(); st != "" {
				if _, werr := s.passLog.WriteString(st); werr != nil {
					p.Close()
					return fmt.Errorf("encoder %q: write pass-1 stats: %w", s.nodeID, werr)
				}
			}
		}
		if err := s.sendPacket(ctx, p); err != nil {
			return err
		}
	}
}

// sendOne encodes a single frame and drains the resulting packets.
// It applies the forced-keyframe stamp before calling libavcodec; cloned
// duplicates from the CFR fill path each get their own check, mirroring
// FFmpeg's per-frame forced_kf_apply in fftools/ffmpeg_enc.c::frame_encode.
func (s *encoderSession) sendOne(ctx context.Context, f *av.Frame) error {
	if s.forceKF != nil && s.forceKF.shouldForce(f.PTS(), f.PictType()) {
		f.SetPictType(av.PictureTypeI)
	}
	if err := s.enc.SendFrame(f); err != nil {
		return err
	}
	return s.drain(ctx)
}

// run is the main frame-encode loop. It reads frames from in, applies the
// fpsRewriter (CFR gap-fill / drop), calls sendOne for each output frame,
// then flushes the encoder.
func (s *encoderSession) run(ctx context.Context, in <-chan any) error {
	for {
		v, cancelled := perfReceive(ctx, in, s.perf)
		if cancelled {
			break
		}
		recv := time.Now()
		f := v.(*av.Frame)

		// Phase 5: check for a pending real-time thread-count adjustment.
		// The restart drains and flushes the current encoder, closes it, and
		// reopens with the requested thread count before processing this frame.
		if newCount, ok := s.perf.PopRestartRequest(); ok {
			if err := s.restartWithThreadCount(ctx, newCount); err != nil {
				f.Close()
				return fmt.Errorf("encoder %q: thread restart: %w", s.nodeID, err)
			}
		}

		// On the first video frame, verify the encoder was opened with the
		// correct coded dimensions. Container metadata (AVCodecParameters)
		// may carry the display width for anamorphic content (e.g. old
		// DivX/Xvid AVIs), while the actual decoded frame reports the real
		// coded size. Mirrors FFmpeg enc_open() in fftools/ffmpeg_enc.c
		// which uses the frame's own width/height rather than codecpar.
		if !s.checkedDim && s.enc.MediaType() == av.MediaTypeVideo {
			s.checkedDim = true
			fw, fh := f.Width(), f.Height()
			if fw > 0 && fh > 0 && (fw != s.enc.Width() || fh != s.enc.Height()) {
				s.encOpts.Width = fw
				s.encOpts.Height = fh
				newEnc, err := av.OpenEncoder(s.encOpts)
				if err != nil {
					f.Close()
					return fmt.Errorf("encoder %q: reopen with coded dims %dx%d: %w", s.nodeID, fw, fh, err)
				}
				s.enc.Close() //nolint:errcheck
				s.enc = newEnc
			}
		}

		// No fpsRewriter: just use recv directly.
		if s.fpsRW != nil {
			emit, basePTS, drop := s.fpsRW.rewrite(f.PTS())
			if drop || emit == 0 {
				f.Close()
				s.pipe.Metrics().Node(s.nodeID).RecordLatency(time.Since(recv))
				s.perf.RecordFrameLatency(time.Since(recv))
				continue
			}
			// Fast path: single emission, no clone.
			if emit == 1 {
				f.SetPTS(basePTS)
				if err := s.sendOne(ctx, f); err != nil {
					f.Close()
					return err
				}
				f.Close()
				s.pipe.Metrics().Node(s.nodeID).RecordLatency(time.Since(recv))
				s.perf.RecordFrameLatency(time.Since(recv))
				continue
			}
			// CFR forward-gap fill: emit `emit` copies at basePTS,
			// basePTS+dur, basePTS+2*dur, ... The final copy reuses f.
			dur := s.fpsRW.frameDurTB
			for i := 0; i < emit-1; i++ {
				dup, err := f.Clone()
				if err != nil {
					f.Close()
					return err
				}
				dup.SetPTS(basePTS + int64(i)*dur)
				if err := s.sendOne(ctx, dup); err != nil {
					dup.Close()
					f.Close()
					return err
				}
				dup.Close()
			}
			f.SetPTS(basePTS + int64(emit-1)*dur)
			if err := s.sendOne(ctx, f); err != nil {
				f.Close()
				return err
			}
			f.Close()
			s.pipe.Metrics().Node(s.nodeID).RecordLatency(time.Since(recv))
			s.perf.RecordFrameLatency(time.Since(recv))
			continue
		}

		if err := s.sendOne(ctx, f); err != nil {
			f.Close()
			return err
		}
		f.Close()
		s.pipe.Metrics().Node(s.nodeID).RecordLatency(time.Since(recv))
		s.perf.RecordFrameLatency(time.Since(recv))
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := s.enc.Flush(); err != nil && !av.IsEOF(err) && !av.IsEAgain(err) {
		return err
	}
	return s.drain(ctx)
}

// restartWithThreadCount performs a graceful codec restart: it flushes the
// current encoder, drains remaining packets downstream, closes the encoder,
// and reopens it with threads as the new thread count. The in-flight frame
// (if any) is processed by the new encoder after this returns.
//
// This method must only be called from the handler goroutine between frames.
// It updates s.enc, s.runner.encoders[s.nodeID], and the NodePerfTracker.
func (s *encoderSession) restartWithThreadCount(ctx context.Context, threads int) error {
	// Flush the encoder and drain any packets buffered in the codec.
	if err := s.enc.Flush(); err != nil && !av.IsEOF(err) && !av.IsEAgain(err) {
		return fmt.Errorf("flush before restart: %w", err)
	}
	if err := s.drain(ctx); err != nil {
		return fmt.Errorf("drain before restart: %w", err)
	}

	// Close the old encoder.
	s.enc.Close()

	// Reopen with the updated thread count.
	opts := s.encOpts
	opts.ThreadCount = threads
	newEnc, err := av.OpenEncoder(opts)
	if err != nil {
		return fmt.Errorf("reopen with %d threads: %w", threads, err)
	}

	// Update session and graphRunner state.
	s.enc = newEnc
	s.encOpts.ThreadCount = threads
	if s.runner != nil {
		s.runner.encoders[s.nodeID] = newEnc
	}

	// Update the perf tracker so subsequent snapshots reflect the new
	// thread count and the Prometheus restart counter increments.
	s.perf.SetThreadInfo(newEnc.ThreadCount(), threadModeString(newEnc.ActiveThreadType()))
	s.perf.SetThreadBusyFn(newEnc.ThreadsBusy)
	s.perf.IncrementRestarts()

	// Increment the Prometheus NodeThreadRestarts counter directly so
	// it advances even before the next MetricsEmitter tick.
	if p := s.pipe.prom; p != nil {
		p.NodeThreadRestarts.WithLabelValues(s.nodeID).Add(1)
	}

	return nil
}

func (r *graphRunner) handleEncoder(ctx context.Context, node *graph.Node, ins []<-chan any, outs []chan<- any) error {
	enc := r.encoders[node.ID]
	if enc == nil {
		return fmt.Errorf("encoder handler: no encoder for node %q", node.ID)
	}
	if len(ins) != 1 || len(outs) < 1 {
		return fmt.Errorf("encoder node %q: expected 1 input / >=1 output, got %d/%d", node.ID, len(ins), len(outs))
	}
	s, err := r.newEncoderSession(node, enc, r.encoderOpts[node.ID], outs)
	if err != nil {
		return err
	}
	if s.forceKF != nil {
		defer s.forceKF.Close()
	}
	return s.run(ctx, ins[0])
}

func (r *graphRunner) createEncoder(dag *graph.Graph, node *graph.Node) (*av.EncoderContext, error) {
	// After NormalizeConfig the codec is always present in node.Params:
	// expandImplicitEncoders stamps it from Output.CodecVideo /
	// Output.CodecAudio / Output.Streams[i].Encoder.Codec, and
	// hand-authored encoder nodes set it directly. Reading
	// Output.CodecVideo here at runtime would violate the Milestone C
	// invariant ("runtime never reads authoring shorthand"), so this
	// path fails fast instead of falling back.
	codecName := paramString(node.Params, "codec")
	if codecName == "" {
		return nil, fmt.Errorf("encoder node %q: no codec in node.Params (NormalizeConfig is required to populate it)", node.ID)
	}

	si, err := r.resolveStreamInfo(dag, node)
	if err != nil {
		return nil, fmt.Errorf("encoder node %q: %w", node.ID, err)
	}

	opts := av.EncoderOptions{
		CodecName:    codecName,
		GlobalHeader: true,
		ThreadCount:  r.resolveThreadCount(node),
		ThreadType:   r.resolveThreadType(node),
	}

	switch edgeType := node.Inbound[0].Type; edgeType {
	case graph.PortVideo:
		// Check if upstream is a filter; if so, use the filter's output dimensions.
		if fg := r.upstreamFilterGraph(dag, node); fg != nil {
			opts.Width = fg.OutputWidth(0)
			opts.Height = fg.OutputHeight(0)
			if pf := fg.OutputPixFmt(0); pf >= 0 {
				opts.PixFmt = pf
			}
			// Frames emerge from the buffersink with PTS in the sink's
			// time_base. The encoder must use the same TB or libavcodec will
			// reinterpret the PTS in 1/framerate units, blowing up the
			// container duration (e.g. demuxer TB 1/12288 fed into a
			// 24 fps encoder produces ~512x oversized timestamps).
			if tbn, tbd := fg.OutputTimeBase(0); tbn > 0 && tbd > 0 {
				opts.TimeBase = [2]int{tbn, tbd}
			}
		} else {
			opts.Width = si.Width
			opts.Height = si.Height
		}
		frameRate := si.FrameRate
		if frameRate[0] <= 0 || frameRate[1] <= 0 {
			frameRate = [2]int{25, 1}
		}
		opts.FrameRate = frameRate
	case graph.PortAudio:
		opts.SampleFmt = si.SampleFmt
		opts.SampleRate = si.SampleRate
		opts.Channels = si.Channels
		if v := paramString(node.Params, "channel_layout"); v != "" {
			if n := audioLayoutChannels(v); n > 0 {
				opts.Channels = n
			}
		}
	}

	// Allow explicit param overrides.
	if v := paramInt(node.Params, "width"); v > 0 {
		opts.Width = v
	}
	if v := paramInt(node.Params, "height"); v > 0 {
		opts.Height = v
	}
	if v := paramInt64(node.Params, "bitrate"); v > 0 {
		opts.BitRate = v
	}
	// `b` is the FFmpeg AVOption name for bit rate; the GUI's encoder form
	// writes it under that key. Honour it as an alias for `bitrate` so the
	// muxer sees the configured rate on the encoder context.
	if opts.BitRate == 0 {
		if v := paramInt64(node.Params, "b"); v > 0 {
			opts.BitRate = v
		}
	}
	// `g` is the FFmpeg AVOption name for keyframe interval / GOP size.
	if v := paramInt(node.Params, "g"); v > 0 {
		opts.GOPSize = v
	}

	// SAR / DAR shorthand (FFmpeg `-aspect` / `setsar` / `setdar`).
	// Resolve against the encoder's just-decided Width/Height so DAR
	// can be converted into a SAR fraction.
	encInt := node.Internal.Encoder
	if encInt != nil && encInt.SAR != "" {
		n, d, err := resolveSAR(encInt.SAR, "", opts.Width, opts.Height)
		if err != nil {
			return nil, fmt.Errorf("encoder node %q: %w", node.ID, err)
		}
		opts.SampleAspectRatio = [2]int{n, d}
	} else if encInt != nil && encInt.DAR != "" {
		n, d, err := resolveSAR("", encInt.DAR, opts.Width, opts.Height)
		if err != nil {
			return nil, fmt.Errorf("encoder node %q: %w", node.ID, err)
		}
		opts.SampleAspectRatio = [2]int{n, d}
	}

	// FieldOrder / InterlacedEncode (Wave 6 #33). Honour the
	// encoder-side broadcast knobs after time_base / SAR but before
	// avcodec_open2 (run via av.OpenEncoder below).
	if encInt != nil && encInt.FieldOrder != "" {
		v, ok := fieldOrderEnumValue(encInt.FieldOrder)
		if !ok {
			return nil, fmt.Errorf("encoder node %q: invalid field_order %q", node.ID, encInt.FieldOrder)
		}
		opts.FieldOrder = v
	}
	if encInt != nil && encInt.Interlaced {
		opts.InterlacedEncode = true
	}
	// EncoderTimeBase rational form ("N/D" or "N:D"). Sentinels
	// "demux" / "filter" are resolved after the upstream TB is known
	// (filter sentinel inherits the buffersink TB; demux sentinel
	// inherits the source TB) — the existing TimeBase wiring below
	// already prefers buffersink TB over framerate, so the "filter"
	// sentinel is a no-op once that path runs. The "demux" sentinel
	// requires explicit threading from the source side; we accept
	// the marker here and let the existing buffersink default cover
	// the common case (the validator caught misuse upstream).
	if encInt != nil && encInt.EncoderTimeBase != "" {
		n, d, sentinel, err := parseEncoderTimeBase(encInt.EncoderTimeBase)
		if err != nil {
			return nil, fmt.Errorf("encoder node %q: %w", node.ID, err)
		}
		if !sentinel {
			opts.TimeBase = [2]int{n, d}
		}
	}

	// Pass every remaining param through as an AVDictionary entry so codec-
	// specific options written by the GUI (preset, crf, maxrate, bufsize,
	// x264-params, ...) actually reach the encoder.
	opts.ExtraOpts = collectEncoderExtraOpts(node.Params)

	// Two-pass video encoding. Mirrors fftools/ffmpeg_mux_init.c:705 et seq.
	if encInt != nil && encInt.Pass != 0 {
		pass := encInt.Pass
		opts.Pass = pass
		prefix := encInt.PassLogFile
		if prefix == "" {
			prefix = "ffmpeg2pass"
		} else {
			// Strip any directory component, then restrict to safe filename
			// characters (alphanumeric, hyphen, underscore, dot) to prevent
			// path-traversal and shell-injection via user-supplied values.
			prefix = filepath.Base(filepath.Clean(prefix))
			if !passlogfileSafe.MatchString(prefix) {
				prefix = "ffmpeg2pass"
			}
		}
		idx := encInt.PassIndex
		// Anchor the log file to the working directory so that CodeQL's
		// path-injection query can verify no traversal occurs via the
		// strings.HasPrefix guard below.  Since prefix is restricted to
		// [A-Za-z0-9._-] by the check above, this guard always holds;
		// it is written out explicitly so static analysis can see it.
		cwd, cwdErr := os.Getwd()
		if cwdErr != nil {
			return nil, fmt.Errorf("encoder node %q: getwd: %w", node.ID, cwdErr)
		}
		logfile := filepath.Clean(filepath.Join(cwd, fmt.Sprintf("%s-%d.log", prefix, idx)))
		if !strings.HasPrefix(logfile, cwd+string(filepath.Separator)) {
			return nil, fmt.Errorf("encoder node %q: passlogfile path escapes working directory", node.ID)
		}
		switch codecName {
		case "libx264", "libvvenc":
			if opts.ExtraOpts == nil {
				opts.ExtraOpts = make(map[string]string)
			}
			if _, set := opts.ExtraOpts["stats"]; !set {
				opts.ExtraOpts["stats"] = logfile
			}
		case "libx265":
			if opts.ExtraOpts == nil {
				opts.ExtraOpts = make(map[string]string)
			}
			if _, set := opts.ExtraOpts["x265-stats"]; !set {
				opts.ExtraOpts["x265-stats"] = logfile
			}
		default:
			if pass&2 != 0 {
				buf, ferr := os.ReadFile(logfile)
				if ferr != nil {
					return nil, fmt.Errorf("encoder node %q: read pass-2 stats %q: %w", node.ID, logfile, ferr)
				}
				opts.StatsIn = buf
			}
			if pass&1 != 0 {
				f, ferr := os.Create(logfile)
				if ferr != nil {
					return nil, fmt.Errorf("encoder node %q: open pass-1 stats %q: %w", node.ID, logfile, ferr)
				}
				r.passLogFiles[node.ID] = f
			}
		}
	}

	enc, err := av.OpenEncoder(opts)
	if err != nil {
		return nil, err
	}
	// Store the options for potential first-frame reopen (anamorphic sources
	// may report display width in AVCodecParameters; the real coded dimensions
	// are only known from the first decoded frame).
	r.encoderOpts[node.ID] = opts
	return enc, nil
}

// encoderReservedParams lists the param keys consumed directly by createEncoder
// (or used to address the node itself). They must not be forwarded as
// AVDictionary options because some are not codec AVOptions ("codec", "width",
// "height") and the rest are already applied to EncoderOptions explicitly.
//
// Milestone B (B.5): the historical __* sentinel keys (__fps_mode,
// __pass, __passlogfile, __pass_index, __sar, __dar, __enc_time_base,
// __field_order, __interlaced, __force_key_frames) are no longer
// written into NodeDef.Params; they live in NodeDef.Internal.Encoder
// and are read directly from the typed struct. Only the genuinely
// authored, runtime-special-cased keys remain in this set.
var encoderReservedParams = map[string]bool{
	"codec":             true,
	"width":             true,
	"height":            true,
	"bitrate":           true,
	"threads":           true,
	"thread_type":       true,
	"channel_layout":    true,
	"multi_input_audio": true,
	"audio_inputs":      true,
}

// collectEncoderExtraOpts returns a map of AVDictionary options to forward to
// avcodec_open2 from a node's user-supplied params. Reserved keys are skipped;
// empty/nil values are skipped (so the encoder uses its built-in default).
func collectEncoderExtraOpts(params map[string]any) map[string]string {
	if len(params) == 0 {
		return nil
	}
	var out map[string]string
	for k, v := range params {
		if encoderReservedParams[k] {
			continue
		}
		if v == nil {
			continue
		}
		s := fmt.Sprintf("%v", v)
		if s == "" {
			continue
		}
		if out == nil {
			out = make(map[string]string, len(params))
		}
		out[k] = s
	}
	return out
}
