// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"testing"

	"github.com/MediaMolder/MediaMolder/graph"
)

// TestExpandImplicitEncodersPerStreamOverride proves Wave 6 #30:
// Output.Streams[].Encoder is overlaid onto the matching synthetic
// encoder node when expandImplicitEncoders runs.
func TestExpandImplicitEncodersPerStreamOverride(t *testing.T) {
	cfg := &Config{
		Outputs: []Output{{
			ID:         "sink",
			URL:        "out.mp4",
			CodecVideo: "libx264",
			EncoderParamsVideo: map[string]any{
				"b":   "5M",
				"crf": "22",
			},
			Streams: []StreamSpec{
				{Type: "v", Index: 1, Encoder: &EncoderOverride{
					Codec: "libx265",
					Options: map[string]any{
						"b":   "2500k",
						"crf": "24",
					},
				}},
			},
		}},
	}
	def := &graph.Def{
		Nodes: []graph.NodeDef{
			{ID: "src", Type: "input"},
			{ID: "sink", Type: "output"},
		},
		Edges: []graph.EdgeDef{
			{From: "src:0", To: "sink", Type: "video"},
			{From: "src:1", To: "sink", Type: "video"},
		},
	}
	expandImplicitEncoders(cfg, def)

	encNodes := []graph.NodeDef{}
	for _, n := range def.Nodes {
		if n.Type == "encoder" {
			encNodes = append(encNodes, n)
		}
	}
	if len(encNodes) != 2 {
		t.Fatalf("encoder nodes = %d, want 2", len(encNodes))
	}
	// Edge 0 (v:0) gets output-level codec + params.
	if encNodes[0].Params["codec"] != "libx264" {
		t.Errorf("enc0.codec = %v, want libx264", encNodes[0].Params["codec"])
	}
	if encNodes[0].Params["b"] != "5M" {
		t.Errorf("enc0.b = %v, want 5M", encNodes[0].Params["b"])
	}
	// Edge 1 (v:1) gets per-stream override.
	if encNodes[1].Params["codec"] != "libx265" {
		t.Errorf("enc1.codec = %v, want libx265", encNodes[1].Params["codec"])
	}
	if encNodes[1].Params["b"] != "2500k" {
		t.Errorf("enc1.b = %v, want 2500k", encNodes[1].Params["b"])
	}
	if encNodes[1].Params["crf"] != "24" {
		t.Errorf("enc1.crf = %v, want 24", encNodes[1].Params["crf"])
	}
}

// TestExpandImplicitEncodersOverrideOptionsOnly verifies that an
// override with empty Codec preserves the output-level codec while
// still applying the Options overlay.
func TestExpandImplicitEncodersOverrideOptionsOnly(t *testing.T) {
	cfg := &Config{
		Outputs: []Output{{
			ID:         "sink",
			URL:        "out.mp4",
			CodecVideo: "libx264",
			Streams: []StreamSpec{
				{Type: "v", Index: 0, Encoder: &EncoderOverride{
					Options: map[string]any{"crf": "18"},
				}},
			},
		}},
	}
	def := &graph.Def{
		Nodes: []graph.NodeDef{
			{ID: "src", Type: "input"},
			{ID: "sink", Type: "output"},
		},
		Edges: []graph.EdgeDef{
			{From: "src:0", To: "sink", Type: "video"},
		},
	}
	expandImplicitEncoders(cfg, def)
	for _, n := range def.Nodes {
		if n.Type != "encoder" {
			continue
		}
		if n.Params["codec"] != "libx264" {
			t.Errorf("codec = %v, want libx264", n.Params["codec"])
		}
		if n.Params["crf"] != "18" {
			t.Errorf("crf = %v, want 18", n.Params["crf"])
		}
		return
	}
	t.Fatal("no encoder node found")
}
