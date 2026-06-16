// Copyright (C) 2025-2026 MediaMolder contributors.
// SPDX-License-Identifier: LGPL-2.1-or-later

package lookahead

import (
	"bytes"
	"testing"

	imgmath "github.com/MediaMolder/MediaMolder/lookahead/internal"
)

// makeGrayFrame returns a packed luma plane of width×height filled with v.
func makeGrayFrame(width, height int, v byte) []byte {
	b := make([]byte, width*height)
	if v != 0 {
		bytes.Repeat([]byte{v}, 1) // force import used
		for i := range b {
			b[i] = v
		}
	}
	return b
}

// makeNoiseFrame returns a frame with a simple deterministic pattern.
func makeNoiseFrame(width, height int) []byte {
	b := make([]byte, width*height)
	for i := range b {
		b[i] = byte(i*7+13) & 0xFF
	}
	return b
}

func TestNewLookaheadScanner_Valid(t *testing.T) {
	s, err := NewLookaheadScanner(40)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Matrix().L != 40 {
		t.Fatalf("L = %d, want 40", s.Matrix().L)
	}
}

func TestNewLookaheadScanner_InvalidL(t *testing.T) {
	for _, l := range []int{0, -1, imgmath.MaxLag + 1} {
		if _, err := NewLookaheadScanner(l); err == nil {
			t.Errorf("L=%d: expected error, got nil", l)
		}
	}
}

func TestLookaheadScanner_SingleFrame(t *testing.T) {
	s, _ := NewLookaheadScanner(5)
	luma := makeGrayFrame(64, 64, 128)
	if err := s.AddFrame(luma, 64, 64, 64); err != nil {
		t.Fatalf("AddFrame: %v", err)
	}
	m := s.Matrix()
	if m.N != 1 {
		t.Fatalf("N = %d, want 1", m.N)
	}
	// First frame has no references: all ratios must be zero.
	for k, r := range m.Ratio[0] {
		if r != 0 {
			t.Errorf("Ratio[0][%d] = %g, want 0", k, r)
		}
	}
}

func TestLookaheadScanner_MatrixDimensions(t *testing.T) {
	const N, L = 8, 4
	s, _ := NewLookaheadScanner(L)
	luma := makeNoiseFrame(64, 64)
	for i := 0; i < N; i++ {
		if err := s.AddFrame(luma, 64, 64, 64); err != nil {
			t.Fatalf("AddFrame %d: %v", i, err)
		}
	}
	m := s.Matrix()
	if m.N != N {
		t.Fatalf("N = %d, want %d", m.N, N)
	}
	if len(m.IntraCost) != N {
		t.Fatalf("len(IntraCost) = %d, want %d", len(m.IntraCost), N)
	}
	if len(m.InterCost) != N {
		t.Fatalf("len(InterCost) = %d, want %d", len(m.InterCost), N)
	}
	if len(m.Ratio) != N {
		t.Fatalf("len(Ratio) = %d, want %d", len(m.Ratio), N)
	}
	for j := 0; j < N; j++ {
		if len(m.InterCost[j]) != L {
			t.Errorf("len(InterCost[%d]) = %d, want %d", j, len(m.InterCost[j]), L)
		}
		if len(m.Ratio[j]) != L {
			t.Errorf("len(Ratio[%d]) = %d, want %d", j, len(m.Ratio[j]), L)
		}
	}
}

func TestLookaheadScanner_IdenticalFrames_LowRatio(t *testing.T) {
	// Adjacent identical frames should have Ratio[1][0] ≪ 1 (highly predictable).
	s, _ := NewLookaheadScanner(5)
	luma := makeGrayFrame(64, 64, 128)
	for i := 0; i < 3; i++ {
		if err := s.AddFrame(luma, 64, 64, 64); err != nil {
			t.Fatalf("AddFrame %d: %v", i, err)
		}
	}
	m := s.Matrix()
	// Frame 1 vs frame 0: ratio should be small (zero residual).
	// With accurate x264 lam=1, lowres_pen=4, intra_pen=5: floor = 4/(5+4)≈0.444
	// ≤ 0.5 is safe upper bound for identical.
	if r := m.Ratio[1][0]; r > 0.5 {
		t.Errorf("Ratio[1][0] = %g, want ≤ 0.5 (identical frames)", r)
	}
}

func TestLookaheadScanner_DifferentFrames_HighRatio(t *testing.T) {
	// Two uncorrelated frames: Ratio[1][0] should be near 1 or exceed it.
	s, _ := NewLookaheadScanner(5)
	f0 := makeGrayFrame(64, 64, 0)   // all black
	f1 := makeGrayFrame(64, 64, 255) // all white
	for _, f := range [][]byte{f0, f1} {
		if err := s.AddFrame(f, 64, 64, 64); err != nil {
			t.Fatalf("AddFrame: %v", err)
		}
	}
	m := s.Matrix()
	if r := m.Ratio[1][0]; r < 0.5 {
		t.Errorf("Ratio[1][0] = %g, want ≥ 0.5 (very different frames)", r)
	}
}

func TestLookaheadScanner_EarlyFramesZeroUnusedLags(t *testing.T) {
	// Frame j < L should have zeros for lags k ≥ j (no reference available).
	const L = 5
	s, _ := NewLookaheadScanner(L)
	luma := makeNoiseFrame(64, 64)
	// Add 3 frames (indices 0,1,2).
	for i := 0; i < 3; i++ {
		_ = s.AddFrame(luma, 64, 64, 64)
	}
	m := s.Matrix()
	// Frame 2 has refs at k=0 (frame 1) and k=1 (frame 0); k≥2 must be zero.
	for k := 2; k < L; k++ {
		if r := m.Ratio[2][k]; r != 0 {
			t.Errorf("Ratio[2][%d] = %g, want 0 (no reference at that lag)", k, r)
		}
	}
}

func TestLookaheadScanner_RingBufferWraps(t *testing.T) {
	// Add L+2 frames; the scanner must still compute valid ratios.
	const L = 3
	s, _ := NewLookaheadScanner(L)
	luma := makeGrayFrame(64, 64, 100)
	for i := 0; i < L+2; i++ {
		if err := s.AddFrame(luma, 64, 64, 64); err != nil {
			t.Fatalf("AddFrame %d: %v", i, err)
		}
	}
	m := s.Matrix()
	if m.N != L+2 {
		t.Fatalf("N = %d, want %d", m.N, L+2)
	}
	// All adjacent-frame ratios should still be near 0 (identical frames).
	for j := 1; j < m.N; j++ {
		if r := m.Ratio[j][0]; r > 0.5 {
			t.Errorf("Ratio[%d][0] = %g after ring wrap, want ≤ 0.5", j, r)
		}
	}
}
