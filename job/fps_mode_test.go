// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import "testing"

func TestFPSRewriterPassthrough(t *testing.T) {
	r := newFPSRewriter("passthrough", 100)
	for _, pts := range []int64{50, 30, 30, 200, 199} {
		emit, base, drop := r.rewrite(pts)
		if drop || emit != 1 || base != pts {
			t.Errorf("passthrough pts=%d: got (emit=%d base=%d drop=%v), want (1, %d, false)", pts, emit, base, drop, pts)
		}
	}
}

func TestFPSRewriterEmptyEqualsPassthrough(t *testing.T) {
	r := newFPSRewriter("", 0)
	emit, base, drop := r.rewrite(42)
	if emit != 1 || base != 42 || drop {
		t.Errorf("empty mode: got (%d, %d, %v), want (1, 42, false)", emit, base, drop)
	}
}

func TestFPSRewriterUnknownDegradesToPassthrough(t *testing.T) {
	r := newFPSRewriter("zalgo", 100)
	emit, base, drop := r.rewrite(1234)
	if emit != 1 || base != 1234 || drop {
		t.Errorf("unknown mode: got (%d, %d, %v), want passthrough", emit, base, drop)
	}
}

func TestFPSRewriterVFRDropsNonMonotonic(t *testing.T) {
	r := newFPSRewriter("vfr", 0)
	cases := []struct {
		pts      int64
		wantDrop bool
	}{
		{100, false},
		{200, false},
		{200, true}, // == previous
		{150, true}, // <  previous
		{300, false},
	}
	for _, tc := range cases {
		emit, _, drop := r.rewrite(tc.pts)
		if drop != tc.wantDrop {
			t.Errorf("vfr pts=%d: drop=%v want=%v (emit=%d)", tc.pts, drop, tc.wantDrop, emit)
		}
	}
}

func TestFPSRewriterDropDropsNearDuplicates(t *testing.T) {
	// frameDurTB = 100 → half-window = 50.
	r := newFPSRewriter("drop", 100)
	cases := []struct {
		pts      int64
		wantDrop bool
	}{
		{0, false},
		{40, true},   // 40 - 0 = 40 < 50 → drop
		{60, false},  // 60 - 0 = 60 ≥ 50 → emit; lastEmitted=60
		{100, true},  // 100 - 60 = 40 < 50 → drop
		{120, false}, // 120 - 60 = 60 ≥ 50 → emit
	}
	for _, tc := range cases {
		_, _, drop := r.rewrite(tc.pts)
		if drop != tc.wantDrop {
			t.Errorf("drop pts=%d: drop=%v want=%v", tc.pts, drop, tc.wantDrop)
		}
	}
}

func TestFPSRewriterCFRRenumberAndDuplicate(t *testing.T) {
	// 30 fps in tb=1/3000 → frameDurTB = 100.
	r := newFPSRewriter("cfr", 100)
	type res struct {
		emit int
		base int64
		drop bool
	}
	cases := []struct {
		pts  int64
		want res
	}{
		{0, res{1, 0, false}},     // first frame primes nextPTS=0; emit at 0; next=100
		{100, res{1, 100, false}}, // exactly on slot 100; next=200
		{205, res{1, 200, false}}, // 5tb late, 1 emission at 200; next=300
		{650, res{4, 300, false}}, // big forward gap → emit at 300,400,500,600; next=700
		{650, res{1, 700, false}}, // 650+50=700 not < 700 → keep; gap=-50 → emit 1 at 700; next=800
		{740, res{0, 0, true}},    // 740+50=790 < 800 → drop (arrived too soon)
		{810, res{1, 800, false}}, // 810+50=860 not < 800 → emit at 800; next=900
	}
	for i, tc := range cases {
		emit, base, drop := r.rewrite(tc.pts)
		got := res{emit, base, drop}
		if got != tc.want {
			t.Errorf("cfr step %d pts=%d: got %+v want %+v", i, tc.pts, got, tc.want)
		}
	}
}

func TestFPSRewriterCFRZeroDurationDegrades(t *testing.T) {
	r := newFPSRewriter("cfr", 0)
	emit, base, drop := r.rewrite(42)
	if emit != 1 || base != 42 || drop {
		t.Errorf("cfr with zero duration must passthrough: got (%d, %d, %v)", emit, base, drop)
	}
}

func TestComputeFrameDurationTB(t *testing.T) {
	cases := []struct {
		fr   [2]int
		tb   [2]int
		want int64
	}{
		{[2]int{30, 1}, [2]int{1, 3000}, 100},  // 30fps in 1/3000
		{[2]int{24, 1}, [2]int{1, 12288}, 512}, // 24fps in 1/12288
		{[2]int{0, 1}, [2]int{1, 1000}, 0},     // bad fr → 0 (degrade)
		{[2]int{30, 1}, [2]int{0, 1}, 0},       // bad tb → 0
	}
	for _, tc := range cases {
		if got := computeFrameDurationTB(tc.fr, tc.tb); got != tc.want {
			t.Errorf("computeFrameDurationTB(fr=%v tb=%v) = %d want %d", tc.fr, tc.tb, got, tc.want)
		}
	}
}
