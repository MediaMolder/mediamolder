// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// synthInput creates a tiny synthetic H.264 MP4 file (1 second, 64x48 @ 25fps)
// in dir using the system ffmpeg binary.  Returns the file path or skips t if
// ffmpeg is not available.
func synthInput(t *testing.T, dir string) string {
	t.Helper()
	ffmpegBin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("system ffmpeg not found; skipping perf integration test")
	}
	out := filepath.Join(dir, "synth_input.mp4")
	cmd := exec.Command(ffmpegBin, "-y",
		"-f", "lavfi",
		"-i", "testsrc2=size=64x48:rate=25:duration=1",
		"-c:v", "libx264", "-preset", "ultrafast", "-crf", "35",
		out,
	)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg failed to generate synthetic input (%v):\n%s", err, b)
	}
	return out
}

// TestPipelinePerfMetrics_Populated runs the runGraph code path with a synthetic
// input and verifies that MetricsSnapshot.Perf is non-empty after the pipeline
// completes.  This validates the Phase 2 tracker allocation and registration.
func TestPipelinePerfMetrics_Populated(t *testing.T) {
	dir := t.TempDir()
	input := synthInput(t, dir)
	output := filepath.Join(dir, "out.mp4")

	codec := pickTestEncoder(t)

	rawCfg := fmt.Sprintf(`{
		"schema_version": "1.0",
		"inputs": [{
			"id": "src",
			"url": %q,
			"streams": [{"input_index": 0, "type": "video", "track": 0}]
		}],
		"graph": {
			"nodes": [
				{"id": "enc", "type": "encoder",
				 "params": {"codec": %q, "preset": "ultrafast", "crf": "30"}}
			],
			"edges": [
				{"from": "src", "to": "enc", "type": "video"},
				{"from": "enc", "to": "out0", "type": "video"}
			]
		},
		"outputs": [{"id": "out0", "url": %q, "format": "mp4"}]
	}`, input, codec, output)

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

	snap := eng.GetMetrics()
	if len(snap.Perf) == 0 {
		t.Fatal("MetricsSnapshot.Perf is empty; expected at least one NodePerfSnapshot")
	}

	t.Logf("got %d NodePerfSnapshots", len(snap.Perf))
	for _, ps := range snap.Perf {
		t.Logf("  node=%q fps=%.2f active=%.2f idle=%.2f stalled=%.2f threads=%d mode=%s busy=%d latency=%v",
			ps.NodeID, ps.FPS, ps.ActiveFrac, ps.IdleFrac, ps.StalledFrac,
			ps.ThreadsConfigured, ps.ThreadMode, ps.ThreadsBusy, ps.FrameLatencyMean)
		// Fractions must be in [0,1] and sum to approximately 1.
		sum := ps.ActiveFrac + ps.IdleFrac + ps.StalledFrac
		if sum < 0.99 || sum > 1.01 {
			t.Errorf("node %q: fractions sum = %.4f, want ~1.0", ps.NodeID, sum)
		}
	}

	if _, err := os.Stat(output); err != nil {
		t.Fatalf("output file missing: %v", err)
	}
}

// TestPipelinePerfMetrics_EncoderThreadInfo verifies that the encoder node's
// NodePerfSnapshot reports a non-zero ThreadsConfigured after the pipeline
// completes, confirming that SetThreadInfo was called from runGraph.
func TestPipelinePerfMetrics_EncoderThreadInfo(t *testing.T) {
	dir := t.TempDir()
	input := synthInput(t, dir)
	output := filepath.Join(dir, "out.mp4")

	codec := pickTestEncoder(t)

	rawCfg := fmt.Sprintf(`{
		"schema_version": "1.0",
		"inputs": [{
			"id": "src",
			"url": %q,
			"streams": [{"input_index": 0, "type": "video", "track": 0}]
		}],
		"graph": {
			"nodes": [
				{"id": "enc", "type": "encoder",
				 "params": {"codec": %q, "preset": "ultrafast", "crf": "30"}}
			],
			"edges": [
				{"from": "src", "to": "enc", "type": "video"},
				{"from": "enc", "to": "out0", "type": "video"}
			]
		},
		"outputs": [{"id": "out0", "url": %q, "format": "mp4"}]
	}`, input, codec, output)

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

	snap := eng.GetMetrics()
	var encSnap *NodePerfSnapshot
	for i := range snap.Perf {
		if snap.Perf[i].NodeID == "enc" {
			encSnap = &snap.Perf[i]
			break
		}
	}
	if encSnap == nil {
		t.Fatal("no NodePerfSnapshot for node \"enc\"")
	}
	if encSnap.ThreadsConfigured <= 0 {
		t.Errorf("enc.ThreadsConfigured = %d, want > 0", encSnap.ThreadsConfigured)
	}
	t.Logf("enc: threads=%d mode=%q busy=%d latency=%v fps=%.2f",
		encSnap.ThreadsConfigured, encSnap.ThreadMode, encSnap.ThreadsBusy,
		encSnap.FrameLatencyMean, encSnap.FPS)
}
