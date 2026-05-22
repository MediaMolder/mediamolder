// SPDX-License-Identifier: BSD-3-Clause
// Copyright (C) 2014-2024 Brandon Castellano <http://www.bcastell.com>.

package imgmath

import (
	"testing"
)

func TestBGRToHSVPlanes_Black(t *testing.T) {
	hP, sP, vP := BGRToHSVPlanes([]byte{0, 0, 0}, 1, 1)
	if hP[0] != 0 || sP[0] != 0 || vP[0] != 0 {
		t.Errorf("black: H=%d S=%d V=%d, want 0 0 0", hP[0], sP[0], vP[0])
	}
}

func TestBGRToHSVPlanes_White(t *testing.T) {
	_, sP, vP := BGRToHSVPlanes([]byte{255, 255, 255}, 1, 1)
	if sP[0] != 0 || vP[0] != 255 {
		t.Errorf("white: S=%d V=%d, want 0 255", sP[0], vP[0])
	}
}

func TestBGRToHSVPlanes_PureRed(t *testing.T) {
	// BGR = (0, 0, 255)
	H, S, V := BGRToHSVPlanes([]byte{0, 0, 255}, 1, 1)
	// OpenCV pure red: H=0, S=255, V=255
	if H[0] != 0 || S[0] != 255 || V[0] != 255 {
		t.Errorf("red BGR(0,0,255): H=%d S=%d V=%d, want 0 255 255", H[0], S[0], V[0])
	}
}

func TestBGRToHSVPlanes_PureGreen(t *testing.T) {
	// BGR = (0, 255, 0) → H=60, S=255, V=255
	H, S, V := BGRToHSVPlanes([]byte{0, 255, 0}, 1, 1)
	if H[0] != 60 || S[0] != 255 || V[0] != 255 {
		t.Errorf("green BGR(0,255,0): H=%d S=%d V=%d, want 60 255 255", H[0], S[0], V[0])
	}
}

func TestBGRToHSVPlanes_PureBlue(t *testing.T) {
	// BGR = (255, 0, 0) → H=120, S=255, V=255
	H, S, V := BGRToHSVPlanes([]byte{255, 0, 0}, 1, 1)
	if H[0] != 120 || S[0] != 255 || V[0] != 255 {
		t.Errorf("blue BGR(255,0,0): H=%d S=%d V=%d, want 120 255 255", H[0], S[0], V[0])
	}
}

func TestBGRToHSVPlanes_OutputLength(t *testing.T) {
	w, h := 4, 3
	bgr := make([]byte, w*h*3)
	H, S, V := BGRToHSVPlanes(bgr, w, h)
	if len(H) != w*h || len(S) != w*h || len(V) != w*h {
		t.Errorf("plane length: H=%d S=%d V=%d, want %d", len(H), len(S), len(V), w*h)
	}
}

func TestBGRToLuma_Black(t *testing.T) {
	y := BGRToLuma([]byte{0, 0, 0}, 1, 1)
	if y[0] != 0 {
		t.Errorf("black: Y=%d, want 0", y[0])
	}
}

func TestBGRToLuma_White(t *testing.T) {
	y := BGRToLuma([]byte{255, 255, 255}, 1, 1)
	if y[0] != 255 {
		t.Errorf("white: Y=%d, want 255", y[0])
	}
}

func TestBGRToLuma_PureBlue(t *testing.T) {
	// BGR = (255, 0, 0) → Y = 0.114*255 = 29.07 ≈ 29
	y := BGRToLuma([]byte{255, 0, 0}, 1, 1)
	if y[0] != 29 {
		t.Errorf("blue BGR(255,0,0): Y=%d, want 29", y[0])
	}
}

func TestBGRToLuma_PureRed(t *testing.T) {
	// BGR = (0, 0, 255) → Y = 0.299*255 = 76.2 ≈ 76
	y := BGRToLuma([]byte{0, 0, 255}, 1, 1)
	if y[0] != 76 {
		t.Errorf("red BGR(0,0,255): Y=%d, want 76", y[0])
	}
}
