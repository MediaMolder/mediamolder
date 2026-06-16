// Copyright (C) 2025-2026 MediaMolder contributors.
// SPDX-License-Identifier: LGPL-2.1-or-later

package imgmath

import "testing"

func texFrame(seed uint32, w, h int) []byte {
	buf := make([]byte, w*h)
	r := seed
	for i := range buf {
		r = r*1664525 + 1013904223
		buf[i] = 16 + byte(r>>16)%224
	}
	return buf
}

// A full-res frame predicts itself almost perfectly at lag 0 distance
// (identical reference), and the engine's cost machinery runs unchanged on
// the native-resolution frame.
func TestInitFullres_SelfPredictionCheap(t *testing.T) {
	const w, h = 128, 96
	tex := texFrame(42, w, h)
	a, err := InitFullres(tex, w, h, w)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := InitFullres(tex, w, h, w)
	inter, intra := LowresFrameCost(a, b, 0)
	if intra <= 0 {
		t.Fatal("intra cost not computed")
	}
	if float64(inter)/float64(intra) > 0.05 {
		t.Errorf("identical frames: inter/intra = %d/%d, want near zero", inter, intra)
	}
}

// smoothFrame renders low-frequency content (the regime where diamond ME
// has a cost gradient to descend — noise textures have no basin and are
// adversarial for any block matcher).
func smoothFrame(phase float64, w, h int) []byte {
	buf := make([]byte, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := 128 + 60*sin01(float64(x)/23+phase)*sin01(float64(y)/17-phase) + 30*sin01(float64(x+y)/41)
			buf[y*w+x] = byte(v)
		}
	}
	return buf
}

func sin01(t float64) float64 {
	// cheap smooth periodic without importing math in more tests
	t -= float64(int(t/6.283185307)) * 6.283185307
	if t < 0 {
		t += 6.283185307
	}
	x := t/3.141592653 - 1
	return x * (1 - absf(x)) * 2 // triangle-smooth approximation in [-1,1]
}

func absf(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// ME at full res finds a small translation on smooth content: a shifted
// copy predicts far better than an unrelated frame.
func TestInitFullres_MEFindsShift(t *testing.T) {
	const w, h = 128, 96
	wide := smoothFrame(0.3, w+8, h)
	cur := make([]byte, w*h)
	ref := make([]byte, w*h)
	for y := 0; y < h; y++ {
		copy(cur[y*w:], wide[y*(w+8)+4:y*(w+8)+4+w]) // shifted 4px
		copy(ref[y*w:], wide[y*(w+8):y*(w+8)+w])
	}
	fc, _ := InitFullres(cur, w, h, w)
	fr, _ := InitFullres(ref, w, h, w)
	fo, _ := InitFullres(smoothFrame(2.1, w, h), w, h, w)
	interShift, intra := LowresFrameCost(fc, fr, 0)
	fc2, _ := InitFullres(cur, w, h, w) // fresh caches for the second ref
	interOther, _ := LowresFrameCost(fc2, fo, 0)
	rShift := float64(interShift) / float64(intra)
	rOther := float64(interOther) / float64(intra)
	if rShift > 0.5*rOther {
		t.Errorf("ME failed to exploit shift: shifted %.3f vs unrelated %.3f", rShift, rOther)
	}
}

// Full resolution sees low-alpha blends better than half resolution WHEN
// the incoming scene's distinctive content is high-frequency: the lowres
// downsampling filter attenuates the fine-detail ghost, the full-res view
// keeps it. (On scale-free white-noise content the two are equivalent —
// the gain is specifically about real content's structured detail.)
func TestInitFullres_LowAlphaMoreVisibleThanLowres(t *testing.T) {
	const w, h = 256, 192
	texA := smoothFrame(0.3, w, h) // smooth old scene
	fine := texFrame(977, w, h)    // new scene: fine high-frequency detail
	texB := make([]byte, w*h)
	for i := range texB {
		texB[i] = byte(128 + (int(fine[i])-128)/2)
	}
	blend := make([]byte, w*h)
	for i := range blend {
		blend[i] = byte((float64(texA[i])*0.9 + float64(texB[i])*0.1) + 0.5)
	}
	fA, _ := InitFullres(texA, w, h, w)
	fBl, _ := InitFullres(blend, w, h, w)
	frInter, frIntra := LowresFrameCost(fBl, fA, 0)
	lA, _ := InitLowres(texA, w, h, w)
	lBl, _ := InitLowres(blend, w, h, w)
	lrInter, lrIntra := LowresFrameCost(lBl, lA, 0)
	frRatio := float64(frInter) / float64(frIntra)
	lrRatio := float64(lrInter) / float64(lrIntra)
	if frRatio <= lrRatio {
		t.Errorf("alpha=0.1 visibility: fullres ratio %.4f <= lowres %.4f", frRatio, lrRatio)
	}
}
