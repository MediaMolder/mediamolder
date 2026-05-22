//
//            PySceneDetect: Python-Based Video Scene Detector
//  -------------------------------------------------------------------
//     [  Github: https://github.com/Breakthrough/PySceneDetect/    ]
//
// Copyright (C) 2025 Brandon Castellano <http://www.bcastell.com>.
// SPDX-License-Identifier: BSD-3-Clause

package goscenedetect

import (
	"testing"
)

// makeTC is a helper that creates a FrameTimecode at fps=25, panicking on error.
func makeTC(frame int64) FrameTimecode {
	ft, err := NewFrameTimecode(frame, 25.0)
	if err != nil {
		panic(err)
	}
	return ft
}

// --- NewFlashFilter construction ---

func TestNewFlashFilter_IntFrames(t *testing.T) {
	f, err := NewFlashFilter(FlashFilterModeMerge, 15)
	if err != nil {
		t.Fatal(err)
	}
	if f.minFrames != 15 {
		t.Errorf("minFrames: got %d, want 15", f.minFrames)
	}
	if f.hasMinSecs {
		t.Error("hasMinSecs should be false for int input")
	}
}

func TestNewFlashFilter_Float64Secs(t *testing.T) {
	f, err := NewFlashFilter(FlashFilterModeSuppress, 0.6)
	if err != nil {
		t.Fatal(err)
	}
	if !f.hasMinSecs {
		t.Error("hasMinSecs should be true for float64 input")
	}
	if f.minSecs != 0.6 {
		t.Errorf("minSecs: got %g, want 0.6", f.minSecs)
	}
}

func TestNewFlashFilter_StringDigits(t *testing.T) {
	f, err := NewFlashFilter(FlashFilterModeMerge, "15")
	if err != nil {
		t.Fatal(err)
	}
	if f.minFrames != 15 {
		t.Errorf("minFrames: got %d, want 15", f.minFrames)
	}
}

func TestNewFlashFilter_StringTimecode(t *testing.T) {
	// "1.0s" at fps=100 placeholder = 1 second
	f, err := NewFlashFilter(FlashFilterModeMerge, "1.0s")
	if err != nil {
		t.Fatal(err)
	}
	if !f.hasMinSecs {
		t.Error("hasMinSecs should be true for timecode string")
	}
	if f.minSecs != 1.0 {
		t.Errorf("minSecs: got %g, want 1.0", f.minSecs)
	}
}

func TestNewFlashFilter_FrameTimecode(t *testing.T) {
	ft := makeTC(25) // 25 frames at 25fps = 1.0 second
	f, err := NewFlashFilter(FlashFilterModeMerge, ft)
	if err != nil {
		t.Fatal(err)
	}
	if !f.hasMinSecs {
		t.Error("hasMinSecs should be true for FrameTimecode input")
	}
	if f.minSecs != 1.0 {
		t.Errorf("minSecs: got %g, want 1.0", f.minSecs)
	}
}

func TestNewFlashFilter_UnsupportedType(t *testing.T) {
	_, err := NewFlashFilter(FlashFilterModeMerge, []byte("bad"))
	if err == nil {
		t.Error("expected error for unsupported type")
	}
}

// --- isDisabled ---

func TestFlashFilter_Disabled_ZeroFrames(t *testing.T) {
	f, _ := NewFlashFilter(FlashFilterModeMerge, 0)
	if !f.isDisabled() {
		t.Error("filter with 0 frames should be disabled")
	}
}

func TestFlashFilter_Disabled_ZeroSecs(t *testing.T) {
	f, _ := NewFlashFilter(FlashFilterModeMerge, 0.0)
	if !f.isDisabled() {
		t.Error("filter with 0.0 seconds should be disabled")
	}
}

func TestFlashFilter_Enabled(t *testing.T) {
	f, _ := NewFlashFilter(FlashFilterModeMerge, 15)
	if f.isDisabled() {
		t.Error("filter with 15 frames should not be disabled")
	}
}

// --- MaxBehind ---

func TestFlashFilter_MaxBehind_Suppress(t *testing.T) {
	f, _ := NewFlashFilter(FlashFilterModeSuppress, 30)
	if f.MaxBehind() != 0 {
		t.Errorf("MaxBehind SUPPRESS: got %d, want 0", f.MaxBehind())
	}
}

func TestFlashFilter_MaxBehind_MergeFrames(t *testing.T) {
	f, _ := NewFlashFilter(FlashFilterModeMerge, 15)
	if f.MaxBehind() != 15 {
		t.Errorf("MaxBehind MERGE frames: got %d, want 15", f.MaxBehind())
	}
}

// --- Filter SUPPRESS mode ---

// TestFlashFilterSuppress_Basic: cuts within min_scene_len are suppressed;
// cuts that meet the threshold are emitted.
func TestFlashFilterSuppress_Basic(t *testing.T) {
	const minLen = 10
	f, _ := NewFlashFilter(FlashFilterModeSuppress, minLen)

	// Frame 0: above threshold, but only 0 frames since "last above" → suppressed.
	got := f.Filter(makeTC(0), true)
	if len(got) != 0 {
		t.Errorf("frame 0: expected no cut (0 frames since start), got %v", got)
	}

	// Frames 1–9: above threshold, not enough elapsed → all suppressed.
	for i := int64(1); i < minLen; i++ {
		got = f.Filter(makeTC(i), true)
		if len(got) != 0 {
			t.Errorf("frame %d: expected suppressed, got %v", i, got)
		}
	}

	// Frame 10: above threshold, exactly minLen frames since lastAbove=0 → emitted.
	got = f.Filter(makeTC(int64(minLen)), true)
	if len(got) != 1 || got[0].FrameNum() != int64(minLen) {
		t.Errorf("frame %d: expected cut at %d, got %v", minLen, minLen, got)
	}
}

func TestFlashFilterSuppress_BelowThreshold_NeverEmits(t *testing.T) {
	f, _ := NewFlashFilter(FlashFilterModeSuppress, 5)
	for i := int64(0); i < 100; i++ {
		got := f.Filter(makeTC(i), false)
		if len(got) != 0 {
			t.Errorf("below-threshold frame %d should never emit; got %v", i, got)
		}
	}
}

// --- Filter MERGE mode ---

// TestFlashFilterMerge_NormalCut: a cut at exactly minLen is emitted immediately.
func TestFlashFilterMerge_NormalCut(t *testing.T) {
	const minLen = int64(5)
	f, _ := NewFlashFilter(FlashFilterModeMerge, minLen)

	// Feed frames 0..minLen-1 below threshold — no cuts.
	for i := int64(0); i < minLen; i++ {
		f.Filter(makeTC(i), false)
	}

	// Frame minLen: above threshold, elapsed = minLen since lastAbove=0 → cut emitted.
	got := f.Filter(makeTC(minLen), true)
	if len(got) != 1 || got[0].FrameNum() != minLen {
		t.Errorf("expected cut at frame %d, got %v", minLen, got)
	}
}

// TestFlashFilterMerge_RapidCut: a burst of above-threshold frames immediately
// after a normal cut triggers merge state; the deferred cut is released once
// both the merge region and the trailing below-threshold region are long enough.
//
// Timeline (minLen=5):
//
//	frames 0-4  : below threshold
//	frame  5    : above → first CUT emitted (elapsed=5 >= 5)
//	frame  6    : above → rapid cut, merge triggered (elapsed=1 < 5)
//	frames 7-11 : above → lastAbove advances to TC(11); mergeElapsed = 11-6 = 5
//	frames 12-15: below threshold → not enough elapsed to release (elapsed < 5)
//	frame  16   : below threshold → elapsed=5, mergeElapsed=5 → RELEASE at TC(11)
func TestFlashFilterMerge_RapidCut(t *testing.T) {
	const minLen = int64(5)
	f, _ := NewFlashFilter(FlashFilterModeMerge, minLen)

	// Frames 0-4: below threshold.
	for i := int64(0); i < minLen; i++ {
		f.Filter(makeTC(i), false)
	}

	// Frame 5: above threshold, elapsed = 5 >= minLen → first cut emitted.
	got := f.Filter(makeTC(minLen), true)
	if len(got) != 1 {
		t.Fatalf("expected first cut at frame %d, got %v", minLen, got)
	}

	// Frame 6: above threshold, elapsed=1 < minLen → rapid cut, merge triggered.
	got = f.Filter(makeTC(minLen+1), true)
	if len(got) != 0 || !f.mergeTriggered {
		t.Fatalf("expected merge triggered, got cuts=%v triggered=%v", got, f.mergeTriggered)
	}

	// Frames 7-11: above threshold (mergeElapsed grows to 5; lastAbove → TC(11)).
	for i := int64(2); i <= minLen+1; i++ {
		f.Filter(makeTC(minLen+i), true)
	}
	wantLastAbove := minLen + (minLen + 1) // = 5 + 6 = 11

	// Frames 12-15: below threshold, elapsed not yet = minLen → no release.
	for i := int64(1); i < minLen; i++ {
		got = f.Filter(makeTC(wantLastAbove+i), false)
		if len(got) != 0 {
			t.Errorf("frame %d: merge still active, expected no cut, got %v",
				wantLastAbove+i, got)
		}
	}

	// Frame 16: below threshold, elapsed=5 >= minLen, mergeElapsed=5 >= minLen → RELEASE.
	releaseFrame := makeTC(wantLastAbove + minLen)
	got = f.Filter(releaseFrame, false)
	if len(got) != 1 {
		t.Fatalf("expected merged cut to be released, got %v (lastAbove=%d)",
			got, f.lastAbove.FrameNum())
	}
	if got[0].FrameNum() != wantLastAbove {
		t.Errorf("merged cut at wrong frame: got %d, want %d",
			got[0].FrameNum(), wantLastAbove)
	}
}

// TestFlashFilterMerge_Disabled: when length=0, every above-threshold frame emits.
func TestFlashFilterMerge_Disabled(t *testing.T) {
	f, _ := NewFlashFilter(FlashFilterModeMerge, 0)
	got := f.Filter(makeTC(5), true)
	if len(got) != 1 || got[0].FrameNum() != 5 {
		t.Errorf("disabled merge: expected cut at 5, got %v", got)
	}
	got = f.Filter(makeTC(5), false)
	if len(got) != 0 {
		t.Errorf("disabled merge: below threshold should return nothing, got %v", got)
	}
}

// TestFlashFilterSuppress_SecondsResolution: ensure seconds-based minLen
// resolves correctly on first frame using the timecode's fps.
func TestFlashFilterSuppress_SecondsResolution(t *testing.T) {
	// 0.4 seconds at 25fps = 10 frames
	f, _ := NewFlashFilter(FlashFilterModeSuppress, 0.4)

	// Frame 0: elapsed = 0, not enough → suppressed.
	f.Filter(makeTC(0), true)

	// Frame 9: elapsed = 9, still < 10 → suppressed.
	got := f.Filter(makeTC(9), true)
	if len(got) != 0 {
		t.Errorf("frame 9: expected suppressed (need 10 frames), got %v", got)
	}

	// Frame 10: elapsed = 10 >= 10 → emitted.
	got = f.Filter(makeTC(10), true)
	if len(got) != 1 {
		t.Errorf("frame 10: expected cut (elapsed meets 0.4s@25fps), got %v", got)
	}
}
