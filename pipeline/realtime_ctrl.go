// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/MediaMolder/MediaMolder/graph"
	"github.com/MediaMolder/MediaMolder/observability"
	"github.com/MediaMolder/MediaMolder/pipeline/snap"
)

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
	targetFPS     float64 // optional graph-level fps_target override

	// Phase 6: bounded decision log shared with HTTP / CLI consumers.
	decMu        sync.Mutex
	decisions    []snap.DecisionRecord
	decisionsCap int
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

// observe takes one performance snapshot and drives the decision logic.
func (c *realtimeController) observe() {
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
		eligible = append(eligible, p)
		if p.FPSDeficit > rtPresetDeficit {
			behind = append(behind, p)
		}
	}
	if len(eligible) == 0 || len(behind) == 0 {
		return
	}
	if float64(len(behind))/float64(len(eligible)) < rtPresetGroupQuorum {
		return
	}
	// Step at most one encoder per tick, prioritising the most-behind node.
	// Stepping all encoders simultaneously causes them to flush+drain+close+reopen
	// at the same time, starving downstream muxers for ~2s. With a 500ms tick
	// the remaining encoders will be stepped on subsequent ticks once their
	// per-node cool-down allows it.
	sort.Slice(eligible, func(i, j int) bool {
		return eligible[i].FPSDeficit > eligible[j].FPSDeficit
	})
	for _, p := range eligible {
		if c.tryPresetStep(p, +1, "group quorum") {
			break // one restart per tick; stagger the rest
		}
	}
}

// logDecision appends d to the bounded ring buffer.
func (c *realtimeController) logDecision(d snap.DecisionRecord) {
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
