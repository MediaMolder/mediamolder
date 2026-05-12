// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/graph"
)

// TestSpliceAudioAdapters_FilterUpstreamOfEncoder verifies that
// spliceAudioAdaptersForEncoders inserts the aformat+asetnsamples chain even
// when the immediate upstream node is a user-placed filter (e.g. amerge).
// Regression test for the bug where any filter upstream suppressed the adapter,
// causing format/frame-size mismatches for codecs like AAC that require fltp
// at exactly 1024 samples/frame.
func TestSpliceAudioAdapters_FilterUpstreamOfEncoder(t *testing.T) {
	def := &graph.Def{
		Nodes: []graph.NodeDef{
			{ID: "in0", Type: "input"},
			{ID: "amerge", Type: "filter", Filter: "amerge"},
			{ID: "enc", Type: "encoder", Params: map[string]any{"codec": "aac"}},
			{ID: "out0", Type: "output"},
		},
		Edges: []graph.EdgeDef{
			{From: "in0:a:6", To: "amerge", Type: "audio"},
			{From: "in0:a:7", To: "amerge", Type: "audio"},
			{From: "amerge", To: "enc", Type: "audio"},
			{From: "enc", To: "out0:a", Type: "audio"},
		},
	}
	spliceAudioAdaptersForEncoders(def)

	// Expect a synthetic __aspl__ adapter node to have been inserted.
	var adapterNode *graph.NodeDef
	for i := range def.Nodes {
		if strings.HasPrefix(def.Nodes[i].ID, "__aspl__") {
			adapterNode = &def.Nodes[i]
		}
	}
	if adapterNode == nil {
		t.Fatal("expected __aspl__ adapter node between amerge and aac encoder, none found")
	}
	if !strings.Contains(adapterNode.Filter, "aformat") {
		t.Errorf("adapter spec %q missing aformat", adapterNode.Filter)
	}
	if !strings.Contains(adapterNode.Filter, "asetnsamples") {
		t.Errorf("adapter spec %q missing asetnsamples (required for aac)", adapterNode.Filter)
	}

	// The encoder's inbound edge should now come from the adapter, not amerge.
	for _, e := range def.Edges {
		if e.To == "enc" && e.Type == "audio" {
			if !strings.HasPrefix(e.From, "__aspl__") {
				t.Errorf("encoder's inbound audio edge comes from %q, want __aspl__ adapter", e.From)
			}
		}
	}
}

// TestSpliceAudioAdapters_SyntheticAdapterNotDoubled verifies that a
// pre-existing __aspl__ synthetic adapter (inserted by a prior pass) is not
// doubled: the function must skip the edge when the immediate upstream is
// already a synthetic adapter.
func TestSpliceAudioAdapters_SyntheticAdapterNotDoubled(t *testing.T) {
	existingAdapter := graph.NodeDef{
		ID:     "__aspl__enc_3",
		Type:   "filter",
		Filter: "aformat=sample_fmts=fltp,asetnsamples=n=1024:p=0",
	}
	def := &graph.Def{
		Nodes: []graph.NodeDef{
			{ID: "in0", Type: "input"},
			existingAdapter,
			{ID: "enc", Type: "encoder", Params: map[string]any{"codec": "aac"}},
			{ID: "out0", Type: "output"},
		},
		Edges: []graph.EdgeDef{
			{From: "in0:a:0", To: "__aspl__enc_3", Type: "audio"},
			{From: "__aspl__enc_3", To: "enc", Type: "audio"},
			{From: "enc", To: "out0:a", Type: "audio"},
		},
	}
	before := len(def.Nodes)
	spliceAudioAdaptersForEncoders(def)
	after := len(def.Nodes)
	if after != before {
		t.Errorf("node count changed from %d to %d; synthetic adapter must not be doubled", before, after)
	}
}
