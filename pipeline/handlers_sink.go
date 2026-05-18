// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/graph"
	"golang.org/x/sync/errgroup"
)

// ---------- Sink handler ----------

// chanState tracks per-channel muxing bookkeeping inside handleSink.
type chanState struct {
	written   int
	lastPTSus int64
	lastPTSok bool
}

// ptsToMicros converts a PTS value (in tb units) to AV_TIME_BASE microseconds,
// returning (us, true) on success or (0, false) when the PTS is unset or the
// time_base is invalid. Mirrors the av_rescale_q(pts, tb, AV_TIME_BASE_Q) calls
// scattered throughout fftools for cross-stream PTS comparison.
func ptsToMicros(pts int64, tb [2]int) (int64, bool) {
	if pts == math.MinInt64 || tb[0] <= 0 || tb[1] <= 0 {
		return 0, false
	}
	return pts * 1_000_000 * int64(tb[0]) / int64(tb[1]), true
}

// shiftPTSus converts deltaUS microseconds into tb units and subtracts it from
// pkt's PTS/DTS. Mirrors of_streamcopy's `pkt->pts -= ts_offset` after
// rebasing the output to start at PTS 0.
func shiftPTSus(pkt *av.Packet, deltaUS int64, tb [2]int) {
	if deltaUS == 0 || tb[0] <= 0 || tb[1] <= 0 {
		return
	}
	off := deltaUS * int64(tb[1]) / (1_000_000 * int64(tb[0]))
	if off != 0 {
		pkt.ShiftTS(-off)
	}
}

// sinkWriter carries the per-output muxing state and implements the per-packet
// and per-channel write operations for handleSink. Separating this type from
// graphRunner makes processOne and writeOne independently testable.
type sinkWriter struct {
	sink    *sinkResources
	node    *graph.Node
	startUS int64       // output-side -ss in AV_TIME_BASE units; av.NoPTSValue = no trim
	stopUS  int64       // output-side -t/-to; noLimitUS = no trim
	shiftUS int64       // subtracted from kept packets so output anchors at PTS 0
	mu      *sync.Mutex // nil for single-stream path; shared across goroutines otherwise
	pipe    *Pipeline
}

// limitForChan returns the max-frames cap for input channel i (0 = unlimited).
func (w *sinkWriter) limitForChan(i int) int {
	if i >= len(w.node.Inbound) {
		return 0
	}
	switch w.node.Inbound[i].Type {
	case graph.PortVideo:
		return w.sink.cfg.MaxFramesVideo
	case graph.PortAudio:
		return w.sink.cfg.MaxFramesAudio
	}
	return 0
}

// recordShortest updates the shared shortestPTSus to min(current, lastPTSus)
// when the -shortest flag is active. Called on natural channel close.
func (w *sinkWriter) recordShortest(lastPTSus int64, ok bool) {
	if !w.sink.shortest || !ok {
		return
	}
	w.sink.shortestMu.Lock()
	if lastPTSus < w.sink.shortestPTSus {
		w.sink.shortestPTSus = lastPTSus
	}
	w.sink.shortestMu.Unlock()
}

// shortestReached returns true when another channel has already closed and the
// -shortest PTS cap has been reached for ptsUS.
func (w *sinkWriter) shortestReached(ptsUS int64, ok bool) bool {
	if !w.sink.shortest || !ok {
		return false
	}
	w.sink.shortestMu.Lock()
	bound := w.sink.shortestPTSus
	w.sink.shortestMu.Unlock()
	return bound != noLimitUS && ptsUS >= bound
}

// writeOne handles a single muxer-bound packet: max_file_size check,
// WritePacket under w.mu (when non-nil), and per-channel bookkeeping.
// Returns (wrote, stopAll, err).
func (w *sinkWriter) writeOne(pkt *av.Packet, i int, dstTB [2]int, st *chanState) (bool, bool, error) {
	frameStart := time.Now()
	if w.mu != nil {
		w.mu.Lock()
	}
	stopAllNow := false
	if w.sink.maxFileSize > 0 {
		if cur := w.sink.muxer.BytesWritten(); cur >= 0 && cur >= w.sink.maxFileSize {
			stopAllNow = true
		}
	}
	var wErr error
	if !stopAllNow {
		wErr = w.sink.muxer.WritePacket(pkt)
	}
	if w.mu != nil {
		w.mu.Unlock()
	}
	if stopAllNow {
		return false, true, nil
	}
	if wErr != nil {
		return false, false, wErr
	}
	st.written++
	if pPTS, hasP := ptsToMicros(pkt.PTS(), dstTB); hasP {
		st.lastPTSus = pPTS
		st.lastPTSok = true
		ptsNs := time.Duration(pkt.PTS()) * time.Second *
			time.Duration(dstTB[0]) / time.Duration(dstTB[1])
		// Track per-stream so a fast AAC encoder doesn't push the
		// node's reported OutputPTS to 100% while libx265 still has
		// minutes left.
		w.pipe.Metrics().Node(w.node.ID).AdvanceOutputPTSStream(i, ptsNs)
	}
	w.pipe.Metrics().Node(w.node.ID).RecordLatency(time.Since(frameStart))
	return true, false, nil
}

// processOne runs the full per-packet pipeline for input channel i:
// max-frames cap, output-side trim drop/stop, ts shift, rescale,
// shortest cap, BSF chain, then writeOne.
// Returns (wrote, stopAll, err); stopAll signals every channel to drain-and-drop.
func (w *sinkWriter) processOne(i int, pkt *av.Packet, dstTB [2]int, rs *sinkRescale, st *chanState) (bool, bool, error) {
	// Per-stream max frames (mirrors FFmpeg's `-frames:v` / `-frames:a`).
	if lim := w.limitForChan(i); lim > 0 && st.written >= lim {
		return false, false, nil
	}
	// Attachment data is carried in codecpar->extradata and written by
	// WriteHeader; the muxer ignores per-packet writes for attachment
	// streams. Drop the packet here so WritePacket is never called.
	if i < len(w.node.Inbound) && w.node.Inbound[i].Type == graph.PortAttachment {
		return false, false, nil
	}
	pkt.SetStreamIndex(i)
	if rs != nil {
		pkt.Rescale(rs.srcTB, rs.dstTB)
	}
	ptsUS, hasPTS := ptsToMicros(pkt.PTS(), dstTB)

	// Output-side `-ss`: drop packets below the configured start.
	// Mirrors of_streamcopy's `if (dts < of->start_time) return EAGAIN`.
	if w.startUS != int64(av.NoPTSValue) && hasPTS && ptsUS < w.startUS {
		return false, false, nil
	}
	// Output-side `-t`/`-to`: stop when any kept packet reaches the end.
	// Mirrors `check_recording_time`'s `av_compare_ts >= 0` => stop.
	if w.stopUS != noLimitUS && hasPTS && ptsUS >= w.stopUS {
		return false, true, nil
	}
	// `-shortest`: stop once another channel has closed at a lower PTS.
	if w.shortestReached(ptsUS, hasPTS) {
		return false, false, nil
	}

	// Shift kept packets so the output file anchors at PTS 0
	// (suppressed under -copyts).
	shiftPTSus(pkt, w.shiftUS, dstTB)

	// BSF chain: drive through av_bsf_send_packet / av_bsf_receive_packet
	// and call writeOne for each output packet. Mirrors
	// fftools/ffmpeg_mux.c::write_packet's BSF loop.
	var bsf *av.BitstreamFilter
	if i < len(w.sink.streamBSF) {
		bsf = w.sink.streamBSF[i]
	}
	if bsf != nil {
		outs, err := bsf.FilterPacket(pkt)
		if err != nil {
			return false, false, fmt.Errorf("bsf filter: %w", err)
		}
		var wroteAny bool
		for _, op := range outs {
			op.SetStreamIndex(i)
			wrote, stopAll, werr := w.writeOne(op, i, dstTB, st)
			op.Close()
			wroteAny = wroteAny || wrote
			if stopAll || werr != nil {
				return wroteAny, stopAll, werr
			}
		}
		return wroteAny, false, nil
	}
	return w.writeOne(pkt, i, dstTB, st)
}

// flushBSF drains residual packets buffered inside the BSF chain at
// end-of-stream by sending a null packet (EOF signal), then writing the
// drained output through WritePacket. Mirrors fftools/ffmpeg_mux.c::mux_thread
// flushing the BSF before WriteTrailer.
func (w *sinkWriter) flushBSF(i int, dstTB [2]int, st *chanState) error {
	var bsf *av.BitstreamFilter
	if i < len(w.sink.streamBSF) {
		bsf = w.sink.streamBSF[i]
	}
	if bsf == nil {
		return nil
	}
	outs, err := bsf.Flush()
	if err != nil {
		return fmt.Errorf("bsf flush: %w", err)
	}
	for _, op := range outs {
		op.SetStreamIndex(i)
		if w.mu != nil {
			w.mu.Lock()
		}
		werr := w.sink.muxer.WritePacket(op)
		if w.mu != nil {
			w.mu.Unlock()
		}
		op.Close()
		if werr != nil {
			return werr
		}
		st.written++
	}
	return nil
}

func (r *graphRunner) handleSink(ctx context.Context, node *graph.Node, ins []<-chan any) error {
	sink := r.sinks[node.ID]
	if sink == nil {
		return fmt.Errorf("sink handler: no resources for node %q", node.ID)
	}

	startUS := sink.timing.startTimestampUS()
	stopUS := sink.timing.stopTimestampUS(sink.copyTS)
	var shiftDownUS int64
	if !sink.copyTS && startUS != int64(av.NoPTSValue) {
		shiftDownUS = startUS
	}

	w := &sinkWriter{
		sink:    sink,
		node:    node,
		startUS: startUS,
		stopUS:  stopUS,
		shiftUS: shiftDownUS,
		pipe:    r.pipe,
	}

	if len(ins) == 1 {
		var rs *sinkRescale
		if len(sink.streamRescale) > 0 {
			rs = sink.streamRescale[0]
		}
		// Use rs.dstTB (pre-WriteHeader for BSF streams, post-header otherwise)
		// so PTS comparisons stay in the same units packets arrive in after rescale.
		dstTB := sink.muxer.StreamTimeBase(0)
		if rs != nil {
			dstTB = rs.dstTB
		}
		st := &chanState{}
		t := perfTrackerFrom(ctx)
		for {
			v, cancelled := perfReceive(ctx, ins[0], t)
			if cancelled {
				break
			}
			pkt := v.(*av.Packet)
			if sink.stopAll.Load() {
				pkt.Close()
				continue
			}
			_, stopAll, err := w.processOne(0, pkt, dstTB, rs, st)
			if stopAll {
				sink.stopAll.Store(true)
			}
			pkt.Close()
			if err != nil {
				return err
			}
		}
		w.recordShortest(st.lastPTSus, st.lastPTSok)
		if err := w.flushBSF(0, dstTB, st); err != nil {
			return err
		}
		return sink.muxer.WriteTrailer()
	}

	// Multiple input streams: interleave with per-stream goroutines.
	eg, _ := errgroup.WithContext(ctx)
	var mu sync.Mutex
	w.mu = &mu

	for i, in := range ins {
		i, in := i, in
		var rs *sinkRescale
		if i < len(sink.streamRescale) {
			rs = sink.streamRescale[i]
		}
		dstTB := sink.muxer.StreamTimeBase(i)
		if rs != nil {
			dstTB = rs.dstTB
		}
		st := &chanState{}
		eg.Go(func() error {
			defer w.recordShortest(st.lastPTSus, st.lastPTSok)
			for v := range in {
				pkt := v.(*av.Packet)
				if sink.stopAll.Load() {
					pkt.Close()
					continue
				}
				_, stopAll, err := w.processOne(i, pkt, dstTB, rs, st)
				if stopAll {
					sink.stopAll.Store(true)
				}
				pkt.Close()
				if err != nil {
					return err
				}
			}
			return w.flushBSF(i, dstTB, st)
		})
	}

	if err := eg.Wait(); err != nil {
		return err
	}
	return sink.muxer.WriteTrailer()
}

func (r *graphRunner) openSink(_ *graph.Graph, node *graph.Node) (*sinkResources, error) {
	out := r.findOutputConfig(node.ID)
	if out == nil {
		return nil, fmt.Errorf("sink node %q: no matching output config", node.ID)
	}

	// Per-output timing flags (FFmpeg's output-side `-ss` / `-t` /
	// `-to`). Stripped from the AVDict before any libav consumer sees
	// them; enforced by handleSink against per-packet PTS, mirroring
	// `fftools/ffmpeg_mux.c::of_streamcopy`.
	outTiming, err := resolveOutputTiming(out.Options, func(format string, args ...any) {
		log.Printf("output %q: "+format, append([]any{out.URL}, args...)...)
	})
	if err != nil {
		return nil, fmt.Errorf("output %q timing: %w", out.URL, err)
	}

	var muxer *av.OutputFormatContext
	switch out.Kind {
	case "tee":
		slavesURL, terr := buildTeeSlavesURL(out.Targets)
		if terr != nil {
			return nil, fmt.Errorf("output %q: %w", out.ID, terr)
		}
		m, oerr := av.OpenTeeOutput(slavesURL)
		if oerr != nil {
			return nil, fmt.Errorf("open tee output %q: %w", out.ID, oerr)
		}
		muxer = m
	default:
		m, oerr := av.OpenOutputWithFormat(out.URL, out.Format)
		if oerr != nil {
			return nil, fmt.Errorf("open output %q: %w", out.URL, oerr)
		}
		muxer = m
	}

	rescales := make([]*sinkRescale, len(node.Inbound))

	// streamsByType records, for each media-type letter ("v"/"a"/"s"/
	// "d"), the absolute output stream indices of the streams of that
	// type in the order they were added to the muxer. This is the same
	// counting convention FFmpeg's `check_stream_specifier` uses for
	// `s:<type>:<idx>`, so `Output.Streams[k] = {Type:"a", Index:1}`
	// resolves to the second audio stream of this output. Built up
	// inside the AddStream loop below.
	streamsByType := map[string][]int{}
	typeLetterFor := func(t graph.PortType) string {
		switch t {
		case "video":
			return "v"
		case "audio":
			return "a"
		case "subtitle":
			return "s"
		case "data":
			return "d"
		}
		return ""
	}

	// codecTagFor returns the configured FourCC codec_tag override for
	// the given edge's stream kind, or "" if none is configured.
	codecTagFor := func(t graph.PortType) string {
		switch t {
		case "video":
			return out.CodecTagVideo
		case "audio":
			return out.CodecTagAudio
		case "subtitle":
			return out.CodecTagSubtitle
		}
		return ""
	}

	// Add one stream per inbound edge. Encoder predecessors register
	// the stream from the encoder context; stream-copy predecessors
	// copy the input codecpar directly so the muxer never sees an
	// encoder for that stream. Topological order guarantees both kinds
	// of predecessor are already prepared.
	for i, e := range node.Inbound {
		from := e.From
		var outIdx int
		switch from.Kind {
		case graph.KindEncoder:
			enc := r.encoders[from.ID]
			if enc == nil {
				muxer.Abort()
				return nil, fmt.Errorf("sink %q: inbound from %q has no encoder", node.ID, from.ID)
			}
			idx, err := muxer.AddStream(enc)
			if err != nil {
				muxer.Abort()
				return nil, fmt.Errorf("sink %q add stream: %w", node.ID, err)
			}
			outIdx = idx
			// Capture the encoder's time_base. AddStream copies it onto
			// the output stream, but some muxers (notably MP4) overwrite
			// the stream's time_base in WriteHeader, leaving encoder
			// packets (whose PTS is in encoder TB) misinterpreted by the
			// muxer in the new TB. We rescale per-packet to compensate.
			rescales[i] = &sinkRescale{srcTB: enc.TimeBase(), dstTB: muxer.StreamTimeBase(outIdx)}
		case graph.KindCopy:
			srcInput, srcIdx, srcTB, err := r.copySourceFor(from)
			if err != nil {
				muxer.Abort()
				return nil, fmt.Errorf("sink %q copy from %q: %w", node.ID, from.ID, err)
			}
			idx, err := muxer.AddStreamFromInput(srcInput, srcIdx)
			if err != nil {
				muxer.Abort()
				return nil, fmt.Errorf("sink %q add copy stream: %w", node.ID, err)
			}
			outIdx = idx
			rescales[i] = &sinkRescale{srcTB: srcTB, dstTB: muxer.StreamTimeBase(outIdx)}
		default:
			muxer.Abort()
			return nil, fmt.Errorf("sink %q: inbound from %q (kind=%v) is not an encoder or copy node", node.ID, from.ID, from.Kind)
		}

		// Apply optional codec_tag override (e.g. -tag:v hvc1).
		if tag := codecTagFor(e.Type); tag != "" {
			if err := muxer.SetStreamCodecTag(outIdx, tag); err != nil {
				muxer.Abort()
				return nil, fmt.Errorf("sink %q set codec_tag for %s stream: %w", node.ID, e.Type, err)
			}
		}

		// Record this stream's absolute index under its media-type
		// letter for FFmpeg-style `s:<type>:<idx>` resolution below.
		if letter := typeLetterFor(e.Type); letter != "" {
			streamsByType[letter] = append(streamsByType[letter], outIdx)
		}
	}

	// Apply per-stream metadata + disposition (`Output.Streams`).
	// Mirrors FFmpeg's `-metadata:s:<type>:<idx>` and
	// `-disposition:s:<type>:<idx>`. Resolution counts streams of the
	// requested media type in muxer-add order, so a job with one video
	// + two audio streams resolves `{Type:"a", Index:1}` to the
	// second audio AVStream regardless of the order video / audio
	// edges were declared on the sink.
	for j, ss := range out.Streams {
		idxs, ok := streamsByType[ss.Type]
		if !ok || ss.Index < 0 || ss.Index >= len(idxs) {
			muxer.Abort()
			return nil, fmt.Errorf("sink %q streams[%d]: no stream matches %s:%d (have %d %s stream(s))",
				node.ID, j, ss.Type, ss.Index, len(idxs), ss.Type)
		}
		streamIdx := idxs[ss.Index]
		if len(ss.Metadata) > 0 {
			if err := muxer.SetStreamMetadata(streamIdx, ss.Metadata); err != nil {
				muxer.Abort()
				return nil, fmt.Errorf("sink %q streams[%d] set metadata: %w", node.ID, j, err)
			}
		}
		if ss.Disposition != "" {
			if err := muxer.SetStreamDisposition(streamIdx, ss.Disposition); err != nil {
				muxer.Abort()
				return nil, fmt.Errorf("sink %q streams[%d] set disposition: %w", node.ID, j, err)
			}
		}
	}

	// Per-stream bitstream-filter chains (Output.BSFVideo /
	// BSFAudio / BSFSubtitle, FFmpeg `-bsf:v` / `-bsf:a` / `-bsf:s`).
	// Each value is the FFmpeg chain spec `f1[=k=v[:k=v]][,f2]` parsed
	// by libavcodec's av_bsf_list_parse_str. Attached after stream
	// creation and before WriteHeader so the chain's par_out replaces
	// the muxer's stream codecpar — exactly the order
	// fftools/ffmpeg_mux.c::bsf_init follows. Stored per inbound
	// channel for processOne to drive packets through.
	bsfSpecFor := func(t graph.PortType) string {
		switch t {
		case graph.PortVideo:
			return out.BSFVideo
		case graph.PortAudio:
			return out.BSFAudio
		case graph.PortSubtitle:
			return out.BSFSubtitle
		}
		return ""
	}
	streamBSF := make([]*av.BitstreamFilter, len(node.Inbound))
	for i, e := range node.Inbound {
		spec := bsfSpecFor(e.Type)
		if spec == "" {
			continue
		}
		bsf, err := muxer.AttachStreamBSF(i, spec)
		if err != nil {
			for _, b := range streamBSF {
				if b != nil {
					_ = b.Close()
				}
			}
			muxer.Abort()
			return nil, fmt.Errorf("sink %q attach %s bsf chain %q: %w", node.ID, e.Type, spec, err)
		}
		streamBSF[i] = bsf
	}

	// Container metadata + chapters. Resolution rules:
	//   - Output.Metadata, when non-nil, fully replaces any
	//     metadata mapped from inputs (mirrors FFmpeg's behaviour
	//     when both `-metadata` and `-map_metadata` are given:
	//     `-metadata` is applied last and wins).
	//   - When Output.Metadata is nil, every input with
	//     MapMetadata=true contributes its container-level
	//     metadata in declaration order; the last writer wins
	//     per key.
	//   - Chapters follow the same precedence: an explicit
	//     Output.Chapters wins; otherwise the first input with
	//     MapChapters=true contributes its chapter table.
	if err := r.applyOutputMetadata(muxer, out); err != nil {
		muxer.Abort()
		return nil, fmt.Errorf("sink %q apply metadata: %w", node.ID, err)
	}
	if err := r.applyOutputChapters(muxer, out); err != nil {
		muxer.Abort()
		return nil, fmt.Errorf("sink %q apply chapters: %w", node.ID, err)
	}

	// Per-stream color metadata + HDR10 (Output.Color / Output.HDR,
	// FFmpeg `-color_*` / `-mastering_display_metadata` /
	// `-content_light_level`). Applied to every inbound video edge's
	// stream codecpar (and codecpar.coded_side_data for HDR side
	// data) before WriteHeader so the muxer writes the corresponding
	// container boxes / SEI passthrough. Audio + subtitle edges are
	// skipped — color metadata is meaningless for them.
	if out.Color != nil || out.HDR != nil {
		for i, e := range node.Inbound {
			if e.Type != graph.PortVideo {
				continue
			}
			if out.Color != nil {
				if err := muxer.SetStreamColor(i, av.ColorParams{
					Range:          out.Color.Range,
					Primaries:      out.Color.Primaries,
					Transfer:       out.Color.Transfer,
					Space:          out.Color.Space,
					ChromaLocation: out.Color.ChromaLocation,
				}); err != nil {
					for _, b := range streamBSF {
						if b != nil {
							_ = b.Close()
						}
					}
					muxer.Abort()
					return nil, fmt.Errorf("sink %q set color: %w", node.ID, err)
				}
			}
			if out.HDR != nil {
				if md := out.HDR.MasteringDisplay; md != nil {
					hasPrim := md.DisplayPrimariesRX != 0 || md.WhitePointX != 0
					hasLum := md.MaxLuminance != 0
					if hasPrim || hasLum {
						if err := muxer.SetStreamMasteringDisplay(i, av.MasteringDisplay{
							HasPrimaries: hasPrim,
							DisplayPrim: [6]int{
								md.DisplayPrimariesRX, md.DisplayPrimariesRY,
								md.DisplayPrimariesGX, md.DisplayPrimariesGY,
								md.DisplayPrimariesBX, md.DisplayPrimariesBY,
							},
							WhitePoint:   [2]int{md.WhitePointX, md.WhitePointY},
							HasLuminance: hasLum,
							MinLuminance: md.MinLuminance,
							MaxLuminance: md.MaxLuminance,
						}); err != nil {
							for _, b := range streamBSF {
								if b != nil {
									_ = b.Close()
								}
							}
							muxer.Abort()
							return nil, fmt.Errorf("sink %q set mastering display: %w", node.ID, err)
						}
					}
				}
				if cll := out.HDR.ContentLightLevel; cll != nil && (cll.MaxCLL != 0 || cll.MaxFALL != 0) {
					if err := muxer.SetStreamContentLightLevel(i, av.ContentLightLevel{
						MaxCLL:  cll.MaxCLL,
						MaxFALL: cll.MaxFALL,
					}); err != nil {
						for _, b := range streamBSF {
							if b != nil {
								_ = b.Close()
							}
						}
						muxer.Abort()
						return nil, fmt.Errorf("sink %q set content light level: %w", node.ID, err)
					}
				}
				if dv := out.HDR.DoVi; dv != nil && dv.Profile != 0 {
					rpu := true
					if dv.RPUPresent != nil {
						rpu = *dv.RPUPresent
					}
					bl := true
					if dv.BLPresent != nil {
						bl = *dv.BLPresent
					}
					if err := muxer.SetStreamDoViConfig(i, av.DoViConfig{
						Profile:           dv.Profile,
						Level:             dv.Level,
						RPUPresent:        rpu,
						ELPresent:         dv.ELPresent,
						BLPresent:         bl,
						BLCompatibilityID: dv.BLCompatibilityID,
					}); err != nil {
						for _, b := range streamBSF {
							if b != nil {
								_ = b.Close()
							}
						}
						muxer.Abort()
						return nil, fmt.Errorf("sink %q set dovi config: %w", node.ID, err)
					}
				}
			}
		}
	}

	// Wave 6 #31: muxed file attachments (matroska / mkv / webm only).
	// Files are read once and copied into the new stream's
	// codecpar->extradata. Mirrors fftools/ffmpeg_mux_init.c
	// of_add_attachments.
	for ai, att := range out.Attachments {
		cleanPath := filepath.Clean(att.Path)
		if strings.Contains(cleanPath, "..") {
			muxer.Abort()
			for _, b := range streamBSF {
				if b != nil {
					_ = b.Close()
				}
			}
			return nil, fmt.Errorf("sink %q attachments[%d]: path traversal in %q", node.ID, ai, att.Path)
		}
		data, err := os.ReadFile(cleanPath)
		if err != nil {
			muxer.Abort()
			for _, b := range streamBSF {
				if b != nil {
					_ = b.Close()
				}
			}
			return nil, fmt.Errorf("sink %q attachments[%d]: read %s: %w", node.ID, ai, cleanPath, err)
		}
		filename := att.Filename
		if filename == "" {
			filename = filepath.Base(cleanPath)
		}
		if _, err := muxer.AddAttachment(data, filename, att.MimeType); err != nil {
			muxer.Abort()
			for _, b := range streamBSF {
				if b != nil {
					_ = b.Close()
				}
			}
			return nil, fmt.Errorf("sink %q attachments[%d]: %w", node.ID, ai, err)
		}
	}

	// Wave 11 #64: cover art embedded as AV_DISPOSITION_ATTACHED_PIC.
	// The stream must be added before WriteHeader; the returned packet is
	// written immediately after WriteHeader below.
	var coverPkt *av.Packet
	if out.CoverArt != "" {
		cleanCA := filepath.Clean(out.CoverArt)
		if strings.Contains(cleanCA, "..") {
			muxer.Abort()
			for _, b := range streamBSF {
				if b != nil {
					_ = b.Close()
				}
			}
			return nil, fmt.Errorf("sink %q cover_art: path traversal in %q", node.ID, out.CoverArt)
		}
		_, pkt, err := muxer.AddCoverArt(cleanCA)
		if err != nil {
			muxer.Abort()
			for _, b := range streamBSF {
				if b != nil {
					_ = b.Close()
				}
			}
			return nil, fmt.Errorf("sink %q cover_art: %w", node.ID, err)
		}
		coverPkt = pkt
	}

	if err := muxer.WriteHeaderWithOptions(buildMuxerOptions(out)); err != nil {
		muxer.Abort()
		return nil, fmt.Errorf("sink %q write header: %w", node.ID, err)
	}

	// Wave 11 #64: write the cover art packet immediately after WriteHeader.
	// The stream was registered above; the single-frame packet must be
	// written before regular content so interleaved muxers see it early.
	if coverPkt != nil {
		if err := muxer.WritePacket(coverPkt); err != nil {
			_ = coverPkt.Close()
			muxer.Abort()
			for _, b := range streamBSF {
				if b != nil {
					_ = b.Close()
				}
			}
			return nil, fmt.Errorf("sink %q write cover art: %w", node.ID, err)
		}
		_ = coverPkt.Close()
	}

	// Some muxers adjust stream time_base in WriteHeader; refresh the
	// post-header value so per-packet rescaling targets the actual
	// container timebase. BSF-attached streams keep the pre-header
	// rescale target (= bsf.time_base_in), since the BSF chain was
	// initialised against that TB and av_interleaved_write_frame
	// rescales the BSF's output PTS to the post-header stream TB.
	for i, rs := range rescales {
		if rs == nil {
			continue
		}
		if i < len(streamBSF) && streamBSF[i] != nil {
			continue
		}
		rs.dstTB = muxer.StreamTimeBase(i)
	}

	return &sinkResources{
		muxer:         muxer,
		cfg:           *out,
		streamRescale: rescales,
		streamBSF:     streamBSF,
		timing:        outTiming,
		copyTS:        r.cfg != nil && r.cfg.CopyTS,
		maxFileSize:   out.MaxFileSize,
		shortest:      out.Shortest,
		shortestPTSus: noLimitUS,
	}, nil
}

// copySourceFor resolves a stream-copy node back to the input it reads
// from, returning the InputFormatContext, the demuxer stream index, and
// the input stream's time_base. The copy node is required to have
// exactly one inbound edge from a KindSource.
