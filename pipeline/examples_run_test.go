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
// testdata/BBB_10sec.mp4. Examples that require unavailable hardware
// encoders, ONNX models, or Linux-only devices are skipped with an
// explanatory message.
func TestExamplesRun(t *testing.T) {
	if testing.Short() {
		t.Skip("example runs execute real encodes (~150 s); use -run TestExamplesRun to include")
	}
	// Use 10 seconds of the full Big Buck Bunny 1080p source (starting at
	// 450 s) — the seek is injected into every file input after ParseConfig
	// via injectBBBSeek. Run scripts/fetch-bbb.sh to obtain the source.
	inputAbs := bbbSourcePath(t)

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
	case strings.HasPrefix(name, "41_"):
		return ".m3u8"
	case strings.HasPrefix(name, "42_"):
		return ".mpd"
	case strings.HasPrefix(name, "43_"):
		return ".ts"
	default:
		return ".mp4"
	}
}

func runExample(t *testing.T, jsonPath, name, inputAbs, _, _ string) {
	t.Helper()

	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read example file: %v", err)
	}
	raw := string(data)

	// --- Skip: requires subtitle stream in input (BBB_10sec.mp4 has none) ---
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
		// Codec may be compiled in but require Intel hardware to open.
		// Probe the QSV device context to detect absence of Intel GPU.
		dev, qsvErr := av.OpenHWDevice(av.HWDeviceQSV, "")
		if qsvErr != nil {
			t.Skipf("h264_qsv requires Intel QSV hardware: %v", qsvErr)
		}
		dev.Close()
	}

	// --- Skip: optional filters not compiled into this FFmpeg build ---
	if strings.Contains(raw, `"drawtext"`) && !av.FindFilter("drawtext") {
		t.Skip("drawtext filter not compiled in this FFmpeg build (requires libfreetype)")
	}
	if (strings.Contains(raw, `"subtitles"`) && !av.FindFilter("subtitles")) ||
		(strings.Contains(raw, `"ass"`) && !av.FindFilter("ass")) {
		t.Skip("subtitles/ass filter not compiled in this FFmpeg build (rebuild with --enable-libass)")
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

	// filterTmpDir is a temp directory reachable via a relative path from
	// the package CWD. Paths embedded in filtergraph option strings must not
	// contain ':' because avfilter_graph_parse_ptr splits option values on
	// all ':' characters before processing escape sequences. On Windows,
	// t.TempDir() may be on a different drive (C:) than the package (E:),
	// making a relative path impossible. In that case, create a local
	// sub-directory within the package directory instead.
	filterTmpDir := tmpDir
	if cwd, cwdErr := os.Getwd(); cwdErr == nil {
		if _, relErr := filepath.Rel(cwd, tmpDir); relErr != nil {
			// Cross-drive or other error: create a local temp dir.
			local, lerr := os.MkdirTemp(".", "testrun-")
			if lerr == nil {
				t.Cleanup(func() { os.RemoveAll(local) })
				filterTmpDir = local
			}
		}
	}

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

	// Metadata output files → redirect to tmpDir so tests don't litter cwd.
	// frame_metadata.txt is embedded in a filter spec (metadata=mode=print:file=…)
	// so it must use filterTmpDir (relative path, no Windows drive-letter colon).
	for _, meta := range []string{"frame_info.jsonl", "scene_changes.jsonl", "detections.jsonl"} {
		dest := filepath.ToSlash(filepath.Join(tmpDir, meta))
		raw = strings.ReplaceAll(raw, `"`+meta+`"`, `"`+dest+`"`)
	}
	{
		dest := filepath.ToSlash(filepath.Join(filterTmpDir, "frame_metadata.txt"))
		raw = strings.ReplaceAll(raw, `"frame_metadata.txt"`, `"`+dest+`"`)
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

	// Two-pass stats file prefix → tmpDir so the `<prefix>-<idx>.log`
	// file the encoder writes (libx264 `stats` AVOption / generic
	// stats_out shuttle) does not pollute the package directory.
	raw = strings.ReplaceAll(raw, "{{passlog}}", filepath.ToSlash(filepath.Join(tmpDir, "ffmpeg2pass")))

	// Loudnorm shuttle stats prefix → filterTmpDir so pass-1 JSON does not
	// pollute the package directory, and so the path has no drive-letter colon
	// (see filterTmpDir comment above).
	raw = strings.ReplaceAll(raw, "{{loudnorm_stats}}", filepath.ToSlash(filepath.Join(filterTmpDir, "mm-loudnorm")))

	// --- Parse config ---
	cfg, err := ParseConfig([]byte(raw))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	injectBBBSeek(cfg)

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
