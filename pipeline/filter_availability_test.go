// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: GPL-3.0-or-later

package pipeline

import (
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
)

func TestValidateFilterAvailability_RejectsUnknownFilter(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.0",
		Inputs:        []Input{{ID: "in0", URL: "in.mp4"}},
		Outputs:       []Output{{ID: "out0", URL: "out.mp4", CodecVideo: "copy"}},
		Graph: GraphDef{
			Nodes: []NodeDef{{ID: "f0", Type: "filter", Filter: "no_such_filter_xyzzy"}},
			Edges: []EdgeDef{
				{From: "in0:v:0", To: "f0:in", Type: "video"},
				{From: "f0:out", To: "out0:v", Type: "video"},
			},
		},
	}
	err := validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "no_such_filter_xyzzy") {
		t.Fatalf("expected unknown-filter rejection, got %v", err)
	}
}

func TestValidateFilterAvailability_OptionalLibHint(t *testing.T) {
	if av.FindFilter("zscale") {
		t.Skip("zscale is built into this libavfilter; can't exercise the missing-lib hint")
	}
	cfg := &Config{
		SchemaVersion: "1.0",
		Inputs:        []Input{{ID: "in0", URL: "in.mp4"}},
		Outputs:       []Output{{ID: "out0", URL: "out.mp4", CodecVideo: "copy"}},
		Graph: GraphDef{
			Nodes: []NodeDef{{ID: "z0", Type: "filter", Filter: "zscale"}},
			Edges: []EdgeDef{
				{From: "in0:v:0", To: "z0:in", Type: "video"},
				{From: "z0:out", To: "out0:v", Type: "video"},
			},
		},
	}
	err := validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "libzimg") {
		t.Fatalf("expected --enable-libzimg hint, got %v", err)
	}
}

func TestValidateFilterAvailability_AllowsKnownFilter(t *testing.T) {
	if !av.FindFilter("scale") {
		t.Skip("scale not built; libavfilter build is broken")
	}
	cfg := &Config{
		SchemaVersion: "1.0",
		Inputs:        []Input{{ID: "in0", URL: "in.mp4"}},
		Outputs:       []Output{{ID: "out0", URL: "out.mp4", CodecVideo: "copy"}},
		Graph: GraphDef{
			Nodes: []NodeDef{{ID: "s0", Type: "filter", Filter: "scale", Params: map[string]any{"w": 640, "h": 360}}},
			Edges: []EdgeDef{
				{From: "in0:v:0", To: "s0:in", Type: "video"},
				{From: "s0:out", To: "out0:v", Type: "video"},
			},
		},
	}
	if err := validate(cfg); err != nil {
		t.Fatalf("expected scale to validate, got %v", err)
	}
}
