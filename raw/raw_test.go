// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package raw

import (
	"image"
	"image/color"
	"testing"
)

func TestIsRAW(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"a.nef", true},
		{"a.cr2", true},
		{"a.cr3", true},
		{"a.arw", true},
		{"a.raf", true},
		{"a.orf", true},
		{"a.rw2", true},
		{"a.pef", true},
		{"a.srw", true},
		{"a.dng", true},
		{"A.NEF", true},                  // case-insensitive
		{"/some/dir/IMG_0001.Cr2", true}, // mixed case + path
		{"a.jpg", false},
		{"a.jpeg", false},
		{"a.png", false},
		{"a.tiff", false},
		{"a.heic", false},
		{"noext", false},
		{"", false},
		{".dng", true}, // dotfile-style, still a RAW extension
	}
	for _, c := range cases {
		if got := IsRAW(c.path); got != c.want {
			t.Errorf("IsRAW(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestIsUniform(t *testing.T) {
	mk := func(fill color.RGBA) *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, 8, 5))
		for y := 0; y < 5; y++ {
			for x := 0; x < 8; x++ {
				img.SetRGBA(x, y, fill)
			}
		}
		return img
	}

	if !IsUniform(mk(color.RGBA{0, 0, 0, 255})) {
		t.Error("all-black image should be uniform")
	}
	if !IsUniform(mk(color.RGBA{10, 20, 30, 255})) {
		t.Error("constant-colour image should be uniform")
	}

	nonUniform := mk(color.RGBA{10, 20, 30, 255})
	nonUniform.SetRGBA(4, 2, color.RGBA{11, 20, 30, 255})
	if IsUniform(nonUniform) {
		t.Error("image with one differing pixel should not be uniform")
	}

	if !IsUniform(image.NewRGBA(image.Rect(0, 0, 0, 0))) {
		t.Error("empty image should be uniform")
	}
	if !IsUniform(nil) {
		t.Error("nil image should be uniform")
	}

	// Sub-image (non-zero origin, stride > 4×width) exercises the stride-aware fast path.
	sub := mk(color.RGBA{7, 7, 7, 255}).SubImage(image.Rect(2, 1, 6, 4)).(*image.RGBA)
	if !IsUniform(sub) {
		t.Error("uniform sub-image should be uniform")
	}
}

func TestVerifySource(t *testing.T) {
	if err := VerifySource([]byte("not the real tarball")); err == nil {
		t.Error("VerifySource should reject mismatched bytes")
	}
}
