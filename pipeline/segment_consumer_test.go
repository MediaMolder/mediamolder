// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MediaMolder/MediaMolder/processors"
)

type stubSegmentConsumer struct {
	mu     sync.Mutex
	events []processors.SegmentEvent
	done   chan struct{}
}

func (s *stubSegmentConsumer) OnSegmentCompleted(_ context.Context, ev processors.SegmentEvent) {
	s.mu.Lock()
	s.events = append(s.events, ev)
	if s.done != nil {
		close(s.done)
		s.done = nil
	}
	s.mu.Unlock()
}

// TestDispatchSegmentCompleted verifies that handlers_sink's
// dispatchSegmentCompleted helper invokes every registered consumer in a
// goroutine and that consumers see the original SegmentEvent payload.
func TestDispatchSegmentCompleted(t *testing.T) {
	r := newGraphRunner(&Config{}, nil)
	c1 := &stubSegmentConsumer{done: make(chan struct{})}
	c2 := &stubSegmentConsumer{done: make(chan struct{})}
	r.segmentConsumers["sink1"] = []processors.SegmentEventConsumer{c1, c2}

	r.dispatchSegmentCompleted(context.Background(), "sink1", "out1", "/tmp/seg0.mp4", 0)

	for _, ch := range []chan struct{}{c1.done, c2.done} {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatal("consumer not called within 1s")
		}
	}

	for i, c := range []*stubSegmentConsumer{c1, c2} {
		c.mu.Lock()
		if len(c.events) != 1 {
			t.Errorf("consumer %d: got %d events, want 1", i, len(c.events))
		} else {
			ev := c.events[0]
			if ev.OutputID != "out1" || ev.FilePath != "/tmp/seg0.mp4" || ev.SegmentIndex != 0 {
				t.Errorf("consumer %d: unexpected payload %+v", i, ev)
			}
		}
		c.mu.Unlock()
	}
}

// TestDispatchSegmentCompleted_NoConsumers is a no-op safety check.
func TestDispatchSegmentCompleted_NoConsumers(t *testing.T) {
	r := newGraphRunner(&Config{}, nil)
	r.dispatchSegmentCompleted(context.Background(), "absent", "out", "f", 0)
}

// TestDispatchSegmentCompleted_Concurrent ensures dispatch is non-blocking:
// even with a slow consumer, dispatch returns immediately and the consumer
// runs in its own goroutine.
func TestDispatchSegmentCompleted_Concurrent(t *testing.T) {
	r := newGraphRunner(&Config{}, nil)
	var entered int32
	gate := make(chan struct{})
	slow := slowConsumer{enter: &entered, release: gate}
	r.segmentConsumers["s"] = []processors.SegmentEventConsumer{slow}

	start := time.Now()
	r.dispatchSegmentCompleted(context.Background(), "s", "o", "f", 0)
	if d := time.Since(start); d > 50*time.Millisecond {
		t.Errorf("dispatch blocked for %v, expected to be non-blocking", d)
	}
	// Wait for the consumer goroutine to actually enter, then release it.
	deadline := time.After(time.Second)
	for atomic.LoadInt32(&entered) == 0 {
		select {
		case <-deadline:
			t.Fatal("consumer did not run")
		case <-time.After(time.Millisecond):
		}
	}
	close(gate)
}

type slowConsumer struct {
	enter   *int32
	release chan struct{}
}

func (s slowConsumer) OnSegmentCompleted(_ context.Context, _ processors.SegmentEvent) {
	atomic.AddInt32(s.enter, 1)
	<-s.release
}
