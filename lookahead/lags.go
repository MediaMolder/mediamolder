// Copyright (C) 2025-2026 MediaMolder contributors.
// SPDX-License-Identifier: LGPL-2.1-or-later

package lookahead

// fibLags returns the Fibonacci lag schedule for a lookahead window of length l.
//
// The schedule is a dense prefix {1, 2, 3, 4, 5} plus Fibonacci values
// {8, 13, 21, 34, ...} up to l.  The dense prefix guarantees that:
//   - lag=1 is always present, so hard-cut detection and the flash filter are exact
//   - lags 1–5 are all sampled, matching the default AggWindow=5 requirement
//   - baseline fitting has 5 dense inlier points within the first 5 frames
//
// For L=34 the result is {1,2,3,4,5,8,13,21,34} — 9 evaluations vs 34 dense
// (3.8× speedup).  For L=55: {1,2,3,4,5,8,13,21,34,55} — 10 evals (5.5×).
//
// The returned slice is always non-nil and sorted ascending.
func fibLags(l int) []int {
	// Dense prefix: all integers 1..5.
	prefix := []int{1, 2, 3, 4, 5}
	var lags []int
	for _, k := range prefix {
		if k <= l {
			lags = append(lags, k)
		}
	}

	// Fibonacci tail: values > 5 generated from (5, 8).
	a, b := 5, 8
	for b <= l {
		lags = append(lags, b)
		a, b = b, a+b
	}

	if len(lags) == 0 {
		// l < 1 is rejected by NewLookaheadScanner; safe fallback.
		return []int{1}
	}
	return lags
}

// lagIndex returns the index of lag k in a sorted Lags slice, or -1 if absent.
// Used by the flash filter which needs to look up k=1 specifically.
func lagIndex(lags []int, k int) int {
	for i, v := range lags {
		if v == k {
			return i
		}
		if v > k {
			break
		}
	}
	return -1
}
