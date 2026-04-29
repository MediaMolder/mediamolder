// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

import "testing"

func TestParseAspectFlag(t *testing.T) {
	cfg, err := ParseArgs([]string{
		"-i", "in.mp4",
		"-c:v", "libx264",
		"-aspect", "16:9",
		"-f", "mp4", "out.mp4",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := cfg.Outputs[0].DAR; got != "16:9" {
		t.Fatalf("DAR: got %q want 16:9", got)
	}
	if got := cfg.Outputs[0].SAR; got != "" {
		t.Fatalf("SAR: got %q want empty", got)
	}
}

func TestParseSetSARSetDAR(t *testing.T) {
	cfg, err := ParseArgs([]string{
		"-i", "in.mp4",
		"-c:v", "libx264",
		"-setsar", "16:15",
		"-f", "mp4", "out.mp4",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := cfg.Outputs[0].SAR; got != "16:15" {
		t.Fatalf("SAR: got %q want 16:15", got)
	}

	cfg, err = ParseArgs([]string{
		"-i", "in.mp4",
		"-c:v", "libx264",
		"-setdar", "4:3",
		"-f", "mp4", "out.mp4",
	})
	if err != nil {
		t.Fatalf("parse setdar: %v", err)
	}
	if got := cfg.Outputs[0].DAR; got != "4:3" {
		t.Fatalf("DAR: got %q want 4:3", got)
	}
}

func TestParseAspectMissingArg(t *testing.T) {
	if _, err := ParseArgs([]string{"-i", "in.mp4", "-aspect"}); err == nil {
		t.Error("expected error for -aspect without arg")
	}
}
