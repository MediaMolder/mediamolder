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
	"sync"
	"time"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/graph"
	"golang.org/x/sync/errgroup"
)

// ---------- Sink handler ----------

func (r *graphRunner) handleSink(ctx context.Context, node *graph.Node, ins []<-chan any) error {
	sink := r.sinks[node.ID]
	if sink == nil {
		return fmt.Errorf("sink handler: no resources for node %q", node.ID)
	}

	// Per-channel max-frames limit derived from Output.MaxFramesVideo /
	// MaxFramesAudio and the inbound edge's media type. 0 = unlimited.
	// Once written >= limit, subsequent packets on that channel are
	// drained-and-dropped so upstream encoders/copy nodes never block;
	// the muxer trailer is written when every channel closes naturally.
	limitForChan := func(i int) int {
		if i >= len(node.Inbound) {
			return 0
		}
		switch node.Inbound[i].Type {
		case graph.PortVideo:
			return sink.cfg.MaxFramesVideo
		case graph.PortAudio:
			return sink.cfg.MaxFramesAudio
		}
		return 0
	}

	// ptsToMicros converts pkt PTS (in dstTB units) to AV_TIME_BASE
	// (microseconds) for cross-stream PTS comparison, returning
	// (us, true) on success or (0, false) when the PTS is unset or
	// the time_base is invalid.
	ptsToMicros := func(pts int64, dstTB [2]int) (int64, bool) {
		if pts == math.MinInt64 || dstTB[0] <= 0 || dstTB[1] <= 0 {
			return 0, false
		}
		return pts * 1_000_000 * int64(dstTB[0]) / int64(dstTB[1]), true
	}

	// shiftPTSus converts a microsecond offset into dstTB units and
	// subtracts it from pkt's PTS/DTS. Mirrors of_streamcopy's
	// `pkt->pts -= ts_offset` after rebasing the output to start at 0.
	shiftPTSus := func(pkt *av.Packet, deltaUS int64, dstTB [2]int) {
		if deltaUS == 0 || dstTB[0] <= 0 || dstTB[1] <= 0 {
			return
		}
		off := deltaUS * int64(dstTB[1]) / (1_000_000 * int64(dstTB[0]))
		if off != 0 {
			pkt.ShiftTS(-off)
		}
	}

	// Output-side trim window in AV_TIME_BASE units. NoPTSValue means
	// "no -ss"; noLimitUS means "no -t/-to".
	startUS := sink.timing.startTimestampUS()
	stopUS := sink.timing.stopTimestampUS(sink.copyTS)

	// shiftDownUS is the offset subtracted from kept packets so the
	// muxed file anchors at 0 (mirrors of_streamcopy's ts_offset).
	// Suppressed under -copyts.
	var shiftDownUS int64
	if !sink.copyTS && startUS != int64(av.NoPTSValue) {
		shiftDownUS = startUS
	}

	// recordShortest is called when a per-channel goroutine exits
	// naturally (channel closed). Updates the shared shortestPTSus
	// to min(current, last_pts) so other channels can stop.
	recordShortest := func(lastPTSus int64, ok bool) {
		if !sink.shortest || !ok {
			return
		}
		sink.shortestMu.Lock()
		if lastPTSus < sink.shortestPTSus {
			sink.shortestPTSus = lastPTSus
		}
		sink.shortestMu.Unlock()
	}

	// shortestReached returns true when the shortest cap has been
	// reached for `ptsUS` (only consulted when shortest is true).
	shortestReached := func(ptsUS int64, ok bool) bool {
		if !sink.shortest || !ok {
			return false
		}
		sink.shortestMu.Lock()
		bound := sink.shortestPTSus
		sink.shortestMu.Unlock()
		return bound != noLimitUS && ptsUS >= bound
	}

	// processOne runs the full per-packet pipeline for input channel
	// i: max-frames cap, output-side trim drop / stop, ts shift,
	// rescale, max-file-size cap, shortest cap, then WritePacket.
	// Returns (wrote, stopAll, err) where stopAll signals every
	// channel of this output to drain-and-drop.
	type chanState struct {
		written   int
		lastPTSus int64
		lastPTSok bool
	}
	processOne := func(i int, pkt *av.Packet, dstTB [2]int, rs *sinkRescale, st *chanState, mu *sync.Mutex) (bool, bool, error) {
		// Per-stream max frames (counts post-encoder packets, mirrors
		// FFmpeg's `-frames:v` / `-frames:a`).
		if lim := limitForChan(i); lim > 0 && st.written >= lim {
			return false, false, nil
		}
		pkt.SetStreamIndex(i)
		if rs != nil {
			pkt.Rescale(rs.srcTB, rs.dstTB)
		}
		// Compute pts in AV_TIME_BASE units against the muxer's
		// time_base for trim / shortest comparisons.
		ptsUS, hasPTS := ptsToMicros(pkt.PTS(), dstTB)

		// Output-side `-ss`: drop packets whose PTS is below the
		// configured start. Mirrors of_streamcopy's
		// `if (dts < of->start_time) return EAGAIN`.
		if startUS != int64(av.NoPTSValue) && hasPTS && ptsUS < startUS {
			return false, false, nil
		}

		// Output-side `-t` / `-to`: stop the entire output when any
		// kept packet's PTS reaches the configured end. Mirrors
		// `check_recording_time`'s `av_compare_ts >= 0` => stop.
		if stopUS != noLimitUS && hasPTS && ptsUS >= stopUS {
			return false, true, nil
		}

		// `-shortest`: stop this packet (and everything else on this
		// output) once any other channel has finished and its end
		// PTS is reached. Mirrors the per-output sync-queue cap in
		// fftools/ffmpeg_mux_init.c.
		if shortestReached(ptsUS, hasPTS) {
			return false, false, nil
		}

		// Shift kept packets back by startUS so the file anchors at
		// PTS 0 (suppressed under -copyts).
		shiftPTSus(pkt, shiftDownUS, dstTB)

		// writeOne handles a single muxer-bound packet: max_file_size
		// check, WritePacket, and per-channel bookkeeping. Returns
		// (wrote, stopAll, err).
		writeOne := func(p *av.Packet) (bool, bool, error) {
			frameStart := time.Now()
			var wErr error
			if mu != nil {
				mu.Lock()
			}
			stopAllNow := false
			if sink.maxFileSize > 0 {
				if cur := sink.muxer.BytesWritten(); cur >= 0 && cur >= sink.maxFileSize {
					stopAllNow = true
				}
			}
			if !stopAllNow {
				wErr = sink.muxer.WritePacket(p)
			}
			if mu != nil {
				mu.Unlock()
			}
			if stopAllNow {
				return false, true, nil
			}
			if wErr != nil {
				return false, false, wErr
			}
			st.written++
			if pPTS, hasP := ptsToMicros(p.PTS(), dstTB); hasP {
				st.lastPTSus = pPTS
				st.lastPTSok = true
				ptsNs := time.Duration(p.PTS()) * time.Second *
					time.Duration(dstTB[0]) / time.Duration(dstTB[1])
				r.pipe.Metrics().Node(node.ID).AdvanceOutputPTS(ptsNs)
			}
			r.pipe.Metrics().Node(node.ID).RecordLatency(time.Since(frameStart))
			return true, false, nil
		}

		// BSF chain (if any): drive the input packet through
		// av_bsf_send_packet / av_bsf_receive_packet and call
		// writeOne for each output packet. Mirrors
		// fftools/ffmpeg_mux.c::write_packet's BSF loop.
		var bsf *av.BitstreamFilter
		if i < len(sink.streamBSF) {
			bsf = sink.streamBSF[i]
		}
		if bsf != nil {
			outs, err := bsf.FilterPacket(pkt)
			if err != nil {
				return false, false, fmt.Errorf("bsf filter: %w", err)
			}
			var wroteAny bool
			for _, op := range outs {
				op.SetStreamIndex(i)
				wrote, stopAll, werr := writeOne(op)
				op.Close()
				wroteAny = wroteAny || wrote
				if stopAll || werr != nil {
					return wroteAny, stopAll, werr
				}
			}
			return wroteAny, false, nil
		}

		return writeOne(pkt)
	}

	// flushBSF drains any residual packets buffered inside the BSF
	// chain at end-of-stream by sending a null packet (EOF signal),
	// then writing the drained output packets through the same
	// per-channel write path. Mirrors fftools/ffmpeg_mux.c::mux_thread
	// flushing the BSF before WriteTrailer.
	flushBSF := func(i int, dstTB [2]int, st *chanState, mu *sync.Mutex) error {
		var bsf *av.BitstreamFilter
		if i < len(sink.streamBSF) {
			bsf = sink.streamBSF[i]
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
			if mu != nil {
				mu.Lock()
			}
			werr := sink.muxer.WritePacket(op)
			if mu != nil {
				mu.Unlock()
			}
			op.Close()
			if werr != nil {
				return werr
			}
			st.written++
		}
		return nil
	}

	if len(ins) == 1 {
		var rs *sinkRescale
		if len(sink.streamRescale) > 0 {
			rs = sink.streamRescale[0]
		}
		// Use rs.dstTB (which equals the muxer's pre-WriteHeader
		// stream TB for BSF streams, post-header otherwise) so PTS
		// comparisons stay in the same units packets arrive in
		// after rescale.
		dstTB := sink.muxer.StreamTimeBase(0)
		if rs != nil {
			dstTB = rs.dstTB
		}
		st := &chanState{}
		for v := range ins[0] {
			pkt := v.(*av.Packet)
			if sink.stopAll.Load() {
				pkt.Close()
				continue
			}
			_, stopAll, err := processOne(0, pkt, dstTB, rs, st, nil)
			if stopAll {
				sink.stopAll.Store(true)
			}
			pkt.Close()
			if err != nil {
				return err
			}
		}
		recordShortest(st.lastPTSus, st.lastPTSok)
		if err := flushBSF(0, dstTB, st, nil); err != nil {
			return err
		}
		return sink.muxer.WriteTrailer()
	}

	// Multiple input streams: interleave with per-stream goroutines.
	eg, _ := errgroup.WithContext(ctx)
	var mu sync.Mutex

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
			defer recordShortest(st.lastPTSus, st.lastPTSok)
			for v := range in {
				pkt := v.(*av.Packet)
				if sink.stopAll.Load() {
					pkt.Close()
					continue
				}
				_, stopAll, err := processOne(i, pkt, dstTB, rs, st, &mu)
				if stopAll {
					sink.stopAll.Store(true)
				}
				pkt.Close()
				if err != nil {
					return err
				}
			}
			return flushBSF(i, dstTB, st, &mu)
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
		data, err := os.ReadFile(att.Path)
		if err != nil {
			muxer.Abort()
			for _, b := range streamBSF {
				if b != nil {
					_ = b.Close()
				}
			}
			return nil, fmt.Errorf("sink %q attachments[%d]: read %s: %w", node.ID, ai, att.Path, err)
		}
		filename := att.Filename
		if filename == "" {
			filename = filepath.Base(att.Path)
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

	if err := muxer.WriteHeaderWithOptions(buildMuxerOptions(out)); err != nil {
		muxer.Abort()
		return nil, fmt.Errorf("sink %q write header: %w", node.ID, err)
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
