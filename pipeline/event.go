package pipeline

import (
	"context"
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

// BufferingPercent is emitted to report the buffering level of a node (0.0–1.0).
type BufferingPercent struct {
	NodeID  string
	Percent float64
	Time    time.Time
}

func (BufferingPercent) eventTag() {}

// MetricsSnapshotEvent is emitted periodically with a point-in-time metrics snapshot.
type MetricsSnapshotEvent struct {
	Snapshot MetricsSnapshot
	Time     time.Time
}

func (MetricsSnapshotEvent) eventTag() {}

// ClockLost is emitted when the pipeline clock source becomes unavailable.
type ClockLost struct {
	Reason string
	Time   time.Time
}

func (ClockLost) eventTag() {}

// MetricsEmitter periodically posts MetricsSnapshotEvent to the event bus.
type MetricsEmitter struct {
	interval time.Duration
	registry *MetricsRegistry
	events   *EventBus
	getState func() State
	cancel   context.CancelFunc
	done     chan struct{}
}

// NewMetricsEmitter creates an emitter that fires every interval.
// If interval <= 0, the default of 5s is used.
func NewMetricsEmitter(interval time.Duration, registry *MetricsRegistry, events *EventBus, getState func() State) *MetricsEmitter {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &MetricsEmitter{
		interval: interval,
		registry: registry,
		events:   events,
		getState: getState,
	}
}

// Start begins emitting periodic metrics snapshots.
func (m *MetricsEmitter) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.done = make(chan struct{})
	go func() {
		defer close(m.done)
		ticker := time.NewTicker(m.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				snap := m.registry.Snapshot()
				snap.State = m.getState().String()
				m.events.Post(MetricsSnapshotEvent{
					Snapshot: snap,
					Time:     time.Now(),
				})
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Stop halts the periodic emitter.
func (m *MetricsEmitter) Stop() {
	if m.cancel != nil {
		m.cancel()
		<-m.done
	}
}

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
