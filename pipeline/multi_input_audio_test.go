// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/graph"
)

// TestSpliceAudioMerge_TwoInputs verifies that an encoder node with
// params["multi_input_audio"]="true" and two inbound audio edges gets a
// synthetic __amerge__ filter spliced in front of it.
func TestSpliceAudioMerge_TwoInputs(t *testing.T) {
	def := &graph.Def{
		Nodes: []graph.NodeDef{
			{ID: "src1", Type: "input"},
			{ID: "src2", Type: "input"},
			{ID: "enc", Type: "encoder", Params: map[string]any{"codec": "aac", "multi_input_audio": "true"}},
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

	// Both original src→enc edges must now target the merge node.
	for _, e := range def.Edges {
		if e.From == "src1:a:0" || e.From == "src2:a:0" {
			if e.To != mergeNode.ID {
				t.Errorf("edge from %s: To = %q, want %q", e.From, e.To, mergeNode.ID)
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
// only one inbound audio edge is left untouched even when multi_input_audio
// is set.
func TestSpliceAudioMerge_SingleInputSkipped(t *testing.T) {
	def := &graph.Def{
		Nodes: []graph.NodeDef{
			{ID: "src", Type: "input"},
			{ID: "enc", Type: "encoder", Params: map[string]any{"codec": "aac", "multi_input_audio": "true"}},
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

// TestSpliceAudioMerge_NoParamSkipped verifies that a normal encoder node
// (no multi_input_audio) is untouched even if it has multiple inbound edges.
func TestSpliceAudioMerge_NoParamSkipped(t *testing.T) {
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
	before := len(def.Nodes)
	spliceAudioMergeForMultiInputEncoders(def)
	if len(def.Nodes) != before {
		t.Errorf("node count changed from %d to %d; expected no splice without multi_input_audio", before, len(def.Nodes))
	}
}
