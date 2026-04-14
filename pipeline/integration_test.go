// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"testing"
	"time"
)

func TestEventBusMultipleConsumers(t *testing.T) {
	bus := NewEventBus(64)
	defer bus.Close()
	ch := bus.Chan()

	// Post multiple events
	bus.Post(StateChanged{From: StateNull, To: StateReady})
	bus.Post(StateChanged{From: StateReady, To: StatePaused})
	bus.Post(EOS{})

	received := 0
	timeout := time.After(100 * time.Millisecond)
loop:
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				break loop
			}
			received++
			if received == 3 {
				break loop
			}
		case <-timeout:
			break loop
		}
	}
	if received != 3 {
		t.Errorf("received %d events, want 3", received)
	}
}

func TestEventBusOverflow(t *testing.T) {
	bus := NewEventBus(2)
	defer bus.Close()

	// Fill buffer
	bus.Post(EOS{})
	bus.Post(EOS{})
	// This should overflow
	bus.Post(EOS{})

	if d := bus.Dropped(); d != 1 {
		t.Errorf("dropped: got %d, want 1", d)
	}
}

func TestMetricsMultipleNodes(t *testing.T) {
	reg := NewMetricsRegistry()

	n1 := reg.Node("decode")
	n2 := reg.Node("encode")

	n1.Frames.Add(1)
	n1.Bytes.Add(1024)
	n1.Frames.Add(1)
	n1.Bytes.Add(2048)
	n2.Frames.Add(1)
	n2.Bytes.Add(512)
	n2.Errors.Add(1)

	snap := reg.Snapshot()
	if len(snap.Nodes) != 2 {
		t.Fatalf("nodes: got %d, want 2", len(snap.Nodes))
	}

	found := map[string]bool{}
	for _, ns := range snap.Nodes {
		found[ns.NodeID] = true
		switch ns.NodeID {
		case "decode":
			if ns.Frames != 2 {
				t.Errorf("decode frames: got %d, want 2", ns.Frames)
			}
			if ns.Bytes != 3072 {
				t.Errorf("decode bytes: got %d, want 3072", ns.Bytes)
			}
		case "encode":
			if ns.Frames != 1 {
				t.Errorf("encode frames: got %d, want 1", ns.Frames)
			}
			if ns.Errors != 1 {
				t.Errorf("encode errors: got %d, want 1", ns.Errors)
			}
		}
	}
	if !found["decode"] || !found["encode"] {
		t.Errorf("missing node metrics")
	}
}

func TestMetricsSameNodeReturned(t *testing.T) {
	reg := NewMetricsRegistry()
	a := reg.Node("x")
	b := reg.Node("x")
	if a != b {
		t.Error("Node() should return same instance for same ID")
	}
}

func TestStateTransitionFullCycle(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.0",
		Inputs:        []Input{{ID: "in", URL: "test.mp4"}},
		Outputs:       []Output{{ID: "out", URL: "out.mp4"}},
	}
	p, err := NewPipeline(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	// NULL → READY
	if err := p.SetState(StateReady); err != nil {
		t.Fatal(err)
	}
	if p.State() != StateReady {
		t.Errorf("state: got %v, want READY", p.State())
	}

	// READY → PAUSED
	if err := p.SetState(StatePaused); err != nil {
		t.Fatal(err)
	}
	if p.State() != StatePaused {
		t.Errorf("state: got %v, want PAUSED", p.State())
	}

	// PAUSED → NULL (teardown)
	if err := p.SetState(StateNull); err != nil {
		t.Fatal(err)
	}
	if p.State() != StateNull {
		t.Errorf("state: got %v, want NULL", p.State())
	}
}

func TestStateInvalidBackward(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.0",
		Inputs:        []Input{{ID: "in", URL: "test.mp4"}},
		Outputs:       []Output{{ID: "out", URL: "out.mp4"}},
	}
	p, err := NewPipeline(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	if err := p.SetState(StatePaused); err != nil {
		t.Fatal(err)
	}
	// PAUSED → READY is invalid (backward, not →NULL)
	err = p.SetState(StateReady)
	if err == nil {
		t.Error("expected error for PAUSED → READY")
	}
}

func TestSeekNullStateError(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.0",
		Inputs:        []Input{{ID: "in", URL: "test.mp4"}},
		Outputs:       []Output{{ID: "out", URL: "out.mp4"}},
	}
	p, err := NewPipeline(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	err = p.SeekTo(5000)
	if err == nil {
		t.Error("expected error for seek in NULL state")
	}
}

func TestSeekInPausedState(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.0",
		Inputs:        []Input{{ID: "in", URL: "test.mp4"}},
		Outputs:       []Output{{ID: "out", URL: "out.mp4"}},
	}
	p, err := NewPipeline(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	if err := p.SetState(StatePaused); err != nil {
		t.Fatal(err)
	}
	if err := p.SeekTo(10000); err != nil {
		t.Fatal(err)
	}
	target, pending := p.seekState()
	if !pending {
		t.Error("seek should be pending")
	}
	if target != 10000 {
		t.Errorf("target: got %d, want 10000", target)
	}
}
