// SPDX-License-Identifier: BSD-3-Clause
// Copyright (C) 2018-2024 Brandon Castellano <http://www.bcastell.com>.

package detectors

import (
	"testing"

	psd "github.com/MediaMolder/MediaMolder/PySceneDetect"
)

// TestThresholdDetector_ImplementsSceneDetector is a compile-time interface check.
func TestThresholdDetector_ImplementsSceneDetector(t *testing.T) {
	var _ psd.SceneDetector = (*ThresholdDetector)(nil)
}

// TestThresholdDetector_InvalidFadeBias verifies that fade_bias outside [-1,1] returns an error.
func TestThresholdDetector_InvalidFadeBias(t *testing.T) {
	for _, bias := range []float64{-1.1, 1.1, 2.0, -99.0} {
		_, err := NewThresholdDetector(12.0, 0, bias, false, ThresholdMethodFloor)
		if err == nil {
			t.Errorf("fade_bias=%g: expected error, got nil", bias)
		}
	}
}

// TestThresholdDetector_ValidFadeBias verifies that boundary values of fade_bias are accepted.
func TestThresholdDetector_ValidFadeBias(t *testing.T) {
	for _, bias := range []float64{-1.0, 0.0, 1.0} {
		_, err := NewThresholdDetector(12.0, 0, bias, false, ThresholdMethodFloor)
		if err != nil {
			t.Errorf("fade_bias=%g: unexpected error: %v", bias, err)
		}
	}
}

// TestThresholdDetector_FirstFrameNoOutput verifies that the first frame produces no cuts.
func TestThresholdDetector_FirstFrameNoOutput(t *testing.T) {
	d, err := NewThresholdDetector(12.0, 0, 0.0, false, ThresholdMethodFloor)
	if err != nil {
		t.Fatalf("NewThresholdDetector: %v", err)
	}
	cuts, err := d.ProcessFrame(makeTC(0), whiteFrame(16, 16))
	if err != nil {
		t.Fatalf("ProcessFrame: %v", err)
	}
	if len(cuts) != 0 {
		t.Fatalf("first frame: expected no cuts, got %v", cuts)
	}
}

// TestThresholdDetector_AllBrightNoOutput verifies that frames that never cross the threshold
// produce no cuts.
func TestThresholdDetector_AllBrightNoOutput(t *testing.T) {
	d, _ := NewThresholdDetector(12.0, 0, 0.0, false, ThresholdMethodFloor)
	for i := int64(0); i < 10; i++ {
		cuts, err := d.ProcessFrame(makeTC(i), whiteFrame(16, 16))
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if len(cuts) != 0 {
			t.Errorf("all-bright: expected no cut at frame %d, got %v", i, cuts)
		}
	}
}

// TestThresholdDetector_FadeOutIn_CutMidpoint verifies a fade-out followed by a fade-in
// emits a cut at the midpoint (fadeBias=0).
//
// Sequence: white(0), black(1,2,3), white(4).
// Fade-out begins at frame 1, fade-in at frame 4. Duration = 4-1 = 3 frames.
// Split = 1 + round(3 * 0.5) = 1 + 2 = 3.
func TestThresholdDetector_FadeOutIn_CutMidpoint(t *testing.T) {
	d, _ := NewThresholdDetector(12.0, 0, 0.0, false, ThresholdMethodFloor)
	sequence := []*psd.FrameData{
		whiteFrame(16, 16), // 0
		blackFrame(16, 16), // 1 ← fade-out
		blackFrame(16, 16), // 2
		blackFrame(16, 16), // 3
		whiteFrame(16, 16), // 4 ← fade-in
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
		t.Errorf("expected cut at frame 3 (midpoint), got %d", allCuts[0].FrameNum())
	}
}

// TestThresholdDetector_FadeBiasNeg1 verifies that fadeBias=-1 places the cut at the
// first dark frame (fade-out start).
//
// Same sequence as above; split = 1 + round(3 * 0) = 1.
func TestThresholdDetector_FadeBiasNeg1(t *testing.T) {
	d, _ := NewThresholdDetector(12.0, 0, -1.0, false, ThresholdMethodFloor)
	sequence := []*psd.FrameData{
		whiteFrame(16, 16), // 0
		blackFrame(16, 16), // 1
		blackFrame(16, 16), // 2
		blackFrame(16, 16), // 3
		whiteFrame(16, 16), // 4
	}
	var allCuts []psd.FrameTimecode
	for i, f := range sequence {
		cuts, _ := d.ProcessFrame(makeTC(int64(i)), f)
		allCuts = append(allCuts, cuts...)
	}
	if len(allCuts) != 1 {
		t.Fatalf("expected 1 cut, got %d", len(allCuts))
	}
	if allCuts[0].FrameNum() != 1 {
		t.Errorf("fade_bias=-1: expected cut at frame 1, got %d", allCuts[0].FrameNum())
	}
}

// TestThresholdDetector_FadeBiasPlus1 verifies that fadeBias=+1 places the cut at the
// first bright frame (fade-in start).
//
// Same sequence; split = 1 + round(3 * 1.0) = 4.
func TestThresholdDetector_FadeBiasPlus1(t *testing.T) {
	d, _ := NewThresholdDetector(12.0, 0, 1.0, false, ThresholdMethodFloor)
	sequence := []*psd.FrameData{
		whiteFrame(16, 16), // 0
		blackFrame(16, 16), // 1
		blackFrame(16, 16), // 2
		blackFrame(16, 16), // 3
		whiteFrame(16, 16), // 4
	}
	var allCuts []psd.FrameTimecode
	for i, f := range sequence {
		cuts, _ := d.ProcessFrame(makeTC(int64(i)), f)
		allCuts = append(allCuts, cuts...)
	}
	if len(allCuts) != 1 {
		t.Fatalf("expected 1 cut, got %d", len(allCuts))
	}
	if allCuts[0].FrameNum() != 4 {
		t.Errorf("fade_bias=+1: expected cut at frame 4, got %d", allCuts[0].FrameNum())
	}
}

// TestThresholdDetector_MinSceneLenSuppresses verifies that a cut is not emitted when
// the elapsed frames since the last cut are fewer than min_scene_len.
//
// Sequence: white(0)→black(1-3)→white(4). elapsed=4, minSceneLen=10 → suppressed.
func TestThresholdDetector_MinSceneLenSuppresses(t *testing.T) {
	d, _ := NewThresholdDetector(12.0, 10, 0.0, false, ThresholdMethodFloor)
	sequence := []*psd.FrameData{
		whiteFrame(16, 16),
		blackFrame(16, 16),
		blackFrame(16, 16),
		blackFrame(16, 16),
		whiteFrame(16, 16),
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

// TestThresholdDetector_StateAdvancesEvenIfSuppressed verifies that the internal fade
// state transitions even when min_scene_len suppresses the cut, so the next fade cycle
// can be evaluated independently.
func TestThresholdDetector_StateAdvancesEvenIfSuppressed(t *testing.T) {
	// min_scene_len = 3. Sequence has a fade at frame 1→3 (elapsed=2 < 3, suppressed)
	// and another at frame 5→7 (elapsed since cut at frame 0 = 6 >= 3, emitted).
	d, _ := NewThresholdDetector(12.0, 3, 0.0, false, ThresholdMethodFloor)
	sequence := []*psd.FrameData{
		whiteFrame(16, 16), // 0: first frame, "in"
		blackFrame(16, 16), // 1: fade-out, lastFadeFrame=1
		whiteFrame(16, 16), // 2: elapsed=2 < 3, suppressed; fade state → "in"
		blackFrame(16, 16), // 3: fade-out again, lastFadeFrame=3
		blackFrame(16, 16), // 4: still out
		whiteFrame(16, 16), // 5: fade-in; elapsed = 5-0 = 5 >= 3, cut emitted
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
		t.Fatalf("expected 1 cut from second fade, got %d: %v", len(allCuts), allCuts)
	}
	// fade-out at 3, fade-in at 5; duration=5-3=2; split=3+round(2*0.5)=3+1=4
	if allCuts[0].FrameNum() != 4 {
		t.Errorf("expected cut at frame 4, got %d", allCuts[0].FrameNum())
	}
}

// TestThresholdDetector_CeilingMode verifies that CEILING mode detects a bright
// flash period between two dark frames.
//
// Sequence: dark(0), bright(1), dark(2). CEILING threshold=200.
// Init: dark(0) → 0 < 200 = true → "out" (Python FLOOR-style init, faithfully ported).
// Frame 1 (bright): "out" and !isFadeOut(255) where isFadeOut=255>=200=true → not taken.
// Frame 2 (dark): "out" and !isFadeOut(0) where isFadeOut=0>=200=false → TAKEN.
//   fOut=frame0, duration=2-0=2, split=0+round(2*0.5)=0+1=1. Cut at frame 1.
func TestThresholdDetector_CeilingMode(t *testing.T) {
	d, _ := NewThresholdDetector(200.0, 0, 0.0, false, ThresholdMethodCeiling)
	sequence := []*psd.FrameData{
		blackFrame(16, 16), // 0
		whiteFrame(16, 16), // 1 ← bright flash
		blackFrame(16, 16), // 2
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
		t.Fatalf("CEILING: expected 1 cut, got %d: %v", len(allCuts), allCuts)
	}
	if allCuts[0].FrameNum() != 1 {
		t.Errorf("CEILING: expected cut at frame 1, got %d", allCuts[0].FrameNum())
	}
}

// TestThresholdDetector_PostProcess_AddFinalScene verifies that PostProcess emits a cut
// when the video ends on a fade-out and addFinalScene=true.
//
// Sequence: white(0), black(1), black(2). After processing, lastFadeType="out",
// lastFadeFrame=1. PostProcess(makeTC(3)) → elapsed=3-0=3 >= minFrames=0 → cut at frame 1.
func TestThresholdDetector_PostProcess_AddFinalScene(t *testing.T) {
	d, _ := NewThresholdDetector(12.0, 0, 0.0, true, ThresholdMethodFloor)
	sequence := []*psd.FrameData{
		whiteFrame(16, 16), // 0
		blackFrame(16, 16), // 1 ← fade-out
		blackFrame(16, 16), // 2
	}
	for i, f := range sequence {
		_, err := d.ProcessFrame(makeTC(int64(i)), f)
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
	}
	cuts, err := d.PostProcess(makeTC(3))
	if err != nil {
		t.Fatalf("PostProcess: %v", err)
	}
	if len(cuts) != 1 {
		t.Fatalf("add_final_scene: expected 1 cut from PostProcess, got %d: %v", len(cuts), cuts)
	}
	if cuts[0].FrameNum() != 1 {
		t.Errorf("add_final_scene: expected cut at frame 1, got %d", cuts[0].FrameNum())
	}
}

// TestThresholdDetector_PostProcess_NoAddFinalScene verifies that PostProcess emits no
// cut when addFinalScene=false (the default).
func TestThresholdDetector_PostProcess_NoAddFinalScene(t *testing.T) {
	d, _ := NewThresholdDetector(12.0, 0, 0.0, false, ThresholdMethodFloor)
	sequence := []*psd.FrameData{
		whiteFrame(16, 16),
		blackFrame(16, 16),
	}
	for i, f := range sequence {
		d.ProcessFrame(makeTC(int64(i)), f) //nolint:errcheck
	}
	cuts, err := d.PostProcess(makeTC(2))
	if err != nil {
		t.Fatalf("PostProcess: %v", err)
	}
	if len(cuts) != 0 {
		t.Errorf("add_final_scene=false: expected no cuts from PostProcess, got %v", cuts)
	}
}

// TestThresholdDetector_PostProcess_NoFrames verifies that PostProcess is safe to call
// without any prior ProcessFrame calls.
func TestThresholdDetector_PostProcess_NoFrames(t *testing.T) {
	d, _ := NewThresholdDetector(12.0, 0, 0.0, true, ThresholdMethodFloor)
	cuts, err := d.PostProcess(makeTC(0))
	if err != nil {
		t.Fatalf("PostProcess with no frames: %v", err)
	}
	if len(cuts) != 0 {
		t.Errorf("no frames: expected no cuts from PostProcess, got %v", cuts)
	}
}

// TestThresholdDetector_GetMetrics verifies that the "average_rgb" metric key is returned.
func TestThresholdDetector_GetMetrics(t *testing.T) {
	d, _ := NewThresholdDetector(12.0, 0, 0.0, false, ThresholdMethodFloor)
	metrics := d.GetMetrics()
	if len(metrics) != 1 || metrics[0] != "average_rgb" {
		t.Errorf("GetMetrics() = %v, want [average_rgb]", metrics)
	}
}

// TestThresholdDetector_EventBufferLength verifies that the event buffer is 0 (no lookahead).
func TestThresholdDetector_EventBufferLength(t *testing.T) {
	d, _ := NewThresholdDetector(12.0, 0, 0.0, false, ThresholdMethodFloor)
	if got := d.EventBufferLength(); got != 0 {
		t.Errorf("EventBufferLength() = %d, want 0", got)
	}
}
