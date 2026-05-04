// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/graph"
)

// ---------- Encoder handler ----------

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
}

// newEncoderSession creates an encoderSession for the given encoder node,
// resolving fpsRewriter and forceKeyFramesMatcher from node.Params.
func (r *graphRunner) newEncoderSession(node *graph.Node, enc *av.EncoderContext, outs []chan<- any) (*encoderSession, error) {
	var fpsRW *fpsRewriter
	if enc.MediaType() == av.MediaTypeVideo {
		mode := paramString(node.Params, "__fps_mode")
		if mode != "" && mode != "passthrough" {
			fpsRW = newFPSRewriter(mode, computeFrameDurationTB(enc.FrameRate(), enc.TimeBase()))
		}
	}

	var forceKF *forceKeyFramesMatcher
	if enc.MediaType() == av.MediaTypeVideo {
		if specStr := paramString(node.Params, "__force_key_frames"); specStr != "" {
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
	for v := range in {
		f := v.(*av.Frame)
		frameStart := time.Now()

		if s.fpsRW != nil {
			emit, basePTS, drop := s.fpsRW.rewrite(f.PTS())
			if drop || emit == 0 {
				f.Close()
				s.pipe.Metrics().Node(s.nodeID).RecordLatency(time.Since(frameStart))
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
				s.pipe.Metrics().Node(s.nodeID).RecordLatency(time.Since(frameStart))
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
			s.pipe.Metrics().Node(s.nodeID).RecordLatency(time.Since(frameStart))
			continue
		}

		if err := s.sendOne(ctx, f); err != nil {
			f.Close()
			return err
		}
		f.Close()
		s.pipe.Metrics().Node(s.nodeID).RecordLatency(time.Since(frameStart))
	}

	if err := s.enc.Flush(); err != nil && !av.IsEOF(err) && !av.IsEAgain(err) {
		return err
	}
	return s.drain(ctx)
}

func (r *graphRunner) handleEncoder(ctx context.Context, node *graph.Node, ins []<-chan any, outs []chan<- any) error {
	enc := r.encoders[node.ID]
	if enc == nil {
		return fmt.Errorf("encoder handler: no encoder for node %q", node.ID)
	}
	if len(ins) != 1 || len(outs) < 1 {
		return fmt.Errorf("encoder node %q: expected 1 input / >=1 output, got %d/%d", node.ID, len(ins), len(outs))
	}
	s, err := r.newEncoderSession(node, enc, outs)
	if err != nil {
		return err
	}
	if s.forceKF != nil {
		defer s.forceKF.Close()
	}
	return s.run(ctx, ins[0])
}

func (r *graphRunner) createEncoder(dag *graph.Graph, node *graph.Node) (*av.EncoderContext, error) {
	// Determine codec: first from node params, then from downstream output config.
	codecName := paramString(node.Params, "codec")
	if codecName == "" {
		for _, e := range node.Outbound {
			if e.To.Kind == graph.KindSink {
				out := r.findOutputConfig(e.To.ID)
				if out != nil {
					switch e.Type {
					case graph.PortVideo:
						codecName = out.CodecVideo
					case graph.PortAudio:
						codecName = out.CodecAudio
					}
				}
			}
		}
	}
	if codecName == "" {
		return nil, fmt.Errorf("encoder node %q: no codec specified", node.ID)
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
	if sar := paramString(node.Params, "__sar"); sar != "" {
		n, d, err := resolveSAR(sar, "", opts.Width, opts.Height)
		if err != nil {
			return nil, fmt.Errorf("encoder node %q: %w", node.ID, err)
		}
		opts.SampleAspectRatio = [2]int{n, d}
	} else if dar := paramString(node.Params, "__dar"); dar != "" {
		n, d, err := resolveSAR("", dar, opts.Width, opts.Height)
		if err != nil {
			return nil, fmt.Errorf("encoder node %q: %w", node.ID, err)
		}
		opts.SampleAspectRatio = [2]int{n, d}
	}

	// FieldOrder / InterlacedEncode (Wave 6 #33). Honour the
	// encoder-side broadcast knobs after time_base / SAR but before
	// avcodec_open2 (run via av.OpenEncoder below).
	if fo := paramString(node.Params, "__field_order"); fo != "" {
		v, ok := fieldOrderEnumValue(fo)
		if !ok {
			return nil, fmt.Errorf("encoder node %q: invalid field_order %q", node.ID, fo)
		}
		opts.FieldOrder = v
	}
	if paramString(node.Params, "__interlaced") == "1" {
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
	if etb := paramString(node.Params, "__enc_time_base"); etb != "" {
		n, d, sentinel, err := parseEncoderTimeBase(etb)
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
	if pass := paramInt(node.Params, "__pass"); pass != 0 {
		opts.Pass = pass
		prefix := paramString(node.Params, "__passlogfile")
		if prefix == "" {
			prefix = "ffmpeg2pass"
		}
		idx := paramInt(node.Params, "__pass_index")
		logfile := fmt.Sprintf("%s-%d.log", prefix, idx)
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

	return av.OpenEncoder(opts)
}

// encoderReservedParams lists the param keys consumed directly by createEncoder
// (or used to address the node itself). They must not be forwarded as
// AVDictionary options because some are not codec AVOptions ("codec", "width",
// "height") and the rest are already applied to EncoderOptions explicitly.
var encoderReservedParams = map[string]bool{
	"codec":       true,
	"width":       true,
	"height":      true,
	"bitrate":     true,
	"threads":     true,
	"thread_type": true,
	// `__fps_mode` is consumed by handleEncoder's per-frame renumberer,
	// not by libavcodec. Keep it out of the AVDictionary forwarded to
	// avcodec_open2 so the encoder doesn't reject the unknown option.
	"__fps_mode": true,
	// `__pass`, `__passlogfile`, `__pass_index` are consumed by
	// createEncoder to drive two-pass video encoding. They never
	// reach avcodec_open2 directly \u2014 createEncoder either sets the
	// codec-specific stats AVOption (libx264 / libvvenc / libx265)
	// or wires AVCodecContext.stats_in / opens a log file for
	// stats_out (generic codecs).
	"__pass":          true,
	"__passlogfile":   true,
	"__pass_index":    true,
	"__sar":           true,
	"__dar":           true,
	"__enc_time_base": true,
	"__field_order":   true,
	"__interlaced":    true,
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
