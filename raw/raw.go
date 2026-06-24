// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

// Package raw provides true camera-RAW decoding — a full demosaiced, white-balanced develop
// to an 8-bit sRGB raster — behind a small, stable API: [Capable], [IsRAW] and [Decode]. It is
// the single boundary that contains the optional LibRaw dependency: the real decoder is
// compiled only under the `with_libraw` build tag (see decode_libraw.go); the default build
// links a stub (decode_stub.go) so callers compile and run with no native dependency.
//
// Why this exists: libav (the engine's normal image path) is a codec library, not a RAW
// pipeline — it renders camera RAW to a black/garbled frame because it does not demosaic the
// colour-filter-array sensor data or apply white balance. LibRaw (digiKam, Krita, ImageMagick)
// is the field-standard develop pipeline; this package wraps it with a fixed, deterministic set
// of parameters (see params.go) so a given file + a given pinned LibRaw version (see pins.go)
// always yields the same raster.
//
// This file holds the pure-Go, dependency-free core ([IsRAW], [IsUniform]) so it compiles and
// is unit-tested in every build, LibRaw or not. [Capable] and [Decode] are supplied by the
// build-tagged files.
package raw

import (
	"errors"
	"image"
	"path/filepath"
	"strings"
)

// ErrUnsupported is returned by [Decode] when this build cannot develop RAW — the default
// `!with_libraw` build — or when the path is not a recognised camera-RAW extension.
var ErrUnsupported = errors.New("raw: RAW decode not available in this build")

// rawExts is the set of camera-RAW extensions [IsRAW] recognises (lower-case, leading dot).
// This is the single source of truth for "is this a RAW file" — callers branch on [IsRAW]
// before invoking [Decode]. Kept deliberately to the formats the develop path commits to;
// LibRaw itself reads many more, which can be added here without changing the contract.
var rawExts = map[string]bool{
	".nef": true, // Nikon
	".cr2": true, // Canon (CR2)
	".cr3": true, // Canon (CR3)
	".arw": true, // Sony
	".raf": true, // Fujifilm
	".orf": true, // Olympus / OM System
	".rw2": true, // Panasonic
	".pef": true, // Pentax
	".srw": true, // Samsung
	".dng": true, // Adobe Digital Negative
}

// IsRAW reports whether path has a supported camera-RAW extension. It is a cheap, pure
// extension check (no I/O) and works in every build, so callers can branch before calling
// [Decode], which only develops RAW in a `with_libraw` build.
func IsRAW(path string) bool {
	return rawExts[strings.ToLower(filepath.Ext(path))]
}

// IsUniform reports whether every pixel of img is identical to the top-left pixel — the
// signature of a failed develop (a flat black or uniform frame). Callers may guard a develop
// result with it: a valid RAW never decodes to a uniform raster, so a uniform result means the
// decode silently failed and the caller should fall back. A nil or empty image is uniform.
func IsUniform(img image.Image) bool {
	if img == nil {
		return true
	}
	b := img.Bounds()
	if b.Empty() {
		return true
	}
	if rgba, ok := img.(*image.RGBA); ok {
		return rgbaUniform(rgba)
	}
	r0, g0, b0, a0 := img.At(b.Min.X, b.Min.Y).RGBA()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			if r != r0 || g != g0 || bl != b0 || a != a0 {
				return false
			}
		}
	}
	return true
}

// rgbaUniform is the fast path of [IsUniform] for *image.RGBA: it compares 4-byte pixels
// directly, honouring the row stride (which may exceed 4×width).
func rgbaUniform(img *image.RGBA) bool {
	b := img.Bounds()
	w := b.Dx()
	first := img.PixOffset(b.Min.X, b.Min.Y)
	p0 := img.Pix[first : first+4]
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := img.PixOffset(b.Min.X, y)
		for x := 0; x < w; x++ {
			px := img.Pix[row+x*4 : row+x*4+4]
			if px[0] != p0[0] || px[1] != p0[1] || px[2] != p0[2] || px[3] != p0[3] {
				return false
			}
		}
	}
	return true
}
