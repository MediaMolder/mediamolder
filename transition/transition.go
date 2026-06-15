// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

// Package transition implements MediaMolder's native video transitions.
//
// A transition composites two already-decoded, already-converted frames — the
// outgoing clip a and the incoming clip b — into a single output frame at a
// given progress. The progress convention is inherited verbatim from FFmpeg's
// libavfilter/vf_xfade.c so the per-pixel formulas can be ported one-to-one:
//
//	progress == 1.0  → output is fully a (the start of the transition window)
//	progress == 0.0  → output is fully b (the end of the transition window)
//
// (The sequence_editor's own timeline progress runs the other way — 0 at the
// start — so the caller passes 1-prog.)
//
// All three frames share the sequence pixel format and dimensions; transitions
// here assume 8-bit planar YUV (the sequence format), operating per plane so the
// chroma planes are handled at their subsampled resolution. Each registered
// transition fills every plane of out.
//
// # Adding a transition
//
// Most transitions are pointwise: each output sample depends only on the two
// co-located input samples plus the pixel's position and the progress. Write a
// pixelFunc and register it with registerPointwise — see builtin.go for ~two
// dozen worked examples. Transitions that fetch from a shifted coordinate (the
// slides) or need neighbourhood access register a RenderFunc directly. New
// names must also be added to processors.seqSupportedTransitions so the
// sequence_editor accepts them at Init.
package transition

import (
	"math"
	"sort"

	"github.com/MediaMolder/MediaMolder/av"
)

// RenderFunc composites outgoing frame a and incoming frame b into out at the
// given progress (1.0 = fully a, 0.0 = fully b; see the package doc). out is a
// freshly allocated writable frame with the same format and dimensions as a and
// b. A RenderFunc must write every plane of out.
type RenderFunc func(out, a, b *av.Frame, progress float64)

var registry = map[string]RenderFunc{}

// Register adds a transition under name. Intended to be called from init().
// It panics on a duplicate name so collisions surface at process start.
func Register(name string, fn RenderFunc) {
	if _, dup := registry[name]; dup {
		panic("transition: duplicate registration for " + name)
	}
	registry[name] = fn
}

// Lookup returns the RenderFunc registered under name, or ok == false.
func Lookup(name string) (fn RenderFunc, ok bool) {
	fn, ok = registry[name]
	return fn, ok
}

// Names returns the sorted names of all registered transitions.
func Names() []string {
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// pixelFunc computes one output sample (clamped to 0..255 by the caller) from
// the co-located input samples a and b at position (x,y) in a plane of size
// w×h. plane is the plane index (0 = luma, 1/2 = chroma) so background-dependent
// transitions can pick the right neutral level; progress is the transition
// progress (1 = a, 0 = b).
type pixelFunc func(a, b float64, x, y, w, h, plane int, progress float64) float64

// renderPointwise applies fn to every sample of every plane, reading the
// co-located a/b samples and writing out. Plane geometry is taken from out, so
// chroma planes are processed at their subsampled size.
func renderPointwise(out, a, b *av.Frame, progress float64, fn pixelFunc) {
	for plane := 0; plane < out.NumPlanes(); plane++ {
		w, h := out.PlaneWidth(plane), out.PlaneHeight(plane)
		ap, bp, dp := a.Plane(plane), b.Plane(plane), out.Plane(plane)
		al, bl, dl := a.Linesize(plane), b.Linesize(plane), out.Linesize(plane)
		if ap == nil || bp == nil || dp == nil {
			continue
		}
		for y := 0; y < h; y++ {
			arow, brow, drow := ap[y*al:], bp[y*bl:], dp[y*dl:]
			for x := 0; x < w; x++ {
				drow[x] = clip8(fn(float64(arow[x]), float64(brow[x]), x, y, w, h, plane, progress))
			}
		}
	}
}

// registerPointwise wraps a pixelFunc as a RenderFunc and registers it.
func registerPointwise(name string, fn pixelFunc) {
	Register(name, func(out, a, b *av.Frame, progress float64) {
		renderPointwise(out, a, b, progress, fn)
	})
}

// --- math helpers, mirroring the inline helpers in vf_xfade.c ---

// mix is xfade's mix(): a weighted by m, b by (1-m).
func mix(a, b, m float64) float64 { return a*m + b*(1-m) }

// smoothstep is the GLSL/xfade smoothstep with Hermite interpolation.
func smoothstep(edge0, edge1, x float64) float64 {
	if edge1 == edge0 {
		if x < edge0 {
			return 0
		}
		return 1
	}
	t := clampf((x-edge0)/(edge1-edge0), 0, 1)
	return t * t * (3 - 2*t)
}

// fract returns the fractional part (a - floor(a)).
func fract(a float64) float64 { return a - math.Floor(a) }

func clampf(x, lo, hi float64) float64 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}

// clip8 rounds and clamps a float sample to an 8-bit byte.
func clip8(v float64) byte {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return byte(v + 0.5)
}

// blackLevel/whiteLevel return xfade's 8-bit background values per plane,
// matching vf_xfade.c exactly (it uses full-range levels regardless of the
// frame's signalled range): luma black/white are 0/255, chroma is neutral at
// max_value/2 = 127 for both, and a fourth (alpha) plane is opaque (255).
func blackLevel(plane int) float64 {
	switch plane {
	case 0:
		return 0
	case 3:
		return 255
	default:
		return 127
	}
}

func whiteLevel(plane int) float64 {
	if plane == 1 || plane == 2 {
		return 127
	}
	return 255
}
