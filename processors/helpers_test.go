// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"image"
	"image/color"
	"math"
	"testing"
)

func TestFrameToRGBA_NilFrame(t *testing.T) {
	// FrameToRGBA should return an error for nil frame.
	_, err := FrameToRGBA(nil)
	if err == nil {
		t.Fatal("expected error for nil frame")
	}
}

func TestFrameToFloat32Tensor_NilFrame(t *testing.T) {
	_, err := FrameToFloat32Tensor(nil, 640)
	if err == nil {
		t.Fatal("expected error for nil frame")
	}
}

// --- Letterbox ---

func TestLetterbox_SquareToSquare(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 100, 100))
	fill(src, color.RGBA{R: 255, G: 0, B: 0, A: 255})

	dst := Letterbox(src, 64, 64)
	if dst.Bounds().Dx() != 64 || dst.Bounds().Dy() != 64 {
		t.Fatalf("expected 64×64, got %d×%d", dst.Bounds().Dx(), dst.Bounds().Dy())
	}
	// Centre pixel should be red (the image fills the entire 64×64 area).
	c := dst.RGBAAt(32, 32)
	if c.R == 0 {
		t.Fatalf("centre pixel should be red, got %v", c)
	}
}

func TestLetterbox_WideToSquare(t *testing.T) {
	// 200×100 source → 100×100 target → image should be 100×50, centred.
	src := image.NewRGBA(image.Rect(0, 0, 200, 100))
	fill(src, color.RGBA{R: 0, G: 255, B: 0, A: 255})

	dst := Letterbox(src, 100, 100)
	if dst.Bounds().Dx() != 100 || dst.Bounds().Dy() != 100 {
		t.Fatalf("expected 100×100, got %d×%d", dst.Bounds().Dx(), dst.Bounds().Dy())
	}
	// Top bar should be black (letterbox padding).
	top := dst.RGBAAt(50, 0)
	if top.R != 0 || top.G != 0 || top.B != 0 {
		t.Fatalf("top bar should be black, got %v", top)
	}
	// Centre should be green.
	mid := dst.RGBAAt(50, 50)
	if mid.G == 0 {
		t.Fatalf("centre pixel should be green, got %v", mid)
	}
}

func TestLetterbox_TallToSquare(t *testing.T) {
	// 100×200 source → 100×100 target → image should be 50×100, centred.
	src := image.NewRGBA(image.Rect(0, 0, 100, 200))
	fill(src, color.RGBA{R: 0, G: 0, B: 255, A: 255})

	dst := Letterbox(src, 100, 100)
	// Left bar should be black.
	left := dst.RGBAAt(0, 50)
	if left.R != 0 || left.G != 0 || left.B != 0 {
		t.Fatalf("left bar should be black, got %v", left)
	}
	// Centre should be blue.
	mid := dst.RGBAAt(50, 50)
	if mid.B == 0 {
		t.Fatalf("centre pixel should be blue, got %v", mid)
	}
}

func TestLetterbox_ZeroSize(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 0, 0))
	dst := Letterbox(src, 64, 64)
	if dst.Bounds().Dx() != 64 || dst.Bounds().Dy() != 64 {
		t.Fatalf("expected 64×64 for zero-size input, got %d×%d", dst.Bounds().Dx(), dst.Bounds().Dy())
	}
}

// --- ImageToFloat32Tensor ---

func TestImageToFloat32Tensor_Dimensions(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 32, 32))
	fill(src, color.RGBA{R: 128, G: 64, B: 255, A: 255})

	tensor := ImageToFloat32Tensor(src, 16)
	expected := 3 * 16 * 16
	if len(tensor) != expected {
		t.Fatalf("tensor length = %d, want %d", len(tensor), expected)
	}
}

func TestImageToFloat32Tensor_Values(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4, 4))
	fill(src, color.RGBA{R: 255, G: 0, B: 128, A: 255})

	tensor := ImageToFloat32Tensor(src, 4)
	plane := 4 * 4

	// First plane is R — should be ~1.0.
	for i := 0; i < plane; i++ {
		if math.Abs(float64(tensor[i])-1.0) > 0.01 {
			t.Fatalf("R plane [%d] = %f, want ~1.0", i, tensor[i])
		}
	}
	// Second plane is G — should be ~0.0.
	for i := plane; i < 2*plane; i++ {
		if math.Abs(float64(tensor[i])) > 0.01 {
			t.Fatalf("G plane [%d] = %f, want ~0.0", i, tensor[i])
		}
	}
	// Third plane is B — should be ~128/255 ≈ 0.502.
	for i := 2 * plane; i < 3*plane; i++ {
		if math.Abs(float64(tensor[i])-128.0/255.0) > 0.01 {
			t.Fatalf("B plane [%d] = %f, want ~0.502", i, tensor[i])
		}
	}
}

func TestImageToFloat32Tensor_NCHW_Layout(t *testing.T) {
	// Verify channel-first (planar) layout: R pixels, then G pixels, then B.
	src := image.NewRGBA(image.Rect(0, 0, 2, 2))
	src.SetRGBA(0, 0, color.RGBA{R: 255, G: 0, B: 0, A: 255})
	src.SetRGBA(1, 0, color.RGBA{R: 0, G: 255, B: 0, A: 255})
	src.SetRGBA(0, 1, color.RGBA{R: 0, G: 0, B: 255, A: 255})
	src.SetRGBA(1, 1, color.RGBA{R: 128, G: 128, B: 128, A: 255})

	tensor := ImageToFloat32Tensor(src, 2)
	// plane size = 4 (2×2)
	// R plane: [1, 0, 0, 0.502] at offsets 0-3
	if tensor[0] < 0.99 { // pixel (0,0) R
		t.Errorf("R(0,0) = %f, want ~1.0", tensor[0])
	}
	if tensor[1] > 0.01 { // pixel (1,0) R
		t.Errorf("R(1,0) = %f, want ~0.0", tensor[1])
	}
	// G plane starts at offset 4
	if tensor[4] > 0.01 { // pixel (0,0) G
		t.Errorf("G(0,0) = %f, want ~0.0", tensor[4])
	}
	if tensor[5] < 0.99 { // pixel (1,0) G
		t.Errorf("G(1,0) = %f, want ~1.0", tensor[5])
	}
}

// --- DrawDetections ---

func TestDrawDetections_DrawsBoxes(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	dets := []Detection{
		{Label: "cat", Confidence: 0.9, BBox: [4]float64{10, 10, 50, 50}},
	}
	DrawDetections(img, dets)

	// Top-left corner of the box should be red.
	c := img.RGBAAt(10, 10)
	if c.R != 255 {
		t.Fatalf("expected red at (10,10), got %v", c)
	}
	// Bottom-right corner of the box should be red.
	c2 := img.RGBAAt(50, 50)
	if c2.R != 255 {
		t.Fatalf("expected red at (50,50), got %v", c2)
	}
	// Point inside the box (but not on edge) should still be black.
	inner := img.RGBAAt(30, 30)
	if inner.R != 0 || inner.G != 0 || inner.B != 0 {
		t.Fatalf("inside box should be untouched, got %v", inner)
	}
}

func TestDrawDetections_ClampsToBounds(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 50, 50))
	dets := []Detection{
		{BBox: [4]float64{-10, -10, 100, 100}},
	}
	// Should not panic.
	DrawDetections(img, dets)
}

func TestDrawDetections_Empty(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	// No detections — should be a no-op.
	DrawDetections(img, nil)
	DrawDetections(img, []Detection{})
}

// --- test helpers ---

func fill(img *image.RGBA, c color.RGBA) {
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}
