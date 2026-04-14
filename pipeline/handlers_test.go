// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/graph"
)

// ---------- configToGraphDef tests ----------

func TestConfigToGraphDef_Basic(t *testing.T) {
	cfg := &Config{
		Inputs: []Input{
			{ID: "src1"},
			{ID: "src2"},
		},
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "scale", Type: "filter", Filter: "scale", Params: map[string]any{"w": "1280"}},
				{ID: "enc", Type: "encoder"},
			},
			Edges: []EdgeDef{
				{From: "src1:v:0", To: "scale", Type: "video"},
				{From: "scale", To: "enc", Type: "video"},
				{From: "enc", To: "out1", Type: "video"},
			},
		},
		Outputs: []Output{{ID: "out1", URL: "out.mp4"}},
	}

	def := configToGraphDef(cfg)

	if len(def.Inputs) != 2 {
		t.Errorf("inputs = %d, want 2", len(def.Inputs))
	}
	if len(def.Nodes) != 2 {
		t.Errorf("nodes = %d, want 2", len(def.Nodes))
	}
	if len(def.Outputs) != 1 {
		t.Errorf("outputs = %d, want 1", len(def.Outputs))
	}
	if len(def.Edges) != 3 {
		t.Errorf("edges = %d, want 3", len(def.Edges))
	}
	if def.Nodes[0].Filter != "scale" {
		t.Errorf("nodes[0].Filter = %q, want %q", def.Nodes[0].Filter, "scale")
	}
}

func TestConfigToGraphDef_BuildsValidGraph(t *testing.T) {
	cfg := &Config{
		Inputs: []Input{{ID: "src"}},
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "filter1", Type: "filter", Filter: "null"},
				{ID: "enc1", Type: "encoder"},
			},
			Edges: []EdgeDef{
				{From: "src", To: "filter1", Type: "video"},
				{From: "filter1", To: "enc1", Type: "video"},
				{From: "enc1", To: "out", Type: "video"},
			},
		},
		Outputs: []Output{{ID: "out"}},
	}

	def := configToGraphDef(cfg)
	g, err := graph.Build(def)
	if err != nil {
		t.Fatalf("graph.Build failed: %v", err)
	}
	if got := len(g.Order); got != 4 {
		t.Errorf("topological order has %d nodes, want 4", got)
	}
	if got := len(g.Sources); got != 1 {
		t.Errorf("sources = %d, want 1", got)
	}
	if got := len(g.Sinks); got != 1 {
		t.Errorf("sinks = %d, want 1", got)
	}
}

func TestConfigToGraphDef_MultiInputOverlay(t *testing.T) {
	cfg := &Config{
		Inputs: []Input{{ID: "bg"}, {ID: "fg"}},
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "overlay", Type: "filter", Filter: "overlay"},
				{ID: "enc", Type: "encoder"},
			},
			Edges: []EdgeDef{
				{From: "bg:v:0", To: "overlay", Type: "video"},
				{From: "fg:v:0", To: "overlay", Type: "video"},
				{From: "overlay", To: "enc", Type: "video"},
				{From: "enc", To: "out", Type: "video"},
			},
		},
		Outputs: []Output{{ID: "out"}},
	}

	def := configToGraphDef(cfg)
	g, err := graph.Build(def)
	if err != nil {
		t.Fatalf("graph.Build failed: %v", err)
	}

	overlayNode := g.NodeByID("overlay")
	if overlayNode == nil {
		t.Fatal("overlay node not found")
	}
	if got := len(overlayNode.Inbound); got != 2 {
		t.Errorf("overlay inbound = %d, want 2", got)
	}
	if got := len(overlayNode.Outbound); got != 1 {
		t.Errorf("overlay outbound = %d, want 1", got)
	}
}

func TestConfigToGraphDef_SplitMultiOutput(t *testing.T) {
	cfg := &Config{
		Inputs: []Input{{ID: "src"}},
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "split", Type: "filter", Filter: "split"},
				{ID: "enc1", Type: "encoder"},
				{ID: "enc2", Type: "encoder"},
			},
			Edges: []EdgeDef{
				{From: "src:v:0", To: "split", Type: "video"},
				{From: "split", To: "enc1", Type: "video"},
				{From: "split", To: "enc2", Type: "video"},
				{From: "enc1", To: "out1", Type: "video"},
				{From: "enc2", To: "out2", Type: "video"},
			},
		},
		Outputs: []Output{{ID: "out1"}, {ID: "out2"}},
	}

	def := configToGraphDef(cfg)
	g, err := graph.Build(def)
	if err != nil {
		t.Fatalf("graph.Build failed: %v", err)
	}

	splitNode := g.NodeByID("split")
	if splitNode == nil {
		t.Fatal("split node not found")
	}
	if got := len(splitNode.Inbound); got != 1 {
		t.Errorf("split inbound = %d, want 1", got)
	}
	if got := len(splitNode.Outbound); got != 2 {
		t.Errorf("split outbound = %d, want 2", got)
	}
	if got := len(g.Sinks); got != 2 {
		t.Errorf("sinks = %d, want 2", got)
	}
}

// ---------- buildComplexFilterSpec tests ----------

func TestBuildComplexFilterSpec(t *testing.T) {
	tests := []struct {
		base   string
		nIn    int
		nOut   int
		expect string
	}{
		{"overlay", 2, 1, "[in0][in1]overlay[out0]"},
		{"split", 1, 2, "[in0]split[out0][out1]"},
		{"null", 1, 1, "[in0]null[out0]"},
		{"amerge", 3, 1, "[in0][in1][in2]amerge[out0]"},
	}
	for _, tc := range tests {
		got := buildComplexFilterSpec(tc.base, tc.nIn, tc.nOut)
		if got != tc.expect {
			t.Errorf("buildComplexFilterSpec(%q, %d, %d) = %q, want %q",
				tc.base, tc.nIn, tc.nOut, got, tc.expect)
		}
	}
}

// ---------- portTypeToAVMediaType tests ----------

func TestPortTypeToAVMediaType(t *testing.T) {
	tests := []struct {
		pt     graph.PortType
		expect av.MediaType
	}{
		{graph.PortVideo, av.MediaTypeVideo},
		{graph.PortAudio, av.MediaTypeAudio},
		{graph.PortData, av.MediaTypeUnknown},
	}
	for _, tc := range tests {
		got := portTypeToAVMediaType(tc.pt)
		if got != tc.expect {
			t.Errorf("portTypeToAVMediaType(%v) = %v, want %v", tc.pt, got, tc.expect)
		}
	}
}

// ---------- param helpers tests ----------

func TestParamHelpers(t *testing.T) {
	m := map[string]any{
		"codec":   "libx264",
		"width":   1280,
		"bitrate": int64(5000000),
	}

	if got := paramString(m, "codec"); got != "libx264" {
		t.Errorf("paramString codec = %q", got)
	}
	if got := paramString(m, "missing"); got != "" {
		t.Errorf("paramString missing = %q", got)
	}
	if got := paramString(nil, "codec"); got != "" {
		t.Errorf("paramString nil = %q", got)
	}
	if got := paramInt(m, "width"); got != 1280 {
		t.Errorf("paramInt width = %d", got)
	}
	if got := paramInt64(m, "bitrate"); got != 5000000 {
		t.Errorf("paramInt64 bitrate = %d", got)
	}
}

// ---------- Integration: runGraph end-to-end ----------

func TestRunGraphLinearChain(t *testing.T) {
	if os.Getenv("MEDIAMOLDER_INTEGRATION") == "" {
		input := filepath.Join("..", "testdata", "test_av.avi")
		if _, err := os.Stat(input); err != nil {
			t.Skip("set MEDIAMOLDER_INTEGRATION=1 or provide testdata/test_av.avi")
		}
	}

	inputURL := filepath.Join("..", "testdata", "test_av.avi")
	outDir := t.TempDir()
	output := filepath.Join(outDir, "graph_out.mp4")
	codec := pickTestEncoder(t)

	rawCfg := fmt.Sprintf(`{
		"schema_version": "1.0",
		"inputs": [{
			"id": "src",
			"url": %q,
			"streams": [{"input_index": 0, "type": "video", "track": 0}]
		}],
		"graph": {
			"nodes": [
				{"id": "filt", "type": "filter", "filter": "null"},
				{"id": "enc",  "type": "encoder", "params": {"codec": %q}}
			],
			"edges": [
				{"from": "src:v:0", "to": "filt", "type": "video"},
				{"from": "filt",    "to": "enc",  "type": "video"},
				{"from": "enc",     "to": "out",  "type": "video"}
			]
		},
		"outputs": [{
			"id": "out",
			"url": %q,
			"codec_video": %q
		}]
	}`, inputURL, codec, output, codec)

	cfg, err := ParseConfig([]byte(rawCfg))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}

	eng, err := NewPipeline(cfg)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}

	if err := eng.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	info, err := os.Stat(output)
	if err != nil {
		t.Fatalf("output file missing: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("output file is empty")
	}
	t.Logf("graph output: %s (%d bytes)", output, info.Size())
}

func TestRunGraphScaleFilter(t *testing.T) {
	if os.Getenv("MEDIAMOLDER_INTEGRATION") == "" {
		input := filepath.Join("..", "testdata", "test_av.avi")
		if _, err := os.Stat(input); err != nil {
			t.Skip("set MEDIAMOLDER_INTEGRATION=1 or provide testdata/test_av.avi")
		}
	}

	inputURL := filepath.Join("..", "testdata", "test_av.avi")
	outDir := t.TempDir()
	output := filepath.Join(outDir, "scaled_out.mp4")
	codec := pickTestEncoder(t)

	rawCfg := fmt.Sprintf(`{
		"schema_version": "1.0",
		"inputs": [{
			"id": "src",
			"url": %q,
			"streams": [{"input_index": 0, "type": "video", "track": 0}]
		}],
		"graph": {
			"nodes": [
				{"id": "scale", "type": "filter", "filter": "scale", "params": {"w": "320", "h": "240"}},
				{"id": "enc",   "type": "encoder", "params": {"codec": %q}}
			],
			"edges": [
				{"from": "src:v:0", "to": "scale", "type": "video"},
				{"from": "scale",   "to": "enc",   "type": "video"},
				{"from": "enc",     "to": "out",   "type": "video"}
			]
		},
		"outputs": [{
			"id": "out",
			"url": %q,
			"codec_video": %q
		}]
	}`, inputURL, codec, output, codec)

	cfg, err := ParseConfig([]byte(rawCfg))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}

	eng, err := NewPipeline(cfg)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}

	if err := eng.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	info, err := os.Stat(output)
	if err != nil {
		t.Fatalf("output file missing: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("output file is empty")
	}
	t.Logf("scaled output: %s (%d bytes)", output, info.Size())
}
