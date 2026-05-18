// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"context"
	"testing"
	"time"
)

// --- NodePerfTracker: initial state ---

func TestNodePerfTracker_InitialSnapshot(t *testing.T) {
	tr := NewNodePerfTracker("enc0", 30.0)
	snap := tr.Snapshot()

	if snap.NodeID != "enc0" {
		t.Errorf("NodeID = %q, want enc0", snap.NodeID)
	}
	if snap.FPSTarget != 30.0 {
		t.Errorf("FPSTarget = %v, want 30.0", snap.FPSTarget)
	}
	if snap.FPS != 0 {
		t.Errorf("FPS = %v, want 0 initially", snap.FPS)
	}
	if snap.ThreadsBusy != -1 {
		t.Errorf("ThreadsBusy = %d, want -1 (unavailable until Phase 2)", snap.ThreadsBusy)
	}
	if snap.ThreadMode != "n/a" {
		t.Errorf("ThreadMode = %q, want n/a", snap.ThreadMode)
	}
	if snap.ThreadsConfigured != 0 {
		t.Errorf("ThreadsConfigured = %d, want 0", snap.ThreadsConfigured)
	}
	if snap.Elapsed <= 0 {
		t.Errorf("Elapsed = %v, want > 0", snap.Elapsed)
	}
}

// --- NodePerfTracker: nil safety ---

func TestNodePerfTracker_NilSafe(t *testing.T) {
	var tr *NodePerfTracker
	// All calls on a nil tracker must not panic.
	tr.BeginIdle()
	tr.EndIdle()
	tr.BeginStall()
	tr.EndStall()
	tr.RecordFrame()
	tr.RecordQueueFill(0.5)
	tr.SetThreadInfo(4, "slice")
	snap := tr.Snapshot()
	if snap.NodeID != "" {
		t.Error("nil Snapshot should return zero-value NodePerfSnapshot")
	}
}

// --- State transitions ---

func TestNodePerfTracker_IdleTransition(t *testing.T) {
	tr := NewNodePerfTracker("src", 0)

	tr.BeginIdle()
	time.Sleep(5 * time.Millisecond)
	tr.EndIdle()

	snap := tr.Snapshot()
	if snap.IdleFrac <= 0 {
		t.Errorf("IdleFrac = %v, want > 0 after idle period", snap.IdleFrac)
	}
	if snap.ActiveFrac <= 0 {
		t.Errorf("ActiveFrac = %v, want > 0 (initial processing period)", snap.ActiveFrac)
	}
	// Fractions must sum to 1.0.
	assertFractionsSum(t, snap)
}

func TestNodePerfTracker_StallTransition(t *testing.T) {
	tr := NewNodePerfTracker("enc0", 0)

	tr.BeginStall()
	time.Sleep(5 * time.Millisecond)
	tr.EndStall()

	snap := tr.Snapshot()
	if snap.StalledFrac <= 0 {
		t.Errorf("StalledFrac = %v, want > 0", snap.StalledFrac)
	}
	if snap.StallCount != 1 {
		t.Errorf("StallCount = %d, want 1", snap.StallCount)
	}
	if snap.MaxStallDuration <= 0 {
		t.Errorf("MaxStallDuration = %v, want > 0", snap.MaxStallDuration)
	}
	assertFractionsSum(t, snap)
}

func TestNodePerfTracker_AllThreeStates(t *testing.T) {
	tr := NewNodePerfTracker("filter0", 0)

	// Cycle: processing → idle → processing → stall → processing.
	tr.BeginIdle()
	time.Sleep(2 * time.Millisecond)
	tr.EndIdle()
	time.Sleep(5 * time.Millisecond) // processing
	tr.BeginStall()
	time.Sleep(2 * time.Millisecond)
	tr.EndStall()

	snap := tr.Snapshot()
	if snap.ActiveFrac <= 0 {
		t.Errorf("ActiveFrac = %v, want > 0", snap.ActiveFrac)
	}
	if snap.IdleFrac <= 0 {
		t.Errorf("IdleFrac = %v, want > 0", snap.IdleFrac)
	}
	if snap.StalledFrac <= 0 {
		t.Errorf("StalledFrac = %v, want > 0", snap.StalledFrac)
	}
	assertFractionsSum(t, snap)
}

func TestNodePerfTracker_SnapshotDuringIdle(t *testing.T) {
	// Snapshot taken while tracker is in IDLE state should reflect in-progress
	// idle period so fractions sum to 1.0.
	tr := NewNodePerfTracker("src", 0)
	tr.BeginIdle()
	time.Sleep(2 * time.Millisecond)

	snap := tr.Snapshot() // called while still idle
	if snap.IdleFrac <= 0 {
		t.Errorf("IdleFrac = %v mid-idle, want > 0", snap.IdleFrac)
	}
	assertFractionsSum(t, snap)
}

func TestNodePerfTracker_SnapshotDuringStall(t *testing.T) {
	tr := NewNodePerfTracker("enc0", 0)
	tr.BeginStall()
	time.Sleep(2 * time.Millisecond)

	snap := tr.Snapshot()
	if snap.StalledFrac <= 0 {
		t.Errorf("StalledFrac = %v mid-stall, want > 0", snap.StalledFrac)
	}
	assertFractionsSum(t, snap)
}

// --- Multiple stalls ---

func TestNodePerfTracker_MultipleStalls(t *testing.T) {
	tr := NewNodePerfTracker("enc0", 0)

	const shortStall = 2 * time.Millisecond
	const longStall = 10 * time.Millisecond

	tr.BeginStall()
	time.Sleep(shortStall)
	tr.EndStall()

	tr.BeginStall()
	time.Sleep(longStall)
	tr.EndStall()

	snap := tr.Snapshot()
	if snap.StallCount != 2 {
		t.Errorf("StallCount = %d, want 2", snap.StallCount)
	}
	if snap.MaxStallDuration < longStall {
		t.Errorf("MaxStallDuration = %v, want ≥ %v", snap.MaxStallDuration, longStall)
	}
}

// --- Windowed FPS ring buffer ---

func TestNodePerfTracker_FPSWithFrames(t *testing.T) {
	tr := NewNodePerfTracker("src", 0)

	// Record 30 frames at ~30 fps.
	const frames = 30
	interval := time.Second / time.Duration(frames)
	for i := 0; i < frames; i++ {
		tr.RecordFrame()
		if i < frames-1 {
			time.Sleep(interval)
		}
	}

	snap := tr.Snapshot()
	// Allow ±20% for sleep imprecision.
	const want = float64(frames)
	if snap.FPS < want*0.8 || snap.FPS > want*1.2 {
		t.Errorf("FPS = %.1f, want ≈ %.0f (±20%%)", snap.FPS, want)
	}
}

func TestNodePerfTracker_FPSZeroWithOneFrame(t *testing.T) {
	tr := NewNodePerfTracker("src", 0)
	tr.RecordFrame()

	snap := tr.Snapshot()
	if snap.FPS != 0 {
		t.Errorf("FPS = %v with 1 frame, want 0 (need ≥2 timestamps)", snap.FPS)
	}
}

func TestNodePerfTracker_FPSRingBufferWraparound(t *testing.T) {
	tr := NewNodePerfTracker("src", 0)

	// Fill beyond capacity to exercise wraparound.
	for i := 0; i < perfTsBufSize+10; i++ {
		tr.RecordFrame()
	}

	tr.mu.Lock()
	if tr.tsBufLen != perfTsBufSize {
		t.Errorf("tsBufLen = %d after overflow, want %d", tr.tsBufLen, perfTsBufSize)
	}
	tr.mu.Unlock()

	snap := tr.Snapshot()
	if snap.FPS < 0 {
		t.Errorf("FPS = %v after ring wraparound, want ≥ 0", snap.FPS)
	}
}

// --- FPS target and deficit ---

func TestNodePerfTracker_FPSDeficit(t *testing.T) {
	tr := NewNodePerfTracker("enc0", 30.0)
	snap := tr.Snapshot()
	// No frames recorded → FPS = 0 → deficit = 30.0.
	if snap.FPSDeficit != 30.0 {
		t.Errorf("FPSDeficit = %v, want 30.0", snap.FPSDeficit)
	}
}

func TestNodePerfTracker_FPSDeficitNoTarget(t *testing.T) {
	tr := NewNodePerfTracker("enc0", 0)
	snap := tr.Snapshot()
	if snap.FPSDeficit != 0 {
		t.Errorf("FPSDeficit = %v with no target, want 0", snap.FPSDeficit)
	}
}

// --- SetThreadInfo and EstimatedCPUCores ---

func TestNodePerfTracker_ThreadInfo(t *testing.T) {
	tr := NewNodePerfTracker("enc0", 0)
	tr.SetThreadInfo(8, "slice")

	snap := tr.Snapshot()
	if snap.ThreadsConfigured != 8 {
		t.Errorf("ThreadsConfigured = %d, want 8", snap.ThreadsConfigured)
	}
	if snap.ThreadMode != "slice" {
		t.Errorf("ThreadMode = %q, want slice", snap.ThreadMode)
	}
}

func TestNodePerfTracker_EstimatedCPUCores(t *testing.T) {
	tr := NewNodePerfTracker("enc0", 0)
	tr.SetThreadInfo(8, "slice")

	// Force some active time by doing a brief idle cycle.
	tr.BeginIdle()
	tr.EndIdle()
	time.Sleep(5 * time.Millisecond)

	snap := tr.Snapshot()
	// EstimatedCPUCores = ThreadsConfigured × ActiveFrac; must be in [0, 8].
	if snap.EstimatedCPUCores < 0 || snap.EstimatedCPUCores > 8 {
		t.Errorf("EstimatedCPUCores = %v, want in [0, 8]", snap.EstimatedCPUCores)
	}
}

// --- QueueFillFrac EWMA ---

func TestNodePerfTracker_QueueFillSaturates(t *testing.T) {
	tr := NewNodePerfTracker("enc0", 0)

	// After many fill=1.0 observations the EWMA converges to 1.0.
	for i := 0; i < 100; i++ {
		tr.RecordQueueFill(1.0)
	}
	snap := tr.Snapshot()
	if snap.QueueFillFrac < 0.9 {
		t.Errorf("QueueFillFrac = %v, want ≈ 1.0 after saturating", snap.QueueFillFrac)
	}
}

func TestNodePerfTracker_QueueFillDecays(t *testing.T) {
	tr := NewNodePerfTracker("enc0", 0)
	// Start saturated.
	for i := 0; i < 100; i++ {
		tr.RecordQueueFill(1.0)
	}
	// Then record all zeros — EWMA should decay toward 0.
	for i := 0; i < 100; i++ {
		tr.RecordQueueFill(0)
	}
	snap := tr.Snapshot()
	if snap.QueueFillFrac > 0.1 {
		t.Errorf("QueueFillFrac = %v after decaying to 0, want ≈ 0", snap.QueueFillFrac)
	}
}

// --- Context helpers ---

func TestWithPerfTracker_RoundTrip(t *testing.T) {
	tr := NewNodePerfTracker("n0", 25.0)
	ctx := withPerfTracker(context.Background(), tr)

	got := perfTrackerFrom(ctx)
	if got != tr {
		t.Error("perfTrackerFrom returned a different tracker than stored")
	}
}

func TestPerfTrackerFrom_Missing(t *testing.T) {
	got := perfTrackerFrom(context.Background())
	if got != nil {
		t.Errorf("perfTrackerFrom(empty ctx) = %v, want nil", got)
	}
	// Calling methods on a nil tracker must not panic.
	got.RecordFrame()
	got.BeginIdle()
	got.EndIdle()
}

// --- perfReceive helper ---

func TestPerfReceive_FastPath(t *testing.T) {
	tr := NewNodePerfTracker("n0", 0)
	ch := make(chan any, 1)
	ch <- "hello"

	v, cancelled := perfReceive(context.Background(), ch, tr)
	if cancelled {
		t.Fatal("perfReceive: unexpected cancellation")
	}
	if v != "hello" {
		t.Errorf("perfReceive: got %v, want hello", v)
	}

	snap := tr.Snapshot()
	// Fast path: channel was ready, no idle time should be significant.
	if snap.IdleFrac > 0.01 {
		t.Errorf("IdleFrac = %v on fast path, want ≈ 0", snap.IdleFrac)
	}
}

func TestPerfReceive_SlowPath(t *testing.T) {
	tr := NewNodePerfTracker("n0", 0)
	ch := make(chan any, 1)

	// Send the value after a short delay to force the slow path.
	go func() {
		time.Sleep(5 * time.Millisecond)
		ch <- "world"
	}()

	v, cancelled := perfReceive(context.Background(), ch, tr)
	if cancelled {
		t.Fatal("perfReceive: unexpected cancellation")
	}
	if v != "world" {
		t.Errorf("perfReceive: got %v, want world", v)
	}

	snap := tr.Snapshot()
	if snap.IdleFrac <= 0 {
		t.Errorf("IdleFrac = %v on slow path, want > 0", snap.IdleFrac)
	}
}

func TestPerfReceive_ContextCancelled(t *testing.T) {
	tr := NewNodePerfTracker("n0", 0)
	ch := make(chan any) // unbuffered, no sender

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(2 * time.Millisecond)
		cancel()
	}()

	_, cancelled := perfReceive(ctx, ch, tr)
	if !cancelled {
		t.Error("perfReceive: expected cancellation on context Done")
	}
}

func TestPerfReceive_ClosedChannel(t *testing.T) {
	tr := NewNodePerfTracker("n0", 0)
	ch := make(chan any)
	close(ch)

	_, cancelled := perfReceive(context.Background(), ch, tr)
	if !cancelled {
		t.Error("perfReceive: expected closed-channel to return cancelled=true")
	}
}

// --- perfSend helper ---

func TestPerfSend_FastPath(t *testing.T) {
	tr := NewNodePerfTracker("n0", 0)
	ch := make(chan any, 2)

	cancelled := perfSend(context.Background(), ch, "v1", tr)
	if cancelled {
		t.Fatal("perfSend: unexpected cancellation")
	}

	snap := tr.Snapshot()
	if snap.StalledFrac > 0.01 {
		t.Errorf("StalledFrac = %v on fast path, want ≈ 0", snap.StalledFrac)
	}
}

func TestPerfSend_SlowPath(t *testing.T) {
	tr := NewNodePerfTracker("n0", 0)
	ch := make(chan any, 1)
	ch <- "blocker" // fill the channel

	// A reader will unblock the send after a delay.
	go func() {
		time.Sleep(5 * time.Millisecond)
		<-ch
	}()

	cancelled := perfSend(context.Background(), ch, "v2", tr)
	if cancelled {
		t.Fatal("perfSend: unexpected cancellation")
	}

	snap := tr.Snapshot()
	if snap.StalledFrac <= 0 {
		t.Errorf("StalledFrac = %v on slow path, want > 0", snap.StalledFrac)
	}
	if snap.StallCount != 1 {
		t.Errorf("StallCount = %d, want 1", snap.StallCount)
	}
}

func TestPerfSend_QueueFillRecorded(t *testing.T) {
	tr := NewNodePerfTracker("n0", 0)
	ch := make(chan any, 4)

	// Send two items into a capacity-4 channel → fill = 0.5 before second send.
	perfSend(context.Background(), ch, "a", tr)
	perfSend(context.Background(), ch, "b", tr)

	snap := tr.Snapshot()
	// After two sends into cap-4, QueueFillFrac EWMA should be > 0.
	if snap.QueueFillFrac <= 0 {
		t.Errorf("QueueFillFrac = %v, want > 0 after sends into partial-full channel", snap.QueueFillFrac)
	}
}

func TestPerfSend_ContextCancelled(t *testing.T) {
	tr := NewNodePerfTracker("n0", 0)
	ch := make(chan any) // unbuffered, no receiver

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(2 * time.Millisecond)
		cancel()
	}()

	cancelled := perfSend(ctx, ch, "x", tr)
	if !cancelled {
		t.Error("perfSend: expected cancellation on context Done")
	}
}

// --- helpers ---

// assertFractionsSum verifies that ActiveFrac + IdleFrac + StalledFrac ≈ 1.0.
func assertFractionsSum(t *testing.T, snap NodePerfSnapshot) {
	t.Helper()
	sum := snap.ActiveFrac + snap.IdleFrac + snap.StalledFrac
	if sum < 0.99 || sum > 1.01 {
		t.Errorf("fraction sum = %.4f, want ≈ 1.0 (Active=%.4f Idle=%.4f Stalled=%.4f)",
			sum, snap.ActiveFrac, snap.IdleFrac, snap.StalledFrac)
	}
}
