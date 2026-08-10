// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package face

import (
	"image"
	"image/color"
	"math"
	"testing"
)

// The blendshape tables are a compiled-in copy of the model's fixed contract; pin their
// shape so a careless edit can't silently shift every coefficient.
func TestBlendshapeTables(t *testing.T) {
	if got := len(blendshapeLandmarkSubset); got != exprSubsetLen {
		t.Fatalf("subset len = %d, want %d", got, exprSubsetLen)
	}
	prev := -1
	for i, v := range blendshapeLandmarkSubset {
		if v <= prev {
			t.Fatalf("subset[%d] = %d not strictly ascending after %d", i, v, prev)
		}
		if v < 0 || v >= exprMeshLandmarks {
			t.Fatalf("subset[%d] = %d out of range [0,%d)", i, v, exprMeshLandmarks)
		}
		prev = v
	}
	for i, want := range map[int]string{
		BlendshapeEyeBlinkLeft:    "eyeBlinkLeft",
		BlendshapeEyeBlinkRight:   "eyeBlinkRight",
		BlendshapeMouthSmileLeft:  "mouthSmileLeft",
		BlendshapeMouthSmileRight: "mouthSmileRight",
		0:                         "_neutral",
		BlendshapeCount - 1:       "noseSneerRight",
	} {
		if BlendshapeNames[i] != want {
			t.Fatalf("BlendshapeNames[%d] = %q, want %q", i, BlendshapeNames[i], want)
		}
	}
}

func TestExprCropGeom(t *testing.T) {
	// Level eyes: axis-aligned window, side = exprContext × max(w,h), bbox-centred.
	lm := [numLandmarks][2]int{{110, 120}, {170, 120}, {140, 150}, {120, 180}, {160, 180}}
	cx, cy, side, cosA, sinA, ok := exprCropGeom([4]int{100, 100, 80, 100}, lm)
	if !ok {
		t.Fatal("valid bbox rejected")
	}
	if cx != 140 || cy != 150 {
		t.Fatalf("center = (%v,%v), want (140,150)", cx, cy)
	}
	if want := exprContext * 100; side != want {
		t.Fatalf("side = %v, want %v", side, want)
	}
	if cosA != 1 || sinA != 0 {
		t.Fatalf("level eyes must not rotate: cos %v sin %v", cosA, sinA)
	}

	// 45°-tilted eyes rotate the window to match.
	lm[0], lm[1] = [2]int{110, 110}, [2]int{160, 160}
	_, _, _, cosA, sinA, _ = exprCropGeom([4]int{100, 100, 80, 100}, lm)
	if math.Abs(cosA-math.Sqrt2/2) > 1e-9 || math.Abs(sinA-math.Sqrt2/2) > 1e-9 {
		t.Fatalf("45° eyes: cos %v sin %v", cosA, sinA)
	}

	// Zero-value landmarks (label-only) and collapsed eyes stay axis-aligned.
	_, _, _, cosA, sinA, _ = exprCropGeom([4]int{100, 100, 80, 100}, [numLandmarks][2]int{})
	if cosA != 1 || sinA != 0 {
		t.Fatalf("zero landmarks must not rotate: cos %v sin %v", cosA, sinA)
	}
	// Degenerate bbox is rejected.
	if _, _, _, _, _, ok := exprCropGeom([4]int{10, 10, 0, 5}, lm); ok {
		t.Fatal("zero-width bbox must be rejected")
	}
}

// A crop with no rotation must reproduce the source pixels; rotation must map the eye-line
// direction onto the output x-axis; out-of-bounds sampling must come back black and opaque.
func TestExprSampleCrop(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			src.SetRGBA(x, y, color.RGBA{R: uint8(4 * x), G: uint8(4 * y), A: 0xff})
		}
	}

	// Identity window: centred on (32,32), side 16, no rotation, sampled at 16 px.
	crop := exprSampleCrop(src, 32, 32, 16, 1, 0, 16)
	for i := 0; i < 16; i++ {
		got := crop.RGBAAt(i, 7)
		if got.R != uint8(4*(24+i)) {
			t.Fatalf("x=%d: R=%d, want %d", i, got.R, 4*(24+i))
		}
	}

	// 90° rotation (eye line pointing down): output +x must walk down the source's y.
	rot := exprSampleCrop(src, 32, 32, 16, 0, 1, 16)
	for i := 1; i < 16; i++ {
		if rot.RGBAAt(i, 7).G <= rot.RGBAAt(i-1, 7).G {
			t.Fatalf("rotated crop: G not increasing along x at %d", i)
		}
	}

	// Window hanging off the image: outside pixels are black, opaque; inside preserved.
	edge := exprSampleCrop(src, 0, 0, 32, 1, 0, 16)
	if px := edge.RGBAAt(0, 0); px.R != 0 || px.G != 0 || px.B != 0 || px.A != 0xff {
		t.Fatalf("out-of-bounds pixel = %v, want opaque black", px)
	}
	if px := edge.RGBAAt(12, 12); px.A != 0xff || (px.R == 0 && px.G == 0) {
		t.Fatalf("in-bounds pixel lost: %v", px)
	}

	// Determinism (byte-identical second pass).
	again := exprSampleCrop(src, 32, 32, 16, 0, 1, 16)
	for i := range rot.Pix {
		if rot.Pix[i] != again.Pix[i] {
			t.Fatalf("non-deterministic sample at byte %d", i)
		}
	}
}

func TestMeshSubsetPoints(t *testing.T) {
	mesh := make([]float32, exprMeshValues)
	for i := 0; i < exprMeshLandmarks; i++ {
		mesh[3*i] = float32(i)         // x encodes the landmark index
		mesh[3*i+1] = float32(i) + 0.5 // y offset distinguishes the column
		mesh[3*i+2] = -999             // z must never leak through
	}
	dst := make([]float32, exprSubsetLen*2)
	meshSubsetPoints(mesh, dst)
	for row, li := range blendshapeLandmarkSubset {
		if dst[2*row] != float32(li) || dst[2*row+1] != float32(li)+0.5 {
			t.Fatalf("row %d: got (%v,%v), want (%d,%d.5)", row, dst[2*row], dst[2*row+1], li, li)
		}
	}
}

func TestExpressionFromCoeffs(t *testing.T) {
	coeffs := make([]float32, BlendshapeCount)
	coeffs[BlendshapeMouthSmileLeft] = 0.8
	coeffs[BlendshapeMouthSmileRight] = 0.6
	coeffs[BlendshapeEyeBlinkLeft] = 0.9
	coeffs[BlendshapeEyeBlinkRight] = 0.7
	e := expressionFromCoeffs(coeffs, 0.97)
	if math.Abs(float64(e.Smile)-0.7) > 1e-6 {
		t.Fatalf("smile = %v, want 0.7", e.Smile)
	}
	if math.Abs(float64(e.EyesOpen)-0.2) > 1e-6 {
		t.Fatalf("eyesOpen = %v, want 0.2", e.EyesOpen)
	}
	if e.Presence != 0.97 {
		t.Fatalf("presence = %v", e.Presence)
	}
	if e.Blendshapes[BlendshapeMouthSmileLeft] != 0.8 {
		t.Fatalf("vector not copied: %v", e.Blendshapes[BlendshapeMouthSmileLeft])
	}
}

func TestSigmoid(t *testing.T) {
	if s := sigmoid(0); s != 0.5 {
		t.Fatalf("sigmoid(0) = %v", s)
	}
	if s := sigmoid(10); s < 0.999 {
		t.Fatalf("sigmoid(10) = %v", s)
	}
	if s := sigmoid(-10); s > 0.001 {
		t.Fatalf("sigmoid(-10) = %v", s)
	}
}
