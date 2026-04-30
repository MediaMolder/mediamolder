// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

import (
	"strings"
	"testing"
)

// TestParseMapMetadataChapters covers Wave 2 #11 ffcli parsing of
// `-map_metadata IDX` and `-map_chapters IDX`. Each invocation must
// emit a metadata_reader + metadata_writer node pair connected by a
// metadata edge, with params.source / params.target / params.section
// populated. The pipeline runtime resolves these to source-to-output
// metadata routing at WriteHeader time.
func TestParseMapMetadataChapters(t *testing.T) {
	// Two inputs so we can prove `-map_metadata 1` routes from the
	// second input independently of the first.
	cmd := "ffmpeg -y -i a.mp4 -i b.mkv -map_metadata 1 -map_chapters 0 -c copy out.mkv"
	cfg, err := Parse(cmd)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(cfg.Inputs) != 2 {
		t.Fatalf("inputs = %d, want 2", len(cfg.Inputs))
	}
	if len(cfg.Outputs) != 1 {
		t.Fatalf("outputs = %d, want 1", len(cfg.Outputs))
	}
	outID := cfg.Outputs[0].ID
	in0, in1 := cfg.Inputs[0].ID, cfg.Inputs[1].ID

	var sawGlobalReader, sawGlobalWriter, sawChapReader, sawChapWriter bool
	for _, n := range cfg.Graph.Nodes {
		switch n.Type {
		case "metadata_reader":
			src, _ := n.Params["source"].(string)
			sec, _ := n.Params["section"].(string)
			if sec == "global" && src == in1 {
				sawGlobalReader = true
			}
			if sec == "chapters" && src == in0 {
				sawChapReader = true
			}
		case "metadata_writer":
			tgt, _ := n.Params["target"].(string)
			sec, _ := n.Params["section"].(string)
			if tgt != outID {
				t.Errorf("metadata_writer target = %q, want %q", tgt, outID)
			}
			if sec == "global" {
				sawGlobalWriter = true
			}
			if sec == "chapters" {
				sawChapWriter = true
			}
		}
	}
	if !sawGlobalReader || !sawGlobalWriter {
		t.Errorf("missing global metadata_reader/writer pair (reader=%v writer=%v)", sawGlobalReader, sawGlobalWriter)
	}
	if !sawChapReader || !sawChapWriter {
		t.Errorf("missing chapters metadata_reader/writer pair (reader=%v writer=%v)", sawChapReader, sawChapWriter)
	}

	// Exactly one metadata edge per pair (two pairs total).
	mdEdges := 0
	for _, e := range cfg.Graph.Edges {
		if e.Type == "metadata" {
			mdEdges++
		}
	}
	if mdEdges != 2 {
		t.Errorf("metadata edges = %d, want 2", mdEdges)
	}
}

func TestParseMapMetadataErrors(t *testing.T) {
	cases := []struct {
		name    string
		cmd     string
		wantErr string
	}{
		{
			name:    "negative index",
			cmd:     "ffmpeg -y -i a.mp4 -map_metadata -1 -c copy out.mkv",
			wantErr: "invalid index",
		},
		{
			name:    "non-numeric",
			cmd:     "ffmpeg -y -i a.mp4 -map_metadata global -c copy out.mkv",
			wantErr: "invalid index",
		},
		{
			name:    "out of range",
			cmd:     "ffmpeg -y -i a.mp4 -map_metadata 2 -c copy out.mkv",
			wantErr: "only 1 input",
		},
		{
			name:    "missing arg",
			cmd:     "ffmpeg -y -map_metadata",
			wantErr: "requires an argument",
		},
		{
			name:    "chapters out of range",
			cmd:     "ffmpeg -y -i a.mp4 -map_chapters 5 -c copy out.mkv",
			wantErr: "only 1 input",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.cmd)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}
