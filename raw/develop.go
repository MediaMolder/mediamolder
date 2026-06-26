// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package raw

import "image"

// This file holds the dependency-free types for the high-precision develop ([DecodeDevelop]),
// so they compile in every build (LibRaw or not). The decode itself is build-tagged
// (develop_libraw.go / develop_stub.go), mirroring [Decode].

// ColorSpace identifies the RGB primaries a developed raster is encoded in. A linear,
// high-precision develop (see [Develop]) is meaningless without it — the consumer needs it to
// colour-manage to the display.
type ColorSpace int

const (
	ColorSRGB      ColorSpace = iota // sRGB / Rec.709 primaries
	ColorRec2020                     // ITU-R BT.2020 primaries
	ColorProPhoto                    // ProPhoto / ROMM RGB primaries
	ColorXYZ                         // CIE XYZ
	ColorDisplayP3                   // Display P3 primaries
)

func (c ColorSpace) String() string {
	switch c {
	case ColorSRGB:
		return "sRGB"
	case ColorRec2020:
		return "Rec.2020"
	case ColorProPhoto:
		return "ProPhoto"
	case ColorXYZ:
		return "XYZ"
	case ColorDisplayP3:
		return "Display P3"
	default:
		return "unknown"
	}
}

// Develop is a high-precision RAW develop produced by [DecodeDevelop]: a 16-bit, linear-light,
// wide-gamut, un-oriented "master" with recovered (not clipped) highlights. It is scene-referred
// — the caller applies the display tone-map, transfer curve, colour-management and orientation.
// The alpha channel is fully opaque (0xFFFF).
//
// This is distinct from [Decode], which returns the canonical 8-bit sRGB raster (used for pixel
// hashing / the embedded-preview-equivalent path). Use [Develop] for high-quality display/export.
type Develop struct {
	Image      *image.NRGBA64 // 16-bit RGBA, linear light, un-oriented, A=0xFFFF
	ColorSpace ColorSpace     // primaries Image is encoded in (linear)
	Version    string         // [DevelopVersion] — for keying a downstream render cache
}

// DevelopVersion identifies the high-precision develop parameters together with the pinned
// LibRaw version, so a consumer can key a render cache on it and rebuild when either changes.
const DevelopVersion = "dev16-rec2020-linear-hl2-libraw" + LibRawVersion
