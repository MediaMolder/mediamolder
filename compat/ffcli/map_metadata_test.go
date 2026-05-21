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
		{
			name:    "too negative",
			cmd:     "ffmpeg -y -i a.mp4 -map_metadata -2 -c copy out.mkv",
			wantErr: "invalid index",
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

// TestImportDefaultMetadata verifies that importing an FFmpeg command with no
// explicit -map_metadata / -map_chapters flags mirrors FFmpeg's implicit
// default behaviour by setting MapMetadata and MapChapters on inputs[0].
// No metadata_reader / metadata_writer nodes should be emitted in this case.
func TestImportDefaultMetadata(t *testing.T) {
	cfg, err := Parse("ffmpeg -i input.mp4 -c:v libx264 -c:a aac output.mp4")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(cfg.Inputs) == 0 {
		t.Fatal("no inputs")
	}
	if !cfg.Inputs[0].MapMetadata {
		t.Error("inputs[0].MapMetadata should be true (FFmpeg default -map_metadata 0)")
	}
	if !cfg.Inputs[0].MapChapters {
		t.Error("inputs[0].MapChapters should be true (FFmpeg default -map_chapters 0)")
	}
	// No extra nodes should be emitted — MapMetadata/MapChapters flags
	// are the shorthand; reader/writer nodes are only for explicit IDX routing.
	for _, n := range cfg.Graph.Nodes {
		if n.Type == "metadata_reader" || n.Type == "metadata_writer" {
			t.Errorf("unexpected %s node %q in default-metadata import", n.Type, n.ID)
		}
	}
}

// TestImportSuppressMetadata verifies that -map_metadata -1 and
// -map_chapters -1 suppress the implicit default so neither
// MapMetadata/MapChapters is set nor any reader/writer nodes are emitted.
func TestImportSuppressMetadata(t *testing.T) {
	cfg, err := Parse("ffmpeg -i input.mp4 -map_metadata -1 -map_chapters -1 -c copy output.mkv")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Inputs[0].MapMetadata {
		t.Error("inputs[0].MapMetadata should be false after -map_metadata -1")
	}
	if cfg.Inputs[0].MapChapters {
		t.Error("inputs[0].MapChapters should be false after -map_chapters -1")
	}
	for _, n := range cfg.Graph.Nodes {
		if n.Type == "metadata_reader" || n.Type == "metadata_writer" {
			t.Errorf("unexpected %s node after explicit suppress", n.Type)
		}
	}
	for _, e := range cfg.Graph.Edges {
		if e.Type == "metadata" {
			t.Errorf("unexpected metadata edge after explicit suppress")
		}
	}
}

// TestImportMultiOutputDefaultMetadata verifies that with two outputs and no
// explicit -map_metadata, both outputs get metadata from input 0. The
// MapMetadata flag is set once on inputs[0] (it applies to all outputs).
func TestImportMultiOutputDefaultMetadata(t *testing.T) {
	cfg, err := Parse("ffmpeg -i input.mp4 -c copy out1.mp4 -c copy out2.mp4")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !cfg.Inputs[0].MapMetadata {
		t.Error("inputs[0].MapMetadata should be true for multi-output default")
	}
	if !cfg.Inputs[0].MapChapters {
		t.Error("inputs[0].MapChapters should be true for multi-output default")
	}
}

// TestImportSuppressFirstOutputOnly verifies that -map_metadata -1 before
// the first output suppresses only that output; the second output should
// still get the default route (MapMetadata=true on inputs[0]).
func TestImportSuppressFirstOutputOnly(t *testing.T) {
	cfg, err := Parse("ffmpeg -i input.mp4 -map_metadata -1 -c copy out1.mp4 -c copy out2.mp4")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// out1 was explicitly suppressed; out2 gets the default.
	if !cfg.Inputs[0].MapMetadata {
		t.Error("inputs[0].MapMetadata should be true: out2 should receive the default metadata route")
	}
	// No reader/writer nodes emitted (MapMetadata flag handles the default).
	for _, n := range cfg.Graph.Nodes {
		if n.Type == "metadata_reader" || n.Type == "metadata_writer" {
			t.Errorf("unexpected %s node", n.Type)
		}
	}
}
