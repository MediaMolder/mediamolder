// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

import "testing"

// TestParsePass verifies that `-pass N` (1|2|3) lands on
// Output.Pass. Mirrors the per-stream OPT_VIDEO int parsed by
// fftools/ffmpeg_opt.c into OutputStream.do_pass.
func TestParsePass(t *testing.T) {
	for _, n := range []int{1, 2, 3} {
		cfg, err := Parse(buildPassCmd(n, ""))
		if err != nil {
			t.Fatalf("pass=%d: %v", n, err)
		}
		if got := cfg.Outputs[0].Pass; got != n {
			t.Errorf("pass=%d: Output.Pass = %d", n, got)
		}
	}
}

// TestParsePassRejectsInvalid covers the validator rejecting values
// outside the 1..3 bit-field band.
func TestParsePassRejectsInvalid(t *testing.T) {
	cases := []string{
		"ffmpeg -i in.mp4 -c:v libx264 -pass 0 out.mp4",
		"ffmpeg -i in.mp4 -c:v libx264 -pass -1 out.mp4",
		"ffmpeg -i in.mp4 -c:v libx264 -pass 4 out.mp4",
		"ffmpeg -i in.mp4 -c:v libx264 -pass banana out.mp4",
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			if _, err := Parse(c); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

// TestParsePasslogfile verifies the prefix lands on Output.PassLogFile.
func TestParsePasslogfile(t *testing.T) {
	cfg, err := Parse("ffmpeg -i in.mp4 -c:v libx264 -pass 1 -passlogfile foo out.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Outputs[0].PassLogFile; got != "foo" {
		t.Errorf("PassLogFile = %q, want foo", got)
	}
}

// TestParseTwoPassRoundTrip verifies the canonical
// `-pass 2 -passlogfile foo` end-to-end mapping.
func TestParseTwoPassRoundTrip(t *testing.T) {
	cfg, err := Parse("ffmpeg -i in.mp4 -c:v libx264 -pass 2 -passlogfile foo out.mp4")
	if err != nil {
		t.Fatal(err)
	}
	out := cfg.Outputs[0]
	if out.Pass != 2 || out.PassLogFile != "foo" {
		t.Fatalf("Output{Pass:%d, PassLogFile:%q}, want {2, foo}", out.Pass, out.PassLogFile)
	}
}

func buildPassCmd(pass int, prefix string) string {
	cmd := "ffmpeg -i in.mp4 -c:v libx264 -pass "
	switch pass {
	case 1:
		cmd += "1"
	case 2:
		cmd += "2"
	case 3:
		cmd += "3"
	}
	if prefix != "" {
		cmd += " -passlogfile " + prefix
	}
	cmd += " out.mp4"
	return cmd
}
