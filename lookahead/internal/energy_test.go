// Copyright (C) 2025-2026 MediaMolder contributors.
// SPDX-License-Identifier: LGPL-2.1-or-later

package imgmath

import "testing"

func lowresFromLuma(t *testing.T, luma []byte, w, h int) *LowresFrame {
	t.Helper()
	f, err := InitLowres(luma, w, h, w)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// A flat frame has zero AC energy: SATD-vs-zero of a constant block is pure
// DC, which the SAD>>1 term removes exactly.
func TestLowresFrameACEnergy_FlatIsZero(t *testing.T) {
	const w, h = 128, 96
	luma := make([]byte, w*h)
	for i := range luma {
		luma[i] = 100
	}
	if e := LowresFrameACEnergy(lowresFromLuma(t, luma, w, h)); e != 0 {
		t.Errorf("flat frame energy = %d, want 0", e)
	}
}

// Energy measures texture, not brightness: an unclipped DC shift leaves it
// unchanged, while doubling the contrast around the mean roughly doubles it.
func TestLowresFrameACEnergy_DCInvariantContrastLinear(t *testing.T) {
	const w, h = 128, 96
	next := func(r *uint32) byte { *r = *r*1664525 + 1013904223; return byte(*r >> 16) }
	rng := uint32(42)
	base := make([]byte, w*h)
	shifted := make([]byte, w*h)
	doubled := make([]byte, w*h)
	for i := range base {
		// Texture in [96, 159] around mean 128: +16 shift and 2x contrast
		// both stay within [0, 255] before and after lowres filtering.
		v := 96 + int(next(&rng))%64
		base[i] = byte(v)
		shifted[i] = byte(v + 16)
		doubled[i] = byte(128 + 2*(v-128))
	}
	eb := LowresFrameACEnergy(lowresFromLuma(t, base, w, h))
	es := LowresFrameACEnergy(lowresFromLuma(t, shifted, w, h))
	ed := LowresFrameACEnergy(lowresFromLuma(t, doubled, w, h))
	if eb <= 0 {
		t.Fatalf("textured frame energy = %d, want > 0", eb)
	}
	if es != eb {
		t.Errorf("DC shift changed energy: %d -> %d", eb, es)
	}
	ratio := float64(ed) / float64(eb)
	if ratio < 1.8 || ratio > 2.2 {
		t.Errorf("2x contrast scaled energy by %.3f, want ~2.0", ratio)
	}
}
