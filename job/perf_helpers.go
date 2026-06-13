// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import "context"

// perfReceive receives one value from ch, recording IDLE wait time in t when
// the channel is empty and the goroutine must block.
//
// Returns (value, false) on success; (nil, true) if the channel is closed or
// ctx is cancelled.
//
// Hot-path cost: when ch has data immediately, only a single non-blocking
// select is executed — no call to time.Now().
func perfReceive(ctx context.Context, ch <-chan any, t *NodePerfTracker) (any, bool) {
	// Sample input queue fill fraction before the receive attempt.
	if cap(ch) > 0 {
		t.RecordInputQueueFill(float64(len(ch)) / float64(cap(ch)))
	}

	// Optimistic non-blocking check — avoids time.Now() on the fast path.
	select {
	case v, ok := <-ch:
		if !ok {
			return nil, true
		}
		t.RecordFrame()
		return v, false
	default:
	}

	// Channel empty: record idle wait.
	t.BeginIdle()
	select {
	case v, ok := <-ch:
		t.EndIdle()
		if !ok {
			return nil, true
		}
		t.RecordFrame()
		return v, false
	case <-ctx.Done():
		t.EndIdle()
		return nil, true
	}
}

// perfSend sends v on ch, recording STALLED wait time in t when the channel
// is full and the goroutine must block.
//
// Returns true if ctx was cancelled before the send could complete.
//
// Hot-path cost: when ch has capacity, only a single non-blocking select is
// executed — no call to time.Now(). Queue fill is always sampled (one len and
// one cap read) to maintain the EWMA.
func perfSend(ctx context.Context, ch chan<- any, v any, t *NodePerfTracker) bool {
	// Sample queue fill before the send attempt (captures backpressure trend).
	if cap(ch) > 0 {
		t.RecordQueueFill(float64(len(ch)) / float64(cap(ch)))
	}

	// Optimistic non-blocking send.
	select {
	case ch <- v:
		return false
	default:
	}

	// Channel full: record stall.
	t.BeginStall()
	select {
	case ch <- v:
		t.EndStall()
		return false
	case <-ctx.Done():
		t.EndStall()
		return true
	}
}
