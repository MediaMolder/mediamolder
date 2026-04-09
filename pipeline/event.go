package pipeline

import (
	"sync"
	"sync/atomic"
	"time"
)

// Event is the interface implemented by all pipeline events.
type Event interface {
	eventTag()
}

// StateChanged is emitted on every pipeline state transition.
type StateChanged struct {
	From     State
	To       State
	Duration time.Duration // time spent transitioning
}

func (StateChanged) eventTag() {}

// ErrorEvent is emitted when a pipeline error occurs.
type ErrorEvent struct {
	NodeID string
	Stage  string
	Err    error
	Time   time.Time
}

func (ErrorEvent) eventTag() {}

// EOS is emitted when the pipeline reaches end of stream.
type EOS struct{}

func (EOS) eventTag() {}

// StreamStart is emitted when a stream begins producing data.
type StreamStart struct {
	NodeID    string
	MediaType string // "video", "audio", etc.
}

func (StreamStart) eventTag() {}

// BufferOverflow is emitted when the event channel is full and events are dropped.
type BufferOverflow struct {
	Dropped int64
}

func (BufferOverflow) eventTag() {}

// EventBus is a non-blocking, typed event bus for pipeline events.
// Events are posted via Post() and consumed via Chan().
// If the consumer is slow, events are dropped and counted.
type EventBus struct {
	ch      chan Event
	dropped atomic.Int64
	closed  atomic.Bool
	once    sync.Once
}

// NewEventBus creates an event bus with the given channel buffer size.
func NewEventBus(bufSize int) *EventBus {
	if bufSize <= 0 {
		bufSize = 256
	}
	return &EventBus{
		ch: make(chan Event, bufSize),
	}
}

// Post sends an event to the bus. Non-blocking: if the channel is full,
// the event is dropped and the drop counter is incremented.
// Safe to call after Close (becomes a no-op).
func (b *EventBus) Post(e Event) {
	if b.closed.Load() {
		return
	}
	select {
	case b.ch <- e:
	default:
		b.dropped.Add(1)
	}
}

// Chan returns the read-only event channel.
func (b *EventBus) Chan() <-chan Event {
	return b.ch
}

// Dropped returns the number of events dropped due to a full channel.
func (b *EventBus) Dropped() int64 {
	return b.dropped.Load()
}

// Close closes the event channel. Safe to call multiple times.
func (b *EventBus) Close() {
	b.once.Do(func() {
		b.closed.Store(true)
		close(b.ch)
	})
}
