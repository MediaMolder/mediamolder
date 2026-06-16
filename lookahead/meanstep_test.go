// Copyright (C) 2025-2026 MediaMolder contributors.
// SPDX-License-Identifier: LGPL-2.1-or-later

package lookahead

import "testing"

// meanStepMatrix builds a CostMatrix whose channel means follow the exact
// blend model: flat (plus optional drift and deterministic noise) at muA
// before S, linear ramp across [S, E-1], flat at muB from E. Channels with
// muA == muB carry no step.
func meanStepMatrix(n, S, D int, muA, muB [3]float32, drift, noise float32) *CostMatrix {
	m := &CostMatrix{N: n}
	rng := uint32(7)
	next := func() float32 {
		rng = rng*1664525 + 1013904223
		return (float32(rng>>16&0xff)/255 - 0.5) * 2 // [-1, 1]
	}
	E := S + D
	for j := 0; j < n; j++ {
		var alpha float32
		switch {
		case j < S:
			alpha = 0
		case j >= E:
			alpha = 1
		default:
			alpha = float32(j-S+1) / float32(D+1)
		}
		for c := 0; c < 3; c++ {
			v := (1-alpha)*muA[c] + alpha*muB[c] + drift*float32(j)/float32(n) + noise*next()
			switch c {
			case 0:
				m.AvgLuma = append(m.AvgLuma, v)
			case 1:
				m.AvgU = append(m.AvgU, v)
			case 2:
				m.AvgV = append(m.AvgV, v)
			}
		}
	}
	return m
}

// Rescue: rough bounds off by several frames are recovered to ±1 from the
// channel-mean ramp alone, for short and mid-length blends.
func TestMeanStepRefine_RecoversRoughBounds(t *testing.T) {
	muA := [3]float32{100, 125, 127}
	muB := [3]float32{98.7, 123.6, 128.4} // ~1.3-unit steps, mixed signs
	for _, D := range []int{4, 8, 22} {
		const S = 120
		E := S + D
		// Frame means average the whole frame, so their per-frame noise is
		// tiny (measured ~0.008 luma units on real content); offsets follow
		// the rough-bounds contract (~25 % of D, minimum a couple frames).
		m := meanStepMatrix(300, S, D, muA, muB, 0.3, 0.01)
		offsets := []struct{ ds, de int }{{+1, -1}, {-2, +2}, {+2, +2}}
		if D > 16 {
			offsets = []struct{ ds, de int }{{+3, -5}, {-4, +6}, {+4, +4}}
		}
		for _, off := range offsets {
			gS, gE, ok := meanStepRefine(m, S+off.ds, E+off.de)
			if !ok {
				t.Errorf("D=%d rough(%+d,%+d): not adopted", D, off.ds, off.de)
				continue
			}
			if abs(gS-S) > 1 || abs(gE-E) > 1 {
				t.Errorf("D=%d rough(%+d,%+d): got [%d,%d], want [%d,%d] ±1",
					D, off.ds, off.de, gS, gE, S, E)
			}
		}
	}
}

// The polish path accepts small moves only OUTWARD (earlier start / later
// end): the mean signal under-sees the blend's low-alpha head and tail, so
// its own ±1 inward preferences are bias, not signal.
func TestMeanStepRefine_PolishIsOutwardOnly(t *testing.T) {
	muA := [3]float32{100, 125, 127}
	muB := [3]float32{97, 122, 130} // huge steps: vis far above polish gate
	const S, D = 120, 6
	E := S + D
	m := meanStepMatrix(300, S, D, muA, muB, 0, 0.01)
	// Incoming start 1 LATE: fit proposes S (outward) → adopted.
	if gS, _, ok := meanStepRefine(m, S+1, E); !ok || gS != S {
		t.Errorf("outward start polish: got S=%d ok=%v, want %d adopted", gS, ok, S)
	}
	// Incoming start 1 EARLY: fit would move inward by 1 → must keep input.
	if gS, _, ok := meanStepRefine(m, S-1, E); ok && gS != S-1 {
		t.Errorf("inward start polish adopted: got S=%d, want %d kept", gS, S-1)
	}
}

// Long blends: per-frame ramp increment below the frame-to-frame noise means
// the fit cannot separate ramp from drift — the visibility gate must skip.
func TestMeanStepRefine_VisibilityGateSkipsLongBlends(t *testing.T) {
	muA := [3]float32{100, 125, 127}
	muB := [3]float32{98.7, 123.6, 128.4}
	const S, D = 200, 100
	m := meanStepMatrix(600, S, D, muA, muB, 0.5, 0.10)
	if _, _, ok := meanStepRefine(m, S+3, S+D-8); ok {
		t.Error("drift-regime blend adopted; want visibility-gate skip")
	}
}

// No step (same scene statistics on every channel), or absent chroma with a
// stepless luma: pass through untouched.
func TestMeanStepRefine_FailsSafe(t *testing.T) {
	mu := [3]float32{100, 125, 127}
	m := meanStepMatrix(300, 120, 8, mu, mu, 0.3, 0.01)
	if _, _, ok := meanStepRefine(m, 120, 128); ok {
		t.Error("stepless content adopted edges")
	}
	m.AvgU, m.AvgV = nil, nil
	if _, _, ok := meanStepRefine(m, 120, 128); ok {
		t.Error("nil chroma + stepless luma adopted edges")
	}
}

// Chroma-only step (identical luma means): U/V alone must carry the fit —
// the motivating case for adding the chroma channels.
func TestMeanStepRefine_ChromaOnlyStep(t *testing.T) {
	muA := [3]float32{100, 125, 127}
	muB := [3]float32{100, 123.5, 128.6} // luma identical
	const S, D = 120, 8
	E := S + D
	m := meanStepMatrix(300, S, D, muA, muB, 0.2, 0.01)
	gS, gE, ok := meanStepRefine(m, S+2, E-2)
	if !ok || abs(gS-S) > 1 || abs(gE-E) > 1 {
		t.Errorf("chroma-only: got [%d,%d] ok=%v, want [%d,%d] ±1", gS, gE, ok, S, E)
	}
}
