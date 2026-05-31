// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

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
// dispatchSegmentCompleted helper invokes every registered consumer and that
// consumers see the original SegmentEvent payload.
func TestDispatchSegmentCompleted(t *testing.T) {
	r := newGraphRunner(&Config{}, nil)
	c1 := &stubSegmentConsumer{done: make(chan struct{})}
	c2 := &stubSegmentConsumer{done: make(chan struct{})}
	r.segmentConsumers["sink1"] = []processors.SegmentEventConsumer{c1, c2}

	// Capture channel references before dispatch: synchronous dispatch
	// closes the channels (and sets the struct fields to nil) before returning.
	done1, done2 := c1.done, c2.done

	r.dispatchSegmentCompleted(context.Background(), "sink1", "out1", "/tmp/seg0.mp4", 0)

	for i, ch := range []chan struct{}{done1, done2} {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatalf("consumer %d not called within 1s", i)
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
// OnSegmentCompleted implementations are contractually required to be
// non-blocking (register via wg.Add then launch a goroutine internally).
// dispatchSegmentCompleted calls them synchronously, which is safe because
// each implementation returns immediately — and eliminates the WaitGroup race
// that arose when dispatch wrapped calls in untracked goroutines.
func TestDispatchSegmentCompleted_Concurrent(t *testing.T) {
	r := newGraphRunner(&Config{}, nil)
	var processed int32
	gate := make(chan struct{})
	slow := goroutineConsumer{processed: &processed, release: gate}
	r.segmentConsumers["s"] = []processors.SegmentEventConsumer{slow}

	start := time.Now()
	r.dispatchSegmentCompleted(context.Background(), "s", "o", "f", 0)
	if d := time.Since(start); d > 50*time.Millisecond {
		t.Errorf("dispatch blocked for %v; OnSegmentCompleted must be non-blocking", d)
	}
	// Release the internal goroutine and verify it ran.
	close(gate)
	deadline := time.After(time.Second)
	for atomic.LoadInt32(&processed) == 0 {
		select {
		case <-deadline:
			t.Fatal("consumer goroutine did not complete")
		case <-time.After(time.Millisecond):
		}
	}
}

// goroutineConsumer models a well-behaved SegmentEventConsumer: OnSegmentCompleted
// is non-blocking — it launches a goroutine for the slow work and returns immediately.
type goroutineConsumer struct {
	processed *int32
	release   chan struct{}
}

func (s goroutineConsumer) OnSegmentCompleted(_ context.Context, _ processors.SegmentEvent) {
	go func() {
		<-s.release
		atomic.AddInt32(s.processed, 1)
	}()
}
