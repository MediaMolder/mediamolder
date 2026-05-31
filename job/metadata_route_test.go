// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMetadataRouteGraphNodes drives an end-to-end remux that uses the
// Wave 2 #11 metadata_reader / metadata_writer graph-node pair
// (instead of Input.MapMetadata / Input.MapChapters) to route
// container metadata from a source into the output. Verifies the
// runtime resolver wiring in pipeline/handlers.go.
func TestMetadataRouteGraphNodes(t *testing.T) {
	inputURL := filepath.Join("..", "testdata", "BBB_10sec.mp4")
	if _, err := os.Stat(inputURL); err != nil {
		t.Skip("testdata/BBB_10sec.mp4 missing")
	}
	ffprobeBin, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not in PATH")
	}
	srcTags := probeFormatTags(t, ffprobeBin, inputURL)
	if len(srcTags) == 0 {
		t.Skip("source has no container-level metadata to map")
	}

	outDir := t.TempDir()
	output := filepath.Join(outDir, "routed.mkv")

	rawCfg := fmt.Sprintf(`{
		"schema_version": "1.1",
		"inputs": [{
			"id": "in0",
			"url": %q,
			"streams": [
				{"input_index": 0, "type": "video", "track": 0},
				{"input_index": 0, "type": "audio", "track": 0}
			]
		}],
		"graph": {
			"nodes": [
				{"id": "mr", "type": "metadata_reader", "params": {"source": "in0", "section": "global"}},
				{"id": "mw", "type": "metadata_writer", "params": {"target": "out0", "section": "global"}}
			],
			"edges": [
				{"from": "in0:v:0", "to": "out0:v", "type": "video"},
				{"from": "in0:a:0", "to": "out0:a", "type": "audio"},
				{"from": "mr", "to": "mw", "type": "metadata"}
			]
		},
		"outputs": [{
			"id": "out0",
			"url": %q,
			"format": "matroska",
			"codec_video": "copy",
			"codec_audio": "copy"
		}]
	}`, inputURL, output)

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

	dstTags := probeFormatTags(t, ffprobeBin, output)
	matched := false
	for k := range srcTags {
		for kk := range dstTags {
			if equalFoldString(k, kk) {
				matched = true
				break
			}
		}
		if matched {
			break
		}
	}
	if !matched {
		t.Errorf("metadata_route nodes did not carry source tags: src=%v dst=%v", srcTags, dstTags)
	}
}

// TestMetadataRouteValidation covers the Wave 2 #11 validation gates
// for metadata_reader / metadata_writer node param shape.
func TestMetadataRouteValidation(t *testing.T) {
	mkCfg := func(nodes []NodeDef) *Config {
		return &Config{
			SchemaVersion: "1.0",
			Inputs:        []Input{{ID: "in0", URL: "x.mp4"}},
			Outputs:       []Output{{ID: "out0", URL: "y.mkv"}},
			Graph:         GraphDef{Nodes: nodes},
		}
	}
	cases := []struct {
		name    string
		nodes   []NodeDef
		wantErr string
	}{
		{
			name: "reader missing source",
			nodes: []NodeDef{
				{ID: "mr", Type: "metadata_reader", Params: map[string]any{}},
			},
			wantErr: "metadata_reader requires params.source",
		},
		{
			name: "reader unknown source",
			nodes: []NodeDef{
				{ID: "mr", Type: "metadata_reader", Params: map[string]any{"source": "ghost"}},
			},
			wantErr: "does not match any input",
		},
		{
			name: "reader bad section",
			nodes: []NodeDef{
				{ID: "mr", Type: "metadata_reader", Params: map[string]any{"source": "in0", "section": "bogus"}},
			},
			wantErr: "params.section must be",
		},
		{
			name: "writer missing target",
			nodes: []NodeDef{
				{ID: "mw", Type: "metadata_writer", Params: map[string]any{}},
			},
			wantErr: "metadata_writer requires params.target",
		},
		{
			name: "writer unknown target",
			nodes: []NodeDef{
				{ID: "mw", Type: "metadata_writer", Params: map[string]any{"target": "ghost"}},
			},
			wantErr: "does not match any output",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validate(mkCfg(tc.nodes))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}
