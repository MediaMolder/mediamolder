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
