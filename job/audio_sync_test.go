// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/graph"
)

// TestSpliceAudioSync_OffByDefault verifies that a config with no
// AudioSync field set produces no `__async__` filter nodes.
func TestSpliceAudioSync_OffByDefault(t *testing.T) {
	cfg := &Config{
		Inputs:  []Input{{ID: "in", URL: "x.mp4"}},
		Outputs: []Output{{ID: "out", URL: "y.mp4"}},
		Graph: GraphDef{
			Edges: []EdgeDef{{From: "in:a:0", To: "out", Type: "audio"}},
		},
	}
	def := configToGraphDef(cfg)
	for _, n := range def.Nodes {
		if strings.HasPrefix(n.ID, "__async__") {
			t.Fatalf("unexpected __async__ node %q with AudioSync=0", n.ID)
		}
	}
}

// TestSpliceAudioSync_OneRendersFirstPTS verifies that AudioSync=1
// emits an aresample node with `async=1:first_pts=0` and that it sits
// upstream of the synthetic encoder (and any aformat adapter).
func TestSpliceAudioSync_OneRendersFirstPTS(t *testing.T) {
	cfg := &Config{
		Inputs: []Input{{ID: "in", URL: "x.mp4"}},
		Outputs: []Output{{
			ID: "out", URL: "y.mp4",
			CodecAudio: "aac",
			AudioSync:  1,
		}},
		Graph: GraphDef{
			Edges: []EdgeDef{{From: "in:a:0", To: "out", Type: "audio"}},
		},
	}
	def := configToGraphDef(cfg)
	asyncNode := findNodeWithPrefix(t, def, "__async__")
	if want := "aresample=async=1:first_pts=0"; asyncNode.Filter != want {
		t.Errorf("filter = %q, want %q", asyncNode.Filter, want)
	}
	// The aresample node must consume the original input edge.
	srcEdge := findEdgeTo(t, def, asyncNode.ID, "audio")
	if srcEdge.From != "in:a:0" {
		t.Errorf("aresample source = %q, want %q", srcEdge.From, "in:a:0")
	}
}

// TestSpliceAudioSync_ContinuousN verifies that AudioSync=1000 emits
// `aresample=async=1000` (no first_pts override).
func TestSpliceAudioSync_ContinuousN(t *testing.T) {
	cfg := &Config{
		Inputs: []Input{{ID: "in", URL: "x.mp4"}},
		Outputs: []Output{{
			ID: "out", URL: "y.mp4",
			CodecAudio: "aac",
			AudioSync:  1000,
		}},
		Graph: GraphDef{
			Edges: []EdgeDef{{From: "in:a:0", To: "out", Type: "audio"}},
		},
	}
	def := configToGraphDef(cfg)
	asyncNode := findNodeWithPrefix(t, def, "__async__")
	if want := "aresample=async=1000"; asyncNode.Filter != want {
		t.Errorf("filter = %q, want %q", asyncNode.Filter, want)
	}
}

// TestSpliceAudioSync_VideoOnlyOutputUntouched verifies that an output
// with AudioSync set but no audio encoder edge produces no async node.
func TestSpliceAudioSync_VideoOnlyOutputUntouched(t *testing.T) {
	cfg := &Config{
		Inputs: []Input{{ID: "in", URL: "x.mp4"}},
		Outputs: []Output{{
			ID: "out", URL: "y.mp4",
			AudioSync: 1000,
		}},
		Graph: GraphDef{
			Edges: []EdgeDef{{From: "in:v:0", To: "out", Type: "video"}},
		},
	}
	def := configToGraphDef(cfg)
	for _, n := range def.Nodes {
		if strings.HasPrefix(n.ID, "__async__") {
			t.Fatalf("unexpected __async__ node %q on a video-only output", n.ID)
		}
	}
}

// TestValidateAudioSyncRejectsNegative ensures the validator catches
// a negative AudioSync value with a useful error.
func TestValidateAudioSyncRejectsNegative(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.0",
		Inputs: []Input{
			{ID: "in", URL: "x.mp4", Streams: []StreamSelect{{Type: "audio"}}},
		},
		Outputs: []Output{{ID: "out", URL: "y.mp4", AudioSync: -1}},
	}
	if err := validate(cfg); err == nil ||
		!strings.Contains(err.Error(), "audio_sync") {
		t.Fatalf("validate err = %v, want one containing \"audio_sync\"", err)
	}
}

// ---------- helpers ----------

func findNodeWithPrefix(t *testing.T, def *graph.Def, prefix string) graph.NodeDef {
	t.Helper()
	for _, n := range def.Nodes {
		if strings.HasPrefix(n.ID, prefix) {
			return n
		}
	}
	t.Fatalf("no node found with prefix %q; have %+v", prefix, def.Nodes)
	return graph.NodeDef{}
}

func findEdgeTo(t *testing.T, def *graph.Def, toID, etype string) graph.EdgeDef {
	t.Helper()
	for _, e := range def.Edges {
		if e.To == toID && e.Type == etype {
			return e
		}
	}
	t.Fatalf("no edge found to %q (type %q); have %+v", toID, etype, def.Edges)
	return graph.EdgeDef{}
}
