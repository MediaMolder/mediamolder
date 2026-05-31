// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"context"
	"testing"
	"time"
)

// TestReadRatePacer_BurstThenPaces walks the pacer through a burst
// window followed by paced packets and asserts that pacing only
// kicks in after the burst window is exhausted. Mirrors the
// behaviour of fftools/ffmpeg_demux.c::readrate_sleep where the
// first `readrate_initial_burst` seconds of media time are not
// throttled.
func TestReadRatePacer_BurstThenPaces(t *testing.T) {
	const us = 1
	_ = us
	p := newReadRatePacer(1.0 /*rate*/, 0.5 /*burst seconds*/, 1.05)

	// First packet initialises the pacer: must not sleep.
	start := time.Now()
	p.maybeSleep(context.Background(), 0, 0)
	if elapsed := time.Since(start); elapsed > 5*time.Millisecond {
		t.Errorf("first packet slept %v (want ~0)", elapsed)
	}

	// Packet inside the burst window (0.4 s of media time) should
	// not sleep regardless of wallclock time elapsed.
	start = time.Now()
	p.maybeSleep(context.Background(), 400_000, 0)
	if elapsed := time.Since(start); elapsed > 5*time.Millisecond {
		t.Errorf("burst-window packet slept %v (want ~0)", elapsed)
	}

	// Packet well past the burst window but inside the wallclock
	// budget (we just opened the pacer; rate=1 means we have
	// roughly 0 us of budget so the packet must sleep). Use 1.5 s
	// of media time and assert that we slept at least ~0.9 s
	// (rate=1, burst=0.5 s, so 1.5 - 0.5 = 1.0 s of pacing minus
	// however long the test took to get here).
	start = time.Now()
	p.maybeSleep(context.Background(), 1_500_000, 0)
	elapsed := time.Since(start)
	if elapsed < 800*time.Millisecond {
		t.Errorf("paced packet slept only %v (want >= ~900 ms)", elapsed)
	}
	if elapsed > 1500*time.Millisecond {
		t.Errorf("paced packet slept %v (want <= ~1.2 s)", elapsed)
	}
}

// TestReadRatePacer_ContextCancelAborts asserts that a long pacing
// sleep is aborted when the supplied context is cancelled, so that
// pipeline shutdown is not held hostage by the pacer.
func TestReadRatePacer_ContextCancelAborts(t *testing.T) {
	p := newReadRatePacer(1.0, 0.0 /*no burst*/, 1.05)
	p.maybeSleep(context.Background(), 0, 0) // initialise

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	// Ask the pacer for a 10 s sleep; should abort within ~50 ms.
	p.maybeSleep(ctx, 10_000_000, 0)
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("paced sleep ignored cancel: slept %v", elapsed)
	}
}

// TestReadRatePacer_NilNoOp guards against the nil-pacer path
// (handleSource calls maybeSleep on src.pacer unconditionally when
// the field is non-nil; this test pins the safety of the nil case
// in case that invariant ever shifts).
func TestReadRatePacer_NilNoOp(t *testing.T) {
	var p *readRatePacer
	start := time.Now()
	p.maybeSleep(context.Background(), 5_000_000, 0)
	if elapsed := time.Since(start); elapsed > 5*time.Millisecond {
		t.Errorf("nil pacer slept %v (want 0)", elapsed)
	}
}
