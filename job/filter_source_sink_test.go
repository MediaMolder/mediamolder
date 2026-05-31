// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: GPL-3.0-or-later

package job

import (
	"strings"
	"testing"
)

// Wave 7 #36a — filter_source / filter_sink validator allow-list.

func TestValidateFilterSource_AllowsKnown(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.1",
		Outputs:       []Output{{ID: "out0", URL: "out.mp4", CodecVideo: "libx264"}},
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "src", Type: "filter_source", Filter: "color", Params: map[string]any{"c": "black", "s": "320x240", "d": 1}},
			},
			Edges: []EdgeDef{
				{From: "src:default", To: "out0:v", Type: "video"},
			},
		},
	}
	if err := validate(cfg); err != nil {
		t.Fatalf("expected color filter_source to validate, got %v", err)
	}
}

func TestValidateFilterSource_RejectsUnknown(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.1",
		Outputs:       []Output{{ID: "out0", URL: "out.mp4", CodecVideo: "libx264"}},
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "src", Type: "filter_source", Filter: "scale"},
			},
			Edges: []EdgeDef{
				{From: "src:default", To: "out0:v", Type: "video"},
			},
		},
	}
	err := validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "not a recognised source filter") {
		t.Fatalf("expected source allow-list rejection, got %v", err)
	}
}

func TestValidateFilterSink_AllowsKnown(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.1",
		Inputs:        []Input{{ID: "in0", URL: "in.mp4"}},
		Outputs:       []Output{{ID: "out0", URL: "out.mp4", CodecVideo: "copy"}},
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "drain", Type: "filter_sink", Filter: "nullsink"},
			},
			Edges: []EdgeDef{
				{From: "in0:v:0", To: "drain:default", Type: "video"},
				{From: "in0:v:0", To: "out0:v", Type: "video"},
			},
		},
	}
	if err := validate(cfg); err != nil {
		t.Fatalf("expected nullsink filter_sink to validate, got %v", err)
	}
}

func TestValidateFilterSink_RejectsUnknown(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.1",
		Inputs:        []Input{{ID: "in0", URL: "in.mp4"}},
		Outputs:       []Output{{ID: "out0", URL: "out.mp4", CodecVideo: "copy"}},
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "drain", Type: "filter_sink", Filter: "scale"},
			},
			Edges: []EdgeDef{
				{From: "in0:v:0", To: "drain:default", Type: "video"},
				{From: "in0:v:0", To: "out0:v", Type: "video"},
			},
		},
	}
	err := validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "not a recognised sink filter") {
		t.Fatalf("expected sink allow-list rejection, got %v", err)
	}
}
