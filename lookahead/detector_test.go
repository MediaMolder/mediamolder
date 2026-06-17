// Copyright (C) 2025-2026 MediaMolder contributors.
// SPDX-License-Identifier: LGPL-2.1-or-later

package lookahead

import (
	"testing"

	psd "github.com/MediaMolder/MediaMolder/go_scene_detect"
)

// solidBGR returns a w×h packed BGR24 frame filled with the given channel values.
func solidBGR(w, h int, b, g, r byte) *psd.FrameData {
	buf := make([]byte, w*h*3)
	for i := 0; i < w*h; i++ {
		buf[i*3] = b
		buf[i*3+1] = g
		buf[i*3+2] = r
	}
	return &psd.FrameData{Width: w, Height: h, BGR: buf}
}

// makeTc creates a FrameTimecode at 25 fps for the given frame number.
func makeTc(n int64) psd.FrameTimecode {
	tc, _ := psd.NewFrameTimecode(n, 25.0)
	return tc
}

func TestNewLookaheadDetector_InvalidL(t *testing.T) {
	// 257+ now invalid after MaxLag bump to 256 for custom long-lag dissolve testing
	for _, l := range []int{0, -1, 257, 300} {
		if _, err := NewLookaheadDetector(l, LookaheadAnalyzer{}); err == nil {
			t.Errorf("l=%d: expected error, got nil", l)
		}
	}
}

func TestNewLookaheadDetector_Valid(t *testing.T) {
	d, err := NewLookaheadDetector(10, LookaheadAnalyzer{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d == nil {
		t.Fatal("nil detector")
	}
}

func TestGetMetrics(t *testing.T) {
	d, _ := NewLookaheadDetector(5, LookaheadAnalyzer{})
	m := d.GetMetrics()
	if len(m) == 0 {
		t.Fatal("expected non-empty metrics")
	}
	if m[0] != "lookahead_ratio_lag1" {
		t.Errorf("unexpected metric name %q", m[0])
	}
}

func TestEventBufferLength(t *testing.T) {
	d, _ := NewLookaheadDetector(5, LookaheadAnalyzer{})
	if d.EventBufferLength() != 0 {
		t.Errorf("expected 0, got %d", d.EventBufferLength())
	}
}

// TestProcessFrame_ReturnsNil checks that ProcessFrame always returns nil cuts.
func TestProcessFrame_ReturnsNil(t *testing.T) {
	d, _ := NewLookaheadDetector(5, LookaheadAnalyzer{})
	frame := solidBGR(64, 64, 128, 128, 128)
	for i := 0; i < 10; i++ {
		cuts, err := d.ProcessFrame(makeTc(int64(i)), frame)
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if len(cuts) != 0 {
			t.Errorf("frame %d: expected no cuts from ProcessFrame, got %d", i, len(cuts))
		}
	}
}

// TestPostProcess_EmptyVideo checks that PostProcess on zero frames returns no cuts.
func TestPostProcess_EmptyVideo(t *testing.T) {
	d, _ := NewLookaheadDetector(5, LookaheadAnalyzer{})
	cuts, err := d.PostProcess(makeTc(0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cuts) != 0 {
		t.Errorf("expected 0 cuts, got %d", len(cuts))
	}
}

// TestPostProcess_SameScene feeds 30 identical frames and expects no cuts.
func TestPostProcess_SameScene(t *testing.T) {
	d, _ := NewLookaheadDetector(10, LookaheadAnalyzer{MinSceneLen: 5})
	frame := solidBGR(64, 64, 100, 100, 100)
	for i := 0; i < 30; i++ {
		if _, err := d.ProcessFrame(makeTc(int64(i)), frame); err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
	}
	cuts, err := d.PostProcess(makeTc(29))
	if err != nil {
		t.Fatalf("PostProcess: %v", err)
	}
	if len(cuts) != 0 {
		t.Errorf("expected 0 cuts for same scene, got %d", len(cuts))
	}
}

// TestPostProcess_HardCut feeds 20 light-gray frames then 20 dark frames.
// A hard cut should be detected near frame 20.
func TestPostProcess_HardCut(t *testing.T) {
	d, _ := NewLookaheadDetector(10, LookaheadAnalyzer{HardCutThreshold: 0.4, MinSceneLen: 5})
	light := solidBGR(64, 64, 200, 200, 200)
	dark := solidBGR(64, 64, 10, 10, 10)

	for i := 0; i < 20; i++ {
		if _, err := d.ProcessFrame(makeTc(int64(i)), light); err != nil {
			t.Fatalf("light frame %d: %v", i, err)
		}
	}
	for i := 20; i < 40; i++ {
		if _, err := d.ProcessFrame(makeTc(int64(i)), dark); err != nil {
			t.Fatalf("dark frame %d: %v", i, err)
		}
	}
	cuts, err := d.PostProcess(makeTc(39))
	if err != nil {
		t.Fatalf("PostProcess: %v", err)
	}
	if len(cuts) == 0 {
		t.Fatal("expected at least one cut for hard cut scenario")
	}
	// The cut should be near frame 20 (within ±5 frames).
	cutFrame := cuts[0].FrameNum()
	if cutFrame < 15 || cutFrame > 25 {
		t.Errorf("expected cut near frame 20, got frame %d", cutFrame)
	}
}

// TestBGRToLuma checks the BT.601 integer approximation.
func TestBGRToLuma(t *testing.T) {
	// Pure gray: R=G=B=128 → Y ≈ 128.
	bgr := []byte{128, 128, 128}
	luma := BGRToLuma(bgr, 1, 1)
	if luma[0] < 126 || luma[0] > 130 {
		t.Errorf("gray luma = %d, want ≈128", luma[0])
	}

	// Pure red: R=255, G=0, B=0 → Y = 0.299*255 ≈ 76.
	bgr = []byte{0, 0, 255}
	luma = BGRToLuma(bgr, 1, 1)
	if luma[0] < 74 || luma[0] > 78 {
		t.Errorf("red luma = %d, want ≈76", luma[0])
	}

	// Pure green: R=0, G=255, B=0 → Y = 0.587*255 ≈ 150.
	bgr = []byte{0, 255, 0}
	luma = BGRToLuma(bgr, 1, 1)
	if luma[0] < 148 || luma[0] > 152 {
		t.Errorf("green luma = %d, want ≈150", luma[0])
	}

	// Pure blue: R=0, G=0, B=255 → Y = 0.114*255 ≈ 29.
	bgr = []byte{255, 0, 0}
	luma = BGRToLuma(bgr, 1, 1)
	if luma[0] < 27 || luma[0] > 31 {
		t.Errorf("blue luma = %d, want ≈29", luma[0])
	}
}

// TestImplementsInterface verifies LookaheadDetector satisfies SceneDetector at compile time.
func TestImplementsInterface(t *testing.T) {
	var _ psd.SceneDetector = (*LookaheadDetector)(nil)
}
