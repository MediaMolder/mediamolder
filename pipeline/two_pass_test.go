// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/graph"
)

// TestValidateTwoPass_PassRange covers the validator rejecting out-of-range
// `pass` values. Mirrors the FFmpeg AV_CODEC_FLAG_PASS1|PASS2 bit-field
// (0..3); negative or larger values are rejected up-front.
func TestValidateTwoPass_PassRange(t *testing.T) {
	cases := []struct {
		name    string
		pass    int
		wantErr bool
	}{
		{"off", 0, false},
		{"pass1", 1, false},
		{"pass2", 2, false},
		{"both", 3, false},
		{"negative", -1, true},
		{"too_large", 4, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{
				SchemaVersion: "1.0",
				Inputs: []Input{
					{ID: "in", URL: "x.mp4", Streams: []StreamSelect{{Type: "video"}}},
				},
				Outputs: []Output{{ID: "out", URL: "y.mp4", Pass: tc.pass}},
			}
			err := validate(cfg)
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "pass") {
					t.Fatalf("validate err = %v, want one mentioning pass", err)
				}
			} else if err != nil {
				t.Fatalf("validate: unexpected err %v", err)
			}
		})
	}
}

// TestValidateTwoPass_PasslogfileRequiresPass ensures `passlogfile` is
// rejected when `pass == 0` — mirrors the FFmpeg behaviour where
// `-passlogfile` is silently ignored without `-pass`, surfaced here as
// an explicit configuration error.
func TestValidateTwoPass_PasslogfileRequiresPass(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.0",
		Inputs: []Input{
			{ID: "in", URL: "x.mp4", Streams: []StreamSelect{{Type: "video"}}},
		},
		Outputs: []Output{{ID: "out", URL: "y.mp4", PassLogFile: "foo"}},
	}
	if err := validate(cfg); err == nil ||
		!strings.Contains(err.Error(), "passlogfile") {
		t.Fatalf("validate err = %v, want one mentioning passlogfile", err)
	}
}

// TestExpandImplicitEncoders_PassPropagation verifies that the
// per-output Pass / PassLogFile fields land on the implicit video
// encoder node as `__pass` / `__passlogfile`, and that the
// post-expansion sequential `__pass_index` walk assigns unique
// indices to each two-pass video encoder in declaration order. This
// is what guarantees the generated `<prefix>-<idx>.log` filenames are
// unique across multiple two-pass video outputs in the same run.
func TestExpandImplicitEncoders_PassPropagation(t *testing.T) {
	cfg := &Config{
		Outputs: []Output{
			{ID: "outA", URL: "a.mp4", CodecVideo: "libx264", Pass: 1, PassLogFile: "statsA"},
			{ID: "outB", URL: "b.mp4", CodecVideo: "libx264", Pass: 2}, // no prefix → default
		},
	}
	def := &graph.Def{
		Nodes: []graph.NodeDef{
			{ID: "src", Type: "source"},
			{ID: "outA", Type: "sink"},
			{ID: "outB", Type: "sink"},
		},
		Edges: []graph.EdgeDef{
			{From: "src", To: "outA", Type: "video"},
			{From: "src", To: "outB", Type: "video"},
		},
	}
	expandImplicitEncoders(cfg, def)

	encs := []graph.NodeDef{}
	for _, n := range def.Nodes {
		if n.Type == "encoder" {
			encs = append(encs, n)
		}
	}
	if len(encs) != 2 {
		t.Fatalf("expected 2 encoder nodes, got %d", len(encs))
	}

	// First encoder: Pass=1, custom prefix, index 0.
	if got := encs[0].Params["__pass"]; got != 1 {
		t.Fatalf("encs[0] __pass = %v, want 1", got)
	}
	if got := encs[0].Params["__passlogfile"]; got != "statsA" {
		t.Fatalf("encs[0] __passlogfile = %v, want statsA", got)
	}
	if got := encs[0].Params["__pass_index"]; got != 0 {
		t.Fatalf("encs[0] __pass_index = %v, want 0", got)
	}

	// Second encoder: Pass=2, no prefix (defaults at createEncoder time),
	// index 1.
	if got := encs[1].Params["__pass"]; got != 2 {
		t.Fatalf("encs[1] __pass = %v, want 2", got)
	}
	if _, set := encs[1].Params["__passlogfile"]; set {
		t.Fatalf("encs[1] __passlogfile should be unset, got %v", encs[1].Params["__passlogfile"])
	}
	if got := encs[1].Params["__pass_index"]; got != 1 {
		t.Fatalf("encs[1] __pass_index = %v, want 1", got)
	}
}
