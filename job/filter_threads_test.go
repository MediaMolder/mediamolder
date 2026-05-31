// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"testing"

	"github.com/MediaMolder/MediaMolder/graph"
)

// Wave 7 #38: per-graph thread cap propagation. Per-node `Threads`
// wins over the pipeline-wide `FilterComplexThreads`, both surface
// on graph.NodeDef.Internal.Filter.Threads (Milestone B), and
// non-filter node types are not annotated.
func TestConfigToGraphDef_FilterThreadsPropagation(t *testing.T) {
	cfg := &Config{
		Inputs:               []Input{{ID: "src"}},
		FilterComplexThreads: 3,
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "scale", Type: "filter", Filter: "scale"},
				{ID: "crop", Type: "filter", Filter: "crop", Threads: 8},
				{ID: "enc", Type: "encoder"},
			},
			Edges: []EdgeDef{
				{From: "src:v:0", To: "scale", Type: "video"},
				{From: "scale", To: "crop", Type: "video"},
				{From: "crop", To: "enc", Type: "video"},
				{From: "enc", To: "out1", Type: "video"},
			},
		},
		Outputs: []Output{{ID: "out1", URL: "out.mp4"}},
	}

	def := configToGraphDef(cfg)

	byID := make(map[string]graph.NodeDef)
	for _, n := range def.Nodes {
		byID[n.ID] = n
	}

	// scale: pipeline-wide cap → 3
	if fi := byID["scale"].Internal.Filter; fi == nil || fi.Threads != 3 {
		t.Errorf("scale Internal.Filter = %+v, want Threads=3", fi)
	}
	// crop: per-node override → 8
	if fi := byID["crop"].Internal.Filter; fi == nil || fi.Threads != 8 {
		t.Errorf("crop Internal.Filter = %+v, want Threads=8", fi)
	}
	// encoder: must NOT be annotated
	if fi := byID["enc"].Internal.Filter; fi != nil {
		t.Errorf("encoder node should not carry Internal.Filter, got %+v", fi)
	}
}

// When neither cap is set, no sentinel is injected.
func TestConfigToGraphDef_FilterThreadsAbsent(t *testing.T) {
	cfg := &Config{
		Inputs: []Input{{ID: "src"}},
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "scale", Type: "filter", Filter: "scale"},
			},
			Edges: []EdgeDef{
				{From: "src:v:0", To: "scale", Type: "video"},
				{From: "scale", To: "out1", Type: "video"},
			},
		},
		Outputs: []Output{{ID: "out1", URL: "out.mp4"}},
	}

	def := configToGraphDef(cfg)
	for _, n := range def.Nodes {
		if n.Internal.Filter != nil && n.Internal.Filter.Threads != 0 {
			t.Errorf("node %q should not carry Internal.Filter.Threads, got %d", n.ID, n.Internal.Filter.Threads)
		}
	}
}
