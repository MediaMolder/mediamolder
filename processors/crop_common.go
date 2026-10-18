// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"fmt"
	"math"
)

// maxExactJSONInt is the largest integer a JSON number (float64) holds exactly.
const maxExactJSONInt = 1 << 53

// cropInt returns a param as an integer within ±2^53: a JSON number that is
// integral, or a Go int or int64.
func cropInt(v any) (int64, error) {
	var i int64
	switch n := v.(type) {
	case float64:
		if n != math.Trunc(n) {
			return 0, fmt.Errorf("%v is not an integer within ±2^53", n)
		}
		if math.Abs(n) > maxExactJSONInt {
			return 0, fmt.Errorf("%v is outside ±2^53", n)
		}
		return int64(n), nil
	case int:
		i = int64(n)
	case int64:
		i = n
	default:
		return 0, fmt.Errorf("has type %T, want a number", v)
	}
	if i < -maxExactJSONInt || i > maxExactJSONInt {
		return 0, fmt.Errorf("%d is outside ±2^53", i)
	}
	return i, nil
}

// cropFloat returns a finite number. JSON numbers arrive as float64; tests
// also pass int and int64.
func cropFloat(v any) (float64, error) {
	var f float64
	switch n := v.(type) {
	case float64:
		f = n
	case int:
		f = float64(n)
	case int64:
		f = float64(n)
	default:
		return 0, fmt.Errorf("has type %T, want a number", v)
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, fmt.Errorf("%v is not finite", v)
	}
	return f, nil
}

// cropRoundTiesDown rounds to the nearest integer, and a value halfway
// between two integers goes to the lower one. Values at or below zero
// round to zero.
func cropRoundTiesDown(e float64) int {
	if e <= 0 {
		return 0
	}
	floor := math.Floor(e)
	x := int(floor)
	if e-floor > 0.5 {
		x++
	}
	return x
}

// cropAlignDown clears the low log2 bits, the same mask vf_crop uses
// (x &= ~((1<<hsub)-1)). log2 <= 0 leaves x unchanged.
func cropAlignDown(x, log2 int) int {
	if log2 <= 0 {
		return x
	}
	return x &^ ((1 << log2) - 1)
}

// cropRoundEven rounds to the nearest even integer. A value halfway between
// two even integers goes away from zero (math.Round on v/2).
func cropRoundEven(v float64) int {
	if v <= 0 || math.IsNaN(v) {
		return 0
	}
	return int(math.Round(v/2)) * 2
}
