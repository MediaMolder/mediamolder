// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/cbs"
	"github.com/MediaMolder/MediaMolder/graph"
)

// handleSmartCopy implements the smart-cut (frame-accurate trim) node.
//
// It is the copy⊕encoder hybrid: the upstream source demuxes and forwards
// the raw elementary bitstream as *av.Packet (exactly like a plain copy
// edge); this node classifies each GOP against the trim window and either
//
//   - forwards the packets verbatim (interior GOPs fully inside the window —
//     no transcode, byte-for-byte), or
//   - decodes the GOP, re-encodes only the frames inside the window, and
//     emits fresh packets (HEAD/TAIL GOPs the cut points land in).
//
// Downstream the sink registers the stream via AddStreamFromInput (the
// source codecpar) and applies the node's smartCopyPlan: the re-encoded
// packets are re-framed to the source's NAL framing and their parameter
// sets join the output avcC under their own ids (H.264 + libx264), are
// renumbered into the output hvcC (HEVC), or travel in-band with the
// source's parameter sets re-inserted on copied keyframes (other H.264
// encoders). Open-GOP leading pictures are decided by presentation time
// and spliced with BLA (HEVC) or POC renumbering (H.264).
//
// See docs/smartcopy.md for the algorithm and constraints.
func (r *graphRunner) handleSmartCopy(ctx context.Context, node *graph.Node, ins []<-chan any, outs []chan<- any) error {
	if len(ins) != 1 || len(outs) < 1 {
		return fmt.Errorf("smartcopy node %q: expected 1 input / >=1 output, got %d/%d", node.ID, len(ins), len(outs))
	}
	t := perfTrackerFrom(ctx)
	// Registered before the first error return: every path out of this
	// handler (bad params, an unsupported codec, a mid-run failure) must
	// release the packets the source already queued on the inbound edge.
	defer drainPackets(ins[0])

	// Resolve the upstream source input + stream index + time_base by
	// tracing this node's inbound edge back to the source (same helper the
	// sink uses to wire AddStreamFromInput).
	input, srcIdx, srcTB, err := r.copySourceFor(node)
	if err != nil {
		return fmt.Errorf("smartcopy node %q: %w", node.ID, err)
	}
	if srcTB[1] <= 0 {
		return fmt.Errorf("smartcopy node %q: source stream %d has invalid time_base %v", node.ID, srcIdx, srcTB)
	}
	si, err := input.StreamInfo(srcIdx)
	if err != nil {
		return fmt.Errorf("smartcopy node %q: stream info: %w", node.ID, err)
	}

	// Trim window (microseconds relative to the source stream start),
	// stamped on the node by expandImplicitEncoders from the output's
	// ss/t/to options. Convert to source-stream PTS units.
	startUS := paramInt64(node.Params, "smartcopy_start_us")
	base := int64(0)
	if si.StartTime != av.NoPTSValue && si.StartTime != math.MinInt64 {
		base = si.StartTime
	}
	startPTS := base + usToTB(startUS, srcTB)
	endPTS := int64(math.MaxInt64)
	if _, ok := node.Params["smartcopy_end_us"]; ok {
		endPTS = base + usToTB(paramInt64(node.Params, "smartcopy_end_us"), srcTB)
	}

	// Audio uses a per-packet, sample-accurate path (no GOPs).
	if si.Type == av.MediaTypeAudio {
		return r.handleSmartAudioCopy(ctx, node, ins[0], outs, t, srcIdx, srcTB, si, startPTS, endPTS)
	}

	plan, err := r.smartCopyPlanFor(node)
	if err != nil {
		return fmt.Errorf("smartcopy node %q: %w", node.ID, err)
	}

	sc := &smartCopyState{
		r:          r,
		node:       node,
		ctx:        ctx,
		outs:       outs,
		t:          t,
		input:      input,
		srcIdx:     srcIdx,
		srcTB:      srcTB,
		startPTS:   startPTS,
		endPTS:     endPTS,
		plan:       plan,
		emitted:    math.MinInt64,
		lastDTS:    math.MinInt64,
		srcReorder: plan.srcReorder,
	}

	in := ins[0]
	defer func() {
		if sc.cur != nil {
			closeAll(sc.cur.pkts)
		}
		closeAll(sc.run)
		closeAll(sc.runPrime)
		closeAll(sc.prime)
		sc.cur, sc.run, sc.runPrime, sc.prime = nil, nil, nil, nil
	}()
	for {
		v, cancelled := perfReceive(ctx, in, t)
		if cancelled {
			break
		}
		pkt, ok := v.(*av.Packet)
		if !ok {
			return fmt.Errorf("smartcopy node %q: expected *av.Packet, got %T", node.ID, v)
		}
		if err := sc.consume(pkt); err != nil {
			return err
		}
	}
	// Classify the GOPs still buffered (their trailing boundary is EOF) and
	// flush a pending run.
	err = sc.finish()
	sc.cur, sc.run, sc.runPrime, sc.prime = nil, nil, nil, nil
	return err
}

// handleSmartAudioCopy implements sample-accurate audio smart-copy: interior
// audio packets (fully inside the window) are copied verbatim, and the packet(s)
// straddling each edge are sliced to the exact sample. Every audio packet is
// independently decodable, so there is no GOP classification — the unit is one
// packet.
//
// Only PCM is supported: it is byte-sliceable at any sample with no decode,
// no encoder priming, and no container edit-list surgery. Compressed audio
// (AAC/FLAC/…) would need per-stream priming / gapless handling — use
// codec_audio:<encoder> for a sample-accurate full re-encode (which auto-
// inserts atrim), or codec_audio:"copy" for a packet-accurate copy.
func (r *graphRunner) handleSmartAudioCopy(ctx context.Context, node *graph.Node, in <-chan any, outs []chan<- any, t *NodePerfTracker, srcIdx int, srcTB [2]int, si av.StreamInfo, startPTS, endPTS int64) error {
	defer drainPackets(in) // rejecting a codec below still leaves queued packets

	encName := av.DefaultEncoderForCodecID(si.CodecID)
	if !strings.HasPrefix(encName, "pcm_") {
		return fmt.Errorf("smartcopy node %q: audio codec %q is not supported for smart audio copy (only PCM is byte-sliceable without re-encode). Use codec_audio:<encoder> for a sample-accurate re-encode, or codec_audio:\"copy\" for a packet-accurate copy", node.ID, encName)
	}
	frameBytes := av.BytesPerSample(si.SampleFmt) * si.Channels
	if frameBytes <= 0 {
		return fmt.Errorf("smartcopy node %q: cannot determine PCM frame size (sample_fmt=%d channels=%d)", node.ID, si.SampleFmt, si.Channels)
	}

	// Reuse the video state's fan-out + DTS-monotonic send.
	sc := &smartCopyState{node: node, ctx: ctx, outs: outs, t: t, srcIdx: srcIdx, srcTB: srcTB, lastDTS: math.MinInt64}

	for {
		v, cancelled := perfReceive(ctx, in, t)
		if cancelled {
			break
		}
		pkt, ok := v.(*av.Packet)
		if !ok {
			return fmt.Errorf("smartcopy node %q: expected *av.Packet, got %T", node.ID, v)
		}
		if pkt.StreamIndex() != srcIdx {
			pkt.Close()
			continue
		}
		pPTS, pDur := pkt.PTS(), pkt.Duration()
		nSamples := int64(pkt.Size()) / int64(frameBytes)
		if pPTS == av.NoPTSValue || pPTS == math.MinInt64 || pDur <= 0 || nSamples <= 0 {
			pkt.Close() // cannot place this packet on the sample timeline
			continue
		}
		pEnd := pPTS + pDur
		switch {
		case pEnd <= startPTS || pPTS >= endPTS:
			pkt.Close() // wholly outside the window
		case pPTS >= startPTS && pEnd <= endPTS:
			if err := sc.send(pkt); err != nil { // interior: copy verbatim
				return err
			}
		default:
			// Boundary: slice the packet to the exact sample sub-range.
			keepStart, keepEnd := startPTS, endPTS
			if pPTS > keepStart {
				keepStart = pPTS
			}
			if pEnd < keepEnd {
				keepEnd = pEnd
			}
			firstSample := (keepStart - pPTS) * nSamples / pDur
			lastSample := (keepEnd - pPTS) * nSamples / pDur
			if lastSample <= firstSample {
				pkt.Close()
				continue
			}
			data := pkt.Data()
			off, end := firstSample*int64(frameBytes), lastSample*int64(frameBytes)
			if off < 0 || end > int64(len(data)) {
				pkt.Close()
				continue
			}
			np, err := av.NewPacketFromBytes(data[off:end])
			pkt.Close()
			if err != nil {
				return fmt.Errorf("smartcopy node %q: slice boundary packet: %w", node.ID, err)
			}
			np.SetStreamIndex(srcIdx)
			np.SetPTS(keepStart)
			np.SetDTS(keepStart)
			np.SetDuration(keepEnd - keepStart)
			if err := sc.send(np); err != nil {
				return err
			}
		}
	}
	return nil
}

// scGOP is one decode-order group: a keyframe packet and every packet up to
// the next keyframe. With open GOPs it also holds the leading pictures
// that display before the keyframe, so its presentation span starts at
// minPTS, not key.
type scGOP struct {
	pkts   []*av.Packet
	key    int64 // keyframe PTS
	minPTS int64 // lowest PTS in the GOP (leading pictures)
	maxPTS int64 // highest PTS in the GOP
}

// smartCopyState carries the per-run GOP state machine for handleSmartCopy.
//
// A GOP is classified by its own presentation span [minPTS, maxPTS] once
// the next keyframe completes it. A copied GOP that follows a re-encoded
// run lends its leading pictures to that run (they reference pictures that
// are no longer in the output). Consecutive boundary GOPs form one run and
// are re-encoded in one encoder session (one IDR).
type smartCopyState struct {
	r      *graphRunner
	node   *graph.Node
	ctx    context.Context
	outs   []chan<- any
	t      *NodePerfTracker
	input  *av.InputFormatContext
	srcIdx int
	srcTB  [2]int

	startPTS int64
	endPTS   int64
	plan     *smartCopyPlan

	cur  *scGOP // GOP being accumulated
	done bool   // window end passed; drop everything after

	// prime holds clones of the last classified GOP: a boundary run is
	// decoded with the preceding GOP first so leading pictures that
	// reference across the keyframe (open GOPs) have their references.
	prime []*av.Packet
	// run accumulates the packets of consecutive boundary GOPs; runPrime
	// is the GOP that preceded the run.
	run      []*av.Packet
	runPrime []*av.Packet

	emitted    int64 // highest PTS sent so far (math.MinInt64 = none)
	prevCopied bool  // the previous GOP was copied
	spliced    bool  // a run was emitted; the next copied keyframe follows it
	nRuns      int
	nFrames    int // re-encoded frames
	nCopied    int // copied packets

	// H.264 POC / frame_num renumbering after an open-GOP splice.
	srcPOC      *h264POCState
	runPOC      *h264POCState
	renumber    bool
	pocDelta    int32
	frameNumDel int32
	pocWarned   bool

	// srcReorder is the source's decode-to-display delay in frames (the
	// plan's probe, else measured from the first GOP); -1 until known.
	srcReorder  int
	depthWarned bool
	// frameTick is one frame in source time_base units, measured as the
	// smallest PTS step of the first complete GOP (exact for a constant
	// rate source, unlike the demuxer's average frame rate); 0 until known.
	frameTick int64
	runOut    []*av.Packet // re-encoded packets of the current run, awaiting DTS assignment

	runFirst   bool                 // next encoder packet is the first of the run
	runPending [][]byte             // parameter sets to insert before the run's first packet
	runDur     map[int64]int64      // kept-frame PTS → duration, for packets the encoder leaves at 0
	runHEVC    *hevcSliceRenumberer // HEVC config mode: renumbers the run's slice headers

	lastDTS    int64 // last emitted DTS (source TB) for the monotonic guard
	dtsClamped bool  // whether the guard ever had to bump a DTS
}

func validPTS(pts int64) bool { return pts != av.NoPTSValue && pts != math.MinInt64 }

// consume routes one demuxer packet into the GOP accumulator.
func (s *smartCopyState) consume(pkt *av.Packet) error {
	if pkt.StreamIndex() != s.srcIdx {
		pkt.Close()
		return nil
	}
	if pkt.IsKeyFrame() {
		// A keyframe completes cur, which can then be classified.
		if s.cur != nil {
			if s.frameTick <= 0 {
				s.frameTick = gopFrameTick(s.cur)
			}
			if s.srcReorder < 0 {
				s.srcReorder = gopReorderDepth(s.cur, s.frameTick)
			}
			g := s.cur
			s.cur = nil
			if !s.done {
				if err := s.classify(g); err != nil {
					return err
				}
			} else {
				closeAll(g.pkts)
			}
		}
		if s.done {
			pkt.Close()
			return nil
		}
		s.cur = &scGOP{pkts: []*av.Packet{pkt}, key: pkt.PTS(), minPTS: pkt.PTS(), maxPTS: pkt.PTS()}
		return nil
	}
	if s.cur == nil || s.done {
		// Non-keyframe with no open GOP (e.g. leading packets after a seek
		// before the first keyframe) — nothing can reference it cleanly.
		pkt.Close()
		return nil
	}
	s.cur.pkts = append(s.cur.pkts, pkt)
	if pts := pkt.PTS(); validPTS(pts) {
		if pts < s.cur.minPTS {
			s.cur.minPTS = pts
		}
		if pts > s.cur.maxPTS {
			s.cur.maxPTS = pts
		}
	}
	return nil
}

// finish classifies the GOP still buffered at EOF, flushes a pending run
// and releases retained state.
func (s *smartCopyState) finish() error {
	var err error
	if s.cur != nil {
		g := s.cur
		s.cur = nil
		if !s.done {
			err = s.classify(g)
		} else {
			closeAll(g.pkts)
		}
	}
	if err == nil && !s.done {
		err = s.flushRun(s.endPTS, nil)
	}
	closeAll(s.run)
	closeAll(s.runPrime)
	closeAll(s.prime)
	s.run, s.runPrime, s.prime = nil, nil, nil
	if err == nil {
		log.Printf("smartcopy node %q: %d packets copied, %d frames re-encoded in %d run(s)", s.node.ID, s.nCopied, s.nFrames, s.nRuns)
	}
	return err
}

// classify decides the fate of GOP g from its presentation span
// [g.minPTS, g.maxPTS] and consumes its packets.
func (s *smartCopyState) classify(g *scGOP) error {
	switch {
	case g.maxPTS < s.startPTS:
		// Entirely before the window: kept only as decoder priming.
		s.setPrime(cloneAll(g.pkts))
		closeAll(g.pkts)
		s.prevCopied = false
		return nil
	case g.minPTS >= s.endPTS:
		// Entirely at/after the window end.
		err := s.flushRun(s.endPTS, nil)
		closeAll(g.pkts)
		s.done = true
		return err
	case g.key >= s.startPTS && g.maxPTS < s.endPTS:
		// Copy GOP. Its leading pictures reference the previous GOP; when
		// that GOP is not in the output as copied (it was re-encoded or
		// dropped), they are re-encoded with the pending run instead.
		needLead := g.minPTS < g.key && !s.prevCopied && s.startPTS < g.key
		if len(s.run) > 0 || needLead {
			if err := s.flushRun(g.key, g); err != nil {
				closeAll(g.pkts)
				return err
			}
		}
		err := s.emitCopy(g)
		s.prevCopied = true
		return err
	default:
		// Boundary GOP: the window starts and/or ends inside it.
		if len(s.run) == 0 {
			closeAll(s.runPrime)
			s.runPrime, s.prime = s.prime, nil
		}
		s.run = append(s.run, g.pkts...)
		s.setPrime(nil)
		s.prevCopied = false
		return nil
	}
}

func (s *smartCopyState) setPrime(p []*av.Packet) {
	closeAll(s.prime)
	s.prime = p
}

// emitCopy forwards the in-window packets of a copied GOP. The first copied
// keyframe after a re-encoded run is spliced: HEVC CRA pictures become BLA
// (new coded video sequence, leading pictures discarded) and H.264 slices
// are renumbered so their POC continues above the run's.
func (s *smartCopyState) emitCopy(g *scGOP) error {
	prime := cloneAll(g.pkts)
	s.setPrime(prime)
	// Leading pictures already re-encoded with the preceding run sit at or
	// below the watermark the run left; the GOP's own packets arrive in
	// decode order, so compare against the watermark as it was before this
	// GOP, not the running maximum.
	watermark := s.emitted
	drop := func(pts int64) bool {
		return validPTS(pts) && (pts < s.startPTS || pts >= s.endPTS || (watermark != math.MinInt64 && pts <= watermark))
	}
	// Leading pictures that are dropped (re-encoded with the run, or before
	// the window) were decoded right after the keyframe in the source, so
	// the keyframe's source DTS sits that many frames too early for the
	// output's decode order. Retime it to its own display slot minus the
	// reorder delay; the trailing packets' DTS already continue from there.
	droppedLeading := 0
	for _, p := range g.pkts[1:] {
		if pts := p.PTS(); validPTS(pts) && pts < g.key && drop(pts) {
			droppedLeading++
		}
	}
	var ret error
	for i, p := range g.pkts {
		if ret != nil {
			p.Close()
			continue
		}
		pts := p.PTS()
		if drop(pts) {
			p.Close() // outside the window, or already re-encoded (leading picture)
			continue
		}
		if i == 0 {
			if err := s.prepareCopiedKeyframe(p); err != nil {
				p.Close()
				ret = err
				continue
			}
			if droppedLeading > 0 && s.srcReorder >= 0 && validPTS(pts) {
				p.SetDTS(pts - int64(s.srcReorder)*s.frameDur(nil))
			}
		}
		if s.renumber {
			if err := s.renumberCopied(p); err != nil {
				p.Close()
				ret = err
				continue
			}
		}
		if err := s.send(p); err != nil {
			ret = err
			continue
		}
		s.nCopied++
		if validPTS(pts) && pts > s.emitted {
			s.emitted = pts
		}
	}
	return ret
}

// prepareCopiedKeyframe applies the splice and in-band adjustments to a
// copied keyframe packet.
func (s *smartCopyState) prepareCopiedKeyframe(p *av.Packet) error {
	plan := s.plan
	if plan.codec == 0 {
		return nil
	}
	data := p.Data()
	changed := false
	if s.spliced {
		s.spliced = false
		switch plan.codec {
		case cbs.CodecH265:
			if hevcCRAToBLA(data, plan.lengthSize) {
				changed = true
			}
		case cbs.CodecH264:
			if err := s.beginRenumber(data); err != nil {
				return err
			}
		}
	}
	if plan.mode == smartCopyModeInBand && len(plan.srcParamSets) > 0 && !packetHasParamSets(plan.codec, data, plan.lengthSize) {
		withPS, err := prependNALs(plan.codec, data, plan.lengthSize, plan.srcParamSets)
		if err != nil {
			return fmt.Errorf("smartcopy %q: insert source parameter sets: %w", s.node.ID, err)
		}
		data = withPS
		changed = true
	}
	if changed {
		return p.ReplaceData(data)
	}
	return nil
}

// beginRenumber starts H.264 POC renumbering at the first copied keyframe
// after a run, unless that keyframe is an IDR (which restarts POC itself).
func (s *smartCopyState) beginRenumber(keyframe []byte) error {
	s.renumber = false
	if packetFirstVCLType(cbs.CodecH264, keyframe, s.plan.lengthSize) == h264NALIDR {
		return nil
	}
	if !s.plan.pocFix || s.runPOC == nil {
		if !s.pocWarned {
			log.Printf("smartcopy node %q: open-GOP H.264 splice at a non-IDR keyframe without POC renumbering; decoders may reorder or drop frames at the head cut", s.node.ID)
			s.pocWarned = true
		}
		return nil
	}
	if s.srcPOC == nil {
		st, err := newH264POCState(s.input.StreamExtraData(s.srcIdx), s.plan.lengthSize)
		if err != nil {
			return fmt.Errorf("smartcopy %q: %w", s.node.ID, err)
		}
		s.srcPOC = st
	}
	// Peek at the keyframe's first slice without rewriting it.
	_, info, err := s.srcPOC.renumber(keyframe, 0, 0)
	if err != nil || !info.ok || info.sps == nil || info.sps.PicOrderCntType != 0 {
		if err == nil {
			err = fmt.Errorf("no type-0 slice header")
		}
		log.Printf("smartcopy node %q: cannot renumber POC at the head splice: %v", s.node.ID, err)
		return nil
	}
	lsbDelta, fnDelta, err := s.runPOC.spliceDelta(info)
	if err != nil {
		log.Printf("smartcopy node %q: cannot renumber POC at the head splice: %v", s.node.ID, err)
		return nil
	}
	s.pocDelta, s.frameNumDel = lsbDelta, fnDelta
	s.renumber = lsbDelta != 0 || fnDelta != 0
	return nil
}

// renumberCopied shifts pic_order_cnt_lsb in a copied packet's slices;
// renumbering stops at the next IDR (which is left untouched) and after a
// picture with memory_management_control_operation 5.
func (s *smartCopyState) renumberCopied(p *av.Packet) error {
	out, info, err := s.srcPOC.renumber(p.Data(), s.pocDelta, s.frameNumDel)
	if err != nil {
		return fmt.Errorf("smartcopy %q: renumber POC: %w", s.node.ID, err)
	}
	if !info.ok {
		return nil
	}
	if info.idr {
		s.renumber = false
		return nil
	}
	if info.mmco5 {
		s.renumber = false
	}
	return p.ReplaceData(out)
}

// flushRun re-encodes the pending boundary run: the preceding GOP (priming),
// the run's GOPs and, when next is a copied GOP, its leading pictures. Frames
// with PTS in [max(startPTS, emitted+1), min(hi, endPTS)) are kept, in
// presentation order; the first is forced to a keyframe (IDR).
func (s *smartCopyState) flushRun(hi int64, next *scGOP) error {
	if len(s.run) == 0 && next == nil {
		return nil
	}
	runPkts, runPrime := s.run, s.runPrime
	s.run, s.runPrime = nil, nil
	defer closeAll(runPkts)
	defer closeAll(runPrime)
	// An empty run (only next's leading pictures to re-encode) is primed
	// with the GOP classified last, which emitCopy replaces afterwards.
	prime := runPrime
	if len(runPkts) == 0 {
		prime = s.prime
	}

	lo := s.startPTS
	if s.emitted != math.MinInt64 && s.emitted+1 > lo {
		lo = s.emitted + 1
	}
	if hi > s.endPTS {
		hi = s.endPTS
	}
	if lo >= hi {
		return nil
	}

	feed := make([]*av.Packet, 0, len(prime)+len(runPkts)+16)
	feed = append(feed, prime...)
	feed = append(feed, runPkts...)
	if next != nil {
		feed = append(feed, next.pkts...)
	}
	// Nothing after the last packet whose PTS is inside the range can be a
	// reference for a kept frame (references precede in decode order).
	last := -1
	for i, p := range feed {
		if pts := p.PTS(); validPTS(pts) && pts < hi {
			last = i
		}
	}
	if last < 0 {
		return nil
	}
	feed = feed[:last+1]

	dec, err := av.OpenDecoder(s.input, s.srcIdx)
	if err != nil {
		return fmt.Errorf("smartcopy %q: open boundary decoder: %w", s.node.ID, err)
	}
	kept, err := s.decodeWindow(dec, feed, lo, hi)
	dec.Close()
	if err != nil {
		return err
	}
	if len(kept) == 0 {
		return nil
	}

	// Stamp every kept frame's duration (the gap to the next kept frame)
	// so the encoder's packets carry it: the mov muxer sizes its edit list
	// from the last packet's pts + duration, and a zero duration there
	// would cut the run's final frame out of the presentation.
	s.runDur = make(map[int64]int64, len(kept))
	for i, f := range kept {
		d := f.Duration()
		if i+1 < len(kept) {
			d = kept[i+1].PTS() - f.PTS()
		} else if d <= 0 && i > 0 {
			d = f.PTS() - kept[i-1].PTS()
		}
		if d <= 0 {
			d = frameDurationTB(s.plan.encOpts.FrameRate, s.srcTB)
		}
		f.SetDuration(d)
		s.runDur[f.PTS()] = d
	}

	enc, err := av.OpenEncoder(s.plan.encOpts)
	if err != nil {
		closeFrames(kept)
		return fmt.Errorf("smartcopy %q: open boundary encoder: %w", s.node.ID, err)
	}
	defer enc.Close()
	s.nRuns++
	if err := s.beginRun(enc); err != nil {
		closeFrames(kept)
		return err
	}

	defer func() {
		closeAll(s.runOut) // only non-empty when a step below failed
		s.runOut = nil
	}()
	for i, f := range kept {
		if i == 0 {
			f.SetPictType(av.PictureTypeI) // force IDR at the segment start
		}
		if err := enc.SendFrame(f); err != nil {
			closeFrames(kept[i:])
			return fmt.Errorf("smartcopy %q: encode frame: %w", s.node.ID, err)
		}
		f.Close()
		s.nFrames++
		if err := s.drainEncoder(enc); err != nil {
			return err
		}
	}
	if err := enc.Flush(); err != nil && !av.IsEOF(err) && !av.IsEAgain(err) {
		return fmt.Errorf("smartcopy %q: flush encoder: %w", s.node.ID, err)
	}
	if err := s.drainEncoder(enc); err != nil {
		return err
	}
	if err := s.sendRun(); err != nil {
		return err
	}
	s.spliced = true
	s.renumber = false
	return nil
}

// sendRun assigns DTS to the buffered run packets and forwards them. A
// stream's DTS timeline is its display-ordered PTS sequence shifted by the
// decode-to-display delay, so the run's k-th packet (decode order) gets the
// PTS of the frame d positions earlier in display order, with d the larger
// of the source's delay (so the run lands exactly where the copied
// packets' DTS leave off) and the run's own reorder depth (so no DTS
// exceeds its PTS). Frames before the run's first are extrapolated at one
// frame duration: for a tail run those are the copied frames just before
// it, for a head run they precede the clip, and either way the step stays
// one frame so the container sees a constant frame rate.
func (s *smartCopyState) sendRun() error {
	pkts := s.runOut
	s.runOut = nil
	if len(pkts) == 0 {
		return nil
	}
	display := make([]int64, 0, len(pkts))
	for _, p := range pkts {
		display = append(display, p.PTS())
	}
	sort.Slice(display, func(i, j int) bool { return display[i] < display[j] })
	rank := make(map[int64]int, len(display))
	for i, pts := range display {
		rank[pts] = i
	}
	depth := 0
	for k, p := range pkts {
		if d := k - rank[p.PTS()]; d > depth {
			depth = d
		}
	}
	if depth > s.srcReorder && s.srcReorder >= 0 && !s.depthWarned {
		// The run reorders deeper than the source: its DTS must start that
		// much earlier, which leaves a larger step at the next join. The
		// plan caps the encoder's B-frame layout to avoid this.
		log.Printf("smartcopy node %q: re-encoded run reorders %d frames deep, source %d; one DTS step at the join will be longer", s.node.ID, depth, s.srcReorder)
		s.depthWarned = true
	}
	if s.srcReorder > depth {
		depth = s.srcReorder
	}
	dur := s.frameDur(display)
	for k, p := range pkts {
		i := k - depth
		if i >= 0 {
			p.SetDTS(display[i])
		} else {
			p.SetDTS(display[0] + int64(i)*dur)
		}
		pts := p.PTS()
		if err := s.send(p); err != nil { // send owns p from here on
			closeAll(pkts[k+1:])
			return err
		}
		if validPTS(pts) && pts > s.emitted {
			s.emitted = pts
		}
	}
	return nil
}

// frameDur is one frame in source time_base units: the step of the
// display-ordered PTS list when it has one, else the tick measured from
// the first GOP, else the stream's frame rate.
func (s *smartCopyState) frameDur(display []int64) int64 {
	dur := int64(0)
	if len(display) > 1 {
		dur = display[1] - display[0]
	}
	if dur <= 0 {
		dur = s.frameTick
	}
	if dur <= 0 {
		dur = frameDurationTB(s.plan.encOpts.FrameRate, s.srcTB)
	}
	if dur <= 0 {
		dur = 1
	}
	return dur
}

// gopFrameTick is the smallest positive PTS step inside a GOP.
func gopFrameTick(g *scGOP) int64 {
	pts := make([]int64, 0, len(g.pkts))
	for _, p := range g.pkts {
		if v := p.PTS(); validPTS(v) {
			pts = append(pts, v)
		}
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i] < pts[j] })
	tick := int64(0)
	for i := 1; i < len(pts); i++ {
		if d := pts[i] - pts[i-1]; d > 0 && (tick == 0 || d < tick) {
			tick = d
		}
	}
	return tick
}

// gopReorderDepth is reorderDepth over one buffered GOP.
func gopReorderDepth(g *scGOP, dur int64) int {
	pts := make([]int64, 0, len(g.pkts))
	dts := make([]int64, 0, len(g.pkts))
	for _, p := range g.pkts {
		pts = append(pts, p.PTS())
		dts = append(dts, p.DTS())
	}
	return reorderDepth(pts, dts, dur)
}

// beginRun prepares per-run bookkeeping once the encoder is open: the H.264
// POC tracker for the splice and, in config mode, a check that the encoder
// produced the parameter sets the output avcC advertises.
func (s *smartCopyState) beginRun(enc *av.EncoderContext) error {
	s.runPending = nil
	s.runFirst = true
	s.runHEVC = nil
	if s.plan.codec == cbs.CodecH265 && s.plan.mode == smartCopyModeConfigPS {
		return s.beginRunHEVC(enc)
	}
	if s.plan.codec != cbs.CodecH264 {
		return nil
	}
	ed := enc.ExtraData()
	if s.plan.mode == smartCopyModeConfigPS {
		var got [][]byte
		for _, n := range splitAnnexB(ed) {
			if t := h264NALType(n); t == h264NALSPS || t == h264NALPPS {
				got = append(got, n)
			}
		}
		if !equalNALSets(got, s.plan.encParamSets) {
			log.Printf("smartcopy node %q: boundary encoder parameter sets differ from the planned ones; sending them in-band", s.node.ID)
			s.runPending = got
		}
	}
	if s.plan.pocFix {
		st, err := newH264POCState(ed, 0)
		if err != nil {
			return fmt.Errorf("smartcopy %q: boundary parameter sets: %w", s.node.ID, err)
		}
		s.runPOC = st
	}
	return nil
}

// beginRunHEVC prepares the slice renumbering for an HEVC config-mode run.
// The encoder's parameter sets must match the ones the plan renumbered
// into the output hvcC (the encoder is deterministic for one option set);
// a different set would need the same ids and cannot share the track.
func (s *smartCopyState) beginRunHEVC(enc *av.EncoderContext) error {
	ed := enc.ExtraData()
	var got [][]byte
	var nals [][]byte
	if len(ed) > 0 && ed[0] == 1 {
		nals, _ = hvcCParamSets(ed)
	} else {
		nals = splitAnnexB(ed)
	}
	for _, n := range nals {
		if nalIsParamSet(cbs.CodecH265, n) {
			got = append(got, n)
		}
	}
	if len(got) == 0 {
		return fmt.Errorf("smartcopy %q: boundary encoder emitted no parameter sets", s.node.ID)
	}
	if !equalNALSets(got, s.plan.encParamSets) {
		// The output hvcC already holds the planned sets under the chosen
		// ids; a different set under the same ids cannot be added to an
		// hvc1 track without ambiguity, so this is a hard error.
		return fmt.Errorf("smartcopy %q: boundary encoder parameter sets differ from the planned ones in the output hvcC", s.node.ID)
	}
	r, err := newHEVCSliceRenumberer(got, s.plan.hevcIDs.pps)
	if err != nil {
		return fmt.Errorf("smartcopy %q: %w", s.node.ID, err)
	}
	s.runHEVC = r
	return nil
}

func equalNALSets(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

// decodeWindow feeds pkts to dec and returns the decoded frames whose PTS is
// inside [lo, hi), in presentation order.
func (s *smartCopyState) decodeWindow(dec *av.DecoderContext, pkts []*av.Packet, lo, hi int64) ([]*av.Frame, error) {
	var kept []*av.Frame
	drain := func() error {
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
			pts := f.PTS()
			if validPTS(pts) && pts >= lo && pts < hi {
				kept = append(kept, f)
			} else {
				f.Close()
			}
		}
	}
	for _, p := range pkts {
		if err := dec.SendPacket(p); err != nil {
			closeFrames(kept)
			return nil, fmt.Errorf("smartcopy %q: decode packet: %w", s.node.ID, err)
		}
		if err := drain(); err != nil {
			closeFrames(kept)
			return nil, err
		}
	}
	if err := dec.Flush(); err != nil && !av.IsEOF(err) && !av.IsEAgain(err) {
		closeFrames(kept)
		return nil, fmt.Errorf("smartcopy %q: flush decoder: %w", s.node.ID, err)
	}
	if err := drain(); err != nil {
		closeFrames(kept)
		return nil, err
	}
	return kept, nil
}

// drainEncoder collects every packet currently available from enc,
// re-framed to the source's NAL framing, for sendRun.
func (s *smartCopyState) drainEncoder(enc *av.EncoderContext) error {
	for {
		p, err := av.AllocPacket()
		if err != nil {
			return err
		}
		if err := enc.ReceivePacket(p); err != nil {
			p.Close()
			if av.IsEAgain(err) || av.IsEOF(err) {
				return nil
			}
			return fmt.Errorf("smartcopy %q: receive packet: %w", s.node.ID, err)
		}
		p.SetStreamIndex(s.srcIdx)
		if p.Duration() <= 0 {
			if d, ok := s.runDur[p.PTS()]; ok {
				p.SetDuration(d)
			}
		}
		if err := s.reframeRunPacket(p); err != nil {
			p.Close()
			return err
		}
		s.runOut = append(s.runOut, p)
	}
}

// reframeRunPacket converts an encoder packet (Annex B) to the source's
// framing and feeds the H.264 POC tracker.
func (s *smartCopyState) reframeRunPacket(p *av.Packet) error {
	plan := s.plan
	if plan.codec == 0 {
		return nil
	}
	data := p.Data()
	if s.runPOC != nil {
		s.runPOC.observe(data)
	}
	var nals [][]byte
	if s.runHEVC != nil {
		var err error
		if nals, err = s.runHEVC.rewritePacket(data); err != nil {
			return fmt.Errorf("smartcopy %q: %w", s.node.ID, err)
		}
	} else {
		nals = splitAnnexB(data)
	}
	if s.runFirst {
		s.runFirst = false
		if len(s.runPending) > 0 {
			nals = append(append([][]byte{}, s.runPending...), nals...)
		}
	}
	framed, err := joinNALs(nals, plan.lengthSize)
	if err != nil {
		return fmt.Errorf("smartcopy %q: frame re-encoded packet: %w", s.node.ID, err)
	}
	return p.ReplaceData(framed)
}

// send forwards one packet to every downstream channel, enforcing a strictly
// increasing DTS (the muxer requires it). Only DTS is clamped — PTS is left
// intact so display timing stays frame-accurate. Ownership of pkt transfers
// to send (it is closed or forwarded).
func (s *smartCopyState) send(pkt *av.Packet) error {
	if dts := pkt.DTS(); dts != av.NoPTSValue && dts != math.MinInt64 {
		if s.lastDTS != math.MinInt64 && dts <= s.lastDTS {
			pkt.SetDTS(s.lastDTS + 1)
			if !s.dtsClamped {
				// One-time warning: a re-encoded boundary packet's DTS met or
				// crossed the copied interior's DTS at a join, so it was bumped
				// to keep the muxer's per-stream DTS strictly increasing. PTS is
				// untouched, so display timing stays frame-accurate; a warning
				// surfaces sources whose B-frame layout stresses the join.
				log.Printf("smartcopy node %q: clamped a boundary packet DTS at a join to preserve monotonic order (source B-frame layout)", s.node.ID)
				s.dtsClamped = true
			}
			dts = s.lastDTS + 1
		}
		s.lastDTS = dts
	}
	if len(s.outs) == 0 {
		pkt.Close()
		return nil
	}
	for i, out := range s.outs {
		p := pkt
		if i < len(s.outs)-1 {
			c, err := av.ClonePacket(pkt)
			if err != nil {
				pkt.Close()
				return err
			}
			p = c
		}
		if perfSend(s.ctx, out, p, s.t) {
			p.Close()
			return s.ctx.Err()
		}
	}
	return nil
}

// buildBoundaryEncoderOptions derives an encoder configuration that matches
// the source stream so re-encoded boundary GOPs stay compatible with the
// copied interior. Structural fields (codec, profile/level, resolution,
// pixfmt, SAR, framerate, timebase) come from the source and are not
// overridable; quality knobs (crf/preset/bitrate/...) may be supplied via the
// node params (from Output.EncoderParamsVideo).
func buildBoundaryEncoderOptions(si av.StreamInfo, srcTB [2]int, params map[string]any) (av.EncoderOptions, error) {
	name := paramString(params, "smartcopy_encoder")
	if name == "" {
		name = av.DefaultEncoderForCodecID(si.CodecID)
	}
	if name == "" || !av.FindEncoder(name) {
		return av.EncoderOptions{}, fmt.Errorf("no encoder available for source codec (id=%d); smartcopy cannot re-encode boundary GOPs", si.CodecID)
	}

	// Pass every non-reserved node param through to the encoder as an AVOption,
	// so the GUI's encoder controls (crf/preset/tune/profile/level/b/g/
	// x264-params/…) apply to the boundary re-encode. Reserved keys are the
	// smartcopy-specific ones and the trim window.
	reserved := map[string]bool{
		"smartcopy_encoder": true, "smartcopy_global_header": true,
		"smartcopy_start_us": true, "smartcopy_end_us": true,
		"codec": true, "ss": true, "t": true, "to": true,
		// Structural fields must stay identical to the source so the
		// re-encoded boundary stays compatible with the copied interior —
		// they are taken from the source stream, never a GUI override.
		"pix_fmt": true, "s": true, "r": true, "g": true,
		"width": true, "height": true, "sar": true, "aspect": true,
	}
	extra := map[string]string{}
	for k, v := range params {
		if reserved[k] {
			continue
		}
		if sv := smartAnyToString(v); sv != "" {
			extra[k] = sv
		}
	}
	// Default to a visually-lossless quality target when the user set no rate
	// control (only 1–2 GOPs are re-encoded, so continuity matters more than
	// exact size).
	if extra["crf"] == "" && extra["qp"] == "" && extra["b"] == "" && extra["bitrate"] == "" {
		extra["crf"] = "18"
	}
	if extra["preset"] == "" {
		extra["preset"] = "medium"
	}

	opts := av.EncoderOptions{
		CodecName:         name,
		Width:             si.Width,
		Height:            si.Height,
		PixFmt:            si.PixFmt,
		FrameRate:         si.FrameRate,
		TimeBase:          srcTB,
		SampleAspectRatio: si.SampleAspectRatio,
		Profile:           si.Profile,
		Level:             si.Level,
		BitRate:           si.BitRate,
		GOPSize:           1 << 20, // effectively single-GOP; keyframe forced at start
		ExtraOpts:         extra,
	}
	if opts.FrameRate[1] <= 0 {
		opts.FrameRate = si.RFrameRate
	}
	return opts, nil
}

// frameDurationTB is one frame of fr in tb units (0 when unknown).
func frameDurationTB(fr, tb [2]int) int64 {
	if fr[0] <= 0 || fr[1] <= 0 || tb[0] <= 0 || tb[1] <= 0 {
		return 0
	}
	return int64(fr[1]) * int64(tb[1]) / (int64(fr[0]) * int64(tb[0]))
}

// usToTB converts microseconds to the given time_base's units.
func usToTB(us int64, tb [2]int) int64 {
	if tb[0] <= 0 || tb[1] <= 0 {
		return 0
	}
	return us * int64(tb[1]) / (int64(tb[0]) * 1_000_000)
}

func smartAnyToString(v any) string {
	if v == nil {
		return ""
	}
	if f, ok := v.(float64); ok {
		return strconv.FormatInt(int64(f), 10)
	}
	return fmt.Sprintf("%v", v)
}

// drainPackets unrefs every packet still queued on in. A smartcopy handler
// that leaves its receive loop early (context cancel, or an error further
// down) would otherwise abandon the packets the source already handed off,
// and each one holds an AVPacket plus its buffer. The scheduler closes a
// node's output channels once its handler returns, so the upstream close
// always arrives and this terminates.
func drainPackets(in <-chan any) {
	for v := range in {
		if p, ok := v.(*av.Packet); ok {
			p.Close()
		}
	}
}

func closeAll(pkts []*av.Packet) {
	for _, p := range pkts {
		if p != nil {
			p.Close()
		}
	}
}

// cloneAll returns ref-clones of pkts (cheap: shares the underlying buffers).
// The clones are owned by the caller and must be closed.
func cloneAll(pkts []*av.Packet) []*av.Packet {
	if len(pkts) == 0 {
		return nil
	}
	out := make([]*av.Packet, 0, len(pkts))
	for _, p := range pkts {
		c, err := av.ClonePacket(p)
		if err != nil {
			continue // best-effort priming; a missing ref only risks concealment
		}
		out = append(out, c)
	}
	return out
}

func closeFrames(frames []*av.Frame) {
	for _, f := range frames {
		if f != nil {
			f.Close()
		}
	}
}
