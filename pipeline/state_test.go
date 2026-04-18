// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"context"
	"testing"
	"time"
)

func TestStateTransitions(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.0",
		Inputs:        []Input{{ID: "in", URL: "dummy.mp4", Streams: []StreamSelect{{Type: "video"}}}},
		Outputs:       []Output{{ID: "out", URL: "dummy_out.mp4", CodecVideo: "libx264"}},
	}

	t.Run("initial state is NULL", func(t *testing.T) {
		p, err := NewPipeline(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if p.State() != StateNull {
			t.Fatalf("expected NULL, got %s", p.State())
		}
	})

	t.Run("NULL to READY", func(t *testing.T) {
		p, err := NewPipeline(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.SetState(StateReady); err != nil {
			t.Fatalf("SetState(READY): %v", err)
		}
		if p.State() != StateReady {
			t.Fatalf("expected READY, got %s", p.State())
		}
		p.Close()
	})

	t.Run("NULL to PAUSED traverses READY", func(t *testing.T) {
		p, err := NewPipeline(cfg)
		if err != nil {
			t.Fatal(err)
		}
		events := collectEvents(p, 2)
		if err := p.SetState(StatePaused); err != nil {
			t.Fatalf("SetState(PAUSED): %v", err)
		}
		if p.State() != StatePaused {
			t.Fatalf("expected PAUSED, got %s", p.State())
		}
		// Should have received NULL→READY and READY→PAUSED events.
		evts := drainEvents(events, 2, time.Second)
		if len(evts) < 2 {
			t.Fatalf("expected 2 events, got %d", len(evts))
		}
		sc1 := evts[0].(StateChanged)
		sc2 := evts[1].(StateChanged)
		if sc1.From != StateNull || sc1.To != StateReady {
			t.Errorf("event 0: expected NULL→READY, got %s→%s", sc1.From, sc1.To)
		}
		if sc2.From != StateReady || sc2.To != StatePaused {
			t.Errorf("event 1: expected READY→PAUSED, got %s→%s", sc2.From, sc2.To)
		}
		p.Close()
	})

	t.Run("invalid backward transition", func(t *testing.T) {
		p, err := NewPipeline(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.SetState(StateReady); err != nil {
			t.Fatal(err)
		}
		err = p.SetState(StateNull)
		if err != nil {
			t.Fatalf("expected NULL transition to succeed: %v", err)
		}
		// Try READY→NULL→PAUSED (should fail because NULL→PAUSED skips READY,
		// but our impl auto-walks forward).
		p2, _ := NewPipeline(cfg)
		if err := p2.SetState(StatePaused); err != nil {
			t.Fatalf("NULL→PAUSED should auto-walk: %v", err)
		}
		// But PAUSED→READY should be invalid.
		err = p2.SetState(StateReady)
		if err == nil {
			t.Fatal("expected error for PAUSED→READY")
		}
		if _, ok := err.(*ErrInvalidStateTransition); !ok {
			t.Fatalf("expected ErrInvalidStateTransition, got %T: %v", err, err)
		}
		p2.Close()
	})

	t.Run("any to NULL", func(t *testing.T) {
		p, err := NewPipeline(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.SetState(StatePaused); err != nil {
			t.Fatal(err)
		}
		if err := p.SetState(StateNull); err != nil {
			t.Fatalf("PAUSED→NULL: %v", err)
		}
		if p.State() != StateNull {
			t.Fatalf("expected NULL, got %s", p.State())
		}
		p.events.Close()
	})
}

func TestEventBus(t *testing.T) {
	t.Run("post and receive", func(t *testing.T) {
		bus := NewEventBus(16)
		defer bus.Close()

		bus.Post(EOS{})
		select {
		case e := <-bus.Chan():
			if _, ok := e.(EOS); !ok {
				t.Fatalf("expected EOS, got %T", e)
			}
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for event")
		}
	})

	t.Run("drop on full", func(t *testing.T) {
		bus := NewEventBus(1)
		defer bus.Close()

		bus.Post(EOS{})
		bus.Post(EOS{}) // should be dropped
		bus.Post(EOS{}) // should be dropped

		if bus.Dropped() < 1 {
			t.Fatal("expected at least 1 dropped event")
		}
	})
}

// collectEvents returns the event channel for a pipeline.
func collectEvents(p *Pipeline, _ int) <-chan Event {
	return p.Events()
}

// drainEvents reads up to n events from a channel with a timeout.
func drainEvents(ch <-chan Event, n int, timeout time.Duration) []Event {
	var out []Event
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for len(out) < n {
		select {
		case e, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, e)
		case <-timer.C:
			return out
		}
	}
	return out
}

func TestRunCompatibility(t *testing.T) {
	// Verify that Run() still works as a convenience method
	// (we can't actually run a real pipeline without test media,
	// but we can test that it fails correctly for a missing input).
	cfg := &Config{
		SchemaVersion: "1.0",
		Inputs:        []Input{{ID: "in", URL: "/nonexistent/file.mp4", Streams: []StreamSelect{{Type: "video"}}}},
		Outputs:       []Output{{ID: "out", URL: "/tmp/test_out.mp4", CodecVideo: "libx264"}},
	}
	p, err := NewPipeline(cfg)
	if err != nil {
		t.Fatal(err)
	}
	err = p.Run(context.Background())
	if err == nil {
		t.Fatal("expected error for nonexistent input")
	}
	// After Run returns the pipeline should be in NULL state.
	if p.State() != StateNull {
		t.Fatalf("expected NULL after Run error, got %s", p.State())
	}
}

func TestSeek(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.0",
		Inputs:        []Input{{ID: "in", URL: "dummy.mp4", Streams: []StreamSelect{{Type: "video"}}}},
		Outputs:       []Output{{ID: "out", URL: "dummy_out.mp4", CodecVideo: "libx264"}},
	}

	t.Run("seek from NULL fails", func(t *testing.T) {
		p, err := NewPipeline(cfg)
		if err != nil {
			t.Skipf("NewPipeline: %v", err)
		}
		defer p.Close()
		if err := p.SeekTo(1000000); err == nil {
			t.Fatal("expected error seeking from NULL")
		}
	})

	t.Run("seek from PAUSED stores target", func(t *testing.T) {
		p, err := NewPipeline(cfg)
		if err != nil {
			t.Skipf("NewPipeline: %v", err)
		}
		defer p.Close()
		if err := p.SetState(StatePaused); err != nil {
			t.Fatal(err)
		}
		if err := p.SeekTo(5000000); err != nil {
			t.Fatalf("Seek: %v", err)
		}
		target, pending := p.seekState()
		if !pending {
			t.Fatal("expected seek to be pending")
		}
		if target != 5000000 {
			t.Fatalf("seek target = %d, want 5000000", target)
		}
		// Second call resets.
		_, pending = p.seekState()
		if pending {
			t.Fatal("expected seek pending to be cleared")
		}
	})
}

func TestMetrics(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.0",
		Inputs:        []Input{{ID: "in", URL: "dummy.mp4", Streams: []StreamSelect{{Type: "video"}}}},
		Outputs:       []Output{{ID: "out", URL: "dummy_out.mp4", CodecVideo: "libx264"}},
	}
	p, err := NewPipeline(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	// Record some metrics.
	m := p.Metrics().Node("demux")
	m.Frames.Add(100)
	m.Bytes.Add(1024 * 1024)
	m.Errors.Add(1)

	p.Metrics().Node("encode").Frames.Add(95)

	// Take a snapshot.
	snap := p.GetMetrics()
	if snap.State != "NULL" {
		t.Errorf("state = %q, want NULL", snap.State)
	}
	if len(snap.Nodes) != 2 {
		t.Fatalf("len(Nodes) = %d, want 2", len(snap.Nodes))
	}

	// Find the demux node.
	var found bool
	for _, ns := range snap.Nodes {
		if ns.NodeID == "demux" {
			found = true
			if ns.Frames != 100 {
				t.Errorf("demux frames = %d, want 100", ns.Frames)
			}
			if ns.Bytes != 1024*1024 {
				t.Errorf("demux bytes = %d, want %d", ns.Bytes, 1024*1024)
			}
			if ns.Errors != 1 {
				t.Errorf("demux errors = %d, want 1", ns.Errors)
			}
		}
	}
	if !found {
		t.Fatal("demux node not found in metrics")
	}
}
