// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: GPL-3.0-or-later

package job

import (
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
)

// crossMediaCfg builds a minimal Config that runs `audio in → filter →
// out` with the specified outbound edge type. Used to exercise the
// validator without spinning up the runtime.
func crossMediaCfg(filterName, outEdgeType, declared string) *Config {
	return &Config{
		SchemaVersion: "1.0",
		Inputs:        []Input{{ID: "in0", URL: "in.mp4"}},
		Outputs:       []Output{{ID: "out0", URL: "out.mp4", CodecVideo: "libx264"}},
		Graph: GraphDef{
			Nodes: []NodeDef{{
				ID:              "sw",
				Type:            "filter",
				Filter:          filterName,
				OutputMediaType: declared,
			}},
			Edges: []EdgeDef{
				{From: "in0:a:0", To: "sw:in", Type: "audio"},
				{From: "sw:out", To: "out0:v", Type: outEdgeType},
			},
		},
	}
}

func TestValidateCrossMediaTypeFilters_AcceptsVideoEdgeForShowwavespic(t *testing.T) {
	if !av.FindFilter("showwavespic") {
		t.Skip("showwavespic not built into this libavfilter")
	}
	if err := validate(crossMediaCfg("showwavespic", "video", "")); err != nil {
		t.Fatalf("expected validate to accept showwavespic→video edge, got %v", err)
	}
}

func TestValidateCrossMediaTypeFilters_RejectsAudioEdgeForShowwavespic(t *testing.T) {
	if !av.FindFilter("showwavespic") {
		t.Skip("showwavespic not built into this libavfilter")
	}
	err := validate(crossMediaCfg("showwavespic", "audio", ""))
	if err == nil || !strings.Contains(err.Error(), "showwavespic") || !strings.Contains(err.Error(), "video") {
		t.Fatalf("expected mismatch error mentioning showwavespic/video, got %v", err)
	}
}

func TestValidateCrossMediaTypeFilters_RejectsConflictingDeclaration(t *testing.T) {
	if !av.FindFilter("showwavespic") {
		t.Skip("showwavespic not built into this libavfilter")
	}
	err := validate(crossMediaCfg("showwavespic", "video", "audio"))
	if err == nil || !strings.Contains(err.Error(), "output_media_type") {
		t.Fatalf("expected output_media_type conflict error, got %v", err)
	}
}

func TestValidateCrossMediaTypeFilters_AcceptsExplicitDeclarationMatching(t *testing.T) {
	if !av.FindFilter("showwavespic") {
		t.Skip("showwavespic not built into this libavfilter")
	}
	if err := validate(crossMediaCfg("showwavespic", "video", "video")); err != nil {
		t.Fatalf("expected validate to accept matching explicit declaration, got %v", err)
	}
}

func TestValidateCrossMediaTypeFilters_IgnoresRegularFilters(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.0",
		Inputs:        []Input{{ID: "in0", URL: "in.mp4"}},
		Outputs:       []Output{{ID: "out0", URL: "out.mp4", CodecVideo: "libx264"}},
		Graph: GraphDef{
			Nodes: []NodeDef{{ID: "s", Type: "filter", Filter: "scale", Params: map[string]any{"w": 640, "h": 360}}},
			Edges: []EdgeDef{
				{From: "in0:v:0", To: "s:in", Type: "video"},
				{From: "s:out", To: "out0:v", Type: "video"},
			},
		},
	}
	if err := validate(cfg); err != nil {
		t.Fatalf("expected scale (non-cross-media) to validate, got %v", err)
	}
}

func TestValidateCrossMediaTypeFilters_RejectsBogusDeclaration(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.0",
		Inputs:        []Input{{ID: "in0", URL: "in.mp4"}},
		Outputs:       []Output{{ID: "out0", URL: "out.mp4", CodecVideo: "libx264"}},
		Graph: GraphDef{
			Nodes: []NodeDef{{ID: "s", Type: "filter", Filter: "scale", OutputMediaType: "banana"}},
			Edges: []EdgeDef{
				{From: "in0:v:0", To: "s:in", Type: "video"},
				{From: "s:out", To: "out0:v", Type: "video"},
			},
		},
	}
	err := validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "output_media_type") {
		t.Fatalf("expected invalid output_media_type rejection, got %v", err)
	}
}

func TestEdgeFromsNode(t *testing.T) {
	cases := []struct {
		ref, node string
		want      bool
	}{
		{"sw", "sw", true},
		{"sw:default", "sw", true},
		{"sw:out0", "sw", true},
		{"sw:0", "sw", true},
		{"swother", "sw", false},
		{"other:sw", "sw", false},
	}
	for _, c := range cases {
		if got := edgeFromsNode(c.ref, c.node); got != c.want {
			t.Errorf("edgeFromsNode(%q, %q) = %v, want %v", c.ref, c.node, got, c.want)
		}
	}
}
