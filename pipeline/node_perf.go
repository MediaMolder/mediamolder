// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MediaMolder/MediaMolder/pipeline/snap"
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

	// Thread info, set externally after av contexts are opened.
	threadsConfigured int
	threadMode        string
	// threadsBusyFn, when non-nil, is called by Snapshot to obtain the live
	// count of av tasks currently executing inside execute2/execute.  Set by
	// runGraph after opening av contexts.
	threadsBusyFn func() int

	// EWMA of frame processing latency (nanoseconds), α = 0.1.
	// Updated by RecordFrameLatency.
	frameLatEWMA float64

	// Adaptive control (Phase 5): restart request set by realtimeController,
	// consumed by the handler goroutine after draining the codec.
	// restartPending is 1 when a restart has been requested, 0 otherwise.
	// restartThreads holds the target thread count for the restart.
	// restartCount is the lifetime count of completed restarts.
	// All three are accessed atomically so the control-loop goroutine can
	// write them without holding mu.
	restartPending atomic.Int32
	restartThreads atomic.Int32
	restartCount   atomic.Int64

	// Frame-drop control (Phase 5, last resort): when dropPeriod > 0 the
	// source handler drops 1 in dropPeriod frames before sending downstream.
	// dropCounter tracks progress within the period.
	dropPeriod  atomic.Int32
	dropCounter atomic.Int64

	// Adaptive preset stepping (Phase 6). codecName / ladder are set once
	// at encoder registration; currentPreset is mutated only by the handler
	// goroutine. presetRequest / presetLocked / presetSwitches are accessed
	// atomically so the control-loop goroutine can write them without
	// holding mu. presetRequest holds the pending preset name (or "");
	// presetLocked, when 1, disables automatic stepping for this node.
	codecName      string
	ladder         PresetLadder
	currentPreset  string
	presetRequest  atomic.Value // string
	presetLocked   atomic.Int32
	presetSwitches atomic.Int64
	lastSwitchUnix atomic.Int64

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

// SetFPSTarget sets the desired output frame rate. Safe to call after
// construction; the updated value is reflected in the next Snapshot.
func (t *NodePerfTracker) SetFPSTarget(fps float64) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.fpsTarget = fps
	t.mu.Unlock()
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

// SetThreadBusyFn stores a function that returns the live count of tasks
// currently executing inside the node's execute2/execute callback.
// Replaces the −1 placeholder in NodePerfSnapshot.ThreadsBusy.
func (t *NodePerfTracker) SetThreadBusyFn(fn func() int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.threadsBusyFn = fn
	t.mu.Unlock()
}

// RecordFrameLatency updates the EWMA of per-frame processing latency.
// d is the elapsed time from when the frame entered the node (perfReceive
// returned) to when the last output derived from that frame was sent.
func (t *NodePerfTracker) RecordFrameLatency(d time.Duration) {
	if t == nil || d <= 0 {
		return
	}
	const alpha = 0.1
	t.mu.Lock()
	t.frameLatEWMA = alpha*float64(d) + (1-alpha)*t.frameLatEWMA
	t.mu.Unlock()
}

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

	threadsBusy := -1
	if t.threadsBusyFn != nil {
		threadsBusy = t.threadsBusyFn()
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
		ThreadsBusy:       threadsBusy,
		EstimatedCPUCores: float64(t.threadsConfigured) * activeFrac,
		FrameLatencyMean:  time.Duration(t.frameLatEWMA),
		ThreadRestarts:    t.restartCount.Load(),

		// Phase 6: adaptive preset stepping (zero values when not an encoder
		// or when the codec doesn't have a ladder).
		CodecName:      t.codecName,
		CurrentPreset:  t.currentPreset,
		PresetLadder:   append([]string(nil), t.ladder.Names...),
		PresetIndex:    t.ladder.IndexOf(t.currentPreset),
		PresetSwitches: t.presetSwitches.Load(),
		PresetLocked:   t.presetLocked.Load() == 1,
	}
}

// NodePerfSnapshot is a point-in-time read of all performance data for one node.
type NodePerfSnapshot = snap.NodePerfSnapshot

// --- Phase 5: restart / frame-drop control ---

// RequestRestart asks the handler goroutine to perform a graceful codec restart
// with threads as the new thread count. Called by realtimeController from its
// own goroutine; the handler goroutine polls via PopRestartRequest.
func (t *NodePerfTracker) RequestRestart(threads int) {
	if t == nil {
		return
	}
	t.restartThreads.Store(int32(threads))
	t.restartPending.Store(1)
}

// PopRestartRequest returns (threads, true) if a restart has been requested
// and atomically clears the pending flag. Called by the handler goroutine.
func (t *NodePerfTracker) PopRestartRequest() (threads int, ok bool) {
	if t == nil {
		return 0, false
	}
	if t.restartPending.Swap(0) == 1 {
		return int(t.restartThreads.Load()), true
	}
	return 0, false
}

// IncrementRestarts records that one graceful restart has completed.
// Called by the handler goroutine after the new codec context is open.
func (t *NodePerfTracker) IncrementRestarts() {
	if t == nil {
		return
	}
	t.restartCount.Add(1)
}

// RestartCount returns the lifetime count of completed graceful restarts.
func (t *NodePerfTracker) RestartCount() int64 {
	if t == nil {
		return 0
	}
	return t.restartCount.Load()
}

// SetDropPeriod enables frame-drop mode on this node. When period > 0 the
// source handler drops 1 in period decoded frames before sending downstream,
// reducing pipeline load as a last resort. period == 0 disables frame-drop.
func (t *NodePerfTracker) SetDropPeriod(period int) {
	if t == nil {
		return
	}
	t.dropPeriod.Store(int32(period))
}

// ShouldDrop returns true if the current frame should be dropped according to
// the configured drop period. Must be called once per frame from the handler
// goroutine; it advances the internal counter. Returns false when frame-drop
// is disabled (period == 0).
func (t *NodePerfTracker) ShouldDrop() bool {
	if t == nil {
		return false
	}
	p := t.dropPeriod.Load()
	if p <= 0 {
		return false
	}
	n := t.dropCounter.Add(1)
	return n%int64(p) == 0
}

// --- Phase 6: adaptive preset stepping ---

// SetPresetInfo records the codec name, its ladder, and the starting preset
// for this node. Called once at encoder registration from runGraph; safe to
// call with an empty ladder for codecs that do not support adaptive stepping
// (the snapshot will then report HasPresetLadder == false).
func (t *NodePerfTracker) SetPresetInfo(codecName, currentPreset string, ladder PresetLadder) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.codecName = codecName
	t.currentPreset = currentPreset
	t.ladder = ladder
	t.mu.Unlock()
}

// CurrentPreset returns the preset name currently in use on this node.
func (t *NodePerfTracker) CurrentPreset() string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.currentPreset
}

// Ladder returns the preset ladder for this node.
func (t *NodePerfTracker) Ladder() PresetLadder {
	if t == nil {
		return PresetLadder{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ladder
}

// RequestPresetChange asks the encoder handler to switch to preset at its next
// IDR boundary (close+reopen for x264/x265; hot reconfig for SVT-AV1). Called
// from the realtime control loop. The handler consumes the request via
// PopPresetRequest. Empty preset is a no-op.
func (t *NodePerfTracker) RequestPresetChange(preset string) {
	if t == nil || preset == "" {
		return
	}
	t.presetRequest.Store(preset)
}

// PopPresetRequest returns the pending preset request and clears it. Called
// by the encoder handler goroutine between frames.
func (t *NodePerfTracker) PopPresetRequest() (preset string, ok bool) {
	if t == nil {
		return "", false
	}
	v := t.presetRequest.Swap("")
	if v == nil {
		return "", false
	}
	s, _ := v.(string)
	if s == "" {
		return "", false
	}
	return s, true
}

// RecordPresetSwitch is called by the encoder handler after a successful
// close+reopen (or hot reconfig) under a new preset. It updates the
// "current preset" snapshot field, increments the switch counter, and
// records the wall-clock time of the transition.
func (t *NodePerfTracker) RecordPresetSwitch(newPreset string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.currentPreset = newPreset
	t.mu.Unlock()
	t.presetSwitches.Add(1)
	t.lastSwitchUnix.Store(time.Now().UnixNano())
}

// PresetSwitches returns the lifetime count of completed preset transitions.
func (t *NodePerfTracker) PresetSwitches() int64 {
	if t == nil {
		return 0
	}
	return t.presetSwitches.Load()
}

// LockPreset sets the auto-stepping lock. When locked the realtime
// controller treats the node as if it had no ladder and skips it.
func (t *NodePerfTracker) LockPreset(locked bool) {
	if t == nil {
		return
	}
	if locked {
		t.presetLocked.Store(1)
	} else {
		t.presetLocked.Store(0)
	}
}

// PresetLocked reports whether automatic preset stepping is disabled for
// this node.
func (t *NodePerfTracker) PresetLocked() bool {
	if t == nil {
		return false
	}
	return t.presetLocked.Load() == 1
}

// LastPresetSwitch returns the wall-clock time of the most recent completed
// preset switch, or the zero time if no switches have occurred.
func (t *NodePerfTracker) LastPresetSwitch() time.Time {
	if t == nil {
		return time.Time{}
	}
	ns := t.lastSwitchUnix.Load()
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
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
