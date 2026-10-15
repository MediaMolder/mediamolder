// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
)

// TestPathCrop_EndToEnd runs a reframing job (copy_ts, input ss/to, format,
// path_crop, window scale, setpts, sidedata)
// over a lossless source whose luma column x holds x. Each output frame's
// first luma column must equal that sample's integer left edge, and every
// path sample must produce exactly one frame.
func TestPathCrop_EndToEnd(t *testing.T) {
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
	// Samples cover frames 10..19, so the frames either side are dropped.
	cx := []float64{16.3, 36.3, 36.3, 77.6, 100.2, 140.5, 141.5, 200, 300, 5}
	want := []int{0, 20, 20, 62, 84, 124, 124, 184, 224, 0}
	frames := make([]map[string]any, len(cx))
	for i := range cx {
		frames[i] = map[string]any{"pts": 400 + 40*i, "cx": cx[i]}
	}
	params, _ := json.Marshal(map[string]any{"width": width, "frames": frames})

	out := filepath.Join(dir, "out.mkv")
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
				{"id":"crop","type":"go_processor","processor":"path_crop","params":%s},
				{"id":"window","type":"filter","filter":"scale","params":{"w":%d,"h":%d}},
				{"id":"rebase","type":"filter","filter":"setpts","params":{"expr":"PTS-STARTPTS"}},
				{"id":"strip","type":"filter","filter":"sidedata","params":{"mode":"delete","type":"-1"}}
			],
			"edges": [
				{"from":"src:v:0","to":"fmt","type":"video"},
				{"from":"fmt","to":"crop","type":"video"},
				{"from":"crop","to":"window","type":"video"},
				{"from":"window","to":"rebase","type":"video"},
				{"from":"rebase","to":"strip","type":"video"},
				{"from":"strip","to":"out:v","type":"video"}
			]
		},
		"outputs": [{"id":"out","url":%q,"format":"matroska","codec_video":"ffv1"}]
	}`, src, params, width, h, out)

	cfg, err := ParseConfig([]byte(rawCfg))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	eng, err := NewPipeline(cfg)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	if err := eng.Run(context.Background()); err != nil {
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
	for i, x := range want {
		luma := raw[i*frameSize:]
		for _, row := range []int{0, h - 1} {
			if got := int(luma[row*width]); got != x {
				t.Errorf("frame %d row %d: first luma column = %d, want %d", i, row, got, x)
			}
			if got := int(luma[row*width+width-1]); got != x+width-1 {
				t.Errorf("frame %d row %d: last luma column = %d, want %d", i, row, got, x+width-1)
			}
		}
	}
}
