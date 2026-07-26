// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package face

import (
	"image"
	"image/color"
	"testing"
)

// fill makes a solid-gray RGBA of the given luma.
func fill(sz, gray int) *image.RGBA {
	m := image.NewRGBA(image.Rect(0, 0, sz, sz))
	for i := 0; i < len(m.Pix); i += 4 {
		m.Pix[i], m.Pix[i+1], m.Pix[i+2], m.Pix[i+3] = uint8(gray), uint8(gray), uint8(gray), 255
	}
	return m
}

func TestCropMetricsSharpness(t *testing.T) {
	sz := 112
	// Flat mid-gray → no edges → sharpness ~0.
	flat := fill(sz, 128)
	fs, _ := cropMetrics(flat)
	if fs > 0.05 {
		t.Fatalf("flat image sharpness = %.3f, want ~0", fs)
	}
	// A high-contrast checkerboard → strong Laplacian → high sharpness. Sharper (finer) checker beats
	// coarser (fewer edges per area is lower — use a 2px checker for max edge density).
	checker := image.NewRGBA(image.Rect(0, 0, sz, sz))
	for y := 0; y < sz; y++ {
		for x := 0; x < sz; x++ {
			v := uint8(0)
			if (x/2+y/2)%2 == 0 {
				v = 255
			}
			i := checker.PixOffset(x, y)
			checker.Pix[i], checker.Pix[i+1], checker.Pix[i+2], checker.Pix[i+3] = v, v, v, 255
		}
	}
	cs, _ := cropMetrics(checker)
	if cs <= fs {
		t.Fatalf("checker sharpness %.3f not > flat %.3f", cs, fs)
	}
	if cs < 0.5 {
		t.Fatalf("high-contrast checker sharpness = %.3f, expected clearly sharp", cs)
	}
}

func TestCropMetricsExposure(t *testing.T) {
	sz := 32
	_, mid := cropMetrics(fill(sz, 128)) // mid-gray → ~1
	_, dark := cropMetrics(fill(sz, 2))  // near-black → clipped + off mid → ~0
	_, bright := cropMetrics(fill(sz, 253))
	if mid < 0.9 {
		t.Fatalf("mid-gray exposure = %.3f, want ~1", mid)
	}
	if dark > 0.1 || bright > 0.1 {
		t.Fatalf("clipped exposure dark=%.3f bright=%.3f, want ~0", dark, bright)
	}
}

func TestAssessFaceAlignsFromLandmarks(t *testing.T) {
	// A crisp textured source; landmarks spanning it → AssessFace produces non-degenerate metrics.
	src := image.NewRGBA(image.Rect(0, 0, 200, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 200; x++ {
			v := uint8(0)
			if (x/3+y/3)%2 == 0 {
				v = 255
			}
			src.Set(x, y, color.RGBA{v, v, v, 255})
		}
	}
	lm := [numLandmarks][2]int{{70, 80}, {130, 80}, {100, 110}, {80, 150}, {120, 150}}
	m := AssessFace(src, lm)
	if m.Sharpness <= 0 {
		t.Fatalf("AssessFace sharpness = %.3f, want > 0 on textured input", m.Sharpness)
	}
}
