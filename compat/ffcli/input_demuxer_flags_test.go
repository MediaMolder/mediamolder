// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

import "testing"

// TestParseStreamLoop covers the per-input integer flag and its
// latch-on-next-input semantics. Mirrors fftools/ffmpeg_opt.c's
// `OPT_INPUT | OPT_OFFSET` storage on InputFile.loop.
func TestParseStreamLoop(t *testing.T) {
	cases := []struct {
		name string
		cli  string
		want int
	}{
		{"finite", "ffmpeg -stream_loop 3 -i in.mp4 -c copy out.mkv", 3},
		{"infinite", "ffmpeg -stream_loop -1 -i in.mp4 -c copy out.mkv", -1},
		{"none", "ffmpeg -i in.mp4 -c copy out.mkv", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Parse(tc.cli)
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.Inputs[0].StreamLoop; got != tc.want {
				t.Errorf("StreamLoop = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestParseStreamLoopRejectsInvalid covers value validation
// (must be integer, must be >= -1).
func TestParseStreamLoopRejectsInvalid(t *testing.T) {
	cases := []string{
		"ffmpeg -stream_loop -2 -i in.mp4 -c copy out.mkv",
		"ffmpeg -stream_loop banana -i in.mp4 -c copy out.mkv",
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			if _, err := Parse(c); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

// TestParseITSOffset covers the per-input float flag (positive,
// negative, sub-second). Mirrors ffmpeg `-itsoffset T`.
func TestParseITSOffset(t *testing.T) {
	cases := []struct {
		cli  string
		want float64
	}{
		{"ffmpeg -itsoffset 5 -i in.mp4 -c copy out.mkv", 5.0},
		{"ffmpeg -itsoffset -0.030 -i in.mp4 -c copy out.mkv", -0.030},
	}
	for _, tc := range cases {
		t.Run(tc.cli, func(t *testing.T) {
			cfg, err := Parse(tc.cli)
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.Inputs[0].ITSOffset; got != tc.want {
				t.Errorf("ITSOffset = %g, want %g", got, tc.want)
			}
		})
	}
}

// TestParseRe covers the `-re` shorthand for `-readrate 1`.
func TestParseRe(t *testing.T) {
	cfg, err := Parse("ffmpeg -re -i in.mp4 -c copy out.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Inputs[0].ReadRate; got != 1.0 {
		t.Errorf("ReadRate = %g, want 1.0 (-re shorthand)", got)
	}
}

// TestParseReadRateAndBurst covers `-readrate FACTOR` and
// `-readrate_initial_burst SECS` and `-readrate_catchup`.
func TestParseReadRateAndBurst(t *testing.T) {
	cfg, err := Parse("ffmpeg -readrate 2.0 -readrate_initial_burst 1.5 -readrate_catchup 2.5 -i in.mp4 -c copy out.mkv")
	if err != nil {
		t.Fatal(err)
	}
	in := cfg.Inputs[0]
	if in.ReadRate != 2.0 {
		t.Errorf("ReadRate = %g, want 2.0", in.ReadRate)
	}
	if in.ReadRateInitialBurst != 1.5 {
		t.Errorf("ReadRateInitialBurst = %g, want 1.5", in.ReadRateInitialBurst)
	}
	if in.ReadRateCatchup != 2.5 {
		t.Errorf("ReadRateCatchup = %g, want 2.5", in.ReadRateCatchup)
	}
}

// TestParseReadRateRejectsInvalid covers value validation.
func TestParseReadRateRejectsInvalid(t *testing.T) {
	cases := []string{
		"ffmpeg -readrate -1 -i in.mp4 out.mkv",
		"ffmpeg -readrate banana -i in.mp4 out.mkv",
		"ffmpeg -readrate_initial_burst -1 -i in.mp4 out.mkv",
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			if _, err := Parse(c); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

// TestParseDemuxerFlagsLatchToNextInput verifies the
// fftools/ffmpeg_opt.c OPT_INPUT semantics: a `-stream_loop` /
// `-itsoffset` / `-re` placed before a specific `-i` attaches to
// that input only, not to a later one.
func TestParseDemuxerFlagsLatchToNextInput(t *testing.T) {
	cfg, err := Parse("ffmpeg -stream_loop -1 -i logo.png -itsoffset 2 -i main.mp4 -c copy out.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Inputs) != 2 {
		t.Fatalf("Inputs = %d, want 2", len(cfg.Inputs))
	}
	if cfg.Inputs[0].StreamLoop != -1 {
		t.Errorf("Inputs[0].StreamLoop = %d, want -1 (latched to logo.png only)", cfg.Inputs[0].StreamLoop)
	}
	if cfg.Inputs[0].ITSOffset != 0 {
		t.Errorf("Inputs[0].ITSOffset = %g, want 0", cfg.Inputs[0].ITSOffset)
	}
	if cfg.Inputs[1].StreamLoop != 0 {
		t.Errorf("Inputs[1].StreamLoop = %d, want 0", cfg.Inputs[1].StreamLoop)
	}
	if cfg.Inputs[1].ITSOffset != 2 {
		t.Errorf("Inputs[1].ITSOffset = %g, want 2 (latched to main.mp4 only)", cfg.Inputs[1].ITSOffset)
	}
}
