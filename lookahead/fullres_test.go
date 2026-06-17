// Copyright (C) 2025-2026 MediaMolder contributors.
// SPDX-License-Identifier: LGPL-2.1-or-later

package lookahead

import "testing"

// stubProvider serves the same frames the scanner saw, as "full-res" luma.
type stubProvider struct {
	frames [][]byte
	w, h   int
	calls  int
}

func (s *stubProvider) FullresLuma(idx int) ([]byte, int, int, int, error) {
	s.calls++
	return s.frames[idx], s.w, s.h, s.w, nil
}

// fullresEdgeMeasure must populate forward/reverse full-res columns with the
// established edge geometry — narrow forward ratios rise at S, narrow
// reverse ratios return to the floor at exactly E — and never change bounds
// (phase 1 is measure-only).
func TestFullresEdgeMeasure_GeometryAndColumns(t *testing.T) {
	const w, h, S, D = 128, 96, 120, 8
	E := S + D
	frames := makeBlendedClip(240, w, h, S, D)
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
	prov := &stubProvider{frames: frames, w: w, h: h}

	_, n, err := fullresEdgeMeasure(m, prov, S-1, E)
	if err != nil || n == 0 {
		t.Fatalf("measure: n=%d err=%v", n, err)
	}
	if len(m.FrLags) == 0 || len(m.FrRevLags) == 0 {
		t.Fatalf("no full-res columns: fwd %v rev %v", m.FrLags, m.FrRevLags)
	}

	frCol := func(k int) int {
		for i, kk := range m.FrLags {
			if kk == k {
				return i
			}
		}
		return -1
	}
	frRevCol := func(k int) int {
		for i, kk := range m.FrRevLags {
			if kk == k {
				return i
			}
		}
		return -1
	}
	c1 := frCol(1)
	if c1 < 0 {
		t.Fatal("no fwd lag-1 column")
	}
	// Forward lag-1: pre-blend frames are within-scene (low); the ratio must
	// step up from S onward.
	pre := float64(m.FrRatio[S-2][c1])
	in := float64(m.FrRatio[S+2][c1])
	if in < pre+0.05 {
		t.Errorf("fwd lag1: in-blend %.3f not above pre-blend %.3f", in, pre)
	}
	r1 := frRevCol(1)
	if r1 < 0 {
		t.Fatal("no rev lag-1 column")
	}
	// Reverse lag-1: elevated at E-1 (predicting a blended frame from pure
	// new), back at the floor by E+2.
	last := float64(m.FrRevRatio[E-1][r1])
	floor := float64(m.FrRevRatio[E+2][r1])
	if last < floor+0.05 {
		t.Errorf("rev lag1: last-blend %.3f not above post floor %.3f", last, floor)
	}
	// Wide mode present too (D+8 = 16).
	if frCol(D+8) < 0 || frRevCol(D+8) < 0 {
		t.Errorf("wide-mode columns missing: fwd %v rev %v", m.FrLags, m.FrRevLags)
	}
}

// Provider failures must surface as an error with nothing half-written that
// breaks the CSV (columns may exist but rows stay zero), and a nil provider
// is a no-op.
func TestFullresEdgeMeasure_FailsSafe(t *testing.T) {
	const w, h = 128, 96
	frames := makeBlendedClip(240, w, h, 120, 8)
	sc, _ := NewLookaheadScannerWithLags([]int{1, 10, 30})
	sc.RetainAllLowres()
	for _, fr := range frames {
		_ = sc.AddFrame(fr, w, h, w)
	}
	m := sc.Matrix()
	if _, n, err := fullresEdgeMeasure(m, nil, 119, 128); n != 0 || err != nil {
		t.Errorf("nil provider: n=%d err=%v, want no-op", n, err)
	}
}

// fullresEdgeMeasure adopts a refined END for short/mid blends: given a rough
// end a few frames off, the full-res reverse foot pulls it back to ~E. For
// blends past fullresMaxAdoptD it must NOT adopt (the narrow lags see only
// noise there).
func TestFullresEdgeMeasure_AdoptsEndShortMidOnly(t *testing.T) {
	const w, h = 128, 96
	for _, tc := range []struct {
		D         int
		wantAdopt bool
	}{
		{22, true},
		{45, false},
	} {
		const S = 120
		E := S + tc.D
		n := 240
		if tc.D > 16 {
			n = 360
		}
		frames := makeBlendedClip(n, w, h, S, tc.D)
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
		prov := &stubProvider{frames: frames, w: w, h: h}
		roughEnd := E + 3
		gotEnd, _, err := fullresEdgeMeasure(m, prov, S-1, roughEnd)
		if err != nil {
			t.Fatalf("D=%d: %v", tc.D, err)
		}
		if tc.wantAdopt {
			if gotEnd == roughEnd {
				t.Errorf("D=%d: end not adopted (stayed %d)", tc.D, roughEnd)
			} else if abs(gotEnd-E) > 1 {
				t.Errorf("D=%d: adopted end %d, want %d ±1", tc.D, gotEnd, E)
			}
		} else if gotEnd != roughEnd {
			t.Errorf("D=%d: end adopted (%d) past fullresMaxAdoptD, want passthrough %d", tc.D, gotEnd, roughEnd)
		}
	}
}
