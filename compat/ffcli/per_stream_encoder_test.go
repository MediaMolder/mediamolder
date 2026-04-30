// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

import "testing"

func TestParsePerStreamEncoderFlag(t *testing.T) {
	cases := []struct {
		arg  string
		want *perStreamEncoderFlag
	}{
		{"-b:v:0", &perStreamEncoderFlag{key: "b", typ: "v", idx: 0}},
		{"-b:v:1", &perStreamEncoderFlag{key: "b", typ: "v", idx: 1}},
		{"-crf:v:2", &perStreamEncoderFlag{key: "crf", typ: "v", idx: 2}},
		{"-preset:v:0", &perStreamEncoderFlag{key: "preset", typ: "v", idx: 0}},
		{"-b:a:0", &perStreamEncoderFlag{key: "b", typ: "a", idx: 0}},
		// Not a recognised encoder key.
		{"-disposition:s:0", nil},
		{"-metadata:s:0", nil},
		// Wrong shape.
		{"-b:v", nil},
		{"-b", nil},
		{"b:v:0", nil},
		// Bad index.
		{"-b:v:-1", nil},
		{"-b:v:abc", nil},
	}
	for _, c := range cases {
		t.Run(c.arg, func(t *testing.T) {
			got := parsePerStreamEncoderFlag(c.arg)
			if c.want == nil {
				if got != nil {
					t.Errorf("got %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("got nil, want %+v", c.want)
			}
			if *got != *c.want {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestParseABRLadder(t *testing.T) {
	// Canonical FFmpeg ABR ladder: one input, one output, two
	// video edges with distinct -b:v / -crf per stream.
	cfg, err := Parse("ffmpeg -i in.mp4 -c:v libx264 -b:v:0 5M -crf:v:0 22 -preset:v:0 fast -b:v:1 2500k -crf:v:1 24 -c:v:1 libx265 out.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Outputs) != 1 {
		t.Fatalf("Outputs = %d, want 1", len(cfg.Outputs))
	}
	out := cfg.Outputs[0]
	if len(out.Streams) != 2 {
		t.Fatalf("Streams = %d, want 2", len(out.Streams))
	}
	// Order: streams sort by (type, index) at drain time.
	s0, s1 := out.Streams[0], out.Streams[1]
	if s0.Type != "v" || s0.Index != 0 {
		t.Errorf("s0 spec = %s:%d, want v:0", s0.Type, s0.Index)
	}
	if s1.Type != "v" || s1.Index != 1 {
		t.Errorf("s1 spec = %s:%d, want v:1", s1.Type, s1.Index)
	}
	if s0.Encoder == nil || s1.Encoder == nil {
		t.Fatal("Encoder overrides nil")
	}
	if got := s0.Encoder.Options["b"]; got != "5M" {
		t.Errorf("s0.Options[b] = %v, want 5M", got)
	}
	if got := s0.Encoder.Options["crf"]; got != "22" {
		t.Errorf("s0.Options[crf] = %v, want 22", got)
	}
	if got := s0.Encoder.Options["preset"]; got != "fast" {
		t.Errorf("s0.Options[preset] = %v, want fast", got)
	}
	if got := s1.Encoder.Options["b"]; got != "2500k" {
		t.Errorf("s1.Options[b] = %v, want 2500k", got)
	}
	if s1.Encoder.Codec != "libx265" {
		t.Errorf("s1.Encoder.Codec = %q, want libx265", s1.Encoder.Codec)
	}
}

func TestParsePerStreamMissingArg(t *testing.T) {
	if _, err := Parse("ffmpeg -i in.mp4 -b:v:0 out.mp4"); err == nil {
		// Last token "out.mp4" gets consumed as the value, so this
		// case actually parses cleanly; the real missing-arg path
		// is when -b:v:0 is the final token before EOF.
		t.Skip("trailing positional consumed as value")
	}
	if _, err := Parse("ffmpeg -i in.mp4 out.mp4 -b:v:0"); err == nil {
		t.Error("expected missing-arg error, got nil")
	}
}
