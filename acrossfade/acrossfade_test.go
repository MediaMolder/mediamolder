// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package acrossfade

import (
	"math"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
)

// Every registered curve must be a valid fade-in gain: g(0)=0, g(1)=1, and
// monotonic non-decreasing across [0,1]. A broken curve would click or invert
// the crossfade.
func TestCurvesAreValidFadeIns(t *testing.T) {
	for _, name := range Names() {
		fn, ok := Lookup(name)
		if !ok {
			t.Fatalf("Names() listed %q but Lookup failed", name)
		}
		if g := fn(0); math.Abs(g) > 1e-6 {
			t.Errorf("curve %q: g(0)=%v, want 0", name, g)
		}
		if g := fn(1); math.Abs(g-1) > 1e-6 {
			t.Errorf("curve %q: g(1)=%v, want 1", name, g)
		}
		prev := fn(0)
		for i := 1; i <= 100; i++ {
			x := float64(i) / 100
			g := fn(x)
			if g < prev-1e-9 {
				t.Errorf("curve %q: not monotonic at x=%.2f (%v < %v)", name, x, g, prev)
			}
			prev = g
		}
	}
}

// The default curve must be registered (Mix falls back to it).
func TestDefaultCurveRegistered(t *testing.T) {
	if _, ok := Lookup(DefaultCurve); !ok {
		t.Fatalf("DefaultCurve %q is not registered", DefaultCurve)
	}
}

// qsin is the equal-power curve: g_in(x)^2 + g_out(x)^2 == 1 for all x, so the
// summed power stays constant across the crossfade (no mid-point dip).
func TestEqualPowerQsin(t *testing.T) {
	fn, _ := Lookup("qsin")
	for i := 0; i <= 100; i++ {
		x := float64(i) / 100
		gIn := fn(x)
		gOut := fn(1 - x)
		if p := gIn*gIn + gOut*gOut; math.Abs(p-1) > 1e-9 {
			t.Fatalf("qsin not equal-power at x=%.2f: power=%v", x, p)
		}
	}
}

// Mix at progress 0 must reproduce the outgoing signal exactly and at progress 1
// the incoming signal, for the triangular (linear) curve.
func TestMixEndpoints(t *testing.T) {
	const n, ch, sr = 64, 2, 48000
	mk := func(v float32) *av.Frame {
		f, err := av.NewAudioFrame(av.SampleFmtFLTP, ch, n, sr)
		if err != nil {
			t.Fatalf("NewAudioFrame: %v", err)
		}
		for c := 0; c < ch; c++ {
			p := f.SamplePlaneF32(c)
			for i := range p {
				p[i] = v
			}
		}
		return f
	}
	a := mk(0.8)
	b := mk(-0.5)
	defer a.Close()
	defer b.Close()

	out0, _ := av.NewAudioFrame(av.SampleFmtFLTP, ch, n, sr)
	defer out0.Close()
	Mix("tri", out0, a, b, 0, 0)
	if got := out0.SamplePlaneF32(0)[0]; math.Abs(float64(got)-0.8) > 1e-6 {
		t.Errorf("Mix p=0: got %v, want outgoing 0.8", got)
	}

	out1, _ := av.NewAudioFrame(av.SampleFmtFLTP, ch, n, sr)
	defer out1.Close()
	Mix("tri", out1, a, b, 1, 1)
	if got := out1.SamplePlaneF32(0)[0]; math.Abs(float64(got)-(-0.5)) > 1e-6 {
		t.Errorf("Mix p=1: got %v, want incoming -0.5", got)
	}

	// Midpoint of a triangular crossfade is the average of the two.
	outM, _ := av.NewAudioFrame(av.SampleFmtFLTP, ch, n, sr)
	defer outM.Close()
	Mix("tri", outM, a, b, 0.5, 0.5)
	if got := outM.SamplePlaneF32(0)[0]; math.Abs(float64(got)-0.15) > 1e-6 {
		t.Errorf("Mix p=0.5 tri: got %v, want 0.15", got)
	}
}

// A nil incoming frame contributes silence (used at the very start of a fade in
// before the incoming clip exists).
func TestMixNilSide(t *testing.T) {
	const n, ch, sr = 16, 1, 48000
	a, _ := av.NewAudioFrame(av.SampleFmtFLTP, ch, n, sr)
	defer a.Close()
	for i := range a.SamplePlaneF32(0) {
		a.SamplePlaneF32(0)[i] = 1.0
	}
	out, _ := av.NewAudioFrame(av.SampleFmtFLTP, ch, n, sr)
	defer out.Close()
	Mix("tri", out, a, nil, 0.25, 0.25)
	// gOut(0.25) = 1-0.25 = 0.75; incoming is silent.
	if got := out.SamplePlaneF32(0)[0]; math.Abs(float64(got)-0.75) > 1e-6 {
		t.Errorf("Mix nil incoming: got %v, want 0.75", got)
	}
}
