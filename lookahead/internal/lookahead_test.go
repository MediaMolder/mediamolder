// Copyright (C) 2025-2026 MediaMolder contributors.
// SPDX-License-Identifier: LGPL-2.1-or-later

package imgmath

import (
	"testing"
)

// --- lrFilter / InitLowres -------------------------------------------------

func TestLrFilter_Uniform(t *testing.T) {
	// For a uniform input, FILTER(v,v,v,v) == v.
	for _, v := range []byte{0, 64, 128, 200, 255} {
		got := lrFilter(v, v, v, v)
		if got != v {
			t.Errorf("lrFilter(%d,%d,%d,%d) = %d, want %d", v, v, v, v, got, v)
		}
	}
}

func TestLrFilter_Known(t *testing.T) {
	// FILTER(0,0,4,4) = (((0+0+1)>>1) + ((4+4+1)>>1) + 1) >> 1
	//                 = (0 + 4 + 1) >> 1 = 5>>1 = 2
	got := lrFilter(0, 0, 4, 4)
	if got != 2 {
		t.Errorf("lrFilter(0,0,4,4) = %d, want 2", got)
	}
	// FILTER(10,10,10,10) = 10
	got = lrFilter(10, 10, 10, 10)
	if got != 10 {
		t.Errorf("lrFilter(10,10,10,10) = %d, want 10", got)
	}
}

func TestInitLowres_Uniform(t *testing.T) {
	// A uniform source should produce uniform lowres planes.
	const (
		w   = 16
		h   = 16
		val = 100
	)
	src := make([]byte, w*h)
	for i := range src {
		src[i] = val
	}
	f, err := InitLowres(src, w, h, w)
	if err != nil {
		t.Fatalf("InitLowres: %v", err)
	}
	if f.Width != w/2 || f.Height != h/2 {
		t.Errorf("dimensions: got %dx%d, want %dx%d", f.Width, f.Height, w/2, h/2)
	}
	for p := 0; p < 4; p++ {
		for y := 0; y < f.Height; y++ {
			for x := 0; x < f.Width; x++ {
				got := f.planeBuf[p][f.planeOffset(x, y)]
				if got != val {
					t.Errorf("plane[%d][%d,%d] = %d, want %d", p, x, y, got, val)
				}
			}
		}
	}
}

func TestInitLowres_BorderReplication(t *testing.T) {
	// After InitLowres the padding region must match the border pixels.
	const (
		w = 8
		h = 8
	)
	src := make([]byte, w*h)
	for i := range src {
		src[i] = byte(i % 64)
	}
	f, err := InitLowres(src, w, h, w)
	if err != nil {
		t.Fatalf("InitLowres: %v", err)
	}
	// Left padding of row 0 must equal the leftmost active pixel of row 0.
	leftmost := f.planeBuf[0][f.planeOffset(0, 0)]
	for dx := 1; dx <= LowresPadH; dx++ {
		v := f.planeBuf[0][f.planeOffset(-dx, 0)]
		if v != leftmost {
			t.Errorf("left padding[-,%d,0] = %d, want %d", dx, v, leftmost)
		}
	}
	// Top padding column 0 must equal the topmost active pixel.
	top := f.planeBuf[0][f.planeOffset(0, 0)]
	for dy := 1; dy <= LowresPadV; dy++ {
		v := f.planeBuf[0][f.planeOffset(0, -dy)]
		if v != top {
			t.Errorf("top padding[0,-%d] = %d, want %d", dy, v, top)
		}
	}
}

func TestInitLowres_ErrorCases(t *testing.T) {
	if _, err := InitLowres(make([]byte, 4), 2, 2, 1); err == nil {
		t.Error("expected error for srcStride < srcW")
	}
	if _, err := InitLowres(nil, 4, 4, 4); err == nil {
		t.Error("expected error for nil src")
	}
	if _, err := InitLowres(make([]byte, 4), 1, 4, 1); err == nil {
		t.Error("expected error for srcW < 2")
	}
}

// --- SATD ------------------------------------------------------------------

func TestSATD8x8_Identical(t *testing.T) {
	// SATD of a block with itself must be 0.
	block := make([]byte, 8*8)
	for i := range block {
		block[i] = byte(i * 3)
	}
	got := SATD8x8(block, 8, block, 8)
	if got != 0 {
		t.Errorf("SATD8x8(self) = %d, want 0", got)
	}
}

func TestSATD8x8_ConstantOffset(t *testing.T) {
	// A block shifted by a constant c: SATD should be 8*8*c / 2 = 32*c
	// (DC component only, Hadamard of all-c is 4*4*c per 4×4 block after
	// transform; two 4×4 blocks per row half; two row halves = 4 blocks total.
	// 4×4 SATD for constant delta c: H-transform row produces [4c,0,0,0],
	// V-transform column produces [4*4c,0,0,0]; sum |values| = 16c, >>1 = 8c.
	// satd8x8 = 4 * 8c = 32c.)
	const c = 10
	src := make([]byte, 8*8)
	ref := make([]byte, 8*8)
	for i := range ref {
		ref[i] = c
	}
	got := SATD8x8(src, 8, ref, 8)
	want := int32(32 * c)
	if got != want {
		t.Errorf("SATD8x8(zeros, +%d) = %d, want %d", c, got, want)
	}
}

func TestSATD4x4_Known(t *testing.T) {
	// Verify a hand-computed 4×4 SATD.
	// fenc = [1,2,3,4, 5,6,7,8, 9,10,11,12, 13,14,15,16]
	// ref  = all zeros
	// Diff = fenc. Hadamard of one row [1,2,3,4]:
	//   row H: [10, -2, 0, -2]
	// Hadamard col of transposed result, then sum |.| >> 1.
	// This test just checks the function is consistent: SATD(x,x) == 0.
	block := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	got := satd4x4(block, 4, block, 4)
	if got != 0 {
		t.Errorf("satd4x4(self) = %d, want 0", got)
	}
}

// --- Intra prediction ------------------------------------------------------

func TestIntraSATD3_UniformBlock(t *testing.T) {
	// A uniform source with uniform neighbours: all three modes predict the
	// same constant, so all SATDs should be 0.
	const v = 100
	src := make([]byte, 8*8)
	for i := range src {
		src[i] = v
	}
	var top, left [8]byte
	for i := range top {
		top[i] = v
		left[i] = v
	}
	dc, h, vv := IntraSATD3_8x8c(src, 8, top, left)
	if dc != 0 || h != 0 || vv != 0 {
		t.Errorf("uniform block: dc=%d h=%d v=%d, want all 0", dc, h, vv)
	}
}

func TestIntraSATD3_BoundaryNeighbours(t *testing.T) {
	// At a border MB (neighbours = 128), costs must still be non-negative.
	src := make([]byte, 8*8)
	for i := range src {
		src[i] = byte(i)
	}
	var top, left [8]byte
	for i := range top {
		top[i] = 128
		left[i] = 128
	}
	dc, h, v := IntraSATD3_8x8c(src, 8, top, left)
	if dc < 0 || h < 0 || v < 0 {
		t.Errorf("negative SATD: dc=%d h=%d v=%d", dc, h, v)
	}
}

func TestPredict8x8cH_Pattern(t *testing.T) {
	// Each row must equal the corresponding left-column pixel.
	left := [8]byte{10, 20, 30, 40, 50, 60, 70, 80}
	var pred [64]byte
	predict8x8cH(left, &pred)
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			if pred[y*8+x] != left[y] {
				t.Errorf("H pred[%d][%d] = %d, want %d", y, x, pred[y*8+x], left[y])
			}
		}
	}
}

func TestPredict8x8cV_Pattern(t *testing.T) {
	// Every row must equal the top-row neighbours.
	top := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
	var pred [64]byte
	predict8x8cV(top, &pred)
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			if pred[y*8+x] != top[x] {
				t.Errorf("V pred[%d][%d] = %d, want %d", y, x, pred[y*8+x], top[x])
			}
		}
	}
}

// --- Motion estimation -----------------------------------------------------

func TestMvCostOne_Zero(t *testing.T) {
	if got := mvCostOne(0); got != 1 {
		t.Errorf("mvCostOne(0) = %d, want 1", got)
	}
}

func TestMvCostOne_Table(t *testing.T) {
	// Values from x264 logs table with lam=1 (round(lam * (2*log2(i+1)+1.718)))
	tests := []struct {
		d    int
		want int32
	}{
		{0, 1},
		{1, 4}, {-1, 4},
		{2, 5}, {-2, 5},
		{3, 6}, {-3, 6},
		{4, 6}, {-4, 6},
	}
	for _, tt := range tests {
		if got := mvCostOne(tt.d); got != tt.want {
			t.Errorf("mvCostOne(%d) = %d, want %d", tt.d, got, tt.want)
		}
	}
}

func TestDiamondME_IdenticalFrames(t *testing.T) {
	// Identical frames: ME should find MV=(0,0) via the fast-skip path.
	// Fast skip triggers when SATD(0,0) < 64; for identical frames SATD=0.
	src := makeTestFrame(64, 64)
	fenc, err := InitLowres(src, 64, 64, 64)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := InitLowres(src, 64, 64, 64)
	if err != nil {
		t.Fatal(err)
	}
	mvx, mvy, cost := DiamondME(fenc, ref, 2, 2, 0, 0) // interior MB, zero MVP
	if mvx != 0 || mvy != 0 {
		t.Errorf("identical frames: MV=(%d,%d), want (0,0)", mvx, mvy)
	}
	// Fast-skip path: cost = pure SATD = 0 (no bit-cost adjustments).
	if cost != 0 {
		t.Errorf("identical frames fast-skip cost = %d, want 0", cost)
	}
}

func TestDiamondME_ShiftedFrame(t *testing.T) {
	// A frame shifted by (4,0) in full-res → (2,0) at lowres.
	// ME should find MV=(2,0) or nearby.
	const shift = 2 // lowres pixels
	srcW, srcH := 64, 64
	src := makeTestFrame(srcW, srcH)

	// Shift src right by shift*2 full-res pixels to produce the reference.
	shifted := make([]byte, srcW*srcH)
	for y := 0; y < srcH; y++ {
		for x := 0; x < srcW; x++ {
			sx := x - shift*2
			if sx < 0 {
				sx = 0
			}
			shifted[y*srcW+x] = src[y*srcW+sx]
		}
	}

	fenc, err := InitLowres(src, srcW, srcH, srcW)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := InitLowres(shifted, srcW, srcH, srcW)
	if err != nil {
		t.Fatal(err)
	}

	// Use a central MB where the shift is well-defined.
	mvx, mvy, _ := DiamondME(fenc, ref, 2, 2, 0, 0) // zero MVP
	_ = mvy
	// Accept ±1 tolerance for the horizontal component.
	if mvx < shift-1 || mvx > shift+1 {
		t.Errorf("shifted frame: MV.x=%d, want ≈%d", mvx, shift)
	}
}

func TestLowresFrameCost_IntraCaching(t *testing.T) {
	// Calling LowresFrameCost twice should return the same iCost (via cache).
	src := makeTestFrame(32, 32)
	fenc, err := InitLowres(src, 32, 32, 32)
	if err != nil {
		t.Fatal(err)
	}
	_, iCost1 := LowresFrameCost(fenc, nil, -1)
	_, iCost2 := LowresFrameCost(fenc, nil, -1)
	if iCost1 != iCost2 {
		t.Errorf("iCost mismatch: %d vs %d", iCost1, iCost2)
	}
}

func TestLowresFrameCost_PVsI(t *testing.T) {
	// For identical frames, pCost must be ≤ iCost (inter at least as good as intra).
	src := makeTestFrame(32, 32)
	fenc, _ := InitLowres(src, 32, 32, 32)
	ref, _ := InitLowres(src, 32, 32, 32)
	pCost, iCost := LowresFrameCost(fenc, ref, 0)
	if pCost > iCost {
		t.Errorf("identical frames: pCost (%d) > iCost (%d)", pCost, iCost)
	}
}

// makeTestFrame produces a simple gradient pattern for testing.
func makeTestFrame(w, h int) []byte {
	src := make([]byte, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			src[y*w+x] = byte((x*3 + y*5) & 0xFF)
		}
	}
	return src
}

// --- M2: frame_cost.go / spatial predictor --------------------------------

func TestMedianInt(t *testing.T) {
	tests := []struct{ a, b, c, want int }{
		{1, 2, 3, 2}, {3, 2, 1, 2}, {2, 1, 3, 2},
		{5, 5, 5, 5}, {0, 0, 10, 0}, {10, 0, 0, 0},
		{-1, 0, 1, 0}, {-3, -1, -2, -2},
	}
	for _, tt := range tests {
		if got := medianInt(tt.a, tt.b, tt.c); got != tt.want {
			t.Errorf("medianInt(%d,%d,%d) = %d, want %d", tt.a, tt.b, tt.c, got, tt.want)
		}
	}
}

func TestComputeSpatialMVP_NoNeighbours(t *testing.T) {
	// Top-left MB: no reverse-scan neighbours have been computed yet.
	// Use 48×48 source → 24×24 lowres → 3×3 MB grid.
	src := makeTestFrame(48, 48)
	f, _ := InitLowres(src, 48, 48, 48)
	// Ensure mvCache is allocated but all entries are uninitMVX.
	f.ensureMVCache(0)
	pmvx, pmvy := computeSpatialMVP(f, f.mvCache[0], 0, 0)
	if pmvx != 0 || pmvy != 0 {
		t.Errorf("no neighbours: MVP=(%d,%d), want (0,0)", pmvx, pmvy)
	}
}

func TestComputeSpatialMVP_SingleNeighbour(t *testing.T) {
	// One computed right-neighbour with MV (3, -2): MVP should equal it.
	// 48×48 source → 24×24 lowres → 3×3 MB grid (MBW=MBH=3).
	src := makeTestFrame(48, 48)
	f, _ := InitLowres(src, 48, 48, 48)
	f.ensureMVCache(0)
	// Simulate the right neighbour of (1,1) — which is (2,1) — having been computed.
	rightNb := 1*f.MBW + 2 // MB (2, 1)
	f.mvCache[0][rightNb] = [2]int16{3, -2}
	pmvx, pmvy := computeSpatialMVP(f, f.mvCache[0], 1, 1)
	if pmvx != 3 || pmvy != -2 {
		t.Errorf("single neighbour: MVP=(%d,%d), want (3,-2)", pmvx, pmvy)
	}
}

func TestComputeSpatialMVP_MedianOfThree(t *testing.T) {
	// Three neighbours: right=(4,0), below=(2,0), below-left=(6,0).
	// Median x: median(4,2,6)=4; median y: median(0,0,0)=0. MVP=(4,0).
	// 64×64 source → 32×32 lowres → 4×4 MB grid (MBW=MBH=4).
	// MB (2,1): right=(3,1), below=(2,2), below-left=(1,2) — all valid.
	src := makeTestFrame(64, 64)
	f, _ := InitLowres(src, 64, 64, 64)
	f.ensureMVCache(0)
	mbw := f.MBW                           // = 4
	f.mvCache[0][1*mbw+3] = [2]int16{4, 0} // right  (3,1)
	f.mvCache[0][2*mbw+2] = [2]int16{2, 0} // below  (2,2)
	f.mvCache[0][2*mbw+1] = [2]int16{6, 0} // below-left (1,2)
	pmvx, pmvy := computeSpatialMVP(f, f.mvCache[0], 2, 1)
	if pmvx != 4 || pmvy != 0 {
		t.Errorf("median-3: MVP=(%d,%d), want (4,0)", pmvx, pmvy)
	}
}

func TestDiamondME_FastSkip(t *testing.T) {
	// Fast skip triggers when SATD(0,0) < 64 with zero MVP.
	// Identical frames guarantee SATD=0 < 64.
	src := makeTestFrame(32, 32)
	fenc, _ := InitLowres(src, 32, 32, 32)
	ref, _ := InitLowres(src, 32, 32, 32)
	_, _, cost := DiamondME(fenc, ref, 1, 1, 0, 0)
	// Fast-skip path returns pure SATD = 0 with no adjustments.
	if cost != 0 {
		t.Errorf("fast-skip identical frames: cost=%d, want 0", cost)
	}
}

func TestDiamondME_NonZeroMVP(t *testing.T) {
	// With a non-zero MVP, the search should still find a good MV.
	// Shifted frame: content moved right by 2 lowres pixels.
	const shift = 2
	srcW, srcH := 64, 64
	src := makeTestFrame(srcW, srcH)
	shifted := make([]byte, srcW*srcH)
	for y := 0; y < srcH; y++ {
		for x := 0; x < srcW; x++ {
			sx := x - shift*2
			if sx < 0 {
				sx = 0
			}
			shifted[y*srcW+x] = src[y*srcW+sx]
		}
	}
	fenc, _ := InitLowres(src, srcW, srcH, srcW)
	ref, _ := InitLowres(shifted, srcW, srcH, srcW)
	// Provide MVP=(1,0) — slightly off from the true shift (2,0).
	// The diamond search should converge to (2,0) ± 1.
	mvx, _, _ := DiamondME(fenc, ref, 2, 2, 1, 0)
	if mvx < shift-1 || mvx > shift+1 {
		t.Errorf("non-zero MVP: MV.x=%d, want ≈%d", mvx, shift)
	}
}

func TestFrameCost_IdenticalFrames(t *testing.T) {
	// For identical frames, interCost must be ≤ intraCost.
	src := makeTestFrame(32, 32)
	ref, _ := InitLowres(src, 32, 32, 32)
	cur, _ := InitLowres(src, 32, 32, 32)
	interCost, intraCost := FrameCost(ref, cur, 0)
	if interCost > intraCost {
		t.Errorf("identical frames: interCost(%d) > intraCost(%d)", interCost, intraCost)
	}
}

func TestFrameCost_Caching(t *testing.T) {
	// Two calls with the same lag must return the same results.
	src := makeTestFrame(32, 32)
	src2 := makeTestFrame(32, 32)
	for i := range src2 {
		src2[i] ^= 0x55
	}
	ref, _ := InitLowres(src, 32, 32, 32)
	cur, _ := InitLowres(src2, 32, 32, 32)
	ip1, ic1 := FrameCost(ref, cur, 0)
	ip2, ic2 := FrameCost(ref, cur, 0)
	if ip1 != ip2 || ic1 != ic2 {
		t.Errorf("caching mismatch: (%d,%d) vs (%d,%d)", ip1, ic1, ip2, ic2)
	}
}

func TestFrameCost_DifferentLags(t *testing.T) {
	// The same cur frame with different lags uses different MV cache slots.
	src := makeTestFrame(32, 32)
	src2 := makeTestFrame(32, 32)
	for i := range src2 {
		src2[i] ^= 0x33
	}
	ref, _ := InitLowres(src, 32, 32, 32)
	cur, _ := InitLowres(src2, 32, 32, 32)
	ip0, ic0 := FrameCost(ref, cur, 0)
	ip1, ic1 := FrameCost(ref, cur, 1)
	// intraCost must be identical regardless of lag (only depends on cur).
	if ic0 != ic1 {
		t.Errorf("intraCost differs by lag: %d vs %d", ic0, ic1)
	}
	// interCost may differ (different MV cache slots, same actual search).
	if ip0 != ip1 {
		t.Logf("note: interCost differs by lag slot (%d vs %d) — expected if MVs differ", ip0, ip1)
	}
}

func TestUniformCrossBrightnessRatio(t *testing.T) {
	light := make([]byte, 64*64)
	dark := make([]byte, 64*64)
	for i := range light {
		light[i] = 200
	}
	for i := range dark {
		dark[i] = 10
	}
	fl, _ := InitLowres(light, 64, 64, 64)
	fd, _ := InitLowres(dark, 64, 64, 64)
	ic := LowresMBIntraCost(fd, 2, 2)
	interc := LowresMBInterCost(fd, fd, 2, 2, 0)
	t.Logf("one mb intra=%d inter_same=%d", ic, interc)
	p, i := LowresFrameCost(fd, fl, 0)
	t.Logf("cross light->dark p=%d i=%d ratio=%f", p, i, float64(p)/float64(i))
	p2, i2 := LowresFrameCost(fd, fd, 0)
	t.Logf("same dark p=%d i=%d ratio=%f", p2, i2, float64(p2)/float64(i2))
}
