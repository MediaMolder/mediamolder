// SPDX-License-Identifier: BSD-3-Clause
// Copyright (C) 2014-2024 Brandon Castellano <http://www.bcastell.com>.

package imgmath

import "testing"

func TestMeanPixelDistance_Identical(t *testing.T) {
	a := []byte{10, 20, 30, 40}
	if got := MeanPixelDistance(a, a); got != 0 {
		t.Errorf("identical slices: got %v, want 0", got)
	}
}

func TestMeanPixelDistance_MaxVsZero(t *testing.T) {
	a := []byte{255, 255, 255, 255}
	b := []byte{0, 0, 0, 0}
	if got := MeanPixelDistance(a, b); got != 255 {
		t.Errorf("max vs zero: got %v, want 255", got)
	}
}

func TestMeanPixelDistance_KnownValue(t *testing.T) {
	// mean(|10-0|, |20-0|, |30-0|, |40-0|) = (10+20+30+40)/4 = 25
	a := []byte{10, 20, 30, 40}
	b := []byte{0, 0, 0, 0}
	if got := MeanPixelDistance(a, b); got != 25 {
		t.Errorf("known: got %v, want 25", got)
	}
}

func TestMeanPixelDistance_LenMismatch(t *testing.T) {
	if got := MeanPixelDistance([]byte{1}, []byte{1, 2}); got != 0 {
		t.Errorf("len mismatch should return 0, got %v", got)
	}
}

func TestMeanPixelDistance_Empty(t *testing.T) {
	if got := MeanPixelDistance(nil, nil); got != 0 {
		t.Errorf("empty should return 0, got %v", got)
	}
}
