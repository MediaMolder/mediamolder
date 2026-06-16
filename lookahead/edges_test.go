// Copyright (C) 2025-2026 MediaMolder contributors.
// SPDX-License-Identifier: LGPL-2.1-or-later

package lookahead

import "testing"

// makeBlendedClip renders n luma frames of two static noise textures with a
// linear cross-fade of D frames starting at S, following the analyzer's blend
// convention: alpha(f) = (f−S+1)/(D+1) for f ∈ [S, S+D), 0 before, 1 from
// E = S+D on. So S is the first (slightly) blended frame and E the first pure
// new-scene frame.
func makeBlendedClip(n, w, h, S, D int) [][]byte {
	next := func(r *uint32) byte { *r = *r*1664525 + 1013904223; return byte(*r >> 16) }
	rngA, rngB := uint32(12345), uint32(987654321)
	texA := make([]byte, w*h)
	texB := make([]byte, w*h)
	for i := range texA {
		texA[i] = 16 + next(&rngA)%224
		texB[i] = 16 + next(&rngB)%224
	}
	frames := make([][]byte, n)
	for f := 0; f < n; f++ {
		var a float64
		switch {
		case f < S:
			a = 0
		case f >= S+D:
			a = 1
		default:
			a = float64(f-S+1) / float64(D+1)
		}
		buf := make([]byte, w*h)
		for i := range buf {
			buf[i] = byte(float64(texA[i])*(1-a) + float64(texB[i])*a + 0.5)
		}
		frames[f] = buf
	}
	return frames
}

// TestAnalyzeStaged_EdgeNarrowing_Exact runs real frames through the full
// staged pipeline (lowres + ME + coarse pass + refinement + plateau consensus
// + progressive lag narrowing) and asserts the dissolve edges are recovered to
// within one frame of ground truth for short blends. This is the end-to-end
// guard the matrix-only golden tests cannot provide.
func TestAnalyzeStaged_EdgeNarrowing_Exact(t *testing.T) {
	const w, h = 128, 96
	const N, S = 240, 120
	for _, D := range []int{4, 7, 12} {
		frames := makeBlendedClip(N, w, h, S, D)
		sc, err := NewLookaheadScannerWithLags([]int{1, 10, 30})
		if err != nil {
			t.Fatal(err)
		}
		sc.RetainAllLowres()
		for _, fr := range frames {
			if err := sc.AddFrame(fr, w, h, w); err != nil {
				t.Fatal(err)
			}
		}
		a := &LookaheadAnalyzer{
			HardCutThreshold: 0.3,
			AggWindow:        12,
			DissolveMinLen:   2,
			DissolveMaxLen:   60,
			MinSceneLen:      8,
		}
		trs, err := a.AnalyzeStaged(sc, []int{1, 2, 3, 5, 15, 30, 45, 60}, nil)
		if err != nil {
			t.Fatalf("D=%d: AnalyzeStaged: %v", D, err)
		}
		var diss []SceneTransition
		for _, tr := range trs {
			if tr.Type == TransitionDissolve {
				diss = append(diss, tr)
			}
		}
		if len(diss) != 1 {
			t.Fatalf("D=%d: expected exactly 1 dissolve, got %v", D, trs)
		}
		// StartFrame = S−1 (last unblended), EndFrame = S+D (first pure new).
		if abs(diss[0].StartFrame-(S-1)) > 1 {
			t.Errorf("D=%d: StartFrame = %d, want %d ±1", D, diss[0].StartFrame, S-1)
		}
		if abs(diss[0].EndFrame-(S+D)) > 1 {
			t.Errorf("D=%d: EndFrame = %d, want %d ±1", D, diss[0].EndFrame, S+D)
		}
	}
}

// TestRefineDissolveEdges_RecoversFromRoughBounds feeds the narrowing ladder
// deliberately WRONG rough bounds — mimicking the few-frame plateau-consensus
// error on real footage — and asserts it walks them back to the exact edges.
// This is the discriminating test for the narrowing itself: the end-to-end
// test above can pass on ideal blends even without narrowing (the plateau feet
// are exact in that regime), this one cannot.
func TestRefineDissolveEdges_RecoversFromRoughBounds(t *testing.T) {
	const w, h = 128, 96
	const S = 120
	for _, D := range []int{4, 7, 12, 30, 45, 90} {
		// Long blends need future frames for the wide reverse end measurement
		// (refs up to ~E + kRev + baseline window).
		n := 240
		if D > 16 {
			n = 360
		}
		if D > 45 {
			n = 480
		}
		frames := makeBlendedClip(n, w, h, S, D)
		sc, err := NewLookaheadScannerWithLags([]int{1, 10, 30})
		if err != nil {
			t.Fatal(err)
		}
		sc.RetainAllLowres()
		for _, fr := range frames {
			if err := sc.AddFrame(fr, w, h, w); err != nil {
				t.Fatal(err)
			}
		}
		m := sc.Matrix()
		lowres := sc.AllLowres()

		// Rough-bound errors in the directions the plateau consensus actually
		// exhibits on real footage. Blends in (rungStartMaxD, energyMinD]
		// have no frame-level start estimator (rung starts are gated — the
		// crossing threshold loses the blend's low-alpha head and quantizes
		// ~0.2·D late — and the energy dip carries too few samples), so the
		// plateau start passes through. Blends over energyMinD get BOTH edge
		// errors: the AC-energy dip fit must recover starts and ends alike.
		offsets := []struct{ ds, de int }{
			{0, +4}, {-2, +5}, {+2, +3}, {-3, 0}, {+1, +6},
		}
		if D > 16 {
			offsets = []struct{ ds, de int }{
				{+3, -8}, {-4, +6}, {0, +12}, {0, 0},
			}
		}
		// Short blends resolve via the smallest rungs (lag 1–3) and must be
		// exact to ±1 (rung quantization); blends over 16 frames resolve their
		// end via the rung run-edge walk and the reverse-foot two-crossing
		// projection, which quantize to ±1 frame.
		tol := 1
		if D <= 4 {
			tol = 0 // lag-1 rung valid: exact
		}
		for _, off := range offsets {
			roughStart := S - 1 + off.ds
			roughEnd := S + D + off.de
			gotS, gotE, _ := refineDissolveEdges(sc, m, lowres, roughStart, roughEnd)
			// Mid-range blends (rungStartMaxD < D <= energyMinD) have no
			// dedicated start estimator: the plateau start passes through,
			// UNLESS the end fix shrinks the running estimate enough for the
			// re-pass rungs to take the start — which then lands on truth.
			// Either outcome is correct; drifting anywhere else is not.
			startOK := abs(gotS-(S-1)) <= tol
			if D > rungStartMaxD && D <= energyMinD {
				startOK = startOK || abs(gotS-roughStart) <= tol
			}
			if !startOK || abs(gotE-(S+D)) > tol {
				t.Errorf("D=%d rough(%+d,%+d): got [%d,%d], want [%d|pass-through,%d] ±%d",
					D, off.ds, off.de, gotS, gotE, S-1, S+D, tol)
			}
		}
	}
}

// makeTwoBlendClip renders two back-to-back cross-fades A→B (D1 frames at S1)
// and B→C (D2 frames at S2), same conventions as makeBlendedClip.
func makeTwoBlendClip(n, w, h, S1, D1, S2, D2 int) [][]byte {
	next := func(r *uint32) byte { *r = *r*1664525 + 1013904223; return byte(*r >> 16) }
	rngA, rngB, rngC := uint32(12345), uint32(987654321), uint32(555555)
	texA := make([]byte, w*h)
	texB := make([]byte, w*h)
	texC := make([]byte, w*h)
	for i := range texA {
		texA[i] = 16 + next(&rngA)%224
		texB[i] = 16 + next(&rngB)%224
		texC[i] = 16 + next(&rngC)%224
	}
	alpha := func(f, S, D int) float64 {
		switch {
		case f < S:
			return 0
		case f >= S+D:
			return 1
		default:
			return float64(f-S+1) / float64(D+1)
		}
	}
	frames := make([][]byte, n)
	for f := 0; f < n; f++ {
		a1 := alpha(f, S1, D1)
		a2 := alpha(f, S2, D2)
		buf := make([]byte, w*h)
		for i := range buf {
			ab := float64(texA[i])*(1-a1) + float64(texB[i])*a1
			buf[i] = byte(ab*(1-a2) + float64(texC[i])*a2 + 0.5)
		}
		frames[f] = buf
	}
	return frames
}

// TestRefineDissolveEdges_AdjacentBlendsNotFused guards the anchor+walk edge
// search: with a second dissolve starting only a few frames after the first
// ends, refining the FIRST blend's bounds must stay on its own edges instead
// of latching onto the neighbour's run inside the search window (which the
// earlier scan-from-window-edge implementation did, fusing the two).
func TestRefineDissolveEdges_AdjacentBlendsNotFused(t *testing.T) {
	const w, h = 128, 96
	const N = 240
	const S1, D1 = 100, 6 // truth bounds [99, 106]
	E1 := S1 + D1
	for _, gap := range []int{4, 6, 10} {
		S2 := E1 + gap
		frames := makeTwoBlendClip(N, w, h, S1, D1, S2, 8)
		sc, err := NewLookaheadScannerWithLags([]int{1, 10, 30})
		if err != nil {
			t.Fatal(err)
		}
		sc.RetainAllLowres()
		for _, fr := range frames {
			if err := sc.AddFrame(fr, w, h, w); err != nil {
				t.Fatal(err)
			}
		}
		m := sc.Matrix()
		lowres := sc.AllLowres()

		for _, off := range []struct{ ds, de int }{{0, 0}, {0, +3}, {-2, +2}} {
			gotS, gotE, _ := refineDissolveEdges(sc, m, lowres, S1-1+off.ds, E1+off.de)
			if gotS != S1-1 || gotE != E1 {
				t.Errorf("gap=%d rough(%+d,%+d): got [%d,%d], want exactly [%d,%d] (neighbour captured?)",
					gap, off.ds, off.de, gotS, gotE, S1-1, E1)
			}
		}
	}
}

// TestRefine_RelocatesColumnsOnLagUnion guards the Refine column-remap fix:
// adding a lag that sorts into the middle of the schedule must relocate the
// existing columns to their new positions, not re-label them. Before the fix,
// adding lag 2 to a [1,10,30] schedule left the lag-10 data under the "lag 2"
// column (and the lag-30 data under "lag 10") everywhere outside the refined
// window.
func TestRefine_RelocatesColumnsOnLagUnion(t *testing.T) {
	const w, h = 128, 96
	const N, S = 80, 40
	frames := makeBlendedClip(N, w, h, S, 1) // hard-cut-like change for nonzero ratios
	sc, err := NewLookaheadScannerWithLags([]int{1, 10, 30})
	if err != nil {
		t.Fatal(err)
	}
	sc.RetainAllLowres()
	for _, fr := range frames {
		if err := sc.AddFrame(fr, w, h, w); err != nil {
			t.Fatal(err)
		}
	}
	m := sc.Matrix()

	// Snapshot lag-10 and lag-30 ratios at probe frames outside the refine window.
	probe := []int{45, 47, 60, 70}
	col10, col30 := 1, 2 // schedule [1,10,30]
	want10 := map[int]float32{}
	want30 := map[int]float32{}
	for _, j := range probe {
		want10[j] = m.Ratio[j][col10]
		want30[j] = m.Ratio[j][col30]
	}
	if want10[45] == 0 && want30[45] == 0 {
		t.Fatal("probe ratios unexpectedly zero; test setup broken")
	}

	// Add lag 2 (sorts into the middle) for a small window only.
	if err := sc.Refine(m, sc.AllLowres(), 10, 20, []int{2}); err != nil {
		t.Fatal(err)
	}

	idx := func(k int) int {
		for i, kk := range m.Lags {
			if kk == k {
				return i
			}
		}
		t.Fatalf("lag %d missing from %v", k, m.Lags)
		return -1
	}
	n10, n30 := idx(10), idx(30)
	for _, j := range probe {
		if got := m.Ratio[j][n10]; got != want10[j] {
			t.Errorf("frame %d: lag-10 ratio relocated wrong: got %v want %v", j, got, want10[j])
		}
		if got := m.Ratio[j][n30]; got != want30[j] {
			t.Errorf("frame %d: lag-30 ratio relocated wrong: got %v want %v", j, got, want30[j])
		}
		// Lag-2 outside its window must be zero (never computed there).
		if got := m.Ratio[j][idx(2)]; got != 0 {
			t.Errorf("frame %d: lag-2 ratio = %v outside refine window, want 0", j, got)
		}
	}
}

// TestComputeReverse_NotForwardCache guards the disjoint reverse cache slots:
// after forward lag-k has been computed for a frame, the reverse measurement
// at the same distance must still measure against the FUTURE reference, not
// return the cached forward cost. Probe inside the blend tail where the two
// directions differ strongly: just after E the forward ratio (vs a blended
// past) is high while the true reverse ratio (vs a pure-new future) is at the
// within-scene floor.
func TestComputeReverse_NotForwardCache(t *testing.T) {
	const w, h = 128, 96
	const N, S, D = 160, 80, 6
	E := S + D
	frames := makeBlendedClip(N, w, h, S, D)
	sc, err := NewLookaheadScannerWithLags([]int{1, 10, 30})
	if err != nil {
		t.Fatal(err)
	}
	sc.RetainAllLowres()
	for _, fr := range frames {
		if err := sc.AddFrame(fr, w, h, w); err != nil {
			t.Fatal(err)
		}
	}
	m := sc.Matrix()
	lowres := sc.AllLowres()

	const k = 5
	// Force the forward lag-k computation first (this is what used to poison
	// the shared cache slot).
	if err := sc.Refine(m, lowres, S-k, E+k+2, []int{k}); err != nil {
		t.Fatal(err)
	}
	col := computeReverse(m, lowres, S-k, E+k+2, k)

	// At j = E+1 (pure new): forward ref E+1−k is blended → high forward
	// ratio; future ref E+1+k is pure new → reverse at the static-scene floor.
	fcol := -1
	for i, kk := range m.Lags {
		if kk == k {
			fcol = i
		}
	}
	fwdV := m.Ratio[E+1][fcol]
	revV := m.RevRatio[E+1][col]
	if fwdV < 0.5 {
		t.Fatalf("test premise broken: forward lag-%d at E+1 = %v, want high", k, fwdV)
	}
	// The floor must be small but STRICTLY POSITIVE: a computed cost always
	// carries ME penalties (> 0), so an exact 0 means computeReverse measured
	// nothing (e.g. an early return or guard mis-fire) — which must fail this
	// test just like a cache collision does.
	if revV <= 0 || revV > 0.2 {
		t.Errorf("reverse lag-%d at E+1 = %v, want small positive floor (0 = not computed; high = forward cache collision)", k, revV)
	}
	// And the reverse run must be elevated inside the blend (reverse elevated
	// run is [S−k, E−1]), proving the measurement really is against the future
	// reference.
	if inBlend := m.RevRatio[S+1][col]; inBlend < 0.5 {
		t.Errorf("reverse lag-%d at S+1 = %v, want elevated (reverse signal not measured?)", k, inBlend)
	}
}

// TestWalkRun_FloorGatedBridge: a sub-threshold frame is bridged as a dip only
// while it stays above the bridge floor. A frame AT the out-of-run floor is
// the run's end — bridging it lets the walk string together floor-level noise
// and inflate the run far past the true edge (measured: a 4-frame blend's
// trailing run walked 6 frames into the new scene over three bridged dips).
func TestWalkRun_FloorGatedBridge(t *testing.T) {
	mk := func(vals []float64) func(int) (float64, bool) {
		return func(j int) (float64, bool) {
			if j < 0 || j >= len(vals) {
				return 0, false
			}
			return vals[j], true
		}
	}
	const th, floor = 0.5, 0.3 // bridge floor passed directly
	// Genuine dip (0.4 >= floor) inside the run: bridged, run extends to 4.
	dip := mk([]float64{0.9, 0.9, 0.4, 0.9, 0.9, 0.1, 0.1})
	if got := walkRun(dip, 0, +1, 6, th, floor); got != 4 {
		t.Errorf("dip: run end %d, want 4", got)
	}
	// Floor-level frame (0.1 < floor) followed by a noise spike that clears
	// th: NOT bridged, the run ends before it.
	noise := mk([]float64{0.9, 0.9, 0.1, 0.6, 0.1, 0.6, 0.1})
	if got := walkRun(noise, 0, +1, 6, th, floor); got != 1 {
		t.Errorf("noise: run end %d, want 1", got)
	}
}
