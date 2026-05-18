// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"context"
	"sync"
	"time"
)

// nodePerfState is the operating state of a node goroutine.
type nodePerfState uint8

const (
	stateProcessing nodePerfState = iota // actively computing
	stateIdle                            // waiting for input (upstream slow)
	stateStalled                         // blocked on output (downstream slow)
)

// perfTsBufSize is the number of frame-output timestamps kept for the windowed
// FPS calculation. At 30 fps this covers ~8.5 s; at 240 fps ~1 s.
const perfTsBufSize = 256

// NodePerfTracker records per-node performance timing across three operating
// states: PROCESSING, IDLE (waiting for input), and STALLED (blocked on a full
// output channel).
//
// All state-transition methods (BeginIdle, EndIdle, BeginStall, EndStall,
// RecordFrame, RecordQueueFill) must be called from a single node goroutine.
// Snapshot may be called from any goroutine.
//
// A nil *NodePerfTracker is valid — all methods are nil-safe and become no-ops.
type NodePerfTracker struct {
	nodeID    string
	fpsTarget float64
	startTime time.Time

	mu sync.Mutex

	// Current state — read and written only under mu.
	state      nodePerfState
	stateStart time.Time

	// Cumulative time accumulators (nanoseconds), updated at each state transition.
	activeNs  int64
	idleNs    int64
	stalledNs int64

	// Stall event stats.
	stallCount int64
	maxStallNs int64

	// EWMA of output queue fill fraction (α = 0.1).
	queueFillEWMA float64

	// Thread info, set externally after av contexts are opened (Phase 2).
	threadsConfigured int
	threadMode        string

	// Timestamp ring buffer for windowed FPS.
	// Stores Unix nanoseconds of the last perfTsBufSize RecordFrame calls.
	tsBuf    [perfTsBufSize]int64
	tsBufLen int // number of valid entries (≤ perfTsBufSize)
	tsBufPos int // index where the next entry will be written
}

// NewNodePerfTracker creates a tracker for nodeID.
// fpsTarget is the desired output frame rate; use 0 if no target is set.
func NewNodePerfTracker(nodeID string, fpsTarget float64) *NodePerfTracker {
	now := time.Now()
	return &NodePerfTracker{
		nodeID:     nodeID,
		fpsTarget:  fpsTarget,
		startTime:  now,
		state:      stateProcessing,
		stateStart: now,
		threadMode: "n/a",
	}
}

// SetThreadInfo records the av context thread configuration.
// Called once by the handler after opening an encoder, decoder, or filter graph.
func (t *NodePerfTracker) SetThreadInfo(configured int, mode string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.threadsConfigured = configured
	t.threadMode = mode
	t.mu.Unlock()
}

// BeginIdle marks the start of an IDLE period (the node is about to block
// waiting for input). Must be called only while in PROCESSING state.
func (t *NodePerfTracker) BeginIdle() {
	if t == nil {
		return
	}
	now := time.Now()
	t.mu.Lock()
	t.activeNs += now.Sub(t.stateStart).Nanoseconds()
	t.state = stateIdle
	t.stateStart = now
	t.mu.Unlock()
}

// EndIdle marks the end of an IDLE period and transitions back to PROCESSING.
func (t *NodePerfTracker) EndIdle() {
	if t == nil {
		return
	}
	now := time.Now()
	t.mu.Lock()
	t.idleNs += now.Sub(t.stateStart).Nanoseconds()
	t.state = stateProcessing
	t.stateStart = now
	t.mu.Unlock()
}

// BeginStall marks the start of a STALLED period (output channel is full).
// Must be called only while in PROCESSING state.
func (t *NodePerfTracker) BeginStall() {
	if t == nil {
		return
	}
	now := time.Now()
	t.mu.Lock()
	t.activeNs += now.Sub(t.stateStart).Nanoseconds()
	t.state = stateStalled
	t.stateStart = now
	t.mu.Unlock()
}

// EndStall marks the end of a STALLED period and transitions back to PROCESSING.
func (t *NodePerfTracker) EndStall() {
	if t == nil {
		return
	}
	now := time.Now()
	t.mu.Lock()
	dur := now.Sub(t.stateStart).Nanoseconds()
	t.stalledNs += dur
	t.stallCount++
	if dur > t.maxStallNs {
		t.maxStallNs = dur
	}
	t.state = stateProcessing
	t.stateStart = now
	t.mu.Unlock()
}

// RecordFrame records that the node emitted one frame or packet.
// It drives the windowed FPS calculation.
func (t *NodePerfTracker) RecordFrame() {
	if t == nil {
		return
	}
	now := time.Now().UnixNano()
	t.mu.Lock()
	t.tsBuf[t.tsBufPos] = now
	t.tsBufPos = (t.tsBufPos + 1) % perfTsBufSize
	if t.tsBufLen < perfTsBufSize {
		t.tsBufLen++
	}
	t.mu.Unlock()
}

// RecordQueueFill updates the EWMA of the output channel fill fraction.
// qf should be len(ch)/cap(ch) sampled just before the send attempt.
func (t *NodePerfTracker) RecordQueueFill(qf float64) {
	if t == nil {
		return
	}
	const alpha = 0.1
	t.mu.Lock()
	t.queueFillEWMA = alpha*qf + (1-alpha)*t.queueFillEWMA
	t.mu.Unlock()
}

// windowedFPS computes FPS from the ring buffer. Must be called with mu held.
func (t *NodePerfTracker) windowedFPS() float64 {
	if t.tsBufLen < 2 {
		return 0
	}
	oldest := t.tsBuf[(t.tsBufPos-t.tsBufLen+perfTsBufSize)%perfTsBufSize]
	newest := t.tsBuf[(t.tsBufPos-1+perfTsBufSize)%perfTsBufSize]
	elapsed := float64(newest-oldest) / 1e9
	if elapsed <= 0 {
		return 0
	}
	return float64(t.tsBufLen-1) / elapsed
}

// Snapshot returns a point-in-time copy of all performance data for this node.
// The state fractions include the in-progress current period up to the snapshot
// time, so fractions always sum to 1.0.
func (t *NodePerfTracker) Snapshot() NodePerfSnapshot {
	if t == nil {
		return NodePerfSnapshot{}
	}
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()

	// Include the in-progress current period so fractions always sum to 1.0.
	sinceStateStart := now.Sub(t.stateStart).Nanoseconds()
	activeNs := t.activeNs
	idleNs := t.idleNs
	stalledNs := t.stalledNs
	switch t.state {
	case stateProcessing:
		activeNs += sinceStateStart
	case stateIdle:
		idleNs += sinceStateStart
	case stateStalled:
		stalledNs += sinceStateStart
	}

	totalNs := activeNs + idleNs + stalledNs
	var activeFrac, idleFrac, stalledFrac float64
	if totalNs > 0 {
		activeFrac = float64(activeNs) / float64(totalNs)
		idleFrac = float64(idleNs) / float64(totalNs)
		stalledFrac = float64(stalledNs) / float64(totalNs)
	}

	fps := t.windowedFPS()
	var deficit float64
	if t.fpsTarget > 0 {
		deficit = t.fpsTarget - fps
	}

	return NodePerfSnapshot{
		NodeID:            t.nodeID,
		FPS:               fps,
		FPSTarget:         t.fpsTarget,
		FPSDeficit:        deficit,
		ActiveFrac:        activeFrac,
		IdleFrac:          idleFrac,
		StalledFrac:       stalledFrac,
		TotalActive:       time.Duration(activeNs),
		TotalIdle:         time.Duration(idleNs),
		TotalStalled:      time.Duration(stalledNs),
		StallCount:        t.stallCount,
		MaxStallDuration:  time.Duration(t.maxStallNs),
		QueueFillFrac:     t.queueFillEWMA,
		Elapsed:           now.Sub(t.startTime),
		ThreadsConfigured: t.threadsConfigured,
		ThreadMode:        t.threadMode,
		ThreadsBusy:       -1, // populated in Phase 2 via execute2 callback
		EstimatedCPUCores: float64(t.threadsConfigured) * activeFrac,
	}
}

// NodePerfSnapshot is a point-in-time read of all performance data for one node.
type NodePerfSnapshot struct {
	NodeID string

	// Windowed throughput (computed over the last ~perfTsBufSize frames).
	FPS       float64
	FPSTarget float64 // desired output frame rate; 0 = no target set
	FPSDeficit float64 // FPSTarget − FPS; positive = behind; negative = headroom

	// Time-distribution fractions (0.0–1.0, always sum to 1.0).
	ActiveFrac  float64 // fraction of time in PROCESSING
	IdleFrac    float64 // fraction of time in IDLE (waiting for input)
	StalledFrac float64 // fraction of time in STALLED (output channel full)

	// Absolute cumulative durations.
	TotalActive  time.Duration
	TotalIdle    time.Duration
	TotalStalled time.Duration

	// Stall event detail.
	StallCount       int64
	MaxStallDuration time.Duration

	// EWMA of output channel fill fraction at send time (0.0–1.0).
	// A sustained value near 1.0 indicates this node produces faster than
	// its downstream can consume.
	QueueFillFrac float64

	// Total elapsed wall-clock time since the node started.
	Elapsed time.Duration

	// Thread information. Populated from av package accessors in Phase 2.
	// ThreadsConfigured and ThreadMode are zero/"n/a" until SetThreadInfo is called.
	// ThreadsBusy is -1 until the execute2/execute callback wiring in Phase 2.
	ThreadsConfigured int     // libav configured thread count (0 = unknown/n/a)
	ThreadMode        string  // "none", "frame", "slice", "auto", "n/a"
	ThreadsBusy       int     // live tasks in-flight from execute2/execute callback
	EstimatedCPUCores float64 // ThreadsConfigured × ActiveFrac; upper-bound estimate
}

// --- context helpers ---

type perfTrackerContextKey struct{}

// withPerfTracker stores t in ctx, returning the derived context.
func withPerfTracker(ctx context.Context, t *NodePerfTracker) context.Context {
	return context.WithValue(ctx, perfTrackerContextKey{}, t)
}

// perfTrackerFrom retrieves the *NodePerfTracker from ctx.
// Returns nil if none was stored; all *NodePerfTracker methods are nil-safe.
func perfTrackerFrom(ctx context.Context) *NodePerfTracker {
	t, _ := ctx.Value(perfTrackerContextKey{}).(*NodePerfTracker)
	return t
}
