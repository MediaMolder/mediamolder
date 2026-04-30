// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package graph

import (
	"strings"
	"testing"
)

// Wave 7 #37 — OutputMediaType propagation + edge-type enforcement.

func TestBuildPropagatesOutputMediaType(t *testing.T) {
	g, err := Build(&Def{
		Inputs: []InputDef{{ID: "in0"}},
		Nodes: []NodeDef{{
			ID:              "sw",
			Type:            "filter",
			Filter:          "showwavespic",
			OutputMediaType: PortVideo,
		}},
		Outputs: []OutputDef{{ID: "out0"}},
		Edges: []EdgeDef{
			{From: "in0", To: "sw", Type: "audio"},
			{From: "sw", To: "out0", Type: "video"},
		},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if g.Nodes["sw"].OutputMediaType != PortVideo {
		t.Errorf("OutputMediaType = %q, want video", g.Nodes["sw"].OutputMediaType)
	}
}

func TestBuildRejectsEdgeMismatchingOutputMediaType(t *testing.T) {
	_, err := Build(&Def{
		Inputs: []InputDef{{ID: "in0"}},
		Nodes: []NodeDef{{
			ID:              "sw",
			Type:            "filter",
			Filter:          "showwavespic",
			OutputMediaType: PortVideo,
		}},
		Outputs: []OutputDef{{ID: "out0"}},
		Edges: []EdgeDef{
			{From: "in0", To: "sw", Type: "audio"},
			{From: "sw", To: "out0", Type: "audio"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "output_media_type") {
		t.Fatalf("expected output_media_type mismatch error, got %v", err)
	}
}

func TestBuildRejectsOutputMediaTypeOnNonFilter(t *testing.T) {
	_, err := Build(&Def{
		Inputs: []InputDef{{ID: "in0"}},
		Nodes: []NodeDef{{
			ID:              "fs",
			Type:            "filter_source",
			Filter:          "color",
			OutputMediaType: PortVideo,
		}},
		Outputs: []OutputDef{{ID: "out0"}},
		Edges: []EdgeDef{
			{From: "fs", To: "out0", Type: "video"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "filter nodes") {
		t.Fatalf("expected non-filter rejection, got %v", err)
	}
}

func TestBuildRejectsInvalidOutputMediaType(t *testing.T) {
	_, err := Build(&Def{
		Inputs: []InputDef{{ID: "in0"}},
		Nodes: []NodeDef{{
			ID:              "f",
			Type:            "filter",
			Filter:          "scale",
			OutputMediaType: PortType("banana"),
		}},
		Outputs: []OutputDef{{ID: "out0"}},
		Edges: []EdgeDef{
			{From: "in0", To: "f", Type: "video"},
			{From: "f", To: "out0", Type: "video"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid output_media_type") {
		t.Fatalf("expected invalid output_media_type rejection, got %v", err)
	}
}
