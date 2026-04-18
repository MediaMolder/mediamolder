// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

import "testing"

func TestToRGBA_YUV420P(t *testing.T) {
	// AV_PIX_FMT_YUV420P = 0
	f := NewTestFrame(t, 64, 48, 0)
	defer f.Close()

	img, err := f.ToRGBA()
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 64 || img.Bounds().Dy() != 48 {
		t.Fatalf("expected 64×48 image, got %d×%d", img.Bounds().Dx(), img.Bounds().Dy())
	}
	if len(img.Pix) != 64*48*4 {
		t.Fatalf("expected %d bytes, got %d", 64*48*4, len(img.Pix))
	}
}

func TestToRGBA_RGB24(t *testing.T) {
	// AV_PIX_FMT_RGB24 = 2
	f := NewTestFrame(t, 32, 32, 2)
	defer f.Close()

	// Fill with a known colour.
	FillTestFrameRGB24(f, 200, 100, 50)

	img, err := f.ToRGBA()
	if err != nil {
		t.Fatal(err)
	}
	// Check centre pixel is approximately correct (swscale may round slightly).
	c := img.RGBAAt(16, 16)
	if c.R < 190 || c.R > 210 || c.G < 90 || c.G > 110 || c.B < 40 || c.B > 60 {
		t.Fatalf("expected ~(200,100,50) got (%d,%d,%d)", c.R, c.G, c.B)
	}
	if c.A != 255 {
		t.Fatalf("expected alpha 255, got %d", c.A)
	}
}

func TestToRGBA_NilFrame(t *testing.T) {
	var f *Frame
	_, err := f.ToRGBA()
	if err == nil {
		t.Fatal("expected error for nil frame")
	}
}

func TestToRGBA_ZeroDimension(t *testing.T) {
	f, err := AllocFrame()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// Frame allocated but no width/height set.
	_, err = f.ToRGBA()
	if err == nil {
		t.Fatal("expected error for zero-dimension frame")
	}
}

func TestPixFmt(t *testing.T) {
	f := NewTestFrame(t, 16, 16, 0)
	defer f.Close()
	if f.PixFmt() != 0 {
		t.Fatalf("expected pix_fmt 0 (YUV420P), got %d", f.PixFmt())
	}
}

func TestPixFmtRGBA(t *testing.T) {
	// AV_PIX_FMT_RGBA is implementation-defined; just check it's positive.
	v := PixFmtRGBA()
	if v < 0 {
		t.Fatalf("expected positive pix fmt for RGBA, got %d", v)
	}
}
