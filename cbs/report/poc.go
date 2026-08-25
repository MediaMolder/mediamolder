// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later
//
// Picture Order Count derivation for the slice summaries:
// Rec. ITU-T H.264 §8.2.1 (all three pic_order_cnt_type modes) and
// Rec. ITU-T H.265 §8.3.1. The tracker sees every unit in decode order —
// parameter sets included, from extradata onward — and keeps the "previous
// picture" state the derivations need. Multiple slices of one picture
// recompute the same value (the update steps are idempotent for equal
// inputs), so no picture-boundary detection is required.

package report

import "github.com/MediaMolder/MediaMolder/cbs"

type pocState struct {
	// Parameter sets observed in decode order (the report layer's own
	// tables; the cbs contexts are not exposed).
	h264SPS map[uint8]*cbs.H264RawSPS
	h264PPS map[uint8]*cbs.H264RawPPS
	h265SPS map[uint8]*cbs.H265RawSPS
	h265PPS map[uint8]*cbs.H265RawPPS

	// H.264 §8.2.1 state.
	h264HasPrev        bool
	prevPicOrderCntMsb int32 // type 0: from the previous reference picture
	prevPicOrderCntLsb int32
	prevFrameNumOffset int32 // types 1 and 2
	prevFrameNum       int32

	// H.265 §8.3.1 state: the previous TemporalId==0 picture that is not
	// RASL, RADL or sub-layer non-reference.
	h265HasPrev    bool
	prevTid0PocLsb int32
	prevTid0PocMsb int32

	// The current picture (from its independent slice segment), reused
	// by dependent slice segments of the same picture.
	h265HaveCurPic bool
	h265CurPicPOC  int32
}

func newPOCState() *pocState {
	return &pocState{
		h264SPS: map[uint8]*cbs.H264RawSPS{},
		h264PPS: map[uint8]*cbs.H264RawPPS{},
		h265SPS: map[uint8]*cbs.H265RawSPS{},
		h265PPS: map[uint8]*cbs.H265RawPPS{},
	}
}

// observePS registers parameter sets; call for every unit in decode order.
func (p *pocState) observePS(u *cbs.Unit) {
	switch c := u.Content.(type) {
	case *cbs.H264RawSPS:
		p.h264SPS[c.SeqParameterSetID] = c
	case *cbs.H264RawPPS:
		p.h264PPS[c.PicParameterSetID] = c
	case *cbs.H265RawSPS:
		p.h265SPS[c.SpsSeqParameterSetID] = c
	case *cbs.H265RawPPS:
		p.h265PPS[c.PpsPicParameterSetID] = c
	}
}

// h264HasMMCO5 reports a memory_management_control_operation equal to 5
// in the slice's dec_ref_pic_marking (terminated by op 0).
func h264HasMMCO5(sh *cbs.H264RawSliceHeader) bool {
	if sh.AdaptiveRefPicMarkingModeFlag == 0 {
		return false
	}
	for i := range sh.Mmco {
		switch sh.Mmco[i].MemoryManagementControlOperation {
		case 0:
			return false
		case 5:
			return true
		}
	}
	return false
}

// h264SlicePOC derives PicOrderCnt for the slice's picture and advances
// the tracker state, per Rec. ITU-T H.264 §8.2.1. ok is false when the
// referenced parameter sets are unknown.
func (p *pocState) h264SlicePOC(sh *cbs.H264RawSliceHeader) (poc int32, ok bool) {
	// Auxiliary slices share the primary picture's POC; they carry no
	// usable dec_ref_pic_marking of their own — skip.
	if sh.NalUnitHeader.NalUnitType == 19 {
		return 0, false
	}
	pps := p.h264PPS[sh.PicParameterSetID]
	if pps == nil {
		return 0, false
	}
	sps := p.h264SPS[pps.SeqParameterSetID]
	if sps == nil {
		return 0, false
	}

	idr := sh.NalUnitHeader.NalUnitType == 5
	isRef := sh.NalUnitHeader.NalRefIdc != 0
	mmco5 := isRef && !idr && h264HasMMCO5(sh)
	frameNum := int32(sh.FrameNum)

	var top, bottom int32
	hasTop := sh.FieldPicFlag == 0 || sh.BottomFieldFlag == 0
	hasBottom := sh.FieldPicFlag == 0 || sh.BottomFieldFlag != 0

	var t0msb, t0lsb int32 // type-0 reference state, applied post-mmco5

	switch sps.PicOrderCntType {
	case 0: // §8.2.1.1
		maxLsb := int32(1) << (sps.Log2MaxPicOrderCntLsbMinus4 + 4)
		lsb := int32(sh.PicOrderCntLsb)
		prevMsb, prevLsb := p.prevPicOrderCntMsb, p.prevPicOrderCntLsb
		if idr || !p.h264HasPrev {
			prevMsb, prevLsb = 0, 0
		}
		var msb int32
		switch {
		case lsb < prevLsb && prevLsb-lsb >= maxLsb/2:
			msb = prevMsb + maxLsb
		case lsb > prevLsb && lsb-prevLsb > maxLsb/2:
			msb = prevMsb - maxLsb
		default:
			msb = prevMsb
		}
		if hasTop {
			top = msb + lsb
		}
		if hasBottom {
			if sh.FieldPicFlag == 0 {
				bottom = msb + lsb + sh.DeltaPicOrderCntBottom
			} else {
				bottom = msb + lsb
			}
		}
		t0msb, t0lsb = msb, lsb

	case 1: // §8.2.1.2
		frameNumOffset := p.frameNumOffset(idr, frameNum, sps)
		cycle := int32(sps.NumRefFramesInPicOrderCntCycle)
		var absFrameNum int32
		if cycle != 0 {
			absFrameNum = frameNumOffset + frameNum
		}
		if !isRef && absFrameNum > 0 {
			absFrameNum--
		}
		var expected int32
		if absFrameNum > 0 {
			var deltaPerCycle int32
			for i := int32(0); i < cycle; i++ {
				deltaPerCycle += sps.OffsetForRefFrame[i]
			}
			cycleCnt := (absFrameNum - 1) / cycle
			inCycle := (absFrameNum - 1) % cycle
			expected = cycleCnt * deltaPerCycle
			for i := int32(0); i <= inCycle; i++ {
				expected += sps.OffsetForRefFrame[i]
			}
		}
		if !isRef {
			expected += sps.OffsetForNonRefPic
		}
		if hasTop {
			top = expected + sh.DeltaPicOrderCnt[0]
		}
		if sh.FieldPicFlag == 0 {
			bottom = top + sps.OffsetForTopToBottomField + sh.DeltaPicOrderCnt[1]
		} else if hasBottom {
			bottom = expected + sps.OffsetForTopToBottomField + sh.DeltaPicOrderCnt[0]
		}
		p.saveFrameNumState(mmco5, frameNum, frameNumOffset)

	case 2: // §8.2.1.3
		frameNumOffset := p.frameNumOffset(idr, frameNum, sps)
		var temp int32
		switch {
		case idr:
			temp = 0
		case !isRef:
			temp = 2*(frameNumOffset+frameNum) - 1
		default:
			temp = 2 * (frameNumOffset + frameNum)
		}
		top, bottom = temp, temp
		p.saveFrameNumState(mmco5, frameNum, frameNumOffset)

	default:
		return 0, false
	}

	// §8.2.1: a picture with mmco5 has its order counts reset — the
	// reported PicOrderCnt becomes 0 and prev state derives from the
	// post-reset values.
	if mmco5 {
		temp := pocOf(hasTop, top, hasBottom, bottom)
		if hasTop {
			top -= temp
		}
		if hasBottom {
			bottom -= temp
		}
	}

	if sps.PicOrderCntType == 0 && isRef {
		if mmco5 {
			p.prevPicOrderCntMsb = 0
			if sh.FieldPicFlag != 0 && sh.BottomFieldFlag != 0 {
				p.prevPicOrderCntLsb = 0
			} else {
				p.prevPicOrderCntLsb = top // post-reset TopFieldOrderCnt
			}
		} else {
			p.prevPicOrderCntMsb = t0msb
			p.prevPicOrderCntLsb = t0lsb
		}
		p.h264HasPrev = true
	}

	return pocOf(hasTop, top, hasBottom, bottom), true
}

// frameNumOffset is the FrameNumOffset derivation shared by pic_order_cnt
// types 1 and 2 (§8.2.1.2 / §8.2.1.3).
func (p *pocState) frameNumOffset(idr bool, frameNum int32, sps *cbs.H264RawSPS) int32 {
	if idr {
		return 0
	}
	maxFrameNum := int32(1) << (sps.Log2MaxFrameNumMinus4 + 4)
	if !p.h264HasPrev {
		return 0
	}
	if p.prevFrameNum > frameNum {
		return p.prevFrameNumOffset + maxFrameNum
	}
	return p.prevFrameNumOffset
}

func (p *pocState) saveFrameNumState(mmco5 bool, frameNum, frameNumOffset int32) {
	if mmco5 {
		p.prevFrameNumOffset = 0
		p.prevFrameNum = 0
	} else {
		p.prevFrameNumOffset = frameNumOffset
		p.prevFrameNum = frameNum
	}
	p.h264HasPrev = true
}

// pocOf is PicOrderCnt: the field's own order count for a field picture,
// Min(TopFieldOrderCnt, BottomFieldOrderCnt) for a frame.
func pocOf(hasTop bool, top int32, hasBottom bool, bottom int32) int32 {
	switch {
	case hasTop && hasBottom:
		return min32(top, bottom)
	case hasTop:
		return top
	default:
		return bottom
	}
}

func min32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

// H.265 NAL unit type predicates (Table 7-1).
func h265IsIDR(t uint8) bool        { return t == 19 || t == 20 } // IDR_W_RADL, IDR_N_LP
func h265IsBLA(t uint8) bool        { return t >= 16 && t <= 18 }
func h265IsIRAP(t uint8) bool       { return t >= 16 && t <= 23 }
func h265IsRADLOrRASL(t uint8) bool { return t >= 6 && t <= 9 }

// h265IsSLNR reports a sub-layer non-reference picture: the *_N VCL types.
func h265IsSLNR(t uint8) bool {
	return t <= 14 && t%2 == 0 // TRAIL_N, TSA_N, STSA_N, RADL_N, RASL_N, VCL_N10/12/14
}

// h265SlicePOC derives PicOrderCntVal per Rec. ITU-T H.265 §8.3.1 and
// advances the prevTid0 state. NoRaslOutputFlag is approximated as true
// for IDR and BLA pictures and for the first IRAP of the stream (a
// mid-stream CRA uses the normal derivation, which matches conformant
// streams that continue across it).
func (p *pocState) h265SlicePOC(sh *cbs.H265RawSliceHeader) (poc int32, ok bool) {
	pps := p.h265PPS[sh.SlicePicParameterSetID]
	if pps == nil {
		return 0, false
	}
	sps := p.h265SPS[pps.PpsSeqParameterSetID]
	if sps == nil {
		return 0, false
	}

	// A dependent slice segment carries no slice_pic_order_cnt_lsb (the
	// struct field is the zero value): it belongs to the same picture as
	// the preceding independent segment. Reuse that POC and leave the
	// prevTid0 state untouched.
	if sh.DependentSliceSegmentFlag != 0 {
		if p.h265HaveCurPic {
			return p.h265CurPicPOC, true
		}
		return 0, false
	}

	typ := sh.NalUnitHeader.NalUnitType
	maxLsb := int32(1) << (sps.Log2MaxPicOrderCntLsbMinus4 + 4)
	lsb := int32(sh.SlicePicOrderCntLsb) // inferred 0 for IDR (not present)

	noRaslOutput := h265IsIDR(typ) || h265IsBLA(typ) ||
		(h265IsIRAP(typ) && !p.h265HasPrev)

	var msb int32
	if !noRaslOutput {
		prevMsb, prevLsb := p.prevTid0PocMsb, p.prevTid0PocLsb
		switch {
		case lsb < prevLsb && prevLsb-lsb >= maxLsb/2:
			msb = prevMsb + maxLsb
		case lsb > prevLsb && lsb-prevLsb > maxLsb/2:
			msb = prevMsb - maxLsb
		default:
			msb = prevMsb
		}
	}
	poc = msb + lsb

	temporalID := sh.NalUnitHeader.NuhTemporalIDPlus1 - 1
	if temporalID == 0 && !h265IsRADLOrRASL(typ) && !h265IsSLNR(typ) {
		p.prevTid0PocLsb = lsb
		p.prevTid0PocMsb = msb
		p.h265HasPrev = true
	}
	p.h265CurPicPOC = poc
	p.h265HaveCurPic = true
	return poc, true
}
