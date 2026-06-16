// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package acrossfade

import "math"

// The crossfade curves, ported from FFmpeg's af_afade.c fade_gain(). Each is a
// fade-in gain g(x) for x in [0,1]; the outgoing clip uses g(1-x). All are
// monotonic with g(0)=0 and g(1)=1.
func init() {
	// tri — triangular / linear. The default. Constant-amplitude sum; dips
	// ~3 dB in power at the mid-point.
	Register("tri", func(x float64) float64 { return x })

	// qsin — quarter sine wave. Equal-power crossfade (constant power, no
	// mid-point dip); the usual choice for unrelated material.
	Register("qsin", func(x float64) float64 { return math.Sin(x * math.Pi / 2) })

	// hsin — half sine wave. Smooth S-curve, slightly faster centre than qsin.
	Register("hsin", func(x float64) float64 { return (1 - math.Cos(x*math.Pi)) / 2 })

	// esin — smooth (Hermite smoothstep) S-curve with zero slope at both ends.
	Register("esin", func(x float64) float64 { return x * x * (3 - 2*x) })

	// qua — quadratic (slow-in). Incoming ramps up slowly then accelerates.
	Register("qua", func(x float64) float64 { return x * x })

	// cub — cubic (slower-in than qua).
	Register("cub", func(x float64) float64 { return x * x * x })

	// squ — square root (fast-in).
	Register("squ", func(x float64) float64 { return math.Sqrt(x) })

	// par — parabolic (fast-in, mirror of qua).
	Register("par", func(x float64) float64 { return 1 - (1-x)*(1-x) })

	// exp — exponential, normalised to g(0)=0, g(1)=1.
	Register("exp", func(x float64) float64 { return (math.Exp(x) - 1) / (math.E - 1) })

	// log — logarithmic (inverse of exp); concave, fast-in.
	Register("log", func(x float64) float64 { return math.Log(1 + (math.E-1)*x) })
}
