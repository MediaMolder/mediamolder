// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package report

import (
	"testing"

	"github.com/MediaMolder/MediaMolder/cbs"
)

// h264Env registers a minimal SPS/PPS pair and returns a slice-header
// factory for the given POC type.
func h264Env(p *pocState, pocType uint8, mut func(*cbs.H264RawSPS)) func(nalType, refIdc uint8, frameNum uint16, lsb uint16) *cbs.H264RawSliceHeader {
	sps := &cbs.H264RawSPS{
		PicOrderCntType:             pocType,
		Log2MaxPicOrderCntLsbMinus4: 0, // MaxPicOrderCntLsb = 16
		Log2MaxFrameNumMinus4:       0, // MaxFrameNum = 16
	}
	if mut != nil {
		mut(sps)
	}
	pps := &cbs.H264RawPPS{PicParameterSetID: 0, SeqParameterSetID: 0}
	p.h264SPS[0] = sps
	p.h264PPS[0] = pps
	return func(nalType, refIdc uint8, frameNum, lsb uint16) *cbs.H264RawSliceHeader {
		return &cbs.H264RawSliceHeader{
			NalUnitHeader:  cbs.H264RawNALUnitHeader{NalUnitType: nalType, NalRefIdc: refIdc},
			FrameNum:       frameNum,
			PicOrderCntLsb: lsb,
		}
	}
}

func mustPOC(t *testing.T, p *pocState, sh *cbs.H264RawSliceHeader) int32 {
	t.Helper()
	poc, ok := p.h264SlicePOC(sh)
	if !ok {
		t.Fatal("h264SlicePOC not derivable")
	}
	return poc
}

func TestH264POCType0(t *testing.T) {
	p := newPOCState()
	slice := h264Env(p, 0, nil)

	cases := []struct {
		nal, ref uint8
		lsb      uint16
		want     int32
	}{
		{5, 3, 0, 0}, // IDR
		{1, 2, 4, 4}, // P ref
		{1, 0, 2, 2}, // B non-ref (state unchanged)
		{1, 2, 8, 8}, // P ref
		{1, 2, 14, 14},
		{1, 2, 2, 18}, // lsb wrapped: msb += 16
		{1, 0, 0, 16}, // non-ref between, still on the wrapped msb
		{1, 2, 6, 22}, //
		{5, 3, 0, 0},  // IDR resets everything
		{1, 2, 4, 4},
	}
	for i, c := range cases {
		if got := mustPOC(t, p, slice(c.nal, c.ref, uint16(i), c.lsb)); got != c.want {
			t.Fatalf("step %d (lsb %d): poc %d, want %d", i, c.lsb, got, c.want)
		}
	}
}

func TestH264POCType0NegativeWrap(t *testing.T) {
	p := newPOCState()
	slice := h264Env(p, 0, nil)
	mustPOC(t, p, slice(5, 3, 0, 0))
	mustPOC(t, p, slice(1, 2, 0, 2)) // ref at lsb 2
	// Non-ref with lsb 12: lsb - prevLsb = 10 > 8 → msb -= 16 → poc -4.
	if got := mustPOC(t, p, slice(1, 0, 1, 12)); got != -4 {
		t.Fatalf("negative wrap: poc %d, want -4", got)
	}
}

func TestH264POCType2(t *testing.T) {
	p := newPOCState()
	slice := h264Env(p, 2, nil)

	if got := mustPOC(t, p, slice(5, 3, 0, 0)); got != 0 {
		t.Fatalf("IDR: %d", got)
	}
	if got := mustPOC(t, p, slice(1, 2, 1, 0)); got != 2 {
		t.Fatalf("P fn=1: %d", got)
	}
	if got := mustPOC(t, p, slice(1, 0, 2, 0)); got != 3 { // non-ref: 2*fn-1
		t.Fatalf("non-ref fn=2: %d", got)
	}
	if got := mustPOC(t, p, slice(1, 2, 15, 0)); got != 30 {
		t.Fatalf("P fn=15: %d", got)
	}
	// frame_num wraps 15 → 0: FrameNumOffset += MaxFrameNum (16).
	if got := mustPOC(t, p, slice(1, 2, 0, 0)); got != 32 {
		t.Fatalf("wrapped fn=0: %d", got)
	}
}

func TestH264POCType1(t *testing.T) {
	p := newPOCState()
	slice := h264Env(p, 1, func(sps *cbs.H264RawSPS) {
		sps.NumRefFramesInPicOrderCntCycle = 1
		sps.OffsetForRefFrame[0] = 2
		sps.OffsetForNonRefPic = -1
	})

	if got := mustPOC(t, p, slice(5, 3, 0, 0)); got != 0 {
		t.Fatalf("IDR: %d", got)
	}
	if got := mustPOC(t, p, slice(1, 2, 1, 0)); got != 2 { // absFrameNum 1 → one cycle offset
		t.Fatalf("P fn=1: %d", got)
	}
	if got := mustPOC(t, p, slice(1, 0, 2, 0)); got != 1 { // non-ref: absFrameNum 1, + offset_for_non_ref_pic
		t.Fatalf("non-ref fn=2: %d", got)
	}
	if got := mustPOC(t, p, slice(1, 2, 3, 0)); got != 6 { // absFrameNum 3
		t.Fatalf("P fn=3: %d", got)
	}
}

func TestH264POCMissingPS(t *testing.T) {
	p := newPOCState()
	sh := &cbs.H264RawSliceHeader{PicParameterSetID: 9}
	if _, ok := p.h264SlicePOC(sh); ok {
		t.Fatal("POC derived without parameter sets")
	}
}

func h265Env(p *pocState) func(nalType, tidPlus1 uint8, lsb uint16) *cbs.H265RawSliceHeader {
	p.h265SPS[0] = &cbs.H265RawSPS{Log2MaxPicOrderCntLsbMinus4: 0} // MaxLsb = 16
	p.h265PPS[0] = &cbs.H265RawPPS{}
	return func(nalType, tidPlus1 uint8, lsb uint16) *cbs.H265RawSliceHeader {
		return &cbs.H265RawSliceHeader{
			NalUnitHeader: cbs.H265RawNALUnitHeader{
				NalUnitType: nalType, NuhTemporalIDPlus1: tidPlus1},
			SlicePicOrderCntLsb: lsb,
		}
	}
}

func TestH265POC(t *testing.T) {
	p := newPOCState()
	slice := h265Env(p)
	mp := func(sh *cbs.H265RawSliceHeader) int32 {
		t.Helper()
		poc, ok := p.h265SlicePOC(sh)
		if !ok {
			t.Fatal("h265SlicePOC not derivable")
		}
		return poc
	}

	if got := mp(slice(19, 1, 0)); got != 0 { // IDR_W_RADL
		t.Fatalf("IDR: %d", got)
	}
	if got := mp(slice(1, 1, 4)); got != 4 { // TRAIL_R tid0
		t.Fatalf("TRAIL_R: %d", got)
	}
	// TRAIL_N at tid1 (sub-layer non-ref): derived but must NOT become
	// the prevTid0 picture.
	if got := mp(slice(0, 2, 2)); got != 2 {
		t.Fatalf("TRAIL_N: %d", got)
	}
	if got := mp(slice(1, 1, 6)); got != 6 {
		t.Fatalf("TRAIL_R lsb 6: %d", got)
	}
	// lsb 6 → 14: delta 8 is not > MaxLsb/2, so no wrap yet.
	if got := mp(slice(1, 1, 14)); got != 14 {
		t.Fatalf("TRAIL_R lsb 14: %d", got)
	}
	if got := mp(slice(1, 1, 2)); got != 18 { // wrap: msb += 16
		t.Fatalf("wrap: %d", got)
	}
	// Mid-stream CRA continues the normal derivation.
	if got := mp(slice(21, 1, 6)); got != 22 {
		t.Fatalf("CRA: %d", got)
	}
	// BLA always resets msb.
	if got := mp(slice(16, 1, 4)); got != 4 {
		t.Fatalf("BLA: %d", got)
	}
	// IDR resets to zero.
	if got := mp(slice(20, 1, 0)); got != 0 { // IDR_N_LP
		t.Fatalf("IDR_N_LP: %d", got)
	}
}

func TestH265POCFirstCRAResets(t *testing.T) {
	p := newPOCState()
	slice := h265Env(p)
	// Stream starting at a CRA: NoRaslOutputFlag → msb forced 0.
	if poc, ok := p.h265SlicePOC(slice(21, 1, 8)); !ok || poc != 8 {
		t.Fatalf("first CRA: %d %v", poc, ok)
	}
}
