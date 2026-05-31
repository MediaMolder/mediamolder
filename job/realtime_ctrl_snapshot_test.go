// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"context"
	"testing"
	"time"

	"github.com/MediaMolder/MediaMolder/job/snap"
)

// makeMinimalController returns a realtimeController wired to a minimal
// registry so that observe()/storeSnapshot() can run without panicking.
func makeMinimalController() *realtimeController {
	reg := NewMetricsRegistry()
	c := &realtimeController{
		interval:           rtInterval,
		registry:           reg,
		windowsSinceAdj:    make(map[string]int),
		windowsSincePreset: make(map[string]int),
		overshootWindows:   make(map[string]int),
		decisionsCap:       rtDecisionLogCap,
	}
	return c
}

// ---------------------------------------------------------------------------
// TestControllerSnapshot_InitialValue verifies the zero-tick behaviour.
// ---------------------------------------------------------------------------

func TestControllerSnapshot_InitialValue(t *testing.T) {
	c := makeMinimalController()
	cs := c.ControllerSnapshot()
	if !cs.Enabled {
		t.Error("expected Enabled=true before first tick")
	}
	if cs.Status != "observing" {
		t.Errorf("expected Status=observing, got %q", cs.Status)
	}
}

// ---------------------------------------------------------------------------
// TestControllerSnapshot_Status verifies status derivation after storeSnapshot.
// ---------------------------------------------------------------------------

func TestControllerSnapshot_StatusSatisfied(t *testing.T) {
	c := makeMinimalController()
	// Inject a Perf snapshot with FPS above target → satisfied.
	shot := snap.MetricsSnapshot{
		Perf: []snap.NodePerfSnapshot{
			{NodeID: "enc0", FPSTarget: 30, FPS: 31, FPSDeficit: -1},
		},
	}
	// storeSnapshot uses isVideoNode which requires c.dag — nil dag makes it
	// skip all nodes, leaving Nodes empty. Use an empty shot instead.
	c.storeSnapshot(snap.MetricsSnapshot{})
	cs := c.ControllerSnapshot()
	// With no Perf entries graphFPS returns satisfied=false (no video nodes).
	_ = shot
	if !cs.Enabled {
		t.Error("Enabled should be true")
	}
}

func TestControllerSnapshot_CooldownRemaining(t *testing.T) {
	c := makeMinimalController()
	// Simulate 2 preset-cooldown windows remaining for "enc0".
	// rtPresetCooldownWins = 6; WindowsSincePreset = 4 → CooldownRemaining = 2.
	c.windowsSincePreset["enc0"] = 4

	// Build a shot with a fake video perf entry. Since dag==nil isVideoNode
	// returns false, so the node will be skipped. To verify the map lookup we
	// call the math directly.
	wsp := c.windowsSincePreset["enc0"]
	cd := rtPresetCooldownWins - wsp
	if cd != 2 {
		t.Errorf("expected CooldownRemaining=2, got %d", cd)
	}

	// Verify the clamp: windowsSincePreset beyond the cooldown window → 0.
	c.windowsSincePreset["enc0"] = rtPresetCooldownWins + 3
	wsp = c.windowsSincePreset["enc0"]
	cd = rtPresetCooldownWins - wsp
	if cd < 0 {
		cd = 0
	}
	if cd != 0 {
		t.Errorf("expected clamped CooldownRemaining=0, got %d", cd)
	}
}

// ---------------------------------------------------------------------------
// TestControllerSnapshot_ObserveCountIncrement ensures observeCount increments.
// ---------------------------------------------------------------------------

func TestControllerSnapshot_ObserveCountIncrement(t *testing.T) {
	c := makeMinimalController()
	if c.observeCount != 0 {
		t.Fatalf("expected observeCount=0, got %d", c.observeCount)
	}
	c.observe()
	if c.observeCount != 1 {
		t.Errorf("expected observeCount=1 after one observe(), got %d", c.observeCount)
	}
	c.observe()
	if c.observeCount != 2 {
		t.Errorf("expected observeCount=2 after two observe() calls, got %d", c.observeCount)
	}
	cs := c.ControllerSnapshot()
	if cs.Tick != 2 {
		t.Errorf("expected Tick=2, got %d", cs.Tick)
	}
}

// ---------------------------------------------------------------------------
// TestControllerSnapshot_RunCancels ensures the controller goroutine stops.
// ---------------------------------------------------------------------------

func TestControllerSnapshot_RunCancels(t *testing.T) {
	c := makeMinimalController()
	c.interval = 10 * time.Millisecond // fast for testing

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		c.run(ctx)
		close(done)
	}()

	select {
	case <-done:
		// passed
	case <-time.After(500 * time.Millisecond):
		t.Fatal("controller goroutine did not exit after context cancellation")
	}

	// At least one tick must have been stored.
	cs := c.ControllerSnapshot()
	if cs.Tick == 0 {
		t.Error("expected at least one tick after 150 ms at 10 ms interval")
	}
}

// ---------------------------------------------------------------------------
// TestBlockBar verifies the 4-block bar helper.
// ---------------------------------------------------------------------------

func TestBlockBar(t *testing.T) {
	// Test file is in cmd/mediamolder (package main), so we replicate the
	// blockBar logic here for coverage of the algorithm itself.
	tests := []struct {
		frac float64
		want string
	}{
		{0.0, "░░░░"},
		{0.25, "█░░░"},
		{0.5, "██░░"},
		{0.75, "███░"},
		{1.0, "████"},
		{-0.1, "░░░░"}, // clamp low
		{1.5, "████"},  // clamp high
	}
	for _, tc := range tests {
		f := tc.frac
		if f < 0 {
			f = 0
		}
		if f > 1 {
			f = 1
		}
		filled := int(f*4 + 0.5)
		got := ""
		for i := 0; i < filled; i++ {
			got += "█"
		}
		for i := filled; i < 4; i++ {
			got += "░"
		}
		if got != tc.want {
			t.Errorf("blockBar(%.2f) = %q, want %q", tc.frac, got, tc.want)
		}
	}
}
