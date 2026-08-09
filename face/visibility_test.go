// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package face

import (
	"image"
	"math"
	"testing"
)

// The pure mask→fraction math builds in every configuration; these tests pin its
// semantics: face-skin and hair are visible, everything else is coverage, and the
// expected-cover calibration maps a clean face to ~0 and a fully covered one to 1.
func TestOcclusionFromMask(t *testing.T) {
	const s = 16
	full := image.Rect(0, 0, s, s)

	fill := func(class uint8) []uint8 {
		m := make([]uint8, s*s)
		for i := range m {
			m[i] = class
		}
		return m
	}

	if got := occlusionFromMask(fill(visClassFaceSkin), s, s, full); got != 0 {
		t.Fatalf("all face-skin = %v, want 0", got)
	}
	if got := occlusionFromMask(fill(visClassHair), s, s, full); got != 0 {
		t.Fatalf("all hair = %v, want 0 (hair is a hairstyle, not an occluder)", got)
	}
	if got := occlusionFromMask(fill(visClassBodySkin), s, s, full); got != 1 {
		t.Fatalf("all body-skin (a hand) = %v, want 1", got)
	}
	if got := occlusionFromMask(fill(visClassBackground), s, s, full); got != 1 {
		t.Fatalf("all background = %v, want 1", got)
	}

	// Half face-skin, half body-skin: cover 0.5 → occlusion 1 − 0.5/0.85 ≈ 0.412.
	half := fill(visClassFaceSkin)
	for i := 0; i < s*s/2; i++ {
		half[i] = visClassBodySkin
	}
	want := 1 - 0.5/visExpectedCover
	if got := occlusionFromMask(half, s, s, full); math.Abs(got-want) > 1e-9 {
		t.Fatalf("half-covered = %v, want %v", got, want)
	}

	// Coverage above the calibration floor still reads as fully visible.
	nearFull := fill(visClassFaceSkin)
	for i := 0; i < s*s/10; i++ { // 10% background — under the 15% allowance
		nearFull[i] = visClassBackground
	}
	if got := occlusionFromMask(nearFull, s, s, full); got != 0 {
		t.Fatalf("90%%-covered clean face = %v, want 0 (within expected cover)", got)
	}

	// The measure region clips to the mask; a degenerate region yields 0, never NaN.
	if got := occlusionFromMask(fill(visClassBackground), s, s, image.Rect(-5, -5, 0, 0)); got != 0 {
		t.Fatalf("degenerate region = %v, want 0", got)
	}
	// Only the measured region counts: background outside a face-skin window is ignored.
	windowed := fill(visClassBackground)
	for y := 4; y < 12; y++ {
		for x := 4; x < 12; x++ {
			windowed[y*s+x] = visClassFaceSkin
		}
	}
	if got := occlusionFromMask(windowed, s, s, image.Rect(4, 4, 12, 12)); got != 0 {
		t.Fatalf("face-skin window = %v, want 0", got)
	}
}

func TestHeadContextAndMeasureRects(t *testing.T) {
	bounds := image.Rect(0, 0, 1000, 800)

	// Interior face: context expands by the margins, measure shrinks inside the bbox.
	bbox := [4]int{400, 300, 200, 200}
	ctx := headContextRect(bbox, bounds)
	if ctx.Min.X != 300 || ctx.Max.X != 700 { // ±0.5·w
		t.Fatalf("context X = %v", ctx)
	}
	if ctx.Min.Y != 150 || ctx.Max.Y != 650 { // ±0.75·h
		t.Fatalf("context Y = %v", ctx)
	}
	m := measureRect(bbox)
	if m != image.Rect(420, 320, 580, 480) { // 10% per side
		t.Fatalf("measure = %v", m)
	}

	// A face at the frame corner: the context clamps to bounds instead of going negative.
	edge := headContextRect([4]int{0, 0, 200, 200}, bounds)
	if edge.Min.X != 0 || edge.Min.Y != 0 {
		t.Fatalf("edge context not clamped: %v", edge)
	}
	// Degenerate boxes resolve to empty, which the caller rejects.
	if !headContextRect([4]int{10, 10, 0, 5}, bounds).Empty() {
		t.Fatal("zero-width bbox must yield an empty context")
	}
}

// letterboxGeom must reproduce letterbox()'s geometry exactly — same scale, same centred
// offsets — for representative shapes (wide, tall, square, upscale).
func TestLetterboxGeomMatchesLetterbox(t *testing.T) {
	for _, tc := range []struct{ w, h int }{{640, 480}, {480, 640}, {256, 256}, {100, 30}, {30, 100}, {3000, 2000}} {
		scale, offX, offY := letterboxGeom(tc.w, tc.h, 256, 256)
		newW := int(math.Round(float64(tc.w) * scale))
		newH := int(math.Round(float64(tc.h) * scale))
		if newW > 256 || newH > 256 {
			t.Fatalf("%dx%d: scaled %dx%d exceeds target", tc.w, tc.h, newW, newH)
		}
		if wantX := (256 - newW) / 2; offX != wantX {
			t.Fatalf("%dx%d: offX = %d, want %d", tc.w, tc.h, offX, wantX)
		}
		if wantY := (256 - newH) / 2; offY != wantY {
			t.Fatalf("%dx%d: offY = %d, want %d", tc.w, tc.h, offY, wantY)
		}
		// The scaled long side fills the target (letterbox invariant).
		if newW != 256 && newH != 256 {
			t.Fatalf("%dx%d: neither side fills the target (%dx%d)", tc.w, tc.h, newW, newH)
		}
	}
}
