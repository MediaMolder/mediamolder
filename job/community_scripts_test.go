// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/processors"
)

// TestCommunityScriptsRun executes every JSON job in
// testdata/community-scripts/ against testdata/BBB_10sec.mp4 (with
// BBB_30sec.mp4 as fallback).
//
// Template variables substituted:
//
//	{{input}}    – primary video fixture (BBB_10sec.mp4 or BBB_30sec.mp4)
//	{{input2}}   – second video fixture (same file, for overlay/xfade tests)
//	{{image}}    – still-image fixture  (testdata/sample.jpg); test skipped when absent
//	{{audio}}    – audio-only fixture   (testdata/sample.aac); test skipped when absent
//	{{raw_yuv}}  – raw 320x240 yuv420p fixture (testdata/sample.yuv); test skipped when absent
//	{{output}}   – temporary output file created in t.TempDir()
//
// Subtitle path ("subs.srt") is rewritten to the relative path used by
// existing examples (../testdata/subs.srt).
//
// Tests that require unavailable encoders, filters, or go_processors are
// skipped with an explanatory message.
func TestCommunityScriptsRun(t *testing.T) {
	if testing.Short() {
		t.Skip("community scripts run real encodes (~140 s); use -run TestCommunityScriptsRun to include")
	}
	// --- Resolve primary video fixture ---
	inputAbs := bbbSourcePath(t)

	// --- Optional fixtures (absent → skip relevant subtests) ---
	imageAbs, _ := filepath.Abs(filepath.Join("..", "testdata", "sample.jpg"))
	audioAbs, _ := filepath.Abs(filepath.Join("..", "testdata", "sample.aac"))
	rawYuvAbs, _ := filepath.Abs(filepath.Join("..", "testdata", "sample.yuv"))

	const communityDir = "../testdata/community-scripts"
	entries, err := os.ReadDir(communityDir)
	if err != nil {
		t.Fatalf("read community-scripts dir: %v", err)
	}

	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		name := ent.Name()
		jsonPath := filepath.Join(communityDir, name)
		t.Run(name, func(t *testing.T) {
			runCommunityScript(t, jsonPath, name, inputAbs, imageAbs, audioAbs, rawYuvAbs)
		})
	}
}

func runCommunityScript(t *testing.T, jsonPath, name, inputAbs, imageAbs, audioAbs, rawYuvAbs string) {
	t.Helper()

	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read community script: %v", err)
	}
	raw := string(data)

	// --- Skip when optional fixtures are absent ---
	if strings.Contains(raw, `"{{image}}"`) {
		if _, err := os.Stat(imageAbs); err != nil {
			t.Skipf("testdata/sample.jpg not found – generate with: ffmpeg -i testdata/BBB_10sec.mp4 -ss 2 -vframes 1 testdata/sample.jpg")
		}
	}
	if strings.Contains(raw, `"{{audio}}"`) {
		if _, err := os.Stat(audioAbs); err != nil {
			t.Skipf("testdata/sample.aac not found – generate with: ffmpeg -i testdata/BBB_10sec.mp4 -vn -c:a aac -t 10 testdata/sample.aac")
		}
	}
	if strings.Contains(raw, `"{{raw_yuv}}"`) {
		if _, err := os.Stat(rawYuvAbs); err != nil {
			t.Skipf("testdata/sample.yuv not found – generate with: ffmpeg -i testdata/BBB_10sec.mp4 -t 1 -vf scale=320x240,format=yuv420p -f rawvideo testdata/sample.yuv")
		}
	}

	// --- Skip when required encoders / filters / processors are absent ---
	if strings.Contains(raw, `"libwebp_anim"`) && !av.FindEncoder("libwebp_anim") {
		t.Skip("libwebp_anim encoder not available (rebuild with libwebp)")
	}
	if strings.Contains(raw, `"drawtext"`) && !av.FindFilter("drawtext") {
		t.Skip("drawtext filter not available (rebuild with libfreetype)")
	}
	if strings.Contains(raw, `"acrossfade"`) && !av.FindFilter("acrossfade") {
		t.Skip("acrossfade filter not available")
	}
	if strings.Contains(raw, `"go_processor"`) {
		if _, err := processors.Get("metadata_file_writer"); err != nil {
			t.Skip("metadata_file_writer go_processor not registered")
		}
	}

	// --- Template substitution ---
	tmpDir := t.TempDir()

	inputFwd := filepath.ToSlash(inputAbs)
	imageFwd := filepath.ToSlash(imageAbs)
	audioFwd := filepath.ToSlash(audioAbs)

	raw = strings.ReplaceAll(raw, "{{input}}", inputFwd)
	raw = strings.ReplaceAll(raw, "{{input2}}", inputFwd) // same file for overlay/xfade tests
	raw = strings.ReplaceAll(raw, "{{image}}", imageFwd)
	raw = strings.ReplaceAll(raw, "{{audio}}", audioFwd)
	raw = strings.ReplaceAll(raw, "{{raw_yuv}}", filepath.ToSlash(rawYuvAbs))

	// Subtitle path: rewrite to relative path so FFmpeg's colon-parsing is safe.
	subsrtRel := filepath.ToSlash(filepath.Join("..", "testdata", "subs.srt"))
	raw = strings.ReplaceAll(raw, `"subs.srt"`, `"`+subsrtRel+`"`)

	// Metadata output files → redirect to tmpDir.
	for _, meta := range []string{"scene_changes.jsonl", "frame_info.jsonl", "detections.jsonl"} {
		dest := filepath.ToSlash(filepath.Join(tmpDir, meta))
		raw = strings.ReplaceAll(raw, `"`+meta+`"`, `"`+dest+`"`)
	}

	ext := communityOutputExt(name)
	outFwd := filepath.ToSlash(filepath.Join(tmpDir, "out"+ext))
	raw = strings.ReplaceAll(raw, "{{output}}", outFwd)

	// --- Parse and run ---
	cfg, err := ParseConfig([]byte(raw))
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

	assertNonEmptyFile(t, filepath.Join(tmpDir, "out"+ext))
}

// communityOutputExt returns the expected output file extension for a
// community-script job based on its filename.
func communityOutputExt(name string) string {
	switch {
	case strings.HasPrefix(name, "11_"): // vid2gif
		return ".gif"
	case strings.HasPrefix(name, "12_"): // webp
		return ".webp"
	case strings.HasPrefix(name, "18_"): // subtitle_add → MKV
		return ".mkv"
	case strings.HasPrefix(name, "21_"), // extract_frame
		strings.HasPrefix(name, "22_"), // tile_thumbnails
		strings.HasPrefix(name, "23_"): // scene_images
		return ".jpg"
	case strings.HasPrefix(name, "26_"): // audio_silence (lavfi anullsrc → wav)
		return ".wav"
	default:
		return ".mp4"
	}
}
