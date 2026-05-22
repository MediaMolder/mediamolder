// SPDX-License-Identifier: BSD-3-Clause
// Copyright (C) 2018-2024 Brandon Castellano <http://www.bcastell.com>.

package detectors

import (
	"testing"

	psd "github.com/MediaMolder/MediaMolder/go_scene_detect"
)

// TestAdaptiveDetector_ImplementsSceneDetector is a compile-time interface check.
func TestAdaptiveDetector_ImplementsSceneDetector(t *testing.T) {
	var _ psd.SceneDetector = (*AdaptiveDetector)(nil)
}

// TestAdaptiveDetector_InvalidWindowWidth verifies that windowWidth < 1 returns an error.
func TestAdaptiveDetector_InvalidWindowWidth(t *testing.T) {
	_, err := NewAdaptiveDetector(3.0, 0, 0, 15.0, DefaultContentWeights, false, 0)
	if err == nil {
		t.Fatal("expected error for window_width=0, got nil")
	}
}

// TestAdaptiveDetector_FirstFrameNoOutput verifies that the first frame (no previous
// state) produces no cuts and no error.
func TestAdaptiveDetector_FirstFrameNoOutput(t *testing.T) {
	d, err := NewAdaptiveDetector(3.0, 0, 2, 15.0, DefaultContentWeights, false, 0)
	if err != nil {
		t.Fatalf("NewAdaptiveDetector: %v", err)
	}
	cuts, err := d.ProcessFrame(makeTC(0), blackFrame(16, 16))
	if err != nil {
		t.Fatalf("ProcessFrame: %v", err)
	}
	if len(cuts) != 0 {
		t.Fatalf("first frame: expected no cuts, got %v", cuts)
	}
}

// TestAdaptiveDetector_BufferFillNoOutput verifies that no cut is emitted until the
// rolling window buffer holds at least 1 + 2*windowWidth scored frames.
func TestAdaptiveDetector_BufferFillNoOutput(t *testing.T) {
	const windowWidth = 2
	d, _ := NewAdaptiveDetector(3.0, 0, windowWidth, 15.0, DefaultContentWeights, false, 0)
	frame := blackFrame(16, 16)
	// Feed N frames: the first produces no score, so we need 1+2*windowWidth+1 frames
	// to fill the buffer (i.e. get 1+2*windowWidth scored frames). Feed one fewer.
	limit := int64(1 + 2*windowWidth) // = 5 frames processed → 4 scored entries
	for i := int64(0); i < limit; i++ {
		cuts, err := d.ProcessFrame(makeTC(i), frame)
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if len(cuts) != 0 {
			t.Errorf("frame %d: expected no cuts during buffer fill, got %v", i, cuts)
		}
	}
}

// TestAdaptiveDetector_CutOnSpike verifies that a single spike frame (sudden bright
// change) surrounded by identical neighbours triggers a cut at the spike timecode.
// Sequence: 0-2 black, 3 white, 4-5 white.
// Score at frame 3 is high (black→white); scores at frames 1,2,4,5 are ~0.
// Window centre = frame 3; avgWindowScore ≈ 0; adaptiveRatio = 255 → cut fires.
func TestAdaptiveDetector_CutOnSpike(t *testing.T) {
	d, _ := NewAdaptiveDetector(3.0, 0, 2, 15.0, DefaultContentWeights, false, 0)

	sequence := []*psd.FrameData{
		blackFrame(16, 16), // 0
		blackFrame(16, 16), // 1
		blackFrame(16, 16), // 2
		whiteFrame(16, 16), // 3 ← spike
		whiteFrame(16, 16), // 4
		whiteFrame(16, 16), // 5
	}

	var allCuts []psd.FrameTimecode
	for i, f := range sequence {
		cuts, err := d.ProcessFrame(makeTC(int64(i)), f)
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		allCuts = append(allCuts, cuts...)
	}

	if len(allCuts) != 1 {
		t.Fatalf("expected 1 cut, got %d: %v", len(allCuts), allCuts)
	}
	if allCuts[0].FrameNum() != 3 {
		t.Errorf("expected cut at frame 3, got %d", allCuts[0].FrameNum())
	}
}

// TestAdaptiveDetector_NoCutUniformChange verifies that a stream of identical frames
// (score 0 throughout) never triggers a cut.
func TestAdaptiveDetector_NoCutUniformChange(t *testing.T) {
	d, _ := NewAdaptiveDetector(3.0, 0, 2, 15.0, DefaultContentWeights, false, 0)
	frame := blackFrame(16, 16)
	for i := int64(0); i < 20; i++ {
		cuts, err := d.ProcessFrame(makeTC(i), frame)
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if len(cuts) != 0 {
			t.Errorf("uniform stream: expected no cut at frame %d, got %v", i, cuts)
		}
	}
}

// TestAdaptiveDetector_MinContentValPreventsCut verifies that a very high minContentVal
// suppresses cuts even when the adaptive ratio would otherwise be 255.
func TestAdaptiveDetector_MinContentValPreventsCut(t *testing.T) {
	// minContentVal=200 is higher than any realistic score from a 16×16 frame.
	d, _ := NewAdaptiveDetector(3.0, 0, 2, 200.0, DefaultContentWeights, false, 0)
	sequence := []*psd.FrameData{
		blackFrame(16, 16),
		blackFrame(16, 16),
		blackFrame(16, 16),
		whiteFrame(16, 16), // spike, but score < 200 for a 16×16 frame
		whiteFrame(16, 16),
		whiteFrame(16, 16),
	}
	for i, f := range sequence {
		cuts, err := d.ProcessFrame(makeTC(int64(i)), f)
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if len(cuts) != 0 {
			t.Errorf("high minContentVal: expected no cut at frame %d, got %v", i, cuts)
		}
	}
}

// TestAdaptiveDetector_MinSceneLenFrames verifies that a cut is suppressed when the
// elapsed frames since the last cut are fewer than minSceneLen.
// Spike at frame 3; lastCut initialised to frame 1 (first scored frame).
// elapsed at evaluation time (frame 5) = 5 - 1 = 4 < minSceneLen=10 → no cut.
func TestAdaptiveDetector_MinSceneLenFrames(t *testing.T) {
	d, _ := NewAdaptiveDetector(3.0, 10, 2, 15.0, DefaultContentWeights, false, 0)
	sequence := []*psd.FrameData{
		blackFrame(16, 16), // 0
		blackFrame(16, 16), // 1
		blackFrame(16, 16), // 2
		whiteFrame(16, 16), // 3
		whiteFrame(16, 16), // 4
		whiteFrame(16, 16), // 5
	}
	for i, f := range sequence {
		cuts, err := d.ProcessFrame(makeTC(int64(i)), f)
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if len(cuts) != 0 {
			t.Errorf("min_scene_len=10: expected no cut at frame %d, got %v", i, cuts)
		}
	}
}

// TestAdaptiveDetector_EventBufferLength verifies that EventBufferLength returns windowWidth.
func TestAdaptiveDetector_EventBufferLength(t *testing.T) {
	d, _ := NewAdaptiveDetector(3.0, 0, 3, 15.0, DefaultContentWeights, false, 0)
	if got := d.EventBufferLength(); got != 3 {
		t.Errorf("EventBufferLength() = %d, want 3", got)
	}
}

// TestAdaptiveDetector_GetMetrics verifies that metrics include both ContentDetector
// base metrics and the adaptive_ratio key.
func TestAdaptiveDetector_GetMetrics(t *testing.T) {
	d, _ := NewAdaptiveDetector(3.0, 0, 2, 15.0, DefaultContentWeights, false, 0)
	metrics := d.GetMetrics()

	var foundAdaptive bool
	for _, m := range metrics {
		if m == "adaptive_ratio (w=2)" {
			foundAdaptive = true
		}
	}
	if !foundAdaptive {
		t.Errorf("GetMetrics() = %v: missing 'adaptive_ratio (w=2)'", metrics)
	}

	base, _ := NewContentDetector(27.0, 0, DefaultContentWeights, 0, psd.FlashFilterModeMerge)
	if len(metrics) <= len(base.GetMetrics()) {
		t.Errorf("AdaptiveDetector.GetMetrics() len=%d, want > ContentDetector len=%d",
			len(metrics), len(base.GetMetrics()))
	}
}

// TestAdaptiveDetector_LumaOnlySuffix verifies that lumaOnly=true adds "_lum" to the
// adaptive_ratio stats key.
func TestAdaptiveDetector_LumaOnlySuffix(t *testing.T) {
	d, _ := NewAdaptiveDetector(3.0, 0, 2, 15.0, LumaOnlyWeights, true, 0)
	metrics := d.GetMetrics()
	var found bool
	for _, m := range metrics {
		if m == "adaptive_ratio_lum (w=2)" {
			found = true
		}
	}
	if !found {
		t.Errorf("GetMetrics() = %v: missing 'adaptive_ratio_lum (w=2)'", metrics)
	}
}
