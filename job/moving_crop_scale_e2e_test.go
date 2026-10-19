// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
)

// TestMovingCropScale_EndToEnd decodes a 256×64 ramp, reframes it to 64×64
// with no scale node, and encodes. The encoder opening at the output size is
// FrameTransformInfo. The frame at the second keyframe, and every frame the
// tail holds, is an unscaled 64×64 window whose first luma column is 32.
func TestMovingCropScale_EndToEnd(t *testing.T) {
	ffmpegBin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("system ffmpeg not found")
	}
	ffprobeBin, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("system ffprobe not found")
	}
	if !av.FindEncoder("ffv1") {
		t.Skip("ffv1 encoder not available")
	}
	const srcW, srcH, outW, outH = 256, 64, 64, 64
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	gen := exec.Command(ffmpegBin, "-v", "error", "-y", "-f", "lavfi",
		"-i", fmt.Sprintf("color=c=black:s=%dx%d:r=25:d=1,format=yuv420p,geq=lum='X':cb='X/2':cr=128", srcW, srcH),
		"-frames:v", "25", "-c:v", "ffv1", src)
	if b, err := gen.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg could not generate the source (%v):\n%s", err, b)
	}
	pts := probePTS(t, ffprobeBin, src)
	if len(pts) < 11 {
		t.Fatalf("source has %d frames, want at least 11", len(pts))
	}
	second := pts[10]

	hold := filepath.Join(dir, "hold.mkv")
	runMovingCrop(t, src, hold, outW, outH, "hold", pts[0], second)
	assertRampTail(t, ffmpegBin, hold, outW, outH, len(pts), 10, 32)

	drop := filepath.Join(dir, "drop.mkv")
	runMovingCrop(t, src, drop, outW, outH, "drop", pts[0], second)
	assertRampTail(t, ffmpegBin, drop, outW, outH, 11, 10, 32)
}

func probePTS(t *testing.T, ffprobe, src string) []int64 {
	t.Helper()
	b, err := exec.Command(ffprobe, "-v", "error", "-select_streams", "v:0",
		"-show_entries", "frame=pts", "-of", "csv=p=0", src).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	var pts []int64
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "N/A" {
			continue
		}
		n, err := strconv.ParseInt(line, 10, 64)
		if err != nil {
			t.Fatalf("pts %q: %v", line, err)
		}
		pts = append(pts, n)
	}
	return pts
}

func runMovingCrop(t *testing.T, src, dst string, outW, outH int, unmatched string, first, second int64) {
	t.Helper()
	params, err := json.Marshal(map[string]any{
		"output":      map[string]any{"width": outW, "height": outH},
		"interpolate": "linear",
		"unmatched":   unmatched,
		"keyframes": []map[string]any{
			{"pts": first, "x": 0, "y": 0, "w": 128, "h": outH},
			{"pts": second, "x": 32, "y": 0, "w": outW, "h": outH},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	rawCfg := fmt.Sprintf(`{
		"schema_version": "1.0",
		"copy_ts": true,
		"inputs": [{
			"id": "src", "url": %q,
			"streams": [{"input_index":0,"type":"video","track":0}]
		}],
		"graph": {
			"nodes": [
				{"id":"reframe","type":"go_processor","processor":"moving_crop_scale","params":%s}
			],
			"edges": [
				{"from":"src:v:0","to":"reframe","type":"video"},
				{"from":"reframe","to":"out:v","type":"video"}
			]
		},
		"outputs": [{"id":"out","url":%q,"format":"matroska","codec_video":"ffv1"}]
	}`, src, params, dst)
	cfg, err := ParseConfig([]byte(rawCfg))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	eng, err := NewPipeline(cfg)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	if err := eng.Run(context.Background()); err != nil {
		t.Fatalf("Run (%s): %v", unmatched, err)
	}
}

func assertRampTail(t *testing.T, ffmpeg, path string, w, h, frames, tailFrom, col0 int) {
	t.Helper()
	raw, err := exec.Command(ffmpeg, "-v", "error", "-i", path,
		"-f", "rawvideo", "-pix_fmt", "yuv420p", "-").Output()
	if err != nil {
		t.Fatalf("decode %s: %v (%s)", path, err, exitStderr(err))
	}
	frameSize := w * h * 3 / 2
	if len(raw) != frames*frameSize {
		t.Fatalf("%s has %d bytes = %.2f frames of %dx%d, want %d",
			path, len(raw), float64(len(raw))/float64(frameSize), w, h, frames)
	}
	for i := tailFrom; i < frames; i++ {
		luma := raw[i*frameSize:]
		for _, row := range []int{0, h - 1} {
			if got := int(luma[row*w]); got != col0 {
				t.Errorf("frame %d row %d: first luma column = %d, want %d", i, row, got, col0)
			}
			if got := int(luma[row*w+w-1]); got != col0+w-1 {
				t.Errorf("frame %d row %d: last luma column = %d, want %d", i, row, got, col0+w-1)
			}
		}
	}
}

// TestMovingCropScale_SampleMatchEndToEnd is the trim: copy_ts, an input
// window, and one keyframe per kept frame. Each keyframe is used once, the
// first luma column is that window's left edge, and Close accepts the run.
// A keyframe past the source fails Close.
func TestMovingCropScale_SampleMatchEndToEnd(t *testing.T) {
	ffmpegBin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("system ffmpeg not found")
	}
	if !av.FindEncoder("ffv1") {
		t.Skip("ffv1 encoder not available")
	}
	const w, h, width = 256, 64, 32
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	gen := exec.Command(ffmpegBin, "-v", "error", "-y", "-f", "lavfi",
		"-i", fmt.Sprintf("color=c=black:s=%dx%d:r=25:d=1,format=yuv420p,geq=lum='X':cb='X/2':cr=128", w, h),
		"-c:v", "ffv1", src)
	if b, err := gen.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg could not generate the source (%v):\n%s", err, b)
	}

	// Matroska time base is 1/1000; at 25 fps frames are 40 ticks apart.
	// Keyframes cover frames 10..19. Frames outside that window are dropped.
	// x = cx - width/2. Bilinear on the luma ramp, so a fractional edge
	// blends the two source columns and rounds half away from zero.
	// cx 300 is entirely past the picture (digital black, luma 16).
	// cx 5 starts 11 pixels left of the picture.
	cx := []float64{16.3, 36.3, 36.3, 77.6, 100.2, 140.5, 141.5, 200, 300, 5}
	want := [][2]int{
		{0, 31}, {20, 51}, {20, 51}, {62, 93}, {84, 115},
		{125, 156}, {126, 157}, {184, 215}, {16, 16}, {16, 20},
	}
	kfs := make([]map[string]any, len(cx))
	for i := range cx {
		kfs[i] = map[string]any{"pts": 400 + 40*i, "cx": cx[i], "cy": h / 2, "w": width, "h": h}
	}
	runSample := func(t *testing.T, keyframes []map[string]any, dst string) error {
		t.Helper()
		params, err := json.Marshal(map[string]any{
			"output":      map[string]any{"width": width, "height": h},
			"match":       "sample",
			"interpolate": "hold",
			"unmatched":   "drop",
			"flags":       "bilinear",
			"keyframes":   keyframes,
		})
		if err != nil {
			t.Fatal(err)
		}
		rawCfg := fmt.Sprintf(`{
			"schema_version": "1.0",
			"copy_ts": true,
			"inputs": [{
				"id": "src", "url": %q,
				"options": {"ss": "0.399999", "to": "0.800001"},
				"streams": [{"input_index":0,"type":"video","track":0}]
			}],
			"graph": {
				"nodes": [
					{"id":"fmt","type":"filter","filter":"format","params":{"pix_fmts":"yuv420p"}},
					{"id":"crop","type":"go_processor","processor":"moving_crop_scale","params":%s},
					{"id":"rebase","type":"filter","filter":"setpts","params":{"expr":"PTS-STARTPTS"}},
					{"id":"strip","type":"filter","filter":"sidedata","params":{"mode":"delete","type":"-1"}}
				],
				"edges": [
					{"from":"src:v:0","to":"fmt","type":"video"},
					{"from":"fmt","to":"crop","type":"video"},
					{"from":"crop","to":"rebase","type":"video"},
					{"from":"rebase","to":"strip","type":"video"},
					{"from":"strip","to":"out:v","type":"video"}
				]
			},
			"outputs": [{"id":"out","url":%q,"format":"matroska","codec_video":"ffv1"}]
		}`, src, params, dst)
		cfg, err := ParseConfig([]byte(rawCfg))
		if err != nil {
			t.Fatalf("ParseConfig: %v", err)
		}
		eng, err := NewPipeline(cfg)
		if err != nil {
			t.Fatalf("NewPipeline: %v", err)
		}
		return eng.Run(context.Background())
	}

	out := filepath.Join(dir, "out.mkv")
	if err := runSample(t, kfs, out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	raw, err := exec.Command(ffmpegBin, "-v", "error", "-i", out,
		"-f", "rawvideo", "-pix_fmt", "yuv420p", "-").Output()
	if err != nil {
		t.Fatalf("decode output: %v (stderr: %s)", err, exitStderr(err))
	}
	frameSize := width * h * 3 / 2
	if len(raw) != len(want)*frameSize {
		t.Fatalf("output has %d bytes = %.2f frames of %dx%d, want %d frames",
			len(raw), float64(len(raw))/float64(frameSize), width, h, len(want))
	}
	for i, edge := range want {
		luma := raw[i*frameSize:]
		for _, row := range []int{0, h - 1} {
			if got := int(luma[row*width]); got != edge[0] {
				t.Errorf("frame %d row %d: first luma column = %d, want %d", i, row, got, edge[0])
			}
			if got := int(luma[row*width+width-1]); got != edge[1] {
				t.Errorf("frame %d row %d: last luma column = %d, want %d", i, row, got, edge[1])
			}
		}
	}

	miss := append(append([]map[string]any{}, kfs...), map[string]any{
		"pts": 5000, "cx": 16.0, "cy": h / 2, "w": width, "h": h,
	})
	err = runSample(t, miss, filepath.Join(dir, "short.mkv"))
	if err == nil || !strings.Contains(err.Error(), "matched no frame") {
		t.Fatalf("Run = %v, want an unused keyframe", err)
	}
}
