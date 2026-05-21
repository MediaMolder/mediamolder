// SPDX-License-Identifier: BSD-3-Clause
// Copyright (C) 2018-2024 Brandon Castellano <http://www.bcastell.com>.

package detectors

import (
	"testing"

	psd "github.com/MediaMolder/MediaMolder/PySceneDetect"
)

// compile-time interface check
var _ psd.SceneDetector = (*HistogramDetector)(nil)

func TestHistogramDetector_ImplementsSceneDetector(t *testing.T) {
	var _ psd.SceneDetector = (*HistogramDetector)(nil)
}

func TestHistogramDetector_InvalidBins(t *testing.T) {
	_, err := NewHistogramDetector(0.05, 0, 15)
	if err == nil {
		t.Fatal("expected error for bins=0, got nil")
	}
}

func TestHistogramDetector_ThresholdInversion(t *testing.T) {
	d, err := NewHistogramDetector(0.05, 256, 15)
	if err != nil {
		t.Fatalf("NewHistogramDetector: %v", err)
	}
	want := 0.95
	if d.threshold != want {
		t.Errorf("internal threshold = %f, want %f", d.threshold, want)
	}
}

func TestHistogramDetector_ThresholdClampedToZero(t *testing.T) {
	d, err := NewHistogramDetector(1.5, 256, 15)
	if err != nil {
		t.Fatalf("NewHistogramDetector: %v", err)
	}
	if d.threshold < 0 {
		t.Errorf("threshold clamped below 0: %f", d.threshold)
	}
}

func TestHistogramDetector_ThresholdClampedToOne(t *testing.T) {
	d, err := NewHistogramDetector(-0.5, 256, 15)
	if err != nil {
		t.Fatalf("NewHistogramDetector: %v", err)
	}
	if d.threshold > 1.0 {
		t.Errorf("threshold clamped above 1: %f", d.threshold)
	}
}

func TestHistogramDetector_FirstFrameNoOutput(t *testing.T) {
	d, err := NewHistogramDetector(0.05, 256, 15)
	if err != nil {
		t.Fatalf("NewHistogramDetector: %v", err)
	}
	cuts, err := d.ProcessFrame(makeTC(0), blackFrame(64, 64))
	if err != nil {
		t.Fatalf("ProcessFrame: %v", err)
	}
	if len(cuts) != 0 {
		t.Fatalf("first frame: expected no cuts, got %v", cuts)
	}
	if d.LastHistDiff() != 0 {
		t.Errorf("first frame LastHistDiff() = %f, want 0", d.LastHistDiff())
	}
}

func TestHistogramDetector_IdenticalFrames_NoCut(t *testing.T) {
	d, err := NewHistogramDetector(0.05, 256, 0)
	if err != nil {
		t.Fatalf("NewHistogramDetector: %v", err)
	}
	if _, err = d.ProcessFrame(makeTC(0), whiteFrame(64, 64)); err != nil {
		t.Fatal(err)
	}
	cuts, err := d.ProcessFrame(makeTC(1), whiteFrame(64, 64))
	if err != nil {
		t.Fatal(err)
	}
	// Identical histograms → correlation=1.0 > internal threshold 0.95 → no cut.
	if len(cuts) != 0 {
		t.Errorf("identical frames: expected no cut, got %v", cuts)
	}
	if d.LastHistDiff() < 0.999 {
		t.Errorf("identical frames: expected correlation ≈ 1, got %f", d.LastHistDiff())
	}
}

func TestHistogramDetector_DifferentFrames_Cut(t *testing.T) {
	// Black→white produces near-zero correlation, well below 0.95.
	d, err := NewHistogramDetector(0.05, 256, 0)
	if err != nil {
		t.Fatalf("NewHistogramDetector: %v", err)
	}
	if _, err = d.ProcessFrame(makeTC(0), blackFrame(64, 64)); err != nil {
		t.Fatal(err)
	}
	cuts, err := d.ProcessFrame(makeTC(1), whiteFrame(64, 64))
	if err != nil {
		t.Fatal(err)
	}
	if len(cuts) == 0 {
		t.Errorf("black→white: expected cut, correlation=%f", d.LastHistDiff())
	}
}

func TestHistogramDetector_MinSceneLenSuppresses(t *testing.T) {
	d, err := NewHistogramDetector(0.05, 256, 5)
	if err != nil {
		t.Fatalf("NewHistogramDetector: %v", err)
	}
	if _, err = d.ProcessFrame(makeTC(0), blackFrame(64, 64)); err != nil {
		t.Fatal(err)
	}
	cuts, err := d.ProcessFrame(makeTC(1), whiteFrame(64, 64))
	if err != nil {
		t.Fatal(err)
	}
	if len(cuts) != 0 {
		t.Errorf("min_scene_len=5: expected no cut at frame 1, got %v", cuts)
	}
}

func TestHistogramDetector_GetMetrics(t *testing.T) {
	d, err := NewHistogramDetector(0.05, 256, 15)
	if err != nil {
		t.Fatalf("NewHistogramDetector: %v", err)
	}
	m := d.GetMetrics()
	if len(m) != 1 || m[0] != "hist_diff [bins=256]" {
		t.Errorf("GetMetrics() = %v, want [\"hist_diff [bins=256]\"]", m)
	}
}

func TestHistogramDetector_EventBufferLength(t *testing.T) {
	d, err := NewHistogramDetector(0.05, 256, 15)
	if err != nil {
		t.Fatalf("NewHistogramDetector: %v", err)
	}
	if d.EventBufferLength() != 0 {
		t.Errorf("EventBufferLength() = %d, want 0", d.EventBufferLength())
	}
}
