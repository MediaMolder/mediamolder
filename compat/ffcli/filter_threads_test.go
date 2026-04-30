// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

import "testing"

// Wave 7 #38: -filter_complex_threads global flag round-trips into
// Config.FilterComplexThreads.
func TestParseFilterComplexThreads(t *testing.T) {
	cfg, err := ParseArgs([]string{
		"-filter_complex_threads", "4",
		"-i", "in.mp4",
		"-vf", "scale=1280:720",
		"-c:v", "libx264",
		"out.mp4",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.FilterComplexThreads != 4 {
		t.Fatalf("want FilterComplexThreads=4, got %d", cfg.FilterComplexThreads)
	}
}

func TestParseFilterComplexThreadsRequiresArg(t *testing.T) {
	_, err := ParseArgs([]string{"-filter_complex_threads"})
	if err == nil {
		t.Fatal("expected error for missing argument")
	}
}

func TestParseFilterComplexThreadsRejectsNegative(t *testing.T) {
	_, err := ParseArgs([]string{"-filter_complex_threads", "-1", "-i", "in.mp4", "out.mp4"})
	if err == nil {
		t.Fatal("expected error for negative value")
	}
}
