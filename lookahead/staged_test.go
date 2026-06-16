// Copyright (C) 2025-2026 MediaMolder contributors.
// SPDX-License-Identifier: LGPL-2.1-or-later

package lookahead

import "testing"

// TestMergeStagedResults_DropsCoarseDissolves guards the staged-merge fix: the
// refined (final) pass owns dissolve reporting, so wide low-score dissolves from
// the coarse (initial) pass must never reach the output (they were the junk
// companions next to every real event). Initial hard cuts / fades are carried
// over only when they do not overlap a refined event.
func TestMergeStagedResults_DropsCoarseDissolves(t *testing.T) {
	final := []SceneTransition{
		{Type: TransitionDissolve, StartFrame: 299, EndFrame: 303, Score: 0.99},
		{Type: TransitionDissolve, StartFrame: 1800, EndFrame: 1830, Score: 0.99},
	}
	initial := []SceneTransition{
		// Wide low-score coarse dissolves — must be dropped.
		{Type: TransitionDissolve, StartFrame: 310, EndFrame: 634, Score: 0.44},
		{Type: TransitionDissolve, StartFrame: 1599, EndFrame: 1923, Score: 0.45},
		// Hard cut overlapping a refined dissolve — should be dropped.
		{Type: TransitionHardCut, StartFrame: 300, EndFrame: 300, Score: 0.7},
		// Genuine hard cut the refined pass did not cover — must be kept.
		{Type: TransitionHardCut, StartFrame: 3596, EndFrame: 3596, Score: 0.8},
	}

	out := mergeStagedResults(initial, final)

	var diss, cuts int
	for _, tr := range out {
		switch tr.Type {
		case TransitionDissolve:
			diss++
			if tr.Score < 0.6 {
				t.Errorf("low-score coarse dissolve leaked: %+v", tr)
			}
		case TransitionHardCut:
			cuts++
			if tr.StartFrame != 3596 {
				t.Errorf("unexpected hard cut kept: %+v", tr)
			}
		}
	}
	if diss != 2 {
		t.Errorf("dissolves: got %d want 2 (the two refined events)", diss)
	}
	if cuts != 1 {
		t.Errorf("hard cuts: got %d want 1 (only the non-overlapping one)", cuts)
	}
}

// TestLagsForDuration_TwoLagsAboveD guards the two-lag-gate fix: for a duration
// near the top of the menu the refined set must still include two lags strictly
// longer than the blend, or the plateau detector can never fire.
func TestLagsForDuration_TwoLagsAboveD(t *testing.T) {
	menu := []int{1, 2, 3, 5, 15, 30, 45, 60, 75, 90, 105, 120, 135}
	got := LagsForDuration(105, menu)

	above := 0
	for _, k := range got {
		if k > 105 {
			above++
		}
	}
	if above < 2 {
		t.Errorf("LagsForDuration(105) = %v: want >=2 lags above 105, got %d", got, above)
	}
}
