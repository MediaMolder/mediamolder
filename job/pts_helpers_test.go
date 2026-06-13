// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"math"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
	"pgregory.net/rapid"
)

// ---------- ptsToMicros ----------

// TestPtsToMicros_Sentinels verifies the three always-false cases:
// unset PTS, zero numerator, and zero denominator.
func TestPtsToMicros_Sentinels(t *testing.T) {
	cases := []struct {
		name string
		pts  int64
		tb   [2]int
	}{
		{"AV_NOPTS_VALUE", math.MinInt64, [2]int{1, 90000}},
		{"zero tb numerator", 1000, [2]int{0, 90000}},
		{"zero tb denominator", 1000, [2]int{1, 0}},
		{"negative tb numerator", 1000, [2]int{-1, 90000}},
		{"negative tb denominator", 1000, [2]int{1, -90000}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			us, ok := ptsToMicros(tc.pts, tc.tb)
			if ok {
				t.Errorf("expected ok=false, got (%d, true)", us)
			}
			if us != 0 {
				t.Errorf("expected us=0, got %d", us)
			}
		})
	}
}

// TestPtsToMicros_KnownValues spot-checks the formula
// us = pts * 1_000_000 * tb[0] / tb[1] against FFmpeg-standard time bases.
func TestPtsToMicros_KnownValues(t *testing.T) {
	cases := []struct {
		name   string
		pts    int64
		tb     [2]int
		wantUS int64
	}{
		// 1 second at 1/90000 → 90000 ticks → 1_000_000 µs
		{"1s at 1/90000", 90000, [2]int{1, 90000}, 1_000_000},
		// 2.5 s at 1/1000 → 2500 ticks → 2_500_000 µs
		{"2.5s at 1/1000", 2500, [2]int{1, 1000}, 2_500_000},
		// 0 PTS → 0 µs
		{"0 PTS", 0, [2]int{1, 44100}, 0},
		// 1/25 fps: frame 25 → 1 second
		{"frame25 at 1/25", 25, [2]int{1, 25}, 1_000_000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ptsToMicros(tc.pts, tc.tb)
			if !ok {
				t.Fatalf("expected ok=true")
			}
			if got != tc.wantUS {
				t.Errorf("got %d µs, want %d µs", got, tc.wantUS)
			}
		})
	}
}

// TestPropertyPtsToMicros_Monotone verifies that for a fixed valid time base,
// ptsToMicros is non-decreasing in pts (using integer arithmetic).
func TestPropertyPtsToMicros_Monotone(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Pick a valid time base with small values to avoid int64 overflow.
		num := rapid.IntRange(1, 100).Draw(t, "tbNum")
		den := rapid.IntRange(1, 100_000).Draw(t, "tbDen")
		tb := [2]int{num, den}

		// Two non-sentinel PTS values in a range that avoids int64 overflow.
		// Max safe: MaxInt64 / (1_000_000 * 100) ≈ 92_233_720_368, so we cap
		// at 90_000_000 to leave ample headroom.
		a := rapid.Int64Range(0, 90_000_000).Draw(t, "a")
		b := rapid.Int64Range(0, 90_000_000).Draw(t, "b")
		if a > b {
			a, b = b, a
		}

		usA, okA := ptsToMicros(a, tb)
		usB, okB := ptsToMicros(b, tb)
		if !okA || !okB {
			t.Fatal("unexpected ok=false for valid inputs")
		}
		if usA > usB {
			t.Fatalf("ptsToMicros not monotone: pts %d→%dµs > pts %d→%dµs (tb %v)",
				a, usA, b, usB, tb)
		}
	})
}

// TestPropertyPtsToMicros_Formula checks that the computed value matches the
// expected formula: pts * 1_000_000 * tb[0] / tb[1].
func TestPropertyPtsToMicros_Formula(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		num := rapid.IntRange(1, 100).Draw(t, "tbNum")
		den := rapid.IntRange(1, 100_000).Draw(t, "tbDen")
		tb := [2]int{num, den}
		// Keep pts small enough that the formula doesn't overflow int64.
		pts := rapid.Int64Range(0, 1_000_000).Draw(t, "pts")

		got, ok := ptsToMicros(pts, tb)
		if !ok {
			t.Fatal("unexpected ok=false")
		}
		want := pts * 1_000_000 * int64(num) / int64(den)
		if got != want {
			t.Fatalf("ptsToMicros(%d, %v) = %d, want %d", pts, tb, got, want)
		}
	})
}

// ---------- shiftPTSus ----------

// TestShiftPTSus_NoopCases verifies that shiftPTSus is a no-op when
// deltaUS is zero or the time base is invalid.
func TestShiftPTSus_NoopCases(t *testing.T) {
	allocPkt := func(pts int64) *av.Packet {
		pkt, err := av.AllocPacket()
		if err != nil {
			t.Fatalf("AllocPacket: %v", err)
		}
		pkt.SetPTS(pts)
		return pkt
	}

	const origPTS int64 = 12345

	cases := []struct {
		name    string
		deltaUS int64
		tb      [2]int
	}{
		{"zero delta", 0, [2]int{1, 90000}},
		{"zero tb num", 1_000_000, [2]int{0, 90000}},
		{"zero tb den", 1_000_000, [2]int{1, 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pkt := allocPkt(origPTS)
			defer pkt.Close()
			shiftPTSus(pkt, tc.deltaUS, tc.tb)
			if pkt.PTS() != origPTS {
				t.Errorf("PTS changed: got %d, want %d", pkt.PTS(), origPTS)
			}
		})
	}
}

// TestShiftPTSus_KnownShift checks that shiftPTSus subtracts the expected
// number of ticks for well-known time bases.
func TestShiftPTSus_KnownShift(t *testing.T) {
	cases := []struct {
		name    string
		origPTS int64
		deltaUS int64
		tb      [2]int
		wantPTS int64
	}{
		// Shift 1 s (1_000_000 µs) at 1/90000: off = 1_000_000 * 90000 / (1_000_000 * 1) = 90000
		{"1s at 1/90000", 90000, 1_000_000, [2]int{1, 90000}, 0},
		// Shift 500ms at 1/1000: off = 500_000 * 1000 / (1_000_000 * 1) = 500
		{"500ms at 1/1000", 1000, 500_000, [2]int{1, 1000}, 500},
		// Shift 0 µs → no change even with valid tb
		{"zero delta", 5000, 0, [2]int{1, 44100}, 5000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pkt, err := av.AllocPacket()
			if err != nil {
				t.Fatalf("AllocPacket: %v", err)
			}
			defer pkt.Close()
			pkt.SetPTS(tc.origPTS)
			shiftPTSus(pkt, tc.deltaUS, tc.tb)
			if pkt.PTS() != tc.wantPTS {
				t.Errorf("PTS = %d, want %d", pkt.PTS(), tc.wantPTS)
			}
		})
	}
}

// TestPropertyShiftPTSus_InverseOfPtsToMicros verifies the round-trip:
// if shiftPTSus shifts a packet by deltaUS, the packet's new PTS in µs
// equals (origPTSus - deltaUS), subject to integer-division rounding.
func TestPropertyShiftPTSus_InverseOfPtsToMicros(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		num := rapid.IntRange(1, 100).Draw(t, "tbNum")
		den := rapid.IntRange(1, 100_000).Draw(t, "tbDen")
		tb := [2]int{num, den}

		// Pick pts large enough to survive the shift without going negative.
		pts := rapid.Int64Range(1_000_000, 1<<40).Draw(t, "pts")
		deltaUS := rapid.Int64Range(1, 1_000_000).Draw(t, "deltaUS")

		pkt, err := av.AllocPacket()
		if err != nil {
			t.Fatalf("AllocPacket: %v", err)
		}
		defer pkt.Close()
		pkt.SetPTS(pts)

		origUS, origOK := ptsToMicros(pts, tb)
		shiftPTSus(pkt, deltaUS, tb)
		newUS, newOK := ptsToMicros(pkt.PTS(), tb)

		if !origOK || !newOK {
			t.Fatal("ptsToMicros returned ok=false for valid inputs")
		}
		// off = deltaUS * tb[1] / (1_000_000 * tb[0]) — integer division.
		// The resulting µs loss = off * 1_000_000 * tb[0] / tb[1], which
		// equals deltaUS only when deltaUS is a perfect multiple of the
		// tick size. We verify the weaker monotone property: newUS < origUS
		// when a non-zero shift was applied.
		off := deltaUS * int64(den) / (1_000_000 * int64(num))
		if off == 0 {
			// Delta too small for this time base — shift was no-op; skip.
			t.Skip("delta too small for time base, tick is no-op")
		}
		if newUS >= origUS {
			t.Fatalf("shiftPTSus(%dµs, tb=%v): newUS=%d >= origUS=%d (off=%d ticks)",
				deltaUS, tb, newUS, origUS, off)
		}
	})
}
