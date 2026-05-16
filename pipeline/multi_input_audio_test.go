// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/graph"
)

// TestSpliceAudioMerge_TwoInputs verifies that an encoder node with two
// inbound audio edges gets a synthetic __amerge__ filter spliced in front.
func TestSpliceAudioMerge_TwoInputs(t *testing.T) {
	def := &graph.Def{
		Nodes: []graph.NodeDef{
			{ID: "src1", Type: "input"},
			{ID: "src2", Type: "input"},
			{ID: "enc", Type: "encoder", Params: map[string]any{"codec": "aac"}},
			{ID: "sink", Type: "output"},
		},
		Edges: []graph.EdgeDef{
			{From: "src1:a:0", To: "enc", Type: "audio"},
			{From: "src2:a:0", To: "enc", Type: "audio"},
			{From: "enc", To: "sink", Type: "audio"},
		},
	}
	spliceAudioMergeForMultiInputEncoders(def)

	// Expect exactly one __amerge__ node.
	var mergeNode *graph.NodeDef
	for i := range def.Nodes {
		if strings.HasPrefix(def.Nodes[i].ID, "__amerge__") {
			mergeNode = &def.Nodes[i]
		}
	}
	if mergeNode == nil {
		t.Fatal("expected __amerge__ node, none found")
	}
	if mergeNode.Filter != "amerge=inputs=2" {
		t.Errorf("merge filter = %q, want %q", mergeNode.Filter, "amerge=inputs=2")
	}
	if mergeNode.Internal.Generated == nil || mergeNode.Internal.Generated.By != "spliceAudioMergeForMultiInputEncoders" {
		t.Error("merge node missing Generated provenance")
	}

	// Both original src→enc edges must now target the merge node on distinct input ports.
	wantPorts := map[string]string{
		"src1:a:0": mergeNode.ID + ":in0",
		"src2:a:0": mergeNode.ID + ":in1",
	}
	for _, e := range def.Edges {
		if want, ok := wantPorts[e.From]; ok {
			if e.To != want {
				t.Errorf("edge from %s: To = %q, want %q", e.From, e.To, want)
			}
		}
	}

	// A new edge merge→enc must exist.
	var gotMergeToEnc bool
	for _, e := range def.Edges {
		if e.From == mergeNode.ID && e.To == "enc" && e.Type == "audio" {
			gotMergeToEnc = true
		}
	}
	if !gotMergeToEnc {
		t.Error("no edge from __amerge__ node to encoder found")
	}
}

// TestSpliceAudioMerge_SingleInputSkipped verifies that an encoder with
// only one inbound audio edge is left untouched.
func TestSpliceAudioMerge_SingleInputSkipped(t *testing.T) {
	def := &graph.Def{
		Nodes: []graph.NodeDef{
			{ID: "src", Type: "input"},
			{ID: "enc", Type: "encoder", Params: map[string]any{"codec": "aac"}},
			{ID: "sink", Type: "output"},
		},
		Edges: []graph.EdgeDef{
			{From: "src:a:0", To: "enc", Type: "audio"},
			{From: "enc", To: "sink", Type: "audio"},
		},
	}
	before := len(def.Nodes)
	spliceAudioMergeForMultiInputEncoders(def)
	if len(def.Nodes) != before {
		t.Errorf("node count changed from %d to %d; expected no splice for single-input encoder", before, len(def.Nodes))
	}
}

// TestSpliceAudioMerge_AlwaysSplicesMultiInput verifies that any encoder
// with multiple inbound audio edges gets the synthetic amerge without
// requiring any opt-in parameter.
func TestSpliceAudioMerge_AlwaysSplicesMultiInput(t *testing.T) {
	def := &graph.Def{
		Nodes: []graph.NodeDef{
			{ID: "src1", Type: "input"},
			{ID: "src2", Type: "input"},
			{ID: "enc", Type: "encoder", Params: map[string]any{"codec": "aac"}},
			{ID: "sink", Type: "output"},
		},
		Edges: []graph.EdgeDef{
			{From: "src1:a:0", To: "enc", Type: "audio"},
			{From: "src2:a:0", To: "enc", Type: "audio"},
			{From: "enc", To: "sink", Type: "audio"},
		},
	}
	spliceAudioMergeForMultiInputEncoders(def)
	var mergeNode *graph.NodeDef
	for i := range def.Nodes {
		if strings.HasPrefix(def.Nodes[i].ID, "__amerge__") {
			mergeNode = &def.Nodes[i]
		}
	}
	if mergeNode == nil {
		t.Fatal("expected __amerge__ node, none found")
	}
}
