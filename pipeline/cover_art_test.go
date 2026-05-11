// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: GPL-3.0-or-later

package pipeline

import (
	"strings"
	"testing"
)

func baseCoverArtConfig(out Output) *Config {
	return &Config{
		SchemaVersion: "1.0",
		Inputs:        []Input{{ID: "in0", URL: "in.mp4"}},
		Outputs:       []Output{out},
		Graph:         GraphDef{Edges: []EdgeDef{{From: "in0:v:0", To: "out0:v", Type: "video"}}},
	}
}

func TestValidateCoverArt_Valid(t *testing.T) {
	out := Output{
		ID: "out0", URL: "out.mp4", Format: "mp4", CodecVideo: "copy",
		CoverArt: "/tmp/cover.jpg",
	}
	if err := validate(baseCoverArtConfig(out)); err != nil {
		t.Fatalf("expected valid for mp4, got %v", err)
	}
}

func TestValidateCoverArt_ValidMatroska(t *testing.T) {
	out := Output{
		ID: "out0", URL: "out.mkv", Format: "matroska", CodecVideo: "copy",
		CoverArt: "/tmp/cover.jpg",
	}
	if err := validate(baseCoverArtConfig(out)); err != nil {
		t.Fatalf("expected valid for matroska, got %v", err)
	}
}

func TestValidateCoverArt_RejectsBadContainer(t *testing.T) {
	out := Output{
		ID: "out0", URL: "out.ts", Format: "mpegts", CodecVideo: "copy",
		CoverArt: "/tmp/cover.jpg",
	}
	err := validate(baseCoverArtConfig(out))
	if err == nil || !strings.Contains(err.Error(), "mp4") {
		t.Fatalf("expected container rejection, got %v", err)
	}
}

func TestValidateCoverArt_EmptyPathNoOp(t *testing.T) {
	out := Output{
		ID: "out0", URL: "out.mp4", Format: "mp4", CodecVideo: "copy",
	}
	if err := validate(baseCoverArtConfig(out)); err != nil {
		t.Fatalf("expected no-op for empty cover_art, got %v", err)
	}
}

func TestValidateCoverArt_NoFormatAllowed(t *testing.T) {
	// When format is not set, validation defers to the muxer (allow any).
	out := Output{
		ID: "out0", URL: "out.mp4", CodecVideo: "copy",
		CoverArt: "/tmp/cover.jpg",
	}
	if err := validate(baseCoverArtConfig(out)); err != nil {
		t.Fatalf("expected valid when format is empty, got %v", err)
	}
}
