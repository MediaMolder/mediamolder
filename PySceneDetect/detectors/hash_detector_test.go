// SPDX-License-Identifier: BSD-3-Clause
// Copyright (C) 2018-2024 Brandon Castellano <http://www.bcastell.com>.

package detectors

import (
	"testing"

	psd "github.com/MediaMolder/MediaMolder/PySceneDetect"
)

// compile-time interface check
var _ psd.SceneDetector = (*HashDetector)(nil)

func TestHashDetector_ImplementsSceneDetector(t *testing.T) {
	var _ psd.SceneDetector = (*HashDetector)(nil)
}

func TestHashDetector_InvalidSize(t *testing.T) {
	_, err := NewHashDetector(0.395, 15, 0, 2)
	if err == nil {
		t.Fatal("expected error for size=0, got nil")
	}
}

func TestHashDetector_InvalidLowpass(t *testing.T) {
	_, err := NewHashDetector(0.395, 15, 16, 0)
	if err == nil {
		t.Fatal("expected error for lowpass=0, got nil")
	}
}

func TestHashDetector_FirstFrameNoOutput(t *testing.T) {
	d, err := NewHashDetector(0.395, 15, 16, 2)
	if err != nil {
		t.Fatalf("NewHashDetector: %v", err)
	}
	cuts, err := d.ProcessFrame(makeTC(0), blackFrame(64, 64))
	if err != nil {
		t.Fatalf("ProcessFrame: %v", err)
	}
	if len(cuts) != 0 {
		t.Fatalf("first frame: expected no cuts, got %v", cuts)
	}
	if d.LastHashDist() != 0 {
		t.Errorf("first frame LastHashDist() = %f, want 0", d.LastHashDist())
	}
}

func TestHashDetector_IdenticalFrames_ZeroDist(t *testing.T) {
	d, err := NewHashDetector(0.001, 0, 16, 2)
	if err != nil {
		t.Fatalf("NewHashDetector: %v", err)
	}
	if _, err = d.ProcessFrame(makeTC(0), whiteFrame(64, 64)); err != nil {
		t.Fatal(err)
	}
	cuts, err := d.ProcessFrame(makeTC(1), whiteFrame(64, 64))
	if err != nil {
		t.Fatal(err)
	}
	if len(cuts) != 0 {
		t.Errorf("identical frames: expected no cut, got %v", cuts)
	}
	if d.LastHashDist() != 0 {
		t.Errorf("identical frames: LastHashDist() = %f, want 0", d.LastHashDist())
	}
}

// TestHashDetector_DifferentFrames_HasDist verifies that a black-then-white
// transition produces a nonzero normalised Hamming distance.
func TestHashDetector_DifferentFrames_HasDist(t *testing.T) {
	// threshold=0.003 catches the minimum possible nonzero distance (1/256 ≈ 0.004).
	d, err := NewHashDetector(0.003, 0, 16, 2)
	if err != nil {
		t.Fatalf("NewHashDetector: %v", err)
	}
	if _, err = d.ProcessFrame(makeTC(0), blackFrame(64, 64)); err != nil {
		t.Fatal(err)
	}
	cuts, err := d.ProcessFrame(makeTC(1), whiteFrame(64, 64))
	if err != nil {
		t.Fatal(err)
	}
	if d.LastHashDist() == 0 {
		t.Error("black→white: expected nonzero hash dist")
	}
	if len(cuts) == 0 {
		t.Errorf("black→white with threshold 0.003: expected cut, LastHashDist=%f", d.LastHashDist())
	}
}

func TestHashDetector_MinSceneLenSuppresses(t *testing.T) {
	// min_scene_len=5 suppresses a cut at frame 1.
	d, err := NewHashDetector(0.003, 5, 16, 2)
	if err != nil {
		t.Fatalf("NewHashDetector: %v", err)
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

func TestHashDetector_MinSceneLenAllowsAfterInterval(t *testing.T) {
	d, err := NewHashDetector(0.003, 3, 16, 2)
	if err != nil {
		t.Fatalf("NewHashDetector: %v", err)
	}
	// Frame 0: black (no comparison)
	if _, err = d.ProcessFrame(makeTC(0), blackFrame(64, 64)); err != nil {
		t.Fatal(err)
	}
	// Frames 1-2: white (suppressed by min_scene_len=3)
	for i := int64(1); i < 3; i++ {
		cuts, err := d.ProcessFrame(makeTC(i), whiteFrame(64, 64))
		if err != nil {
			t.Fatal(err)
		}
		if len(cuts) != 0 {
			t.Errorf("frame %d: expected suppression, got %v", i, cuts)
		}
	}
	// Frame 3: white again; elapsed=3 >= min_scene_len=3 → cut allowed
	cuts, err := d.ProcessFrame(makeTC(3), blackFrame(64, 64))
	if err != nil {
		t.Fatal(err)
	}
	if len(cuts) == 0 {
		t.Errorf("frame 3 after min_scene_len elapsed: expected cut, LastHashDist=%f", d.LastHashDist())
	}
}

func TestHashDetector_GetMetrics(t *testing.T) {
	d, err := NewHashDetector(0.395, 15, 16, 2)
	if err != nil {
		t.Fatalf("NewHashDetector: %v", err)
	}
	m := d.GetMetrics()
	if len(m) != 1 || m[0] != "hash_dist [size=16 lowpass=2]" {
		t.Errorf("GetMetrics() = %v, want [\"hash_dist [size=16 lowpass=2]\"]", m)
	}
}

func TestHashDetector_EventBufferLength(t *testing.T) {
	d, err := NewHashDetector(0.395, 15, 16, 2)
	if err != nil {
		t.Fatalf("NewHashDetector: %v", err)
	}
	if d.EventBufferLength() != 0 {
		t.Errorf("EventBufferLength() = %d, want 0", d.EventBufferLength())
	}
}
