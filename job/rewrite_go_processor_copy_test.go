// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"testing"

	"github.com/MediaMolder/MediaMolder/graph"
)

// TestRewriteGoProcessorCopyEdges_Basic verifies that a go_processor → copy
// edge is rewritten to source → copy so that the copy node receives raw
// demuxer packets rather than decoded frames.
func TestRewriteGoProcessorCopyEdges_Basic(t *testing.T) {
	def := &graph.Def{
		Inputs: []graph.InputDef{{ID: "in0"}},
		Nodes: []graph.NodeDef{
			{ID: "scene", Type: "go_processor"},
			{ID: "cp", Type: "copy"},
		},
		Outputs: []graph.OutputDef{{ID: "out"}},
		Edges: []graph.EdgeDef{
			{From: "in0:v:0", To: "scene:default", Type: "video"},
			{From: "scene:default", To: "cp", Type: "video"}, // ← should be rewritten
			{From: "cp", To: "out:v", Type: "video"},
		},
	}

	rewriteGoProcessorCopyEdges(def)

	// The scene → cp edge should now read from in0:v:0 directly.
	var sceneOut, cpIn string
	for _, e := range def.Edges {
		if e.To == "cp" && e.Type == "video" {
			cpIn = e.From
		}
		if e.From == "scene:default" && e.Type == "video" {
			sceneOut = e.To
		}
	}

	if cpIn != "in0:v:0" {
		t.Errorf("copy node inbound From = %q, want %q", cpIn, "in0:v:0")
	}
	// The scene → cp edge should have been rewritten; scene now has no outbound copy edge.
	if sceneOut == "cp" {
		t.Errorf("scene still feeds cp directly after rewrite")
	}
}

// TestRewriteGoProcessorCopyEdges_Chain verifies that a multi-hop
// go_processor chain is also rewritten correctly.
func TestRewriteGoProcessorCopyEdges_Chain(t *testing.T) {
	def := &graph.Def{
		Inputs: []graph.InputDef{{ID: "in0"}},
		Nodes: []graph.NodeDef{
			{ID: "proc1", Type: "go_processor"},
			{ID: "proc2", Type: "go_processor"},
			{ID: "cp", Type: "copy"},
		},
		Outputs: []graph.OutputDef{{ID: "out"}},
		Edges: []graph.EdgeDef{
			{From: "in0:v:0", To: "proc1", Type: "video"},
			{From: "proc1", To: "proc2", Type: "video"},
			{From: "proc2", To: "cp", Type: "video"}, // ← should trace back to in0:v:0
			{From: "cp", To: "out:v", Type: "video"},
		},
	}

	rewriteGoProcessorCopyEdges(def)

	var cpIn string
	for _, e := range def.Edges {
		if e.To == "cp" && e.Type == "video" {
			cpIn = e.From
		}
	}
	if cpIn != "in0:v:0" {
		t.Errorf("copy node inbound From = %q, want %q", cpIn, "in0:v:0")
	}
}

// TestRewriteGoProcessorCopyEdges_NonCopyUnchanged verifies that edges
// between a go_processor and a non-copy node are left untouched.
func TestRewriteGoProcessorCopyEdges_NonCopyUnchanged(t *testing.T) {
	def := &graph.Def{
		Inputs: []graph.InputDef{{ID: "in0"}},
		Nodes: []graph.NodeDef{
			{ID: "scene", Type: "go_processor"},
			{ID: "enc", Type: "encoder"},
		},
		Outputs: []graph.OutputDef{{ID: "out"}},
		Edges: []graph.EdgeDef{
			{From: "in0:v:0", To: "scene:default", Type: "video"},
			{From: "scene:default", To: "enc", Type: "video"}, // not a copy node — must stay
			{From: "enc", To: "out:v", Type: "video"},
		},
	}

	rewriteGoProcessorCopyEdges(def)

	var sceneOut string
	for _, e := range def.Edges {
		if e.From == "scene:default" && e.Type == "video" {
			sceneOut = e.To
		}
	}
	if sceneOut != "enc" {
		t.Errorf("scene→enc edge was modified: To = %q, want %q", sceneOut, "enc")
	}
}
