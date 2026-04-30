// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

import "testing"

// TestParseEncoderTiming covers Wave 6 #33: per-output `-enc_time_base`,
// `-field_order`, and `-flags +ildct+ilme` round-trip onto Output.
func TestParseEncoderTiming(t *testing.T) {
	cfg, err := Parse("ffmpeg -i in.mp4 -c:v libx264 -enc_time_base 1/30000 -field_order tt -flags +ildct+ilme out.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Outputs) != 1 {
		t.Fatalf("Outputs = %d, want 1", len(cfg.Outputs))
	}
	out := cfg.Outputs[0]
	if got := out.EncoderTimeBase; got != "1/30000" {
		t.Errorf("EncoderTimeBase = %q, want %q", got, "1/30000")
	}
	if got := out.FieldOrder; got != "tt" {
		t.Errorf("FieldOrder = %q, want %q", got, "tt")
	}
	if !out.InterlacedEncode {
		t.Error("InterlacedEncode = false, want true")
	}
}

func TestParseEncoderTimingErrors(t *testing.T) {
	cases := []string{
		"ffmpeg -i in.mp4 -enc_time_base out.mp4",
		"ffmpeg -i in.mp4 -field_order out.mp4",
		"ffmpeg -i in.mp4 -flags out.mp4",
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			if _, err := Parse(c); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}
