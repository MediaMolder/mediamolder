// SPDX-License-Identifier: BSD-3-Clause
// Copyright (C) 2014-2024 Brandon Castellano <http://www.bcastell.com>.

package imgmath

import (
	"math"
	"testing"
)

func TestCalc_EmptySlice(t *testing.T) {
	h := Calc(nil)
	if len(h) != 256 {
		t.Fatalf("len %d, want 256", len(h))
	}
	for i, v := range h {
		if v != 0 {
			t.Errorf("hist[%d] = %v, want 0 for empty input", i, v)
		}
	}
}

func TestCalc_UniformImage(t *testing.T) {
	// 256 pixels of value 100 → hist[100]=1.0, all others 0.
	gray := make([]byte, 256)
	for i := range gray {
		gray[i] = 100
	}
	h := Calc(gray)
	if math.Abs(h[100]-1.0) > 1e-12 {
		t.Errorf("hist[100] = %v, want 1.0", h[100])
	}
	for i, v := range h {
		if i == 100 {
			continue
		}
		if v != 0 {
			t.Errorf("hist[%d] = %v, want 0", i, v)
		}
	}
}

func TestCalc_SumsToOne(t *testing.T) {
	gray := make([]byte, 256)
	for i := range gray {
		gray[i] = byte(i)
	}
	h := Calc(gray)
	var sum float64
	for _, v := range h {
		sum += v
	}
	if math.Abs(sum-1.0) > 1e-10 {
		t.Errorf("histogram sum = %v, want 1.0", sum)
	}
}

func TestCorrelation_Identical(t *testing.T) {
	// Use a non-uniform distribution (variance > 0) so Pearson is well-defined.
	// Most pixels are dark, a few are bright.
	gray := make([]byte, 1024)
	for i := 0; i < 900; i++ {
		gray[i] = 10
	}
	for i := 900; i < 1000; i++ {
		gray[i] = 200
	}
	for i := 1000; i < 1024; i++ {
		gray[i] = 255
	}
	h := Calc(gray)
	c := Correlation(h, h)
	if math.Abs(c-1.0) > 1e-10 {
		t.Errorf("identical histograms: correlation = %v, want 1.0", c)
	}
}

func TestCorrelation_Uniform(t *testing.T) {
	// Two different uniform histograms (all weight on one bin each).
	// They share no common bins → correlation should be −1/(N−1) ≈ –1/255.
	// Practically very close to –1/255.
	h1 := make([]float64, 256)
	h2 := make([]float64, 256)
	h1[0] = 1.0
	h2[255] = 1.0
	c := Correlation(h1, h2)
	// Both deviate from mean 1/256 at different bins; cross-term is 0
	// for non-overlapping bins except via the mean shift.
	// The result must be < 0 (negative correlation) and ≥ –1.
	if c >= 0 || c < -1-1e-9 {
		t.Errorf("non-overlapping histograms: correlation = %v, want (−1, 0)", c)
	}
}

func TestCorrelation_LenMismatch(t *testing.T) {
	h1 := []float64{1.0}
	h2 := []float64{1.0, 0.0}
	if c := Correlation(h1, h2); c != 0 {
		t.Errorf("len mismatch should return 0, got %v", c)
	}
}

func TestCorrelation_Empty(t *testing.T) {
	if c := Correlation(nil, nil); c != 0 {
		t.Errorf("empty should return 0, got %v", c)
	}
}
