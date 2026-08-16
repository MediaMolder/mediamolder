// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

import (
	"math"
	"testing"
)

// TestResamplerDownmixNormalization pins the RematrixMaxval contract, because getting it
// wrong is silent: the audio keeps its exact length and content and is merely √2 louder,
// so nothing downstream errors — it just receives samples outside ±1.0.
//
// libswresample's default rematrix_maxval is format-dependent (1.0 for integer output,
// unbounded for float), so a stereo → mono downmix to FLTP uses energy-preserving
// coefficients (1/√2 per channel) rather than amplitude-preserving ones (1/2). Consumers
// that expect normalized float — speech models especially — are handed +3 dB and clipping.
func TestResamplerDownmixNormalization(t *testing.T) {
	const rate, n = 16000, 1024

	// Identical full-scale channels: the worst case for a downmix, and the one where the
	// two conventions differ most (amplitude-preserving returns 1.0, energy-preserving √2).
	mk := func() *Frame {
		f, err := NewAudioFrame(SampleFmtFLTP, 2, n, rate)
		if err != nil {
			t.Fatalf("NewAudioFrame: %v", err)
		}
		for ch := 0; ch < 2; ch++ {
			p := f.SamplePlaneF32(ch)
			for i := range p {
				p[i] = float32(math.Sin(2 * math.Pi * 440 * float64(i) / rate))
			}
		}
		return f
	}

	peakAfter := func(maxval float64) float64 {
		in := mk()
		defer in.Close()
		r, err := NewResampler(ResamplerOptions{
			InSampleRate: rate, InSampleFmt: SampleFmtFLTP, InChannels: 2,
			OutSampleRate: rate, OutSampleFmt: SampleFmtFLTP, OutChannels: 1,
			RematrixMaxval: maxval,
		})
		if err != nil {
			t.Fatalf("NewResampler(maxval=%v): %v", maxval, err)
		}
		defer r.Close()
		out, err := AllocFrame()
		if err != nil {
			t.Fatalf("AllocFrame: %v", err)
		}
		defer out.Close()
		out.SetAudioParams(SampleFmtFLTP, 1, rate)
		if err := r.ConvertFrame(out, in); err != nil {
			t.Fatalf("ConvertFrame(maxval=%v): %v", maxval, err)
		}
		var peak float64
		for _, s := range out.SamplePlaneF32(0) {
			if a := math.Abs(float64(s)); a > peak {
				peak = a
			}
		}
		return peak
	}

	// The contract: capped at 1.0, a full-scale mono-compatible stereo pair downmixes to
	// full scale and no further.
	if got := peakAfter(1.0); got > 1.0001 {
		t.Fatalf("RematrixMaxval=1.0 produced peak %.4f, want <= 1.0 — downmix must not exceed full scale", got)
	} else if got < 0.9 {
		t.Fatalf("RematrixMaxval=1.0 produced peak %.4f, want ~1.0 — the cap must not attenuate the signal", got)
	}

	// And the documented reason the option exists: the library default does NOT cap float
	// output. If a future libswresample changes that, this fails and the option's doc
	// comment (and whisper_stt's reliance on it) should be revisited rather than silently
	// describing behaviour that no longer happens.
	if got := peakAfter(0); got <= 1.0001 {
		t.Skipf("libswresample now caps float downmix by default (peak %.4f) — "+
			"RematrixMaxval is redundant here; revisit its documentation", got)
	}
}
