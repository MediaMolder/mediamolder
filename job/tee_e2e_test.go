// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestTeeMuxer_FanOutToMP4AndMKV stream-copies a 10 s clip through
// a tee output that writes the encoded packet stream once to an MP4
// container and once to a Matroska container. Both files must
// independently ffprobe to the source's duration.
func TestTeeMuxer_FanOutToMP4AndMKV(t *testing.T) {
	inputURL := filepath.Join("..", "testdata", "BBB_10sec.mp4")
	if _, err := os.Stat(inputURL); err != nil {
		t.Skip("testdata/BBB_10sec.mp4 missing")
	}
	ffprobeBin, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not in PATH")
	}

	dir := t.TempDir()
	mp4Out := filepath.Join(dir, "tee.mp4")
	mkvOut := filepath.Join(dir, "tee.mkv")

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
			"url": "tee",
			"kind": "tee",
			"codec_video": "copy",
			"codec_audio": "copy",
			"targets": [
				{"url": %q, "format": "mp4"},
				{"url": %q, "format": "matroska"}
			]
		}]
	}`, inputURL, mp4Out, mkvOut)

	cfg, err := ParseConfig([]byte(rawCfg))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	eng, err := NewPipeline(cfg)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := eng.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, p := range []string{mp4Out, mkvOut} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("tee target %q not created: %v", p, err)
			continue
		}
		dur := probeDurationSeconds(t, ffprobeBin, p)
		if math.Abs(dur-10.0) > 1.5 {
			t.Errorf("%s duration = %.2fs, want ~10s", p, dur)
		}
	}
}
