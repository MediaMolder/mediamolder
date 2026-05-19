// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"context"
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
}

const (
	rtInterval           = 500 * time.Millisecond
	rtMinCooldownWindows = 3   // stable windows required between adjustments
	rtDeficitThreshold   = 0.5 // fps below target to trigger action
	rtDropDeficit        = 1.0 // fps below target to activate frame-drop
	rtThreadStep         = 2   // threads to add per adjustment
	rtDropPeriod         = 4   // drop 1 in 4 frames (25% frame-drop)
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
		interval:        rtInterval,
		budget:          budget,
		registry:        registry,
		events:          events,
		runner:          runner,
		dag:             dag,
		prom:            prom,
		windowsSinceAdj: make(map[string]int),
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
	snap := c.registry.Snapshot()
	for _, p := range snap.Perf {
		if p.FPSTarget <= 0 {
			continue // no target configured on this node
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
		} else {
			// Budget exhausted — emit violation and optionally drop frames.
			reason := "thread budget exhausted"
			if p.FPSDeficit > rtDropDeficit {
				tracker.SetDropPeriod(rtDropPeriod)
				reason = "thread budget exhausted; frame-drop enabled (1 in 4)"
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
		// Emit an advisory violation so the operator can lower the preset.
		c.events.Post(RealTimeViolation{
			NodeID:     p.NodeID,
			FPSDeficit: p.FPSDeficit,
			Reason:     "sequential bottleneck: codec threads underutilised; consider a faster preset",
			Time:       time.Now(),
		})
		c.windowsSinceAdj[p.NodeID] = 0

	case p.StalledFrac > 0.5:
		// Downstream bottleneck: this node is fast but blocked on its output
		// channel. The real bottleneck is the downstream node; the control
		// loop will catch it on its own turn. Do nothing for this node.
	}
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
