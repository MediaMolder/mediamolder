// SPDX-License-Identifier: BSD-3-Clause
// Copyright (C) 2014-2024 Brandon Castellano <http://www.bcastell.com>.

package imgmath

import (
	"math"
	"testing"
)

// TestDCT2D_Constant verifies that a constant (DC-only) signal produces a
// non-zero coefficient only at index [0,0] and zeros everywhere else.
func TestDCT2D_Constant(t *testing.T) {
	const n = 4
	data := make([]float32, n*n)
	for i := range data {
		data[i] = 1.0
	}
	out := DCT2D(data, n, n)
	if out == nil {
		t.Fatal("DCT2D returned nil")
	}
	// DC coefficient: w(0)*sum = (1/sqrt(N))*N = sqrt(N)
	// 2-D: (sqrt(N))^2 = N (because 1/sqrt(N)*N applied twice)
	// More precisely: dc = (1/sqrt(N) * sum_row) applied column-wise
	//   row result: each row → sqrt(N) at k=0, 0 elsewhere
	//   column result on that column of sqrt(N)'s: sqrt(N) * sqrt(N) = N
	wantDC := float64(n)
	gotDC := float64(out[0])
	if math.Abs(gotDC-wantDC) > 1e-4 {
		t.Errorf("DC coeff: got %v, want %v", gotDC, wantDC)
	}
	// All other coefficients must be ~0.
	for i := 1; i < n*n; i++ {
		if math.Abs(float64(out[i])) > 1e-4 {
			t.Errorf("coeff[%d] = %v, want ~0", i, out[i])
		}
	}
}

// TestDCT2D_Size verifies the output dimensions match the input.
func TestDCT2D_Size(t *testing.T) {
	w, h := 8, 4
	data := make([]float32, w*h)
	out := DCT2D(data, w, h)
	if len(out) != w*h {
		t.Errorf("len %d, want %d", len(out), w*h)
	}
}

// TestDCT2D_NilOnBadInput checks that malformed input returns nil.
func TestDCT2D_NilOnBadInput(t *testing.T) {
	if DCT2D(nil, 4, 4) != nil {
		t.Error("expected nil for nil input")
	}
	if DCT2D(make([]float32, 8), 4, 4) != nil {
		t.Error("expected nil for wrong length")
	}
}

// TestDCT1D_Constant checks the 1-D transform of a constant sequence.
// X[0] should be (1/sqrt(N))*N = sqrt(N); all others 0.
func TestDCT1D_Constant(t *testing.T) {
	const n = 8
	x := make([]float64, n)
	for i := range x {
		x[i] = 1.0
	}
	dct1D(x)
	wantDC := math.Sqrt(float64(n))
	if math.Abs(x[0]-wantDC) > 1e-9 {
		t.Errorf("DC: got %v, want %v", x[0], wantDC)
	}
	for k := 1; k < n; k++ {
		if math.Abs(x[k]) > 1e-9 {
			t.Errorf("coeff[%d] = %v, want ~0", k, x[k])
		}
	}
}
