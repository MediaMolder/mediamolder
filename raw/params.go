// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package raw

// Params is the fixed, deterministic set of LibRaw develop parameters. They are applied to
// libraw_output_params_t before dcraw_process()/dcraw_make_mem_image() (see decode_libraw.go),
// and are NOT user-tunable in this version: the output depends only on the input file and the
// pinned LibRaw version (see pins.go), which is what makes a develop reproducible.
//
// The intent is a single faithful "camera-rendering-quality" develop — as-shot white balance,
// sRGB, no creative adjustments — not an editor. Each field documents the LibRaw struct member
// it maps to.
type Params struct {
	OutputBPS    int     // output_bps: 8 — 8-bit, matching the engine's canonical sRGB raster
	OutputColor  int     // output_color: 1 — sRGB primaries (same space as the libav path)
	GammaPower   float64 // gamm[0]: 1/2.4 — sRGB transfer curve power
	GammaSlope   float64 // gamm[1]: 12.92 — sRGB transfer curve toe slope
	NoAutoBright int     // no_auto_bright: 1 — disable histogram auto-exposure (predictable)
	UseCameraWB  int     // use_camera_wb: 1 — as-shot white balance from the file
	UseAutoWB    int     // use_auto_wb: 0 — never compute WB from image statistics
	UserQual     int     // user_qual: 3 — AHD demosaic (one pinned algorithm)
	HalfSize     int     // half_size: 0 — full resolution
	Highlight    int     // highlight: 0 — clip highlights (deterministic)
	UserFlip     int     // user_flip: 0 — NO rotation; the caller owns orientation
	FourColorRGB int     // four_color_rgb: 0 — off
	OutputTIFF   int     // output_tiff: 0 — we use make_mem_image, not TIFF
}

// DefaultParams are the pinned develop parameters used by [Decode]. UserFlip=0 is load-bearing:
// the raster is returned un-oriented so the caller applies orientation last (exactly as for the
// libav path); were LibRaw to auto-rotate, an oriented RAW would be double-rotated downstream.
var DefaultParams = Params{
	OutputBPS:    8,
	OutputColor:  1,
	GammaPower:   1.0 / 2.4,
	GammaSlope:   12.92,
	NoAutoBright: 1,
	UseCameraWB:  1,
	UseAutoWB:    0,
	UserQual:     3,
	HalfSize:     0,
	Highlight:    0,
	UserFlip:     0,
	FourColorRGB: 0,
	OutputTIFF:   0,
}
