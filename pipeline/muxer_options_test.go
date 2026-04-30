// Copyright (C) 2026 Thomas Vaughan
//
// SPDX-License-Identifier: GPL-3.0-or-later

package pipeline

import "testing"

// TestBuildMuxerOptionsTimestampPolicy covers Wave 6 #29: per-output
// muxdelay / muxpreload / avoid_negative_ts emit the libavformat
// AVDict keys that avformat_write_header consumes.
func TestBuildMuxerOptionsTimestampPolicy(t *testing.T) {
	out := &Output{
		ID:              "o0",
		MuxDelay:        0.7,
		MuxPreload:      0.25,
		AvoidNegativeTS: "make_zero",
	}
	got := buildMuxerOptions(out)
	if got["max_delay"] != "700000" {
		t.Errorf("max_delay = %q, want 700000 (0.7s * AV_TIME_BASE)", got["max_delay"])
	}
	if got["preload"] != "250000" {
		t.Errorf("preload = %q, want 250000 (0.25s * AV_TIME_BASE)", got["preload"])
	}
	if got["avoid_negative_ts"] != "make_zero" {
		t.Errorf("avoid_negative_ts = %q, want make_zero", got["avoid_negative_ts"])
	}
}

// TestValidateStartAtZeroRequiresCopyTS verifies the cross-field
// constraint described at fftools/ffmpeg_demux.c L486 — start_at_zero
// only modulates the existing -copyts shift suppression, so without
// copy_ts it would be a no-op (and silently no-op'd flags are bug
// magnets).
func TestValidateStartAtZeroRequiresCopyTS(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.0",
		StartAtZero:   true,
		Inputs:        []Input{{ID: "in0", URL: "in.mp4", Streams: []StreamSelect{{Type: "video"}}}},
		Outputs:       []Output{{ID: "out0", URL: "out.mp4"}},
	}
	if err := validate(cfg); err == nil {
		t.Fatal("expected validate to reject start_at_zero without copy_ts")
	}
	cfg.CopyTS = true
	if err := validate(cfg); err != nil {
		t.Fatalf("expected validate to accept start_at_zero with copy_ts, got: %v", err)
	}
}

// TestValidateAvoidNegativeTSEnum guards the AvoidNegativeTS enum.
func TestValidateAvoidNegativeTSEnum(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.0",
		Inputs:        []Input{{ID: "in0", URL: "in.mp4", Streams: []StreamSelect{{Type: "video"}}}},
		Outputs:       []Output{{ID: "out0", URL: "out.mp4", AvoidNegativeTS: "bogus"}},
	}
	if err := validate(cfg); err == nil {
		t.Fatal("expected validate to reject bogus avoid_negative_ts value")
	}
	for _, v := range []string{"", "auto", "disabled", "make_non_negative", "make_zero"} {
		cfg.Outputs[0].AvoidNegativeTS = v
		if err := validate(cfg); err != nil {
			t.Errorf("avoid_negative_ts=%q: unexpected error %v", v, err)
		}
	}
}
