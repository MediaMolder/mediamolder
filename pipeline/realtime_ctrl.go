// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MediaMolder/MediaMolder/graph"
	"github.com/MediaMolder/MediaMolder/observability"
	"github.com/MediaMolder/MediaMolder/pipeline/snap"
)

// rtPrerollStatus is the per-output preroll buffer state logged each tick.
type rtPrerollStatus struct {
	NodeID     string `json:"node_id"`
	State      string `json:"state"`
	BufferedNs int64  `json:"buffered_ns"`
	TargetNs   int64  `json:"target_ns"`
	Evictions  int64  `json:"evictions"`
}

// rtLogEntry is one line of the realtime controller debug log.
// Written as JSONL (one JSON object per line) when RealtimeLogPath is set.
type rtLogEntry struct {
	T                    string                `json:"t"`          // RFC3339Nano wall time
	ElapsedNs            int64                 `json:"elapsed_ns"` // pipeline elapsed time
	Tick                 int64                 `json:"tick"`       // monotonic tick counter
	FPSTarget            float64               `json:"fps_target"` // graph-level target
	FPSActual            float64               `json:"fps_actual"` // graph-level actual
	Satisfied            bool                  `json:"satisfied"`
	HighestQualityPreset string                `json:"highest_quality_preset,omitempty"`
	GroupStep            bool                  `json:"group_step"`
	Nodes                []rtLogNode           `json:"nodes"`
	Prerolls             []rtPrerollStatus     `json:"prerolls,omitempty"`
	Decisions            []snap.DecisionRecord `json:"decisions,omitempty"` // decisions made THIS tick only
}

// rtLogNode is one node's performance data plus the controller's internal
// state for that node. The embedded NodePerfSnapshot fields appear at the
// top level in JSON (PascalCase names, matching the existing metrics API).
type rtLogNode struct {
	snap.NodePerfSnapshot
	WindowsSinceAdj    int `json:"windows_since_adj"`    // consecutive windows this node has been behind
	WindowsSincePreset int `json:"windows_since_preset"` // ticks since last preset change
	OvershootWindows   int `json:"overshoot_windows"`    // consecutive windows of excess headroom
}

// realtimeController is the Phase 5 adaptive control loop. It observes per-node
// performance snapshots every rtInterval and, when a node falls behind its FPS
// target, attempts to increase that node's codec thread count (within the
// ThreadBudget). If the budget is exhausted it emits a RealTimeViolation event
// and, as a last resort, enables frame-drop on the upstream source.
//
// Decision logic (from node_perf_monitoring_design.md §5):
//
//	FPSDeficit > rtDeficitThreshold →
//	  ActiveFrac > 0.9 ∧ ThreadsBusy ≈ ThreadsConfigured → thread-limited → add 2
//	  ActiveFrac > 0.9 ∧ ThreadsBusy ≪ ThreadsConfigured → sequential bottleneck → advisory
//	  StalledFrac > 0.5                                   → downstream bottleneck → skip
//
// A cool-down of rtMinCooldownWindows observation windows must elapse after
// each adjustment before the next one to prevent oscillation.
type realtimeController struct {
	interval time.Duration
	budget   *ThreadBudget
	registry *MetricsRegistry
	events   *EventBus
	runner   *graphRunner
	dag      *graph.Graph
	prom     *observability.Metrics // nil = no Prometheus; Update already handles RealtimeSatisfied

	// windowsSinceAdj tracks how many observation windows have elapsed since
	// the last thread-count adjustment for each node.
	windowsSinceAdj map[string]int

	// Phase 6: per-node cool-down after a preset switch and overshoot
	// window counters. windowsSincePreset records ticks since the last
	// preset adjustment; overshootWindows counts consecutive windows
	// where the node had > rtPresetOvershoot fps headroom.
	windowsSincePreset map[string]int
	overshootWindows   map[string]int

	// Phase 6: configurable bounds and policy.
	highestQualityPreset string  // slowest (highest quality) preset allowed; controller may step faster freely
	groupStep            bool    // step every eligible video encoder together when quorum met
	targetFPS            float64 // optional graph-level fps_target override

	// Phase 6: bounded decision log shared with HTTP / CLI consumers.
	decMu        sync.Mutex
	decisions    []snap.DecisionRecord
	decisionsCap int

	// Debug log: when logPath is non-empty the controller writes one
	// JSONL record per observe() tick to this file.
	logPath       string
	logMu         sync.Mutex
	logBuf        *bufio.Writer
	logFileCloser io.Closer
	tickCount     int64
	tickDecisions []snap.DecisionRecord // decisions made in the current tick

	// Phase 8: observeCount is the total number of observe() calls
	// (always incremented, unlike tickCount which requires the log file).
	// lastSnapshot holds the most recently computed RTControllerSnapshot
	// so HTTP handlers can read it without acquiring any lock.
	observeCount int64
	lastSnapshot atomic.Pointer[snap.RTControllerSnapshot]
}

const (
	rtInterval           = 500 * time.Millisecond
	rtMinCooldownWindows = 3   // stable windows required between adjustments
	rtDeficitThreshold   = 0.5 // fps below target to trigger action
	rtDropDeficit        = 1.0 // fps below target to activate frame-drop
	rtThreadStep         = 2   // threads to add per adjustment
	rtDropPeriod         = 4   // drop 1 in 4 frames (25% frame-drop)

	// Phase 6 constants.
	rtPresetDeficit      = 0.5  // fps below target before preset step
	rtPresetCooldownWins = 6    // ticks (~3s @ 500ms) between preset adjustments
	rtPresetOvershoot    = -3.0 // negative deficit ⇒ headroom; triggers back-off
	rtOvershootWindows   = 6    // consecutive windows of headroom required
	rtPresetGroupQuorum  = 0.5  // fraction of behind video encoders required for group step
	rtDecisionLogCap     = 256  // bounded ring capacity for /realtime/decisions

	// rtGroupStepActiveThreshold is the minimum ActiveFrac an encoder must
	// exhibit before it is counted as "behind due to compute" for the group
	// step quorum. Encoders below this threshold are source-starved (pacer
	// sleep, audio back-pressure, …) rather than CPU-bound; a preset step
	// cannot help them and would sacrifice quality for no benefit.
	rtGroupStepActiveThreshold = 0.5
)

func newRealtimeController(
	budget *ThreadBudget,
	registry *MetricsRegistry,
	events *EventBus,
	runner *graphRunner,
	dag *graph.Graph,
	prom *observability.Metrics,
) *realtimeController {
	return &realtimeController{
		interval:           rtInterval,
		budget:             budget,
		registry:           registry,
		events:             events,
		runner:             runner,
		dag:                dag,
		prom:               prom,
		windowsSinceAdj:    make(map[string]int),
		windowsSincePreset: make(map[string]int),
		overshootWindows:   make(map[string]int),
		decisionsCap:       rtDecisionLogCap,
	}
}

// run is the main loop. It blocks until ctx is cancelled.
func (c *realtimeController) run(ctx context.Context) {
	if c.logPath != "" {
		if err := c.openLogFile(); err != nil {
			// Log the error but continue running without the log file
			// rather than aborting the whole pipeline.
			_ = fmt.Errorf("realtime log: %w", err) // surfaced via stderr below
			_, _ = fmt.Fprintf(os.Stderr, "mediamolder: realtime controller: cannot open log %q: %v\n", c.logPath, err)
		} else {
			defer c.closeLogFile()
		}
	}
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.observe()
		case <-ctx.Done():
			return
		}
	}
}

// openLogFile creates (or truncates) the debug log file and writes a
// header line describing the controller configuration.
func (c *realtimeController) openLogFile() error {
	// Sanitise the caller-supplied path to prevent path traversal (CWE-22).
	clean := filepath.Clean(c.logPath)
	if strings.Contains(clean, "..") {
		return fmt.Errorf("realtime log path must not traverse parent directories: %q", c.logPath)
	}
	f, err := os.OpenFile(clean, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	c.logMu.Lock()
	c.logBuf = bufio.NewWriterSize(f, 64*1024)
	c.logFileCloser = f
	c.logMu.Unlock()
	// Write a self-describing header as the first line.
	hdr := map[string]any{
		"type":                   "header",
		"version":                1,
		"t":                      time.Now().Format(time.RFC3339Nano),
		"highest_quality_preset": c.highestQualityPreset,
		"group_step":             c.groupStep,
		"target_fps":             c.targetFPS,
		"interval_ms":            c.interval.Milliseconds(),
		"cooldown_wins":          rtMinCooldownWindows,
		"preset_cooldown_wins":   rtPresetCooldownWins,
		"deficit_threshold":      rtDeficitThreshold,
		"preset_deficit":         rtPresetDeficit,
		"overshoot_threshold":    rtPresetOvershoot,
		"overshoot_wins":         rtOvershootWindows,
		"group_quorum":           rtPresetGroupQuorum,
	}
	return c.writeJSON(hdr)
}

// closeLogFile flushes and closes the debug log file.
func (c *realtimeController) closeLogFile() {
	c.logMu.Lock()
	defer c.logMu.Unlock()
	if c.logBuf != nil {
		_ = c.logBuf.Flush()
		c.logBuf = nil
	}
	if c.logFileCloser != nil {
		_ = c.logFileCloser.Close()
		c.logFileCloser = nil
	}
}

// writeJSON marshals v as JSON and appends a newline to the log.
func (c *realtimeController) writeJSON(v any) error {
	c.logMu.Lock()
	defer c.logMu.Unlock()
	if c.logBuf == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = c.logBuf.Write(b)
	if err == nil {
		_ = c.logBuf.WriteByte('\n')
	}
	// Flush every write so data survives a kill/panic.
	_ = c.logBuf.Flush()
	return err
}

// writeLogEntry emits one JSONL tick record.
func (c *realtimeController) writeLogEntry(shot snap.MetricsSnapshot) {
	if c.logBuf == nil {
		return
	}
	c.tickCount++
	fpsTarget, fpsActual, satisfied := graphFPS(shot, c.dag)
	nodes := make([]rtLogNode, 0, len(shot.Perf))
	for _, p := range shot.Perf {
		nodes = append(nodes, rtLogNode{
			NodePerfSnapshot:   p,
			WindowsSinceAdj:    c.windowsSinceAdj[p.NodeID],
			WindowsSincePreset: c.windowsSincePreset[p.NodeID],
			OvershootWindows:   c.overshootWindows[p.NodeID],
		})
	}
	var prerolls []rtPrerollStatus
	if c.runner != nil {
		for _, s := range c.runner.sinks {
			if s.preroll != nil {
				prerolls = append(prerolls, rtPrerollStatus{
					NodeID:     s.preroll.NodeID(),
					State:      s.preroll.State().String(),
					BufferedNs: int64(s.preroll.BufferedDuration()),
					TargetNs:   int64(s.preroll.TargetDur()),
					Evictions:  s.preroll.Evictions(),
				})
			}
		}
	}
	entry := rtLogEntry{
		T:                    time.Now().Format(time.RFC3339Nano),
		ElapsedNs:            int64(shot.Elapsed),
		Tick:                 c.tickCount,
		FPSTarget:            fpsTarget,
		FPSActual:            fpsActual,
		Satisfied:            satisfied,
		HighestQualityPreset: c.highestQualityPreset,
		GroupStep:            c.groupStep,
		Nodes:                nodes,
		Prerolls:             prerolls,
		Decisions:            c.tickDecisions,
	}
	_ = c.writeJSON(entry)
}

// observe takes one performance snapshot and drives the decision logic.
func (c *realtimeController) observe() {
	// Reset the per-tick decision buffer so writeLogEntry only reports
	// decisions made during this tick.
	c.tickDecisions = c.tickDecisions[:0]
	c.observeCount++

	shot := c.registry.Snapshot()

	// Tick every node's preset cooldown counter so successive observe()
	// calls eventually allow a new preset adjustment.
	for nid := range c.windowsSincePreset {
		c.windowsSincePreset[nid]++
	}

	for _, p := range shot.Perf {
		if p.FPSTarget <= 0 {
			continue
		}
		// Focus exclusively on video nodes. Audio / subtitle / data
		// processing is background noise for the realtime controller:
		// their throughput is governed by codec-independent factors
		// (sample-rate, mux pacing) and cannot be improved by adjusting
		// video encoder presets.
		if !c.isVideoNode(p.NodeID) {
			continue
		}

		// Phase 6: overshoot detection — node has spare headroom for
		// several consecutive windows ⇒ step one preset slower so we
		// reclaim quality without falling behind.
		if p.FPSDeficit < rtPresetOvershoot {
			c.overshootWindows[p.NodeID]++
			if c.overshootWindows[p.NodeID] >= rtOvershootWindows {
				c.tryPresetStep(p, -1, "overshoot back-off")
				c.overshootWindows[p.NodeID] = 0
			}
		} else {
			c.overshootWindows[p.NodeID] = 0
		}

		if p.FPSDeficit <= rtDeficitThreshold {
			// Node is meeting its target — reset cool-down window counter.
			c.windowsSinceAdj[p.NodeID] = 0
			continue
		}

		// Node is behind. Increment the stable-deficit window count.
		c.windowsSinceAdj[p.NodeID]++
		if c.windowsSinceAdj[p.NodeID] < rtMinCooldownWindows {
			continue // not enough consecutive windows to act
		}

		c.decide(p)
	}

	// Phase 6: group-step coordination — when more than the quorum of
	// preset-capable video encoders are simultaneously behind, step them
	// all one preset faster in the same tick. This is gated by
	// groupStep being enabled (default true).
	if c.groupStep {
		c.maybeGroupStep(shot)
	}

	// Write one JSONL record to the debug log (no-op when log is disabled).
	c.writeLogEntry(shot)
	// Build and store a snapshot for HTTP consumers (Phase 8).
	c.storeSnapshot(shot)
}

// decide applies the decision tree for one node that is behind target.
func (c *realtimeController) decide(p snap.NodePerfSnapshot) {
	node := c.dag.NodeByID(p.NodeID)
	if node == nil {
		return
	}
	// Only act on encoder and filter nodes. Sources and sinks are not
	// restartable; HW encoders consume GPU resources not CPU threads.
	switch node.Kind {
	case graph.KindEncoder, graph.KindFilter:
	default:
		return
	}

	tracker := c.runner.trackers[p.NodeID]
	if tracker == nil {
		return
	}

	switch {
	case p.ActiveFrac > 0.9 && isThreadLimited(p):
		// Thread-limited bottleneck: the codec is saturating all configured
		// threads. Increase the thread count if the budget allows.
		newCount := p.ThreadsConfigured + rtThreadStep
		if c.budget.CanAllocate(p.NodeID, newCount) {
			c.budget.Allocate(p.NodeID, newCount)
			tracker.RequestRestart(newCount)
			c.windowsSinceAdj[p.NodeID] = 0
			c.logDecision(snap.DecisionRecord{
				Time: time.Now(), NodeID: p.NodeID,
				Action: "restart_threads", Deficit: p.FPSDeficit,
				Reason: "thread-limited",
			})
		} else {
			// Budget exhausted — try a preset step before resorting to drops.
			if c.tryPresetStep(p, +1, "thread budget exhausted") {
				c.windowsSinceAdj[p.NodeID] = 0
				return
			}
			reason := "thread budget exhausted"
			if p.FPSDeficit > rtDropDeficit {
				tracker.SetDropPeriod(rtDropPeriod)
				reason = "thread budget exhausted; frame-drop enabled (1 in 4)"
				c.logDecision(snap.DecisionRecord{
					Time: time.Now(), NodeID: p.NodeID,
					Action: "drop_frames", Deficit: p.FPSDeficit, Reason: reason,
				})
			}
			c.events.Post(RealTimeViolation{
				NodeID:     p.NodeID,
				FPSDeficit: p.FPSDeficit,
				Reason:     reason,
				Time:       time.Now(),
			})
			c.windowsSinceAdj[p.NodeID] = 0
		}

	case p.ActiveFrac > 0.9 && p.ThreadsBusy >= 0 &&
		float64(p.ThreadsBusy) < float64(p.ThreadsConfigured)*0.5:
		// Sequential bottleneck: threads are allocated but underutilised.
		// Adding more threads won't help (e.g. entropy coding is serial).
		// Phase 6: step one preset faster instead of merely advising.
		if c.tryPresetStep(p, +1, "sequential bottleneck") {
			c.windowsSinceAdj[p.NodeID] = 0
			return
		}
		// No further preset room — fall back to the original advisory.
		c.events.Post(RealTimeViolation{
			NodeID:     p.NodeID,
			FPSDeficit: p.FPSDeficit,
			Reason:     "sequential bottleneck: codec threads underutilised; preset already at floor",
			Time:       time.Now(),
		})
		c.windowsSinceAdj[p.NodeID] = 0

	case p.StalledFrac > 0.5:
		// Downstream bottleneck: this node is fast but blocked on its output
		// channel. The real bottleneck is the downstream node; the control
		// loop will catch it on its own turn. Do nothing for this node.
	}
}

// tryPresetStep attempts to move node p by n positions on its preset ladder
// (n > 0 faster, n < 0 slower). Honours floor/ceiling bounds, the per-node
// cool-down window, the lock flag, and PresetCapability. Returns true when
// a step was requested (a decision was logged); false when no action was
// possible (no ladder, locked, clamped at requested end, in cool-down).
func (c *realtimeController) tryPresetStep(p snap.NodePerfSnapshot, n int, reason string) bool {
	if len(p.PresetLadder) == 0 || p.PresetLocked {
		return false
	}
	if c.windowsSincePreset[p.NodeID] != 0 &&
		c.windowsSincePreset[p.NodeID] < rtPresetCooldownWins {
		return false
	}
	ladder, ok := LadderFor(p.CodecName)
	if !ok {
		return false
	}
	next, clamped := ladder.Step(p.CurrentPreset, n)
	if next == p.CurrentPreset {
		return false
	}
	// Clamp: never step slower (higher quality) than highestQualityPreset.
	if c.highestQualityPreset != "" {
		qi := ladder.IndexOf(c.highestQualityPreset)
		ni := ladder.IndexOf(next)
		if qi >= 0 && ni >= 0 && ni < qi {
			next = c.highestQualityPreset
		}
	}
	if next == p.CurrentPreset {
		return false
	}
	tracker := c.runner.trackers[p.NodeID]
	if tracker == nil {
		return false
	}
	tracker.RequestPresetChange(next)
	c.windowsSincePreset[p.NodeID] = 1
	action := "step_faster"
	if n < 0 {
		action = "step_slower"
	}
	if clamped {
		reason = reason + "; clamped at ladder end"
	}
	c.logDecision(snap.DecisionRecord{
		Time: time.Now(), NodeID: p.NodeID,
		Action: action, From: p.CurrentPreset, To: next,
		Deficit: p.FPSDeficit, Reason: reason,
	})
	c.events.Post(PresetSwitchPlanned{
		NodeID: p.NodeID, From: p.CurrentPreset, To: next, Time: time.Now(),
	})
	if c.prom != nil && c.prom.RealtimeDecisions != nil {
		c.prom.RealtimeDecisions.WithLabelValues(action).Inc()
	}
	return true
}

// maybeGroupStep implements the quorum-based "step them all together" rule:
// when at least rtPresetGroupQuorum of the preset-capable, behind-target
// video encoders are simultaneously falling behind, step every eligible
// encoder one preset faster. The per-node cool-down still applies.
func (c *realtimeController) maybeGroupStep(shot snap.MetricsSnapshot) {
	var eligible, behind []snap.NodePerfSnapshot
	for _, p := range shot.Perf {
		if len(p.PresetLadder) == 0 || p.PresetLocked || p.FPSTarget <= 0 {
			continue
		}
		// Only video encoders participate in the group step. Audio and
		// other media types have no preset ladder in practice, but this
		// guard is explicit for correctness.
		if !c.isVideoNode(p.NodeID) {
			continue
		}
		eligible = append(eligible, p)
		// Count a node as "behind due to compute" only when it is
		// actually CPU-bound. A source-side pause (pacer sleep, audio
		// back-pressure) causes fps deficit at low ActiveFrac — stepping
		// the preset faster cannot fix that and wastes quality.
		if p.FPSDeficit > rtPresetDeficit && p.ActiveFrac >= rtGroupStepActiveThreshold {
			behind = append(behind, p)
		}
	}
	if len(eligible) == 0 || len(behind) == 0 {
		return
	}
	if float64(len(behind))/float64(len(eligible)) < rtPresetGroupQuorum {
		return
	}
	for _, p := range eligible {
		// Step every eligible encoder, not only those currently behind,
		// so an upstream stage doesn't become the new bottleneck.
		c.tryPresetStep(p, +1, "group quorum")
	}
}

// logDecision appends d to the bounded ring buffer and to the
// per-tick slice (used by writeLogEntry to record only this-tick decisions).
func (c *realtimeController) logDecision(d snap.DecisionRecord) {
	// Per-tick slice (not mutex-protected: always called from observe() goroutine).
	c.tickDecisions = append(c.tickDecisions, d)

	c.decMu.Lock()
	defer c.decMu.Unlock()
	if len(c.decisions) >= c.decisionsCap {
		// Shift left by 1 to drop the oldest entry.
		copy(c.decisions, c.decisions[1:])
		c.decisions = c.decisions[:len(c.decisions)-1]
	}
	c.decisions = append(c.decisions, d)
}

// snapshotDecisions returns a copy of the decision ring.
func (c *realtimeController) snapshotDecisions() []snap.DecisionRecord {
	c.decMu.Lock()
	defer c.decMu.Unlock()
	out := make([]snap.DecisionRecord, len(c.decisions))
	copy(out, c.decisions)
	return out
}

// isVideoNode reports whether nodeID is a video-processing node in the
// compiled graph. It returns true when at least one of the node's inbound
// or outbound edges carries graph.PortVideo. Source nodes are excluded:
// their throughput is controlled by the demuxer/pacer, not by codec
// presets, so the realtime controller cannot improve them by adjusting
// encoder settings.
func (c *realtimeController) isVideoNode(nodeID string) bool {
	n := c.dag.NodeByID(nodeID)
	if n == nil || n.Kind == graph.KindSource {
		return false
	}
	for _, e := range n.Outbound {
		if e.Type == graph.PortVideo {
			return true
		}
	}
	for _, e := range n.Inbound {
		if e.Type == graph.PortVideo {
			return true
		}
	}
	return false
}

// isThreadLimited returns true when the snapshot suggests the codec is
// saturating its configured thread count. When ThreadsBusy is unavailable
// (−1) we infer saturation from ActiveFrac alone.
func isThreadLimited(p snap.NodePerfSnapshot) bool {
	if p.ThreadsBusy < 0 {
		// No ThreadsBusy data — assume thread-limited when ActiveFrac is high.
		return true
	}
	// Busy ≥ 80% of configured = saturated.
	return float64(p.ThreadsBusy) >= float64(p.ThreadsConfigured)*0.8
}

// storeSnapshot builds a RTControllerSnapshot from the current tick's
// MetricsSnapshot and controller internal state, then stores it atomically
// so HTTP handlers can read it concurrently without holding any lock.
// Must be called from the observe() goroutine (single writer).
func (c *realtimeController) storeSnapshot(shot snap.MetricsSnapshot) {
	fpsTarget, fpsActual, satisfied := graphFPS(shot, c.dag)

	nodes := make([]snap.ControllerNodeSnapshot, 0, len(shot.Perf))
	maxCooldown := 0
	for _, p := range shot.Perf {
		if !c.isVideoNode(p.NodeID) {
			continue
		}
		wsp := c.windowsSincePreset[p.NodeID]
		cd := rtPresetCooldownWins - wsp
		if cd < 0 {
			cd = 0
		}
		if cd > maxCooldown {
			maxCooldown = cd
		}
		nodes = append(nodes, snap.ControllerNodeSnapshot{
			NodeID:               p.NodeID,
			FPS:                  p.FPS,
			FPSTarget:            p.FPSTarget,
			FPSDeficit:           p.FPSDeficit,
			ActiveFrac:           p.ActiveFrac,
			StalledFrac:          p.StalledFrac,
			IdleFrac:             p.IdleFrac,
			ThreadsConfigured:    p.ThreadsConfigured,
			ThreadsBusy:          p.ThreadsBusy,
			InputBufferFillFrac:  p.InputQueueFillFrac,
			OutputBufferFillFrac: p.QueueFillFrac,
			FrameLatencyMean:     p.FrameLatencyMean,
			CurrentPreset:        p.CurrentPreset,
			PresetIndex:          p.PresetIndex,
			PresetLadder:         append([]string(nil), p.PresetLadder...),
			PresetLocked:         p.PresetLocked,
			PresetSwitches:       p.PresetSwitches,
			WindowsSincePreset:   wsp,
			CooldownRemaining:    cd,
			OvershootWindows:     c.overshootWindows[p.NodeID],
			ThreadRestarts:       p.ThreadRestarts,
		})
	}

	var sinks []snap.SinkNodeSnapshot
	if c.runner != nil {
		for _, s := range c.runner.sinks {
			if s.preroll != nil {
				fillFrac := 0.0
				target := s.preroll.TargetDur()
				if target > 0 {
					fillFrac = float64(s.preroll.BufferedDuration()) / float64(target)
					if fillFrac > 1 {
						fillFrac = 1
					}
				}
				sinks = append(sinks, snap.SinkNodeSnapshot{
					NodeID:               s.preroll.NodeID(),
					OutputBufferFillFrac: fillFrac,
					BufferedNs:           int64(s.preroll.BufferedDuration()),
					TargetNs:             int64(target),
					AheadNs:              s.preroll.AheadNs(),
				})
			}
		}
	}

	// Derive status string.
	status := "observing"
	switch {
	case satisfied:
		status = "satisfied"
	case maxCooldown > 0:
		status = "cooldown"
	default:
		// Check whether any node is currently dropping frames.
		if c.runner != nil {
			for _, tr := range c.runner.trackers {
				if tr != nil && tr.dropPeriod.Load() > 0 {
					status = "dropping"
					break
				}
			}
		}
	}

	cs := snap.RTControllerSnapshot{
		Enabled:              true,
		Status:               status,
		Tick:                 c.observeCount,
		Elapsed:              shot.Elapsed,
		FPSTarget:            fpsTarget,
		FPSActual:            fpsActual,
		Satisfied:            satisfied,
		HighestQualityPreset: c.highestQualityPreset,
		GroupStep:            c.groupStep,
		CooldownWindows:      maxCooldown,
		TickIntervalMs:       c.interval.Milliseconds(),
		Nodes:                nodes,
		Sinks:                sinks,
		RecentDecisions:      c.snapshotDecisions(),
	}
	c.lastSnapshot.Store(&cs)
}

// ControllerSnapshot returns the most recently stored RTControllerSnapshot.
// Returns a snapshot with Enabled=true and Status="observing" if no tick
// has completed yet.
func (c *realtimeController) ControllerSnapshot() snap.RTControllerSnapshot {
	p := c.lastSnapshot.Load()
	if p == nil {
		return snap.RTControllerSnapshot{Enabled: true, Status: "observing"}
	}
	return *p
}
