// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build with_libraw

package raw

import (
	"bytes"
	"image"
	"testing"
)

// These tests exercise the real LibRaw develop (with_libraw build). The fixture
// raw/testdata/sample.dng is a tiny, self-authored RGGB-Bayer DNG (CC0) whose CFA data is a
// deterministic gradient, so a correct demosaic yields a non-uniform sRGB raster.

const (
	fixture  = "testdata/sample.dng"
	fixtureW = 64 // sensor width; LibRaw may trim a few border pixels on develop
	fixtureH = 48 // sensor height (landscape — user_flip=0 keeps it landscape)
)

func decodeFixture(t *testing.T) *image.RGBA {
	t.Helper()
	if !Capable() {
		t.Skip("raw: not built with_libraw")
	}
	img, err := Decode(fixture)
	if err != nil {
		t.Fatalf("Decode(%s): %v", fixture, err)
	}
	rgba, ok := img.(*image.RGBA)
	if !ok {
		t.Fatalf("Decode returned %T, want *image.RGBA", img)
	}
	return rgba
}

func TestDecodeFixture(t *testing.T) {
	rgba := decodeFixture(t)
	b := rgba.Bounds()
	t.Logf("decoded %dx%d", b.Dx(), b.Dy())

	if b.Dx() <= 0 || b.Dy() <= 0 {
		t.Fatalf("empty image %dx%d", b.Dx(), b.Dy())
	}
	// LibRaw may crop a small border; output must be close to the sensor size and never larger.
	if b.Dx() > fixtureW || b.Dy() > fixtureH || b.Dx() < fixtureW-8 || b.Dy() < fixtureH-8 {
		t.Errorf("unexpected dims %dx%d, want ~%dx%d", b.Dx(), b.Dy(), fixtureW, fixtureH)
	}
	// user_flip=0: no rotation, so a landscape sensor stays landscape.
	if b.Dx() <= b.Dy() {
		t.Errorf("output not landscape (%dx%d) — orientation should be un-applied", b.Dx(), b.Dy())
	}
	// A real develop is never a flat/black frame.
	if IsUniform(rgba) {
		t.Error("decoded image is uniform — develop produced a flat frame")
	}
	// Fully opaque.
	for i := 3; i < len(rgba.Pix); i += 4 {
		if rgba.Pix[i] != 0xFF {
			t.Fatalf("pixel %d alpha = %d, want 255", i/4, rgba.Pix[i])
		}
	}
}

func TestDecodeDeterministic(t *testing.T) {
	a := decodeFixture(t)
	b := decodeFixture(t)
	if a.Bounds() != b.Bounds() {
		t.Fatalf("dims differ: %v vs %v", a.Bounds(), b.Bounds())
	}
	if !bytes.Equal(a.Pix, b.Pix) {
		t.Error("two decodes of the same file differ — develop is not deterministic")
	}
}

func TestDecodeNonRAWUnsupported(t *testing.T) {
	if _, err := Decode("testdata/sample.jpg"); err != ErrUnsupported {
		t.Errorf("Decode(non-RAW) err = %v, want ErrUnsupported", err)
	}
}
