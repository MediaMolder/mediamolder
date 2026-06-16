// Copyright (C) 2025-2026 MediaMolder contributors.
// SPDX-License-Identifier: LGPL-2.1-or-later

package lookahead

import "testing"

func scanClip(t *testing.T, frames [][]byte, w, h int) (*LookaheadScanner, *CostMatrix) {
	t.Helper()
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
	return sc, sc.Matrix()
}

// A dissolve must carve a U-shaped dip into the per-frame AC energy:
// minimum inside the blend near the alpha=0.5 midpoint, well below both
// flank levels.
func TestEnergy_DissolveDipGeometry(t *testing.T) {
	const w, h = 128, 96
	const S = 120
	for _, D := range []int{30, 45} {
		_, m := scanClip(t, makeBlendedClip(360, w, h, S, D), w, h)
		if len(m.Energy) != m.N {
			t.Fatalf("D=%d: Energy len %d, want %d", D, len(m.Energy), m.N)
		}
		minJ := S
		for j := S - 20; j < S+D+20; j++ {
			if m.Energy[j] < m.Energy[minJ] {
				minJ = j
			}
		}
		mid := S + D/2
		if abs(minJ-mid) > D/4 {
			t.Errorf("D=%d: energy minimum at %d, want within %d of midpoint %d", D, minJ, D/4, mid)
		}
		pre := float64(m.Energy[S-10])
		post := float64(m.Energy[S+D+10])
		dip := float64(m.Energy[minJ])
		if dip > 0.9*pre || dip > 0.9*post {
			t.Errorf("D=%d: dip %.0f not clearly below flanks (%.0f, %.0f)", D, dip, pre, post)
		}
	}
}

// energyEdgeRefine must recover deliberately wrong rough bounds to within
// ±1 on clean blends — including the start, which no other estimator
// resolves at frame level for long blends.
func TestEnergyEdgeRefine_RecoversRoughBounds(t *testing.T) {
	const w, h = 128, 96
	const S = 120
	for _, D := range []int{30, 45} {
		_, m := scanClip(t, makeBlendedClip(360, w, h, S, D), w, h)
		E := S + D
		for _, off := range []struct{ ds, de int }{{+3, -8}, {-4, +6}, {+5, +5}, {0, 0}} {
			gS, gE, snr, ok := energyEdgeRefine(m, S+off.ds, E+off.de)
			if off.ds == 0 && off.de == 0 {
				// Already exact: the fit must not churn the bounds.
				if gS != S || gE != E {
					t.Errorf("D=%d exact input moved to [%d,%d]", D, gS, gE)
				}
				continue
			}
			if !ok {
				t.Errorf("D=%d rough(%+d,%+d): not adopted (snr %.1f)", D, off.ds, off.de, snr)
				continue
			}
			if abs(gS-S) > 1 || abs(gE-E) > 1 {
				t.Errorf("D=%d rough(%+d,%+d): got [%d,%d], want [%d,%d] ±1 (snr %.1f)",
					D, off.ds, off.de, gS, gE, S, E, snr)
			}
		}
	}
}

// No dip, no adoption: a static clip must fail the depth gates, and a matrix
// without the Energy column must pass through untouched.
func TestEnergyEdgeRefine_FailsSafe(t *testing.T) {
	const w, h = 128, 96
	// Static clip: blend of duration 0 (makeBlendedClip with D=0 yields a
	// hard cut at S; use the pre-cut static segment only).
	frames := makeBlendedClip(360, w, h, 1000, 4) // blend far outside the window
	_, m := scanClip(t, frames, w, h)
	if gS, gE, _, ok := energyEdgeRefine(m, 120, 150); ok || gS != 120 || gE != 150 {
		t.Errorf("static clip: got [%d,%d] ok=%v, want passthrough", gS, gE, ok)
	}
	m.Energy = nil
	if gS, gE, _, ok := energyEdgeRefine(m, 120, 150); ok || gS != 120 || gE != 150 {
		t.Errorf("nil Energy: got [%d,%d] ok=%v, want passthrough", gS, gE, ok)
	}
}
