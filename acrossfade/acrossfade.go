// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

// Package acrossfade is the native Go audio crossfade engine used by the
// sequence_editor to mix the outgoing and incoming clips' audio across a
// transition window. It is the audio analogue of package transition: a small
// registry of named gain curves (triangular, equal-power, exponential, …) plus
// a Mix helper that blends two planar-float (fltp) frames sample-by-sample.
//
// The curves are ported from FFmpeg's af_afade.c fade-gain functions so the
// crossfade names and shapes match what users expect from `acrossfade`. Each
// curve is expressed as a fade-in gain g(x) for x in [0,1] (0 → silent, 1 →
// unity); the outgoing clip is faded with g(1-x). For the equal-power curve
// (qsin) this yields constant power across the transition (no mid-point dip).
//
// See docs/architecture/transitions.md for the design and how to add a curve.
package acrossfade

import (
	"sort"

	"github.com/MediaMolder/MediaMolder/av"
)

// CurveFunc maps a transition fraction x in [0,1] to a fade-in gain in [0,1].
// The incoming clip is scaled by CurveFunc(x); the outgoing clip by
// CurveFunc(1-x).
type CurveFunc func(x float64) float64

// DefaultCurve is used when a crossfade is requested with an empty/unknown
// name. "tri" (triangular/linear) matches FFmpeg's acrossfade default.
const DefaultCurve = "tri"

var registry = map[string]CurveFunc{}

// Register adds a named crossfade curve. It panics on a duplicate name so
// collisions are caught at init time.
func Register(name string, fn CurveFunc) {
	if _, dup := registry[name]; dup {
		panic("acrossfade: duplicate curve " + name)
	}
	registry[name] = fn
}

// Lookup returns the curve registered under name.
func Lookup(name string) (CurveFunc, bool) {
	fn, ok := registry[name]
	return fn, ok
}

// Names returns the sorted list of registered curve names. Exposed so the GUI's
// audio crossfade picker stays in lockstep with what the engine implements.
func Names() []string {
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Mix blends the outgoing frame a and incoming frame b into out using the named
// curve, ramping the transition fraction linearly from p0 (first sample) to p1
// (last sample). All three frames must be planar-float (fltp) with the same
// channel count; out provides the sample count (samples beyond a's or b's length
// are treated as silence on that side). A nil a or b contributes silence. An
// unknown curve name falls back to DefaultCurve.
//
// Mix never closes its inputs; the caller owns a, b and out.
func Mix(curve string, out, a, b *av.Frame, p0, p1 float64) {
	if out == nil {
		return
	}
	fn, ok := Lookup(curve)
	if !ok {
		fn, _ = Lookup(DefaultCurve)
	}
	if fn == nil { // DefaultCurve always registered; defensive
		fn = func(x float64) float64 { return x }
	}
	n := out.NbSamples()
	if a != nil && a.NbSamples() < n {
		n = a.NbSamples()
	}
	if b != nil && b.NbSamples() < n {
		n = b.NbSamples()
	}
	ch := out.Channels()
	for c := 0; c < ch; c++ {
		od := out.SamplePlaneF32(c)
		if od == nil {
			continue
		}
		var ad, bd []float32
		if a != nil {
			ad = a.SamplePlaneF32(c)
		}
		if b != nil {
			bd = b.SamplePlaneF32(c)
		}
		for i := 0; i < n; i++ {
			x := p0
			if n > 1 {
				x = p0 + (p1-p0)*float64(i)/float64(n-1)
			}
			if x < 0 {
				x = 0
			} else if x > 1 {
				x = 1
			}
			gIn := fn(x)
			gOut := fn(1 - x)
			var av32, bv float32
			if ad != nil {
				av32 = ad[i]
			}
			if bd != nil {
				bv = bd[i]
			}
			od[i] = av32*float32(gOut) + bv*float32(gIn)
		}
		// Any trailing out samples (when out is longer than the shorter input)
		// keep whatever they held; callers allocate out at exactly n.
	}
}
