// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import "testing"

// Wave 7 #38: per-graph thread cap propagation. Per-node `Threads`
// wins over the pipeline-wide `FilterComplexThreads`, both surface
// as the `__filter_threads` sentinel in graph.NodeDef.Params, and
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

	byID := make(map[string]map[string]any)
	for _, n := range def.Nodes {
		byID[n.ID] = n.Params
	}

	// scale: pipeline-wide cap → 3
	if v, _ := byID["scale"]["__filter_threads"].(int); v != 3 {
		t.Errorf("scale __filter_threads = %v, want 3", byID["scale"]["__filter_threads"])
	}
	// crop: per-node override → 8
	if v, _ := byID["crop"]["__filter_threads"].(int); v != 8 {
		t.Errorf("crop __filter_threads = %v, want 8", byID["crop"]["__filter_threads"])
	}
	// encoder: must NOT be annotated
	if _, ok := byID["enc"]["__filter_threads"]; ok {
		t.Errorf("encoder node should not carry __filter_threads, got %v", byID["enc"])
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
		if _, ok := n.Params["__filter_threads"]; ok {
			t.Errorf("node %q should not carry __filter_threads, got %v", n.ID, n.Params)
		}
	}
}
