// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

import "testing"

// NewAudioFrame must allocate a writable fltp frame whose accessors report the
// requested geometry and whose per-channel float32 planes are independently
// addressable.
func TestNewAudioFrame(t *testing.T) {
	const sr, ch, n = 48000, 2, 1024
	f, err := NewAudioFrame(SampleFmtFLTP, ch, n, sr)
	if err != nil {
		t.Fatalf("NewAudioFrame: %v", err)
	}
	defer f.Close()

	if got := f.NbSamples(); got != n {
		t.Errorf("NbSamples=%d, want %d", got, n)
	}
	if got := f.Channels(); got != ch {
		t.Errorf("Channels=%d, want %d", got, ch)
	}
	if got := f.SampleRate(); got != sr {
		t.Errorf("SampleRate=%d, want %d", got, sr)
	}
	if got := f.SampleFmt(); got != SampleFmtFLTP {
		t.Errorf("SampleFmt=%d, want fltp(%d)", got, SampleFmtFLTP)
	}

	for c := 0; c < ch; c++ {
		p := f.SamplePlaneF32(c)
		if len(p) != n {
			t.Fatalf("SamplePlaneF32(%d) len=%d, want %d", c, len(p), n)
		}
		for i := range p {
			p[i] = float32(c) + float32(i)*0.001
		}
	}
	// Channels write independently (plane 1 untouched by plane 0 writes).
	if f.SamplePlaneF32(1)[0] != 1.0 {
		t.Errorf("plane 1 sample 0 = %v, want 1.0", f.SamplePlaneF32(1)[0])
	}
	// Out-of-range channel yields nil.
	if f.SamplePlaneF32(ch) != nil {
		t.Errorf("SamplePlaneF32 out of range should be nil")
	}
}

func TestSampleFormatHelpers(t *testing.T) {
	if got := SampleFormatFromName("fltp"); got != SampleFmtFLTP {
		t.Errorf("SampleFormatFromName(fltp)=%d, want %d", got, SampleFmtFLTP)
	}
	if got := SampleFormatFromName("not-a-format"); got != -1 {
		t.Errorf("SampleFormatFromName(bad)=%d, want -1", got)
	}
	if got := BytesPerSample(SampleFmtFLTP); got != 4 {
		t.Errorf("BytesPerSample(fltp)=%d, want 4", got)
	}
	if !SampleFmtIsPlanar(SampleFmtFLTP) {
		t.Errorf("fltp should be planar")
	}
}

// A video frame's audio accessors return zero-ish without panicking, so the
// audio-aware code can defensively query any frame.
func TestAudioAccessorsOnVideoFrame(t *testing.T) {
	v, err := NewVideoFrame(16, 16, 0) // yuv420p
	if err != nil {
		t.Fatalf("NewVideoFrame: %v", err)
	}
	defer v.Close()
	if v.SamplePlaneF32(0) != nil {
		t.Errorf("SamplePlaneF32 on non-fltp frame should be nil")
	}
}
