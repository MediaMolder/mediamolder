// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"fmt"
	"time"

	"github.com/MediaMolder/MediaMolder/pipeline/snap"
)

// SetPresetOverride manually requests a preset change for nodeID. The change
// is honoured at the encoder's next IDR (x264/x265) or next frame
// (SVT-AV1). The override also locks the node against automatic stepping
// until ClearPresetOverride is called.
//
// Returns an error when the node does not exist or has no preset ladder.
func (p *Pipeline) SetPresetOverride(nodeID, preset string) error {
	if p == nil {
		return fmt.Errorf("nil pipeline")
	}
	tracker := p.trackerFor(nodeID)
	if tracker == nil {
		return fmt.Errorf("unknown node %q", nodeID)
	}
	ladder := tracker.Ladder()
	if len(ladder.Names) == 0 {
		return fmt.Errorf("node %q has no preset ladder", nodeID)
	}
	if ladder.IndexOf(preset) < 0 {
		return fmt.Errorf("preset %q not on %s ladder", preset, ladder.Codec)
	}
	tracker.LockPreset(true)
	tracker.RequestPresetChange(preset)
	return nil
}

// ClearPresetOverride releases the manual lock set by SetPresetOverride so
// the realtime controller may resume automatic stepping on nodeID.
func (p *Pipeline) ClearPresetOverride(nodeID string) error {
	if p == nil {
		return fmt.Errorf("nil pipeline")
	}
	tracker := p.trackerFor(nodeID)
	if tracker == nil {
		return fmt.Errorf("unknown node %q", nodeID)
	}
	tracker.LockPreset(false)
	return nil
}

// RealtimeDecisions returns a snapshot of the bounded decision log
// produced by the adaptive control loop. Returns an empty slice when
// realtime mode is disabled.
func (p *Pipeline) RealtimeDecisions() []snap.DecisionRecord {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	ctrl := p.realtimeCtrl
	p.mu.Unlock()
	if ctrl == nil {
		return nil
	}
	return ctrl.snapshotDecisions()
}

// RealtimeStatus returns a compact summary suitable for the toolbar pill
// and the `mediamolder perf` header.
func (p *Pipeline) RealtimeStatus() snap.RealtimeSnapshot {
	if p == nil {
		return snap.RealtimeSnapshot{}
	}
	p.mu.Lock()
	ctrl := p.realtimeCtrl
	p.mu.Unlock()
	if ctrl == nil {
		return snap.RealtimeSnapshot{}
	}
	shot := p.metrics.Snapshot()
	rt := snap.RealtimeSnapshot{Enabled: true, Decisions: ctrl.snapshotDecisions()}
	rt.FPSTarget, rt.FPSActual, rt.Satisfied = graphFPS(shot)
	return rt
}

// trackerFor returns the perf tracker for nodeID, or nil if none.
func (p *Pipeline) trackerFor(nodeID string) *NodePerfTracker {
	p.mu.Lock()
	runner := p.graphRunner
	p.mu.Unlock()
	if runner == nil {
		return nil
	}
	return runner.trackers[nodeID]
}

// graphFPS computes the max-target / min-actual fps across video encoders.
func graphFPS(shot snap.MetricsSnapshot) (target, actual float64, satisfied bool) {
	satisfied = true
	first := true
	for _, p := range shot.Perf {
		if p.FPSTarget <= 0 {
			continue
		}
		if p.FPSTarget > target {
			target = p.FPSTarget
		}
		if first || p.FPS < actual {
			actual = p.FPS
			first = false
		}
		if p.FPSDeficit > 0.5 {
			satisfied = false
		}
	}
	if first {
		satisfied = false
	}
	_ = time.Now // keep time import if needed elsewhere
	return
}
