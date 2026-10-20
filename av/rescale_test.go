// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

import "testing"

func TestSecondsToTicks(t *testing.T) {
	got, err := SecondsToTicks(1, 1, 1000)
	if err != nil || got != 1000 {
		t.Fatalf("1s in 1/1000 = %d (%v), want 1000", got, err)
	}
	// 0.5s in 1/25 is 12.5 ticks. av_rescale_q rounds halfway away from zero.
	got, err = SecondsToTicks(0.5, 1, 25)
	if err != nil || got != 13 {
		t.Fatalf("0.5s in 1/25 = %d (%v), want 13", got, err)
	}
	if _, err := SecondsToTicks(1, 0, 25); err == nil {
		t.Fatal("zero time base succeeded")
	}
	if _, err := SecondsToTicks(-1, 1, 25); err == nil {
		t.Fatal("negative seconds succeeded")
	}
}
