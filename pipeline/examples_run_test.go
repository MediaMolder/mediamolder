// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/processors"
)

// TestExamplesRun executes every JSON job in testdata/examples against
// testdata/dental_video.mp4.  Examples that require unavailable hardware
// encoders, ONNX models, or Linux-only devices are skipped with an
// explanatory message.
func TestExamplesRun(t *testing.T) {
	// Use the 10-second trimmed clip for fast test runs (VP9 and other slow
	// encoders would time out against the full 18-minute source video).
	// The clip is created by: ffmpeg -i dental_video.mp4 -t 10 -c copy dental_video_10s.mp4
	inputAbs, err := filepath.Abs(filepath.Join("..", "testdata", "dental_video_10s.mp4"))
	if err != nil {
		t.Fatalf("abs path for input: %v", err)
	}
	if _, err := os.Stat(inputAbs); err != nil {
		// Fall back to the full video if the short clip doesn't exist.
		inputAbs, err = filepath.Abs(filepath.Join("..", "testdata", "dental_video.mp4"))
		if err != nil {
			t.Fatalf("abs path for input: %v", err)
		}
		if _, err := os.Stat(inputAbs); err != nil {
			t.Fatalf("dental_video_10s.mp4 (or dental_video.mp4) not found: %v", err)
		}
	}

	subsrtAbs, _ := filepath.Abs(filepath.Join("..", "testdata", "subs.srt"))
	subassAbs, _ := filepath.Abs(filepath.Join("..", "testdata", "subs.ass"))

	const examplesDir = "../testdata/examples"
	entries, err := os.ReadDir(examplesDir)
	if err != nil {
		t.Fatalf("read examples dir: %v", err)
	}

	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		name := ent.Name()
		jsonPath := filepath.Join(examplesDir, name)
		t.Run(name, func(t *testing.T) {
			runExample(t, jsonPath, name, inputAbs, subsrtAbs, subassAbs)
		})
	}
}

// outputExt returns the expected file extension for an example's primary output.
func outputExt(name string) string {
	switch {
	case strings.HasPrefix(name, "07_"):
		return ".mp3"
	case strings.HasPrefix(name, "08_"):
		return ".mkv"
	case strings.HasPrefix(name, "12_"):
		return ".webm"
	case strings.HasPrefix(name, "25_"):
		return ".mkv"
	case strings.HasPrefix(name, "27_"):
		return ".ts"
	default:
		return ".mp4"
	}
}

func runExample(t *testing.T, jsonPath, name, inputAbs, subsrtAbs, subassAbs string) {
	t.Helper()

	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read example file: %v", err)
	}
	raw := string(data)

	// --- Skip: requires subtitle stream in input (dental_video.mp4 has none) ---
	if strings.HasPrefix(name, "25_") {
		t.Skip("requires MKV input with an embedded subtitle track")
	}

	// --- Skip: hardware encoder / device checks ---
	if strings.Contains(raw, `"h264_nvenc"`) {
		if !av.FindEncoder("h264_nvenc") {
			t.Skip("h264_nvenc not available (no NVIDIA GPU / NVENC)")
		}
	}
	if strings.Contains(raw, `"vaapi"`) || strings.Contains(raw, `"h264_vaapi"`) {
		t.Skip("VAAPI is Linux-only")
	}
	if strings.Contains(raw, `"h264_qsv"`) {
		if !av.FindEncoder("h264_qsv") {
			t.Skip("h264_qsv not available (no Intel QSV)")
		}
	}

	// --- Skip: optional filters not compiled into this FFmpeg build ---
	if strings.Contains(raw, `"drawtext"`) && !av.FindFilter("drawtext") {
		t.Skip("drawtext filter not compiled in this FFmpeg build (requires libfreetype)")
	}

	// --- Skip: YOLOv8 requires with_onnx build tag and model files ---
	if strings.Contains(raw, `"yolo_v8"`) {
		if _, err := processors.Get("yolo_v8"); err != nil {
			t.Skip("yolo_v8 processor not registered (rebuild with -tags with_onnx)")
		}
		// Skip CUDA-mode YOLOv8 if no NVENC (proxy for CUDA availability)
		if strings.Contains(raw, `"device": "cuda"`) && !av.FindEncoder("h264_nvenc") {
			t.Skip("yolo_v8 CUDA mode requires NVIDIA GPU")
		}
		// Skip if the model file is missing
		if strings.Contains(raw, "/models/yolov8n.onnx") {
			if _, err := os.Stat("/models/yolov8n.onnx"); err != nil {
				t.Skip("YOLOv8 model not found at /models/yolov8n.onnx")
			}
		}
		if strings.Contains(raw, "/models/custom_5class.onnx") {
			if _, err := os.Stat("/models/custom_5class.onnx"); err != nil {
				t.Skip("custom YOLOv8 model not found at /models/custom_5class.onnx")
			}
		}
	}

	// --- Template substitution ---
	tmpDir := t.TempDir()

	// Paths as forward-slash strings (accepted by FFmpeg and JSON)
	inputFwd := filepath.ToSlash(inputAbs)

	raw = strings.ReplaceAll(raw, "{{input}}", inputFwd)

	// Subtitle filter filenames: use relative paths (no drive-letter colon)
	// so FFmpeg's filter option parser does not treat ':' as a separator.
	// Go tests run with CWD = package directory (pipeline/), so
	// "../testdata/subs.srt" resolves correctly at runtime.
	subsrtRel := filepath.ToSlash(filepath.Join("..", "testdata", "subs.srt"))
	subassRel := filepath.ToSlash(filepath.Join("..", "testdata", "subs.ass"))
	raw = strings.ReplaceAll(raw, `"subs.srt"`, `"`+subsrtRel+`"`)
	raw = strings.ReplaceAll(raw, `"subs.ass"`, `"`+subassRel+`"`)

	// Metadata output files → redirect to tmpDir so tests don't litter cwd
	for _, meta := range []string{"frame_info.jsonl", "scene_changes.jsonl", "detections.jsonl"} {
		dest := filepath.ToSlash(filepath.Join(tmpDir, meta))
		raw = strings.ReplaceAll(raw, `"`+meta+`"`, `"`+dest+`"`)
	}

	// ABR ladder has four named output variables; all others have one {{output}}
	if strings.HasPrefix(name, "35_") {
		raw = strings.ReplaceAll(raw, "{{out_1080}}", filepath.ToSlash(filepath.Join(tmpDir, "out_1080.mp4")))
		raw = strings.ReplaceAll(raw, "{{out_720}}", filepath.ToSlash(filepath.Join(tmpDir, "out_720.mp4")))
		raw = strings.ReplaceAll(raw, "{{out_540}}", filepath.ToSlash(filepath.Join(tmpDir, "out_540.mp4")))
		raw = strings.ReplaceAll(raw, "{{out_360}}", filepath.ToSlash(filepath.Join(tmpDir, "out_360.mp4")))
	} else {
		ext := outputExt(name)
		outFwd := filepath.ToSlash(filepath.Join(tmpDir, "out"+ext))
		raw = strings.ReplaceAll(raw, "{{output}}", outFwd)
	}

	// --- Parse config ---
	cfg, err := ParseConfig([]byte(raw))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}

	// --- Run pipeline ---
	eng, err := NewPipeline(cfg)
	if err != nil {
		if strings.Contains(err.Error(), "CUDA provider") || strings.Contains(err.Error(), "CUDA execution provider") {
			t.Skipf("ONNX Runtime CUDA provider not available: %v", err)
		}
		t.Fatalf("NewPipeline: %v", err)
	}
	if err := eng.Run(context.Background()); err != nil {
		if strings.Contains(err.Error(), "CUDA provider") || strings.Contains(err.Error(), "CUDA execution provider") {
			t.Skipf("ONNX Runtime CUDA provider not available: %v", err)
		}
		t.Fatalf("Run: %v", err)
	}

	// --- Verify outputs ---
	if strings.HasPrefix(name, "35_") {
		for _, f := range []string{"out_1080.mp4", "out_720.mp4", "out_540.mp4", "out_360.mp4"} {
			assertNonEmptyFile(t, filepath.Join(tmpDir, f))
		}
	} else {
		assertNonEmptyFile(t, filepath.Join(tmpDir, "out"+outputExt(name)))
	}
}

func assertNonEmptyFile(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Errorf("output file missing: %v", err)
		return
	}
	if info.Size() == 0 {
		t.Errorf("output file is empty: %s", path)
	} else {
		t.Logf("%-30s %d bytes", filepath.Base(path), info.Size())
	}
}
