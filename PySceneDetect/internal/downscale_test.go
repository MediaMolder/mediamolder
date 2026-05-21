// SPDX-License-Identifier: BSD-3-Clause
// Copyright (C) 2014-2024 Brandon Castellano <http://www.bcastell.com>.

package imgmath

import "testing"

func TestResizeBGR24_OutputSize(t *testing.T) {
	// 4×4 BGR24 → 2×2: output must have length 2*2*3 = 12.
	src := make([]byte, 4*4*3)
	// Fill with a single solid colour (128,64,32) to keep the resize result
	// predictable regardless of interpolation filter.
	for i := 0; i < len(src); i += 3 {
		src[i], src[i+1], src[i+2] = 128, 64, 32
	}
	dst, err := ResizeBGR24(src, 4, 4, 2, 2, InterpArea)
	if err != nil {
		t.Fatalf("ResizeBGR24: %v", err)
	}
	if len(dst) != 2*2*3 {
		t.Errorf("dst len %d, want 12", len(dst))
	}
}

func TestResizeBGR24_UniformColour(t *testing.T) {
	// A solid-colour image must stay the same colour after any resize.
	src := make([]byte, 8*8*3)
	for i := 0; i < len(src); i += 3 {
		src[i], src[i+1], src[i+2] = 200, 100, 50
	}
	dst, err := ResizeBGR24(src, 8, 8, 4, 4, InterpArea)
	if err != nil {
		t.Fatalf("ResizeBGR24: %v", err)
	}
	for i := 0; i < len(dst); i += 3 {
		if dst[i] != 200 || dst[i+1] != 100 || dst[i+2] != 50 {
			t.Errorf("pixel %d: got (%d,%d,%d), want (200,100,50)",
				i/3, dst[i], dst[i+1], dst[i+2])
			break
		}
	}
}

func TestResizeBGR24_ErrorCases(t *testing.T) {
	src := make([]byte, 4*4*3)
	if _, err := ResizeBGR24(src, 4, 4, 0, 2, InterpLinear); err == nil {
		t.Error("expected error for dstW=0")
	}
	if _, err := ResizeBGR24(src[:1], 4, 4, 2, 2, InterpLinear); err == nil {
		t.Error("expected error for wrong src length")
	}
}

func TestResizeGRAY8_OutputSize(t *testing.T) {
	src := make([]byte, 4*4)
	for i := range src {
		src[i] = 128
	}
	dst, err := ResizeGRAY8(src, 4, 4, 2, 2, InterpArea)
	if err != nil {
		t.Fatalf("ResizeGRAY8: %v", err)
	}
	if len(dst) != 2*2 {
		t.Errorf("dst len %d, want 4", len(dst))
	}
}

func TestResizeGRAY8_UniformValue(t *testing.T) {
	src := make([]byte, 8*8)
	for i := range src {
		src[i] = 180
	}
	dst, err := ResizeGRAY8(src, 8, 8, 4, 4, InterpArea)
	if err != nil {
		t.Fatalf("ResizeGRAY8: %v", err)
	}
	for i, v := range dst {
		if v != 180 {
			t.Errorf("pixel %d: got %d, want 180", i, v)
			break
		}
	}
}
