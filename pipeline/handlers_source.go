// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/graph"
)

// ---------- Source handler ----------

func (r *graphRunner) handleSource(ctx context.Context, node *graph.Node, outs []chan<- any) error {
	src := r.sources[node.ID]
	if src == nil {
		return fmt.Errorf("source handler: no resources for node %q", node.ID)
	}
	// Publish the input's known duration once so the GUI can compute
	// percent-complete / ETA. Stays 0 for live or unknown-duration
	// inputs; the GUI hides the progress bar in that case and shows
	// only elapsed-time and processed-media-time.
	r.pipe.Metrics().Node(node.ID).SetMediaDuration(src.mediaDuration)

	// Build per-stream-index routing maps so that each outbound edge
	// receives only the frames/packets from the specific source stream
	// it requested (e.g. "a:6" → decoder for the 7th audio stream
	// only). Using a single type-keyed bucket is wrong when two edges
	// request different tracks from the same source: both would be
	// sent the same *av.Frame pointer, the first consumer would free
	// it, and the second would read freed memory.
	//
	// streamIdxToFrameChans[absIdx] = outbound channel indices that
	//   want decoded frames from the stream at position absIdx.
	// streamIdxToCopyChans[absIdx] = outbound channel indices that
	//   want raw packets forwarded from the stream at position absIdx.
	//
	// Build a (MediaType, trackPos) → absStreamIdx map first by
	// sorting selected stream indices per type; the Nth index in
	// ascending order is track N.
	typeStreamsSorted := make(map[av.MediaType][]int)
	for idx, si := range src.streams {
		typeStreamsSorted[si.Type] = append(typeStreamsSorted[si.Type], idx)
	}
	for t := range typeStreamsSorted {
		sort.Ints(typeStreamsSorted[t])
	}
	type mtTrack struct {
		mt    av.MediaType
		track int
	}
	trackToStreamIdx := make(map[mtTrack]int)
	for mt, indices := range typeStreamsSorted {
		for trackPos, streamIdx := range indices {
			trackToStreamIdx[mtTrack{mt, trackPos}] = streamIdx
		}
	}
	// portEdgeTrack extracts the 0-based track number from a FromPort
	// key.  "a:6" → 6, "v:0" → 0, "default" or bare type letter → 0.
	portEdgeTrack := func(fromPort string) int {
		if parts := strings.SplitN(fromPort, ":", 2); len(parts) == 2 {
			if n, err := strconv.Atoi(parts[1]); err == nil {
				return n
			}
		}
		return 0
	}
	portTypeToMT := func(pt graph.PortType) av.MediaType {
		switch pt {
		case graph.PortVideo:
			return av.MediaTypeVideo
		case graph.PortAudio:
			return av.MediaTypeAudio
		case graph.PortSubtitle:
			return av.MediaTypeSubtitle
		case graph.PortData:
			return av.MediaTypeData
		case graph.PortAttachment:
			return av.MediaTypeAttachment
		default:
			return av.MediaTypeUnknown
		}
	}
	streamIdxToFrameChans := make(map[int][]int)
	streamIdxToCopyChans := make(map[int][]int)
	for i, e := range node.Outbound {
		mt := portTypeToMT(e.Type)
		if mt == av.MediaTypeUnknown {
			continue
		}
		trackN := portEdgeTrack(e.FromPort)
		streamIdx, ok := trackToStreamIdx[mtTrack{mt, trackN}]
		if !ok {
			continue
		}
		if e.To != nil && e.To.Kind == graph.KindCopy {
			streamIdxToCopyChans[streamIdx] = append(streamIdxToCopyChans[streamIdx], i)
		} else {
			streamIdxToFrameChans[streamIdx] = append(streamIdxToFrameChans[streamIdx], i)
		}
	}

	t := perfTrackerFrom(ctx)

	// sendFrame delivers f to each listed output channel. When more
	// than one channel is listed (multiple consumers of the same
	// stream) the frame is cloned for all but the last recipient so
	// each consumer owns an independent reference.
	sendFrame := func(f *av.Frame, indices []int) error {
		// Phase 5 frame-drop: discard the frame when the real-time control
		// loop has enabled drop mode on this source to shed pipeline load.
		// This is a last-resort measure; it is always logged at warning level.
		if t.ShouldDrop() {
			f.Close()
			return nil
		}
		for i, idx := range indices {
			toSend := f
			if i < len(indices)-1 {
				// Not the last recipient — clone so the
				// earlier consumer can safely close its copy.
				var err error
				toSend, err = f.Clone()
				if err != nil {
					return err
				}
			}
			if perfSend(ctx, outs[idx], toSend, t) {
				if toSend != f {
					toSend.Close()
				}
				return ctx.Err()
			}
		}
		t.RecordFrame()
		return nil
	}

	// sendPacketCopies clones the demuxer packet once per copy-out and
	// forwards each clone. Cloning is necessary because the source
	// reuses a single AVPacket across the demux loop (Unref before
	// each ReadPacket); without a clone the downstream copy node would
	// see freed buffers.
	sendPacketCopies := func(pkt *av.Packet, indices []int) error {
		for _, idx := range indices {
			c, err := av.ClonePacket(pkt)
			if err != nil {
				return err
			}
			if perfSend(ctx, outs[idx], c, t) {
				c.Close()
				return ctx.Err()
			}
		}
		return nil
	}

	// rescaleAudioPTS converts an audio frame's pts from the input stream's
	// time_base to (1, sample_rate) units. The downstream audio filter
	// graph (abuffer source) is configured at (1, sample_rate) granularity
	// to keep sample-accurate timing through filters like asetnsamples.
	// Many container/codec combinations (e.g. MP3 in AVI, where the stream
	// time_base is 1/(sample_rate/1152)) deliver decoded frames whose pts
	// would otherwise be misinterpreted as one sample apart.
	rescaleAudioPTS := func(f *av.Frame, si av.StreamInfo) {
		pts := f.PTS()
		if pts == math.MinInt64 || si.TimeBase[1] <= 0 || si.SampleRate <= 0 {
			return
		}
		// new_pts = pts * tb_num * sample_rate / tb_den
		// Use big-int-style ordering to minimise overflow risk.
		f.SetPTS(pts * int64(si.TimeBase[0]) * int64(si.SampleRate) / int64(si.TimeBase[1]))
	}

	receiveAll := func(dec av.FrameDecoder, si av.StreamInfo) error {
		indices := streamIdxToFrameChans[si.Index]
		if len(indices) == 0 {
			return nil
		}
		for {
			f, err := av.AllocFrame()
			if err != nil {
				return err
			}
			if err := dec.ReceiveFrame(f); err != nil {
				f.Close()
				if av.IsEAgain(err) || av.IsEOF(err) {
					return nil
				}
				return err
			}
			if si.Type == av.MediaTypeAudio {
				rescaleAudioPTS(f, si)
			}
			if err := sendFrame(f, indices); err != nil {
				f.Close()
				return err
			}
		}
	}

	// Demux + decode loop.
	pkt, err := av.AllocPacket()
	if err != nil {
		return err
	}
	defer pkt.Close()

	// Per-loop-iteration min/max packet PTS (in AV_TIME_BASE
	// microseconds) — used by the `-stream_loop` rewind path to
	// compute the cycle's media duration so post-rewind packets can
	// be PTS-shifted by the right amount. Mirrors `Demuxer.min_pts`
	// / `Demuxer.max_pts` in fftools/ffmpeg_demux.c::ts_fixup.
	// Reset at every successful seek_to_start.
	var iterMinPTSus, iterMaxPTSus int64 = math.MaxInt64, math.MinInt64

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		pkt.Unref()
		frameStart := time.Now()
		if err := src.input.ReadPacket(pkt); err != nil {
			if av.IsEOF(err) {
				// `-stream_loop` semantics. Mirrors
				// fftools/ffmpeg_demux.c::seek_to_start:
				// rewind, accumulate the cycle's media
				// duration into loopOffsetUS, decrement the
				// remaining-loops counter (unless it is -1,
				// which means infinite), and continue
				// reading. On seek failure we fall through
				// to the normal EOF break (matches FFmpeg's
				// behaviour: any failure inside seek_to_start
				// terminates the input).
				if src.streamLoopRemaining != 0 && iterMaxPTSus > math.MinInt64 {
					seekTo := src.input.StartTime()
					if seekTo == av.NoPTSValue {
						seekTo = 0
					}
					if serr := src.input.SeekFile(seekTo); serr == nil {
						minPTSus := iterMinPTSus
						if minPTSus == math.MaxInt64 {
							minPTSus = 0
						}
						src.loopOffsetUS += iterMaxPTSus - minPTSus
						iterMinPTSus = math.MaxInt64
						iterMaxPTSus = math.MinInt64
						if src.streamLoopRemaining > 0 {
							src.streamLoopRemaining--
						}
						continue
					}
				}
				break
			}
			return err
		}
		dec := src.decoders[pkt.StreamIndex()]
		subDec := src.subDecoders[pkt.StreamIndex()]
		si, known := src.streams[pkt.StreamIndex()]
		if dec == nil && subDec == nil && !known {
			continue
		}

		// Honour per-input -t / -to: stop demuxing once any selected
		// stream's packet PTS reaches the absolute stop point computed
		// from the input's recording_time. Mirrors
		// fftools/ffmpeg_demux.c::input_packet_process()'s `dts >=
		// recording_time + start_time` check (with FFmpeg's default
		// non-copy_ts start_time of 0; we apply the seek timestamp via
		// stopPTSus instead of via per-packet ts_offset). Skips packets
		// without a valid PTS so we don't bail out on a probe-only
		// header. The check runs against the raw PTS (before ts_offset)
		// so the comparison stays in source coordinates.
		if src.stopPTSus != noLimitUS {
			if pts := pkt.PTS(); pts != math.MinInt64 && si.TimeBase[1] > 0 {
				// Convert pkt PTS (in stream time_base units) to
				// AV_TIME_BASE units (microseconds).
				ptsUS := pts * 1_000_000 * int64(si.TimeBase[0]) / int64(si.TimeBase[1])
				if ptsUS >= src.stopPTSus {
					break
				}
			}
		}

		// Apply ts_offset: rebase every packet's PTS/DTS so the
		// pipeline starts at 0 even when -ss seeked into the middle of
		// the source. Mirrors fftools/ffmpeg_demux.c::ts_fixup() in
		// non-copy_ts mode. Rescales the AV_TIME_BASE-unit ts_offset
		// into the packet's stream time_base before adding.
		if src.tsOffsetUS != 0 && si.TimeBase[1] > 0 {
			offsetTB := src.tsOffsetUS * int64(si.TimeBase[1]) /
				(1_000_000 * int64(si.TimeBase[0]))
			pkt.ShiftTS(offsetTB)
		}

		// Apply the per-iteration loop offset for `-stream_loop`.
		// loopOffsetUS is the cumulative duration (in AV_TIME_BASE
		// microseconds) of all completed iterations; adding it
		// keeps post-rewind PTS monotone. Mirrors `pkt->pts +=
		// duration` in fftools/ffmpeg_demux.c::ts_fixup. Tracked
		// separately from tsOffsetUS so the cycle-duration
		// arithmetic doesn't entangle with the `-ss` /
		// `-itsoffset` shift.
		if src.loopOffsetUS != 0 && si.TimeBase[1] > 0 {
			loopTB := src.loopOffsetUS * int64(si.TimeBase[1]) /
				(1_000_000 * int64(si.TimeBase[0]))
			pkt.ShiftTS(loopTB)
		}

		// Track per-iteration min/max packet PTS in AV_TIME_BASE
		// microseconds so the loop rewind path (above) can compute
		// `max - min` as the cycle's media duration. Done in
		// post-shift coordinates, exactly as
		// fftools/ffmpeg_demux.c::ts_fixup updates `Demuxer.min_pts`
		// / `Demuxer.max_pts` after the offset additions.
		if src.streamLoopRemaining != 0 {
			if pts := pkt.PTS(); pts != math.MinInt64 && si.TimeBase[1] > 0 {
				ptsUSshifted := pts * 1_000_000 * int64(si.TimeBase[0]) / int64(si.TimeBase[1])
				if ptsUSshifted < iterMinPTSus {
					iterMinPTSus = ptsUSshifted
				}
				dur := pkt.Duration()
				endUS := ptsUSshifted
				if dur > 0 {
					endUS += dur * 1_000_000 * int64(si.TimeBase[0]) / int64(si.TimeBase[1])
				}
				if endUS > iterMaxPTSus {
					iterMaxPTSus = endUS
				}
			}
		}

		// Pace the demux loop when the user requested
		// `-readrate` / `-re`. Done after PTS shifts so the
		// pacer compares wallclock-elapsed against the
		// post-offset packet PTS — matches
		// fftools/ffmpeg_demux.c::readrate_sleep, which uses
		// `ds->dts` *after* `ts_fixup` has finished.
		if src.pacer != nil {
			if pts := pkt.PTS(); pts != math.MinInt64 && si.TimeBase[1] > 0 {
				ptsUS := pts * 1_000_000 * int64(si.TimeBase[0]) / int64(si.TimeBase[1])
				src.pacer.maybeSleep(ctx, ptsUS, pkt.StreamIndex())
			}
		}

		// Publish media-time progress so the GUI can compute
		// percent-complete / ETA. Skip packets without a valid PTS
		// (AV_NOPTS_VALUE == math.MinInt64) and streams without a
		// known timebase.
		if pts := pkt.PTS(); pts != math.MinInt64 && si.TimeBase[1] > 0 {
			ptsNs := time.Duration(pts) * time.Second *
				time.Duration(si.TimeBase[0]) / time.Duration(si.TimeBase[1])
			r.pipe.Metrics().Node(node.ID).AdvanceMediaPTS(ptsNs)
		}

		// Route to copy outs (per stream index).
		if copyChans := streamIdxToCopyChans[si.Index]; len(copyChans) > 0 {
			if err := sendPacketCopies(pkt, copyChans); err != nil {
				return err
			}
		}

		// Handle subtitle streams via subtitle decoder.
		if subDec != nil {
			if subChans := streamIdxToFrameChans[si.Index]; len(subChans) > 0 {
				sub, got, err := subDec.Decode(pkt)
				if err != nil {
					return err
				}
				if got {
					for _, idx := range subChans {
						if perfSend(ctx, outs[idx], sub, t) {
							sub.Close()
							return ctx.Err()
						}
					}
				}
				r.pipe.Metrics().Node(node.ID).RecordLatency(time.Since(frameStart))
				continue
			}
		}

		if dec == nil {
			// Copy-only or data-only stream: nothing to decode.
			r.pipe.Metrics().Node(node.ID).RecordLatency(time.Since(frameStart))
			continue
		}
		// If no downstream node consumes decoded frames of this
		// stream, skip the decoder entirely. Otherwise packets pile
		// up in the decoder's internal queue until SendPacket
		// returns EAGAIN and the source aborts with averror(-35).
		// (Copy consumers were already serviced above.)
		if len(streamIdxToFrameChans[si.Index]) == 0 {
			r.pipe.Metrics().Node(node.ID).RecordLatency(time.Since(frameStart))
			continue
		}
		if err := dec.SendPacket(pkt); err != nil {
			return err
		}
		if err := receiveAll(dec, si); err != nil {
			return err
		}
		r.pipe.Metrics().Node(node.ID).RecordLatency(time.Since(frameStart))
	}

	// Flush every decoder that has at least one downstream frame
	// consumer (per-stream routing, not per-type).
	for idx, dec := range src.decoders {
		si := src.streams[idx]
		chans := streamIdxToFrameChans[si.Index]
		if len(chans) == 0 {
			continue
		}
		if err := dec.Flush(); err != nil && !av.IsEOF(err) && !av.IsEAgain(err) {
			return err
		}
		// Drain remaining decoded frames.
		for {
			f, err := av.AllocFrame()
			if err != nil {
				return err
			}
			if err := dec.ReceiveFrame(f); err != nil {
				f.Close()
				if av.IsEOF(err) || av.IsEAgain(err) {
					break
				}
				return err
			}
			if si.Type == av.MediaTypeAudio {
				rescaleAudioPTS(f, si)
			}
			if err := sendFrame(f, chans); err != nil {
				f.Close()
				return err
			}
		}
	}
	return nil
}

// ---------- Copy handler ----------
//
// A copy node is a verbatim demuxer-packet-to-muxer pipeline: it neither
// decodes nor encodes. The source emits raw AVPackets in the input
// stream's time_base; the sink rescales to the output stream's time_base
// at write time. handleCopy is therefore a thin passthrough; it exists as
// a distinct kind so the graph can express stream-copy intent and so the
// muxer can be configured via AddStreamFromInput rather than from an
// encoder context.
func (r *graphRunner) handleCopy(ctx context.Context, node *graph.Node, ins []<-chan any, outs []chan<- any) error {
	if len(ins) != 1 || len(outs) < 1 {
		return fmt.Errorf("copy node %q: expected 1 input / >=1 output, got %d/%d", node.ID, len(ins), len(outs))
	}
	in := ins[0]
	for v := range in {
		pkt, ok := v.(*av.Packet)
		if !ok {
			return fmt.Errorf("copy node %q: expected *av.Packet, got %T", node.ID, v)
		}
		frameStart := time.Now()
		// Fan out to each downstream channel (clone for all but the last
		// so each muxer owns an independent ref and can rescale/set the
		// stream index without racing the others).
		var sendErr error
		for i, out := range outs {
			var p *av.Packet
			if i == len(outs)-1 {
				p = pkt
			} else {
				c, err := av.ClonePacket(pkt)
				if err != nil {
					pkt.Close()
					sendErr = err
					break
				}
				p = c
			}
			select {
			case out <- p:
			case <-ctx.Done():
				p.Close()
				sendErr = ctx.Err()
			}
			if sendErr != nil {
				break
			}
		}
		if sendErr != nil {
			return sendErr
		}
		r.pipe.Metrics().Node(node.ID).RecordLatency(time.Since(frameStart))
	}
	return nil
}

// ---------- Resource pre-opening ----------

// isHwSurfaceFmtName reports whether name is an explicit hardware-surface
// pixel format — frames should remain on the GPU after decode (for a
// zero-copy HW encoder pipeline). An empty name is NOT a hw surface;
// it means "use the default", which is hw→sw transfer (mirrors FFmpeg's
// -hwaccel default behaviour where frames are always moved to system RAM
// unless an explicit hw surface format is requested). (Wave 10 #59)
func isHwSurfaceFmtName(name string) bool {
	switch strings.ToLower(name) {
	case "cuda", "vaapi", "qsv", "videotoolbox", "d3d11va", "dxva2", "opencl", "vulkan":
		return true
	}
	return false
}

// isSwPixFmtName reports whether name is a software (system-RAM) pixel format.
func isSwPixFmtName(name string) bool {
	return name != "" && !isHwSurfaceFmtName(name)
}

// isDeviceFormat reports whether the libavformat demuxer name refers to a
// live capture device. Device inputs do not support seeking — attempting
// avformat_seek_file on them returns an error or blocks indefinitely.
// The set mirrors FFmpeg's built-in device demuxers on all supported platforms.
func isDeviceFormat(name string) bool {
	switch name {
	case "dshow", "avfoundation", "v4l2", "gdigrab", "x11grab", "decklink":
		return true
	}
	return false
}

func (r *graphRunner) openSource(cfg Input, srcNode *graph.Node, decOpts av.DecoderOptions) (*sourceResources, error) {
	var inputOpts map[string]string
	if len(cfg.Options) > 0 {
		inputOpts = make(map[string]string, len(cfg.Options))
		for k, v := range cfg.Options {
			inputOpts[k] = fmt.Sprintf("%v", v)
		}
	}

	// Per-input timing flags (FFmpeg's `-ss` / `-t` / `-to`). These
	// are not AVOptions consumed by avformat_open_input — the runtime
	// enforces them itself, mirroring the logic in
	// fftools/ffmpeg_demux.c::ist_add_input_file(). Strip them from
	// the dictionary so libav doesn't see leftover unknown options.
	timing, err := resolveInputTiming(cfg.Options, func(format string, args ...any) {
		log.Printf("input %q: "+format, append([]any{cfg.URL}, args...)...)
	})
	if err != nil {
		return nil, fmt.Errorf("input %q timing: %w", cfg.URL, err)
	}
	delete(inputOpts, "ss")
	delete(inputOpts, "t")
	delete(inputOpts, "to")

	// Map Input.Kind onto a libavformat input-format name. Empty Kind (or
	// "file") falls through to OpenInput's URL-probing behaviour. "lavfi"
	// routes through libavformat's lavfi virtual demuxer so the URL is
	// interpreted as a filtergraph spec (anullsrc, color, sine, testsrc, …).
	var formatName string
	switch cfg.Kind {
	case "", "file":
		// default file probing; honour explicit Format override
		formatName = cfg.Format
	case "lavfi":
		formatName = "lavfi"
	case "raw":
		// kind="raw" requires Format to name the rawvideo / PCM
		// demuxer (validated upstream by validateInputDemuxerFields).
		formatName = cfg.Format
	case "concat":
		// kind="concat" pins the demuxer; if ConcatList was supplied
		// the runtime serialises it to a temp listfile (handled
		// below); otherwise URL points at an existing listfile.
		formatName = "concat"
	default:
		return nil, fmt.Errorf("input %q: unsupported kind %q (want \"file\", \"lavfi\", \"raw\" or \"concat\")", cfg.ID, cfg.Kind)
	}

	// Promote the typed demuxer fields (Wave 5 #23-#28) into the
	// AVDictionary the demuxer actually consumes. Each field maps to
	// the canonical AVOption name documented on the libavformat
	// demuxer that recognises it; collisions with cfg.Options leave
	// the typed field as the winner so the schema layer is the
	// source of truth.
	openURL := cfg.URL
	var concatCleanup func()
	defer func() {
		if concatCleanup != nil {
			// On failure between materialisation and successful return.
			concatCleanup()
		}
	}()
	if cfg.Kind == "concat" && len(cfg.ConcatList) > 0 {
		path, cleanup, err := materialiseConcatList(cfg.ConcatList)
		if err != nil {
			return nil, fmt.Errorf("input %q: materialise concat list: %w", cfg.ID, err)
		}
		openURL = path
		concatCleanup = cleanup
	}
	if inputOpts == nil {
		inputOpts = map[string]string{}
	}
	if cfg.FrameRate > 0 {
		inputOpts["framerate"] = strconv.FormatFloat(cfg.FrameRate, 'f', -1, 64)
	}
	if cfg.PixelFormat != "" {
		inputOpts["pixel_format"] = cfg.PixelFormat
	}
	if cfg.VideoSize != "" {
		inputOpts["video_size"] = cfg.VideoSize
	}
	if cfg.SampleRate > 0 {
		inputOpts["sample_rate"] = strconv.Itoa(cfg.SampleRate)
	}
	if cfg.Channels > 0 {
		inputOpts["channels"] = strconv.Itoa(cfg.Channels)
	}
	if cfg.SampleFormat != "" {
		inputOpts["sample_fmt"] = cfg.SampleFormat
	}
	if cfg.ThreadQueueSize > 0 {
		inputOpts["thread_queue_size"] = strconv.Itoa(cfg.ThreadQueueSize)
	}
	if len(cfg.ProtocolWhitelist) > 0 {
		inputOpts["protocol_whitelist"] = strings.Join(cfg.ProtocolWhitelist, ",")
	}
	if cfg.PatternType != "" {
		inputOpts["pattern_type"] = cfg.PatternType
	}
	if cfg.SeekTimestamp {
		inputOpts["seek_timestamp"] = "1"
	}
	// AccurateSeek: only emit when explicitly false (FFmpeg default
	// is true). Mapped to "noaccurate_seek" in the runtime via
	// dropping the +start_time addition below; we still pass
	// through so any future libavformat consumer sees it.
	if cfg.AccurateSeek != nil && !*cfg.AccurateSeek {
		inputOpts["accurate_seek"] = "0"
	}
	if len(inputOpts) == 0 {
		inputOpts = nil
	}

	input, err := av.OpenInputWithFormat(openURL, formatName, inputOpts)
	if err != nil {
		return nil, fmt.Errorf("open input %q: %w", cfg.URL, err)
	}

	// Mirror fftools/ffmpeg_demux.c: compute the seek target and apply
	// avformat_seek_file. Uses AV_TIME_BASE units (microseconds). The
	// container's reported start_time is added in to align with FFmpeg
	// for formats whose first PTS is non-zero (e.g. MPEG-TS). Skipped
	// for lavfi inputs — virtual sources don't support seeking and
	// always start at zero, so any -ss value is converted to the
	// per-packet stop check via timing's recording_time path.
	// Device inputs (dshow, v4l2, avfoundation, gdigrab, x11grab,
	// decklink) likewise never support seeking.
	if timing.haveStart && formatName != "lavfi" && !isDeviceFormat(formatName) {
		targetUS := timing.seekTimestampUS(input.StartTime())
		if err := input.SeekFile(targetUS); err != nil {
			input.Close()
			return nil, fmt.Errorf("seek input %q to %d us: %w", cfg.URL, targetUS, err)
		}
	}

	allStreams, err := input.AllStreams()
	if err != nil {
		input.Close()
		return nil, fmt.Errorf("enumerate streams %q: %w", cfg.URL, err)
	}

	// Determine which stream types feed *only* copy nodes (no decoder
	// needed) versus types that have at least one decoded consumer.
	// Streams whose type appears only on copy edges skip decoder open.
	copyOnly := map[string]bool{}
	if srcNode != nil {
		seenAny := map[string]bool{}
		seenNonCopy := map[string]bool{}
		for _, e := range srcNode.Outbound {
			t := string(e.Type)
			seenAny[t] = true
			if e.To == nil || e.To.Kind != graph.KindCopy {
				seenNonCopy[t] = true
			}
		}
		for t := range seenAny {
			if !seenNonCopy[t] {
				copyOnly[t] = true
			}
		}
	}

	decoders := make(map[int]av.FrameDecoder)
	subDecoders := make(map[int]*av.SubtitleDecoderContext)
	streams := make(map[int]av.StreamInfo)

	// Resolve the hardware device for per-input hwaccel (Wave 10 #59).
	// A non-empty cfg.HWAccel triggers the hw decoder path for video
	// streams. cfg.HWAccelDevice may name a pre-opened hardware_devices
	// entry (r.hwDevices[name]) or be empty (open on first use below).
	var resolvedHWDev *av.HWDeviceContext
	var ownedHWDev *av.HWDeviceContext // non-nil only when we opened it ourselves
	defer func() {
		if ownedHWDev != nil {
			// On the success path this is set to nil before the
			// function returns; here we handle all error paths.
			ownedHWDev.Close()
		}
	}()
	if cfg.HWAccel != "" && cfg.HWAccel != "none" {
		if cfg.HWAccelDevice != "" {
			if ctx, ok := r.hwDevices[cfg.HWAccelDevice]; ok {
				resolvedHWDev = ctx
			}
			// validate() already ensures cfg.HWAccelDevice names a declared entry,
			// so a missing lookup here is a programming error — leave resolvedHWDev nil
			// and fall back to software decoding.
		} else {
			// No named device: open a transient context.
			dt := av.ParseHWDeviceType(cfg.HWAccel)
			if dt != av.HWDeviceNone {
				var openErr error
				ownedHWDev, openErr = av.OpenHWDevice(dt, "")
				if openErr == nil {
					resolvedHWDev = ownedHWDev
				}
				// On error: log and fall back to software decode.
			}
		}
	}

	// Resolve the selector list (handles All / Optional / Negate /
	// Program — Wave 2 #9 + #10) into the concrete set of input
	// stream indices to demux. Selectors are walked in declaration
	// order; Negate selectors subtract from the running set; missing
	// non-Optional matches fail fast (the previous silent-skip
	// behaviour produced confusing downstream graph errors).
	selectedIdx, err := resolveStreamSelection(cfg.Streams, allStreams, input.Programs())
	if err != nil {
		input.Close()
		return nil, fmt.Errorf("input %q: %w", cfg.URL, err)
	}
	streamByIdx := make(map[int]av.StreamInfo, len(allStreams))
	for _, si := range allStreams {
		streamByIdx[si.Index] = si
	}
	for _, idx := range selectedIdx {
		si := streamByIdx[idx]
		typ := si.Type.String()
		switch {
		case copyOnly[typ] || typ == "attachment" || typ == "data" || typ == "unknown":
			// Stream-copy only: don't open a decoder.
			// Attachment data lives in codecpar->extradata and is written
			// by WriteHeader; no packet decoding is needed.
		case typ == "subtitle":
			subDec, err := av.OpenSubtitleDecoderWithOptions(input, si.Index, av.SubtitleDecoderOptions{
				Charenc: cfg.SubtitleCharenc,
			})
			if err != nil {
				for _, d := range decoders {
					d.Close()
				}
				for _, d := range subDecoders {
					d.Close()
				}
				input.Close()
				return nil, fmt.Errorf("open subtitle decoder for stream %d: %w", si.Index, err)
			}
			subDecoders[si.Index] = subDec
		default:
			// Use hardware-accelerated decoder when HWAccel is set on
			// this input and a device context is available (Wave 10 #59).
			// Only video streams benefit from GPU decode; audio stays on
			// the software path (mirrors FFmpeg: -hwaccel never applies
			// to audio decoders).
			if resolvedHWDev != nil && si.Type == av.MediaTypeVideo {
				// Determine whether to auto-transfer frames to SW.
				// The default (empty hwaccel_output_format) is to transfer,
				// mirroring FFmpeg's -hwaccel behaviour: frames are always
				// moved to system RAM unless the caller explicitly requests
				// a hardware surface format (e.g. "cuda", "vaapi") for a
				// zero-copy GPU encoder pipeline.
				autoTransfer := !isHwSurfaceFmtName(cfg.HWAccelOutputFormat)
				hwDec, err := av.OpenHWDecoder(input, si.Index, resolvedHWDev, av.HWDecoderOptions{
					AutoTransfer: autoTransfer,
					ThreadCount:  decOpts.ThreadCount,
					ThreadType:   decOpts.ThreadType,
				})
				if err != nil {
					for _, d := range decoders {
						d.Close()
					}
					for _, d := range subDecoders {
						d.Close()
					}
					input.Close()
					return nil, fmt.Errorf("open hw decoder for %s stream %d: %w", typ, si.Index, err)
				}
				decoders[si.Index] = hwDec
			} else {
				dec, libavErr := av.OpenDecoderWithOptions(input, si.Index, decOpts)
				switch {
				case libavErr == nil:
					decoders[si.Index] = dec
				case av.IsVTCodec(si.CodecTag):
					// LibAV has no codec for this tag (e.g. ProRes RAW 'aprn'/
					// 'aprh'); try the VideoToolbox-native path instead.
					vtDec, vtErr := av.OpenVTDecoder(input, si.Index)
					if vtErr != nil {
						for _, d := range decoders {
							d.Close()
						}
						for _, d := range subDecoders {
							d.Close()
						}
						input.Close()
						return nil, fmt.Errorf("open decoder for %s stream %d: no LibAV decoder (%v); VT decoder: %w", typ, si.Index, libavErr, vtErr)
					}
					decoders[si.Index] = vtDec
				default:
					for _, d := range decoders {
						d.Close()
					}
					for _, d := range subDecoders {
						d.Close()
					}
					input.Close()
					return nil, fmt.Errorf("open decoder for %s stream %d: %w", typ, si.Index, libavErr)
				}
			}
		}
		streams[si.Index] = si
	}

	// Compute longest selected stream duration for progress reporting.
	// Skips streams the user didn't pick (e.g. unselected audio
	// tracks), so a video-only job reports against the video duration
	// rather than a longer audio stream.
	var mediaDuration time.Duration
	for _, si := range streams {
		if si.Duration <= 0 || si.TimeBase[1] <= 0 {
			continue
		}
		d := time.Duration(si.Duration) * time.Second *
			time.Duration(si.TimeBase[0]) / time.Duration(si.TimeBase[1])
		if d > mediaDuration {
			mediaDuration = d
		}
	}

	// Compute ts_offset only when -ss actually triggered a seek.
	// FFmpeg additionally compensates for the container's reported
	// start_time even without -ss, but doing so unconditionally would
	// alter PTS for every existing job (e.g. MPEG-TS captures whose
	// first PTS is non-zero); restricting it to seeked jobs preserves
	// backward compatibility while still mirroring FFmpeg for the
	// trim use case the user is exercising.
	//
	// When Config.CopyTS is true the shift is suppressed so the
	// original demuxer PTS reach downstream nodes intact — mirrors
	// FFmpeg's global `-copyts` flag, which sets `ifile->ts_offset`
	// to 0 (or to `input_ts_offset`, which we don't model) instead
	// of `-timestamp` in fftools/ffmpeg_demux.c.
	//
	// `Config.StartAtZero` re-enables the shift even under CopyTS so
	// the first kept packet still anchors at PTS 0 (mirrors the
	// `start_at_zero ? 0 : f->start_time_effective` branch at
	// fftools/ffmpeg_demux.c L486 — `-start_at_zero` overrides the
	// `-copyts` suppression).
	var tsOffsetUS int64
	if timing.haveStart && (!(r.cfg != nil && r.cfg.CopyTS) || (r.cfg != nil && r.cfg.StartAtZero)) {
		tsOffsetUS = -timing.seekTimestampUS(input.StartTime())
	}

	// Compose `-itsoffset` additively with the seek compensation.
	// FFmpeg's fftools/ffmpeg_demux.c does the same:
	//   `f->ts_offset = o->input_ts_offset - timestamp;`
	// (where `timestamp` is the value passed to avformat_seek_file).
	// `Input.ITSOffset` is in seconds; convert to AV_TIME_BASE
	// microseconds before adding.
	if cfg.ITSOffset != 0 {
		tsOffsetUS += int64(cfg.ITSOffset * 1_000_000)
	}

	// Build the read-rate pacer when the user enabled pacing.
	// Mirrors fftools/ffmpeg_demux.c's `Demuxer.readrate` /
	// `readrate_initial_burst` / `readrate_catchup` defaults: when
	// burst is unset it falls back to 0.5s, and catchup falls back
	// to readrate × 1.05.
	var pacer *readRatePacer
	if cfg.ReadRate > 0 {
		burst := cfg.ReadRateInitialBurst
		if burst == 0 {
			burst = 0.5
		}
		catchup := cfg.ReadRateCatchup
		if catchup == 0 {
			catchup = cfg.ReadRate * 1.05
		}
		pacer = newReadRatePacer(cfg.ReadRate, burst, catchup)
	}

	res := &sourceResources{
		input:               input,
		decoders:            decoders,
		subDecoders:         subDecoders,
		streams:             streams,
		cfg:                 cfg,
		mediaDuration:       mediaDuration,
		stopPTSus:           timing.stopTimestampUS(input.StartTime()),
		tsOffsetUS:          tsOffsetUS,
		streamLoopRemaining: cfg.StreamLoop,
		pacer:               pacer,
		concatCleanup:       concatCleanup,
		ownedHWDev:          ownedHWDev,
	}
	concatCleanup = nil // ownership transferred to res.Close()
	ownedHWDev = nil    // ownership transferred to res.Close()
	return res, nil
}

func (r *graphRunner) copySourceFor(copyNode *graph.Node) (*av.InputFormatContext, int, [2]int, error) {
	if len(copyNode.Inbound) != 1 {
		return nil, 0, [2]int{}, fmt.Errorf("copy node %q must have exactly 1 inbound edge, got %d", copyNode.ID, len(copyNode.Inbound))
	}
	in := copyNode.Inbound[0]
	from := in.From
	if from.Kind != graph.KindSource {
		return nil, 0, [2]int{}, fmt.Errorf("copy node %q: upstream %q (kind=%v) must be a source", copyNode.ID, from.ID, from.Kind)
	}
	src := r.sources[from.ID]
	if src == nil {
		return nil, 0, [2]int{}, fmt.Errorf("copy node %q: source %q has no resources", copyNode.ID, from.ID)
	}
	mt := portTypeToAVMediaType(in.Type)

	// Parse the 0-based track index from the source port name ("v:0", "t:2", …).
	// When the port is "default" or carries no index, fall back to track 0.
	wantTrack := 0
	if parts := strings.SplitN(in.FromPort, ":", 2); len(parts) == 2 {
		if n, err := strconv.Atoi(parts[1]); err == nil && n >= 0 {
			wantTrack = n
		}
	}

	// Collect and sort stream indices for deterministic resolution when
	// there are multiple streams of the same type (e.g. several attachment
	// streams or audio tracks).
	indices := make([]int, 0, len(src.streams))
	for idx := range src.streams {
		indices = append(indices, idx)
	}
	sort.Ints(indices)

	track := 0
	for _, idx := range indices {
		si := src.streams[idx]
		if si.Type != mt {
			continue
		}
		if track == wantTrack {
			return src.input, idx, si.TimeBase, nil
		}
		track++
	}
	return nil, 0, [2]int{}, fmt.Errorf("copy node %q: source %q has no %v stream (track %d)", copyNode.ID, from.ID, in.Type, wantTrack)
}
