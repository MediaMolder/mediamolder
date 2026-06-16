// SPDX-License-Identifier: BSD-3-Clause
// Copyright (C) 2018-2024 Brandon Castellano <http://www.bcastell.com>.

package detectors

import (
	"testing"

	psd "github.com/MediaMolder/MediaMolder/go_scene_detect"
)

func makeTC(n int64) psd.FrameTimecode {
	tc, _ := psd.NewFrameTimecode(n, 25.0)
	return tc
}

func blackFrame(w, h int) *psd.FrameData {
	return &psd.FrameData{Width: w, Height: h, BGR: make([]byte, w*h*3)}
}

func whiteFrame(w, h int) *psd.FrameData {
	bgr := make([]byte, w*h*3)
	for i := range bgr {
		bgr[i] = 255
	}
	return &psd.FrameData{Width: w, Height: h, BGR: bgr}
}

// TestContentDetector_FirstFrameReturnsNil verifies that the first frame
// produces no cuts and leaves the score at 0.
func TestContentDetector_FirstFrameReturnsNil(t *testing.T) {
	d, err := NewContentDetector(27.0, 0, DefaultContentWeights, 0, psd.FlashFilterModeMerge)
	if err != nil {
		t.Fatalf("NewContentDetector: %v", err)
	}
	cuts, err := d.ProcessFrame(makeTC(0), blackFrame(16, 16))
	if err != nil {
		t.Fatalf("ProcessFrame: %v", err)
	}
	if len(cuts) != 0 {
		t.Fatalf("first frame: expected no cuts, got %v", cuts)
	}
	if d.Score() != 0 {
		t.Errorf("first frame Score() = %f, want 0", d.Score())
	}
}

// TestContentDetector_IdenticalFrames verifies that identical consecutive frames
// produce a score of exactly 0 and no cut.
func TestContentDetector_IdenticalFrames(t *testing.T) {
	d, _ := NewContentDetector(27.0, 0, DefaultContentWeights, 0, psd.FlashFilterModeMerge)
	frame := blackFrame(16, 16)
	_, _ = d.ProcessFrame(makeTC(0), frame)
	cuts, err := d.ProcessFrame(makeTC(1), frame)
	if err != nil {
		t.Fatalf("ProcessFrame: %v", err)
	}
	if len(cuts) != 0 {
		t.Errorf("identical frames: expected no cut, got %v", cuts)
	}
	if d.Score() != 0 {
		t.Errorf("identical frames: Score() = %f, want 0", d.Score())
	}
}

// TestContentDetector_BlackToWhiteCut verifies that a sudden change from a
// fully-black frame to a fully-white frame triggers a cut above threshold=27.
func TestContentDetector_BlackToWhiteCut(t *testing.T) {
	d, _ := NewContentDetector(27.0, 0, DefaultContentWeights, 0, psd.FlashFilterModeMerge)
	_, _ = d.ProcessFrame(makeTC(0), blackFrame(16, 16))
	cuts, err := d.ProcessFrame(makeTC(1), whiteFrame(16, 16))
	if err != nil {
		t.Fatalf("ProcessFrame: %v", err)
	}
	if len(cuts) == 0 {
		t.Errorf("black→white: expected a cut, score was %f (threshold 27.0)", d.Score())
	}
}

// TestContentDetector_HighThresholdNocut verifies that a very high threshold
// suppresses detection of even a maximal change.
func TestContentDetector_HighThresholdNocut(t *testing.T) {
	d, _ := NewContentDetector(200.0, 0, DefaultContentWeights, 0, psd.FlashFilterModeMerge)
	_, _ = d.ProcessFrame(makeTC(0), blackFrame(16, 16))
	cuts, _ := d.ProcessFrame(makeTC(1), whiteFrame(16, 16))
	if len(cuts) != 0 {
		t.Errorf("threshold=200: expected no cut, got %v (score %f)", cuts, d.Score())
	}
}

// TestContentDetector_LumaOnlyWeights verifies that a hue-only change (equal
// brightness) does not trigger a cut under LumaOnlyWeights.
func TestContentDetector_LumaOnlyWeights(t *testing.T) {
	// Red pixel:  BGR = [0, 0, 255] → HSV = [0, 255, 255]
	// Cyan pixel: BGR = [255, 255, 0] → HSV = [90, 255, 255]
	// Both have V=255, so delta_lum ≈ 0; only hue and sat differ.
	w, h := 8, 8
	red := make([]byte, w*h*3)
	cyan := make([]byte, w*h*3)
	for i := 0; i < w*h; i++ {
		red[i*3+2] = 255  // B=0, G=0, R=255
		cyan[i*3+0] = 255 // B=255, G=255, R=0
		cyan[i*3+1] = 255
	}
	redFrame := &psd.FrameData{Width: w, Height: h, BGR: red}
	cyanFrame := &psd.FrameData{Width: w, Height: h, BGR: cyan}

	d, _ := NewContentDetector(27.0, 0, LumaOnlyWeights, 0, psd.FlashFilterModeMerge)
	_, _ = d.ProcessFrame(makeTC(0), redFrame)
	cuts, err := d.ProcessFrame(makeTC(1), cyanFrame)
	if err != nil {
		t.Fatalf("ProcessFrame: %v", err)
	}
	if len(cuts) != 0 {
		t.Errorf("luma-only with equal brightness: expected no cut, got %v (score %f)", cuts, d.Score())
	}
}

// TestContentDetector_ScoreIncreaseWithChange verifies that increasing the
// magnitude of the pixel change increases the score monotonically.
func TestContentDetector_ScoreIncreaseWithChange(t *testing.T) {
	w, h := 8, 8
	base := blackFrame(w, h)

	makeGray := func(v byte) *psd.FrameData {
		bgr := make([]byte, w*h*3)
		for i := range bgr {
			bgr[i] = v
		}
		return &psd.FrameData{Width: w, Height: h, BGR: bgr}
	}

	var prev float64
	for _, v := range []byte{64, 128, 192, 255} {
		d, _ := NewContentDetector(0.0, 0, DefaultContentWeights, 0, psd.FlashFilterModeMerge)
		_, _ = d.ProcessFrame(makeTC(0), base)
		_, _ = d.ProcessFrame(makeTC(1), makeGray(v))
		if d.Score() <= prev {
			t.Errorf("gray=%d: score %f not greater than gray=%d score %f", v, d.Score(), v-64, prev)
		}
		prev = d.Score()
	}
}

// TestContentDetector_GetMetrics verifies the exact metric key list.
func TestContentDetector_GetMetrics(t *testing.T) {
	d, _ := NewContentDetector(27.0, 0, DefaultContentWeights, 0, psd.FlashFilterModeMerge)
	want := []string{"content_val", "delta_hue", "delta_sat", "delta_lum", "delta_edges"}
	got := d.GetMetrics()
	if len(got) != len(want) {
		t.Fatalf("GetMetrics length: got %d, want %d", len(got), len(want))
	}
	for i, k := range want {
		if got[i] != k {
			t.Errorf("GetMetrics[%d]: got %q, want %q", i, got[i], k)
		}
	}
}

// TestContentDetector_ImplementsSceneDetector is a compile-time interface check.
func TestContentDetector_ImplementsSceneDetector(t *testing.T) {
	d, _ := NewContentDetector(27.0, 0, DefaultContentWeights, 0, psd.FlashFilterModeMerge)
	var _ psd.SceneDetector = d
}

// TestContentDetector_StatsManager verifies that SetStats causes per-frame
// metrics to be written to the StatsManager.
func TestContentDetector_StatsManager(t *testing.T) {
	sm := psd.NewStatsManager(25.0)
	d, _ := NewContentDetector(27.0, 0, DefaultContentWeights, 0, psd.FlashFilterModeMerge)
	d.SetStats(sm)

	_, _ = d.ProcessFrame(makeTC(0), blackFrame(8, 8))
	_, _ = d.ProcessFrame(makeTC(1), whiteFrame(8, 8))

	vals := sm.GetMetrics(1, []string{"content_val", "delta_lum"})
	if vals[0] == 0 {
		t.Error("StatsManager: content_val should be non-zero after black→white")
	}
	if vals[1] == 0 {
		t.Error("StatsManager: delta_lum should be non-zero after black→white")
	}
}

// TestContentDetector_PostProcessReturnsNil verifies PostProcess returns nil.
func TestContentDetector_PostProcessReturnsNil(t *testing.T) {
	d, _ := NewContentDetector(27.0, 0, DefaultContentWeights, 0, psd.FlashFilterModeMerge)
	cuts, err := d.PostProcess(makeTC(100))
	if err != nil {
		t.Fatalf("PostProcess: %v", err)
	}
	if len(cuts) != 0 {
		t.Errorf("PostProcess: expected nil, got %v", cuts)
	}
}
