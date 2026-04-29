// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

import "testing"

// TestParseForceKeyFramesExpr verifies the canonical HLS/DASH idiom
// `expr:gte(t,n_forced*2)` lands intact on Output.ForceKeyFrames.
func TestParseForceKeyFramesExpr(t *testing.T) {
	cfg, err := Parse(`ffmpeg -i in.mp4 -c:v libx264 -force_key_frames expr:gte(t,n_forced*2) out.mp4`)
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Outputs[0].ForceKeyFrames
	want := "expr:gte(t,n_forced*2)"
	if got != want {
		t.Errorf("ForceKeyFrames = %q, want %q", got, want)
	}
}

// TestParseForceKeyFramesSource verifies the `source` grammar.
func TestParseForceKeyFramesSource(t *testing.T) {
	cfg, err := Parse("ffmpeg -i in.mp4 -c:v libx264 -force_key_frames source out.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Outputs[0].ForceKeyFrames; got != "source" {
		t.Errorf("ForceKeyFrames = %q, want source", got)
	}
}

// TestParseForceKeyFramesTimeList verifies a comma-separated time
// list lands verbatim (sorting/parsing happens in pipeline at config
// load).
func TestParseForceKeyFramesTimeList(t *testing.T) {
	cfg, err := Parse("ffmpeg -i in.mp4 -c:v libx264 -force_key_frames 3,7.5,10.25 out.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Outputs[0].ForceKeyFrames; got != "3,7.5,10.25" {
		t.Errorf("ForceKeyFrames = %q", got)
	}
}

// TestParseForceKeyFramesRequiresArg covers the missing-arg branch.
func TestParseForceKeyFramesRequiresArg(t *testing.T) {
	if _, err := Parse("ffmpeg -i in.mp4 -c:v libx264 -force_key_frames"); err == nil {
		t.Fatal("expected error for missing -force_key_frames argument")
	}
}
