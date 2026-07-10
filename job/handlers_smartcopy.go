// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"context"
	"fmt"
	"log"
	"math"
	"strconv"

	"github.com/MediaMolder/MediaMolder/av"
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
// source codecpar), so both the copied and re-encoded packets share the
// source codec and time_base. The re-encode matches the source
// codec/profile/level/pixfmt/SAR/timebase so its parameter sets stay
// compatible with the copied interior.
//
// See docs/smartcopy.md for the algorithm and constraints.
func (r *graphRunner) handleSmartCopy(ctx context.Context, node *graph.Node, ins []<-chan any, outs []chan<- any) error {
	if len(ins) != 1 || len(outs) < 1 {
		return fmt.Errorf("smartcopy node %q: expected 1 input / >=1 output, got %d/%d", node.ID, len(ins), len(outs))
	}
	t := perfTrackerFrom(ctx)

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

	encOpts, err := buildBoundaryEncoderOptions(si, srcTB, node.Params)
	if err != nil {
		return fmt.Errorf("smartcopy node %q: %w", node.ID, err)
	}

	sc := &smartCopyState{
		r:        r,
		node:     node,
		ctx:      ctx,
		outs:     outs,
		t:        t,
		input:    input,
		srcIdx:   srcIdx,
		srcTB:    srcTB,
		startPTS: startPTS,
		endPTS:   endPTS,
		encOpts:  encOpts,
		lastDTS:  math.MinInt64,
	}

	in := ins[0]
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
	// Flush the final buffered GOP (its trailing boundary is EOF).
	return sc.finish()
}

// smartCopyState carries the per-run GOP state machine for handleSmartCopy.
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
	encOpts  av.EncoderOptions

	// cur holds the packets of the GOP currently being accumulated (owned
	// by the state until classified). curStart is its keyframe PTS.
	cur      []*av.Packet
	curStart int64
	done     bool // window end passed; drop everything after

	// prev holds cloned packets of the immediately preceding GOP. When a
	// boundary GOP is re-encoded, prev is fed to the decoder first so that
	// leading frames referencing the previous GOP (open-GOP sources) decode
	// with their references present. Frames from prev are all before the
	// window and are dropped from the encode.
	prev []*av.Packet

	lastDTS    int64 // last emitted DTS (source TB) for the monotonic guard
	dtsClamped bool  // whether the guard ever had to bump a DTS
}

// consume routes one demuxer packet into the GOP accumulator.
func (s *smartCopyState) consume(pkt *av.Packet) error {
	if pkt.StreamIndex() != s.srcIdx {
		// Not our video stream (should not happen — source routes only the
		// selected stream here). Drop.
		pkt.Close()
		return nil
	}
	if pkt.IsKeyFrame() {
		// A keyframe closes the previous GOP and opens a new one.
		if len(s.cur) > 0 {
			if err := s.classify(s.curStart, pkt.PTS()); err != nil {
				return err
			}
		}
		if s.done {
			pkt.Close()
			return nil
		}
		s.cur = []*av.Packet{pkt}
		s.curStart = pkt.PTS()
		return nil
	}
	if len(s.cur) == 0 || s.done {
		// Non-keyframe with no open GOP (e.g. leading packets after a seek
		// before the first keyframe) — nothing can reference it cleanly.
		pkt.Close()
		return nil
	}
	s.cur = append(s.cur, pkt)
	return nil
}

// finish classifies the last open GOP at EOF and releases retained state.
func (s *smartCopyState) finish() error {
	var err error
	if len(s.cur) > 0 {
		err = s.classify(s.curStart, math.MaxInt64)
	}
	closeAll(s.prev)
	s.prev = nil
	return err
}

// classify decides the fate of the accumulated GOP [gStart, gEnd) and emits
// it. It consumes s.cur (frees or forwards every packet) and resets it, then
// retains a clone of the GOP as s.prev for priming the next boundary decode.
func (s *smartCopyState) classify(gStart, gEnd int64) error {
	cur := s.cur
	s.cur = nil
	prevClones := cloneAll(cur) // retained as s.prev after this GOP
	var err error
	switch {
	case gEnd <= s.startPTS:
		// Entirely before the window.
		closeAll(cur)
	case gStart >= s.endPTS:
		// Entirely at/after the window end.
		closeAll(cur)
		s.done = true
	case gStart >= s.startPTS && gEnd <= s.endPTS:
		// Fully interior — copy verbatim (no transcode).
		err = s.emitCopy(cur)
	default:
		// Boundary GOP (HEAD and/or TAIL): re-encode the in-window frames,
		// priming the decoder with the previous GOP for reference safety.
		err = s.emitBoundary(gStart, cur)
	}
	closeAll(s.prev)
	s.prev = prevClones
	return err
}

// emitCopy forwards interior-GOP packets unchanged.
func (s *smartCopyState) emitCopy(pkts []*av.Packet) error {
	for _, p := range pkts {
		if err := s.send(p); err != nil {
			return err
		}
	}
	return nil
}

// emitBoundary decodes the boundary GOP, keeps frames whose PTS falls inside
// [startPTS, endPTS), forces the first kept frame to a keyframe, re-encodes,
// and emits the fresh packets. A fresh decoder+encoder is used per boundary
// GOP: there are at most two per clip and each GOP starts on a keyframe, so
// this is both correct (no stale reference state) and cheap.
func (s *smartCopyState) emitBoundary(gopStart int64, pkts []*av.Packet) error {
	defer closeAll(pkts)

	dec, err := av.OpenDecoder(s.input, s.srcIdx)
	if err != nil {
		return fmt.Errorf("smartcopy %q: open boundary decoder: %w", s.node.ID, err)
	}
	defer dec.Close()

	// Prime references from the previous GOP (handles open-GOP sources whose
	// leading frames reference across the boundary). Primed frames are decoded
	// for reference only and dropped: keep only frames belonging to THIS
	// boundary GOP (pts >= gopStart) that also fall inside the window.
	prime := make([]*av.Packet, 0, len(s.prev)+len(pkts))
	prime = append(prime, s.prev...)
	prime = append(prime, pkts...)
	lo := gopStart
	if s.startPTS > lo {
		lo = s.startPTS
	}

	// Decode the whole GOP; keep the in-window frames in presentation order.
	kept, err := s.decodeWindow(dec, prime, lo)
	if err != nil {
		return err
	}
	if len(kept) == 0 {
		return nil
	}

	enc, err := av.OpenEncoder(s.encOpts)
	if err != nil {
		closeFrames(kept)
		return fmt.Errorf("smartcopy %q: open boundary encoder: %w", s.node.ID, err)
	}
	defer enc.Close()

	for i, f := range kept {
		if i == 0 {
			f.SetPictType(av.PictureTypeI) // force IDR at the segment start
		}
		if err := enc.SendFrame(f); err != nil {
			closeFrames(kept[i:])
			return fmt.Errorf("smartcopy %q: encode frame: %w", s.node.ID, err)
		}
		f.Close()
		if err := s.drainEncoder(enc); err != nil {
			return err
		}
	}
	if err := enc.Flush(); err != nil && !av.IsEOF(err) && !av.IsEAgain(err) {
		return fmt.Errorf("smartcopy %q: flush encoder: %w", s.node.ID, err)
	}
	return s.drainEncoder(enc)
}

// decodeWindow feeds every GOP packet to dec and returns the decoded frames
// whose PTS is inside [startPTS, endPTS), in presentation order.
func (s *smartCopyState) decodeWindow(dec *av.DecoderContext, pkts []*av.Packet, lo int64) ([]*av.Frame, error) {
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
			if pts != math.MinInt64 && pts >= lo && pts < s.endPTS {
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

// drainEncoder emits every packet currently available from enc.
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
		if err := s.send(p); err != nil {
			return err
		}
	}
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

	globalHeader := smartParamBool(params, "smartcopy_global_header", true)

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
		GlobalHeader:      globalHeader,
		ExtraOpts:         extra,
	}
	if opts.FrameRate[1] <= 0 {
		opts.FrameRate = si.RFrameRate
	}
	return opts, nil
}

// usToTB converts microseconds to the given time_base's units.
func usToTB(us int64, tb [2]int) int64 {
	if tb[0] <= 0 || tb[1] <= 0 {
		return 0
	}
	return us * int64(tb[1]) / (int64(tb[0]) * 1_000_000)
}

func smartParamBool(m map[string]any, key string, def bool) bool {
	if m == nil {
		return def
	}
	switch v := m[key].(type) {
	case bool:
		return v
	case string:
		return v == "true" || v == "1"
	case float64:
		return v != 0
	default:
		return def
	}
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
