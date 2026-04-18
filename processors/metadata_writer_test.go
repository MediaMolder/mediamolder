// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
)

func TestMetadataWriter_MissingOutputFile(t *testing.T) {
	w := &MetadataWriter{}
	err := w.Init(map[string]any{
		"inner_processor": "frame_counter",
	})
	if err == nil || !strings.Contains(err.Error(), "output_file") {
		t.Fatalf("expected output_file error, got %v", err)
	}
}

func TestMetadataWriter_MissingInnerProcessor(t *testing.T) {
	w := &MetadataWriter{}
	err := w.Init(map[string]any{
		"output_file": "/tmp/test.jsonl",
	})
	if err == nil || !strings.Contains(err.Error(), "inner_processor") {
		t.Fatalf("expected inner_processor error, got %v", err)
	}
}

func TestMetadataWriter_UnknownInnerProcessor(t *testing.T) {
	w := &MetadataWriter{}
	err := w.Init(map[string]any{
		"output_file":     "/tmp/test.jsonl",
		"inner_processor": "nonexistent_processor_xyz",
	})
	if err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("expected unknown processor error, got %v", err)
	}
}

func TestMetadataWriter_WrapsFrameCounter(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "meta.jsonl")

	w := &MetadataWriter{}
	err := w.Init(map[string]any{
		"output_file":     outPath,
		"inner_processor": "frame_counter",
		"log_every":       float64(1), // emit metadata on every frame
	})
	if err != nil {
		t.Fatal(err)
	}

	// Simulate 3 frames through the wrapper.
	for i := uint64(0); i < 3; i++ {
		frame, err := av.AllocFrame()
		if err != nil {
			t.Fatal(err)
		}
		ctx := ProcessorContext{
			FrameIndex: i,
			PTS:        int64(i * 1000),
			MediaType:  av.MediaTypeVideo,
		}
		out, md, err := w.Process(frame, ctx)
		if err != nil {
			frame.Close()
			t.Fatal(err)
		}
		if out == nil {
			t.Fatal("expected frame passthrough, got nil")
		}
		// frame_counter with log_every=1 emits metadata on every frame.
		if md == nil {
			t.Fatalf("frame %d: expected metadata from frame_counter", i)
		}
		frame.Close()
	}

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	// Verify the output file contains 3 JSON Lines.
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %s", len(lines), string(data))
	}

	// Parse first line and verify structure.
	var rec metadataRecord
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("failed to parse first record: %v", err)
	}
	if rec.FrameIndex != 0 {
		t.Fatalf("expected frame_index 0, got %d", rec.FrameIndex)
	}
	if rec.PTS != 0 {
		t.Fatalf("expected pts 0, got %d", rec.PTS)
	}
	if rec.Metadata == nil {
		t.Fatal("expected non-nil metadata")
	}

	// Parse third line to check PTS.
	var rec3 metadataRecord
	if err := json.Unmarshal([]byte(lines[2]), &rec3); err != nil {
		t.Fatalf("failed to parse third record: %v", err)
	}
	if rec3.FrameIndex != 2 {
		t.Fatalf("expected frame_index 2, got %d", rec3.FrameIndex)
	}
	if rec3.PTS != 2000 {
		t.Fatalf("expected pts 2000, got %d", rec3.PTS)
	}
}

func TestMetadataWriter_NoMetadataSkipsWrite(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "meta.jsonl")

	w := &MetadataWriter{}
	err := w.Init(map[string]any{
		"output_file":     outPath,
		"inner_processor": "null", // null processor returns no metadata
	})
	if err != nil {
		t.Fatal(err)
	}

	frame, err := av.AllocFrame()
	if err != nil {
		t.Fatal(err)
	}
	out, md, pErr := w.Process(frame, ProcessorContext{FrameIndex: 0})
	if pErr != nil {
		t.Fatal(pErr)
	}
	if out == nil {
		t.Fatal("expected passthrough")
	}
	if md != nil {
		t.Fatal("null processor should not emit metadata")
	}
	frame.Close()

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	// File should exist but be empty.
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.TrimSpace(string(data))) != 0 {
		t.Fatalf("expected empty file, got %q", string(data))
	}
}

func TestMetadataWriter_Registered(t *testing.T) {
	names := Names()
	found := false
	for _, n := range names {
		if n == "metadata_file_writer" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("metadata_file_writer not found in registered processors")
	}
}
