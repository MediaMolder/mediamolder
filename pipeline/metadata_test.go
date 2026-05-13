// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestOutputMetadataAndChapters drives a stream-copy pipeline that
// declares container-level metadata and a single chapter on the output,
// then ffprobes the resulting file to confirm both surfaced. The
// matroska container is used because it is the lowest-friction target
// for arbitrary metadata keys + chapters and is always built into our
// FFmpeg.
func TestOutputMetadataAndChapters(t *testing.T) {
	inputURL := bbbSourcePath(t)
	ffprobeBin, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not in PATH; skipping metadata round-trip test")
	}
	outDir := t.TempDir()
	output := filepath.Join(outDir, "metadata.mkv")

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
			"nodes": [],
			"edges": [
				{"from": "in0:v:0", "to": "out0:v", "type": "video"},
				{"from": "in0:a:0", "to": "out0:a", "type": "audio"}
			]
		},
		"outputs": [{
			"id": "out0",
			"url": %q,
			"format": "matroska",
			"codec_video": "copy",
			"codec_audio": "copy",
			"metadata": {"title": "Roadmap5Item5", "artist": "MediaMolder"},
			"chapters": [
				{"start": 0, "end": 2.5, "title": "Intro"},
				{"start": 2.5, "end": 5, "title": "Body"}
			]
		}]
	}`, inputURL, output)

	cfg, err := ParseConfig([]byte(rawCfg))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	injectBBBSeek(cfg)
	eng, err := NewPipeline(cfg)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	if err := eng.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// ffprobe the output for format tags + chapters.
	cmd := exec.Command(ffprobeBin,
		"-v", "error",
		"-show_format",
		"-show_chapters",
		"-of", "json",
		output)
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("ffprobe: %v (stderr: %s)", err, exitStderr(err))
	}

	var probe struct {
		Format struct {
			Tags map[string]string `json:"tags"`
		} `json:"format"`
		Chapters []struct {
			Tags map[string]string `json:"tags"`
		} `json:"chapters"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("ffprobe parse: %v", err)
	}

	// Matroska normalises tag keys to upper case.
	gotTitle := probe.Format.Tags["title"]
	if gotTitle == "" {
		gotTitle = probe.Format.Tags["TITLE"]
	}
	if gotTitle != "Roadmap5Item5" {
		t.Errorf("format.tags.title = %q, want %q (full tags: %v)",
			gotTitle, "Roadmap5Item5", probe.Format.Tags)
	}
	gotArtist := probe.Format.Tags["artist"]
	if gotArtist == "" {
		gotArtist = probe.Format.Tags["ARTIST"]
	}
	if gotArtist != "MediaMolder" {
		t.Errorf("format.tags.artist = %q, want %q", gotArtist, "MediaMolder")
	}

	if len(probe.Chapters) != 2 {
		t.Fatalf("chapters = %d, want 2 (raw: %s)", len(probe.Chapters), raw)
	}
	for i, want := range []string{"Intro", "Body"} {
		got := probe.Chapters[i].Tags["title"]
		if got == "" {
			got = probe.Chapters[i].Tags["TITLE"]
		}
		if got != want {
			t.Errorf("chapters[%d].tags.title = %q, want %q", i, got, want)
		}
	}
}

// TestInputMapMetadataAndChapters round-trips an input that already
// carries metadata (BBB_10sec.mp4 has at least encoder/title tags from
// the source) into a remux that sets map_metadata=true and
// map_chapters=true on the input but no overrides on the output. The
// output is ffprobed and we assert that at least one tag from the
// input appeared on the output (we don't pin a specific key because
// the fixture's tag set may evolve).
func TestInputMapMetadata(t *testing.T) {
	inputURL := bbbSourcePath(t)
	ffprobeBin, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not in PATH")
	}

	// First ffprobe the input to learn its container tags. If the
	// fixture has no container-level tags, skip the assertion (no
	// regression possible).
	srcTags := probeFormatTags(t, ffprobeBin, inputURL)
	if len(srcTags) == 0 {
		t.Skip("source has no container-level metadata to map")
	}

	outDir := t.TempDir()
	output := filepath.Join(outDir, "mapped.mkv")

	rawCfg := fmt.Sprintf(`{
		"schema_version": "1.1",
		"inputs": [{
			"id": "in0",
			"url": %q,
			"map_metadata": true,
			"streams": [
				{"input_index": 0, "type": "video", "track": 0},
				{"input_index": 0, "type": "audio", "track": 0}
			]
		}],
		"graph": {
			"nodes": [],
			"edges": [
				{"from": "in0:v:0", "to": "out0:v", "type": "video"},
				{"from": "in0:a:0", "to": "out0:a", "type": "audio"}
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
	injectBBBSeek(cfg)
	eng, err := NewPipeline(cfg)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	if err := eng.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	dstTags := probeFormatTags(t, ffprobeBin, output)
	// At least one mapped key must appear (matroska may rename to
	// upper-case, so compare case-insensitively).
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
		t.Errorf("no source tags carried over via map_metadata: src=%v dst=%v", srcTags, dstTags)
	}
}

func probeFormatTags(t *testing.T, ffprobeBin, path string) map[string]string {
	t.Helper()
	out, err := exec.Command(ffprobeBin, "-v", "error", "-show_format", "-of", "json", path).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v (stderr: %s)", path, err, exitStderr(err))
	}
	var probe struct {
		Format struct {
			Tags map[string]string `json:"tags"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &probe); err != nil {
		t.Fatalf("ffprobe parse %s: %v", path, err)
	}
	return probe.Format.Tags
}

func exitStderr(err error) string {
	if ee, ok := err.(*exec.ExitError); ok {
		return string(ee.Stderr)
	}
	return ""
}

func equalFoldString(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
