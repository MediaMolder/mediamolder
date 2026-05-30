// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package queue

import (
	"context"
	"sync"
	"time"

	"github.com/MediaMolder/MediaMolder/pipeline"
)

// pending holds a task together with the earliest time it may be delivered.
type pending struct {
	task      pipeline.Task
	notBefore time.Time
}

// leaseRecord retains the task so Nack can re-enqueue it.
type leaseRecord struct {
	task  pipeline.Task
	until time.Time
}

// InMemory is a development-grade in-process task queue. It is safe for
// concurrent use and supports retries with per-task delivery delays.
//
// Notification uses a close-and-replace pattern: Publish closes the current
// "notify" channel (waking any blocked Receive) and replaces it with a fresh
// one. Receive re-reads the notify channel under the mutex so it always waits
// on the channel that was current when it found no tasks.
//
// Delayed tasks (Nack with retryAfter > 0) are re-added to the pending list
// with a future notBefore; a 100 ms polling fallback in Receive handles them
// without a separate goroutine.
type InMemory struct {
	mu     sync.Mutex
	items  []pending
	notify chan struct{} // closed-and-replaced by Publish
	leases map[string]leaseRecord
}

// NewInMemory creates a new in-memory queue.
func NewInMemory() *InMemory {
	return &InMemory{
		notify: make(chan struct{}),
		leases: make(map[string]leaseRecord),
	}
}

// Publish enqueues a task for immediate delivery.
func (q *InMemory) Publish(_ context.Context, t pipeline.Task) error {
	q.mu.Lock()
	q.items = append(q.items, pending{task: t, notBefore: time.Now()})
	ch := q.notify
	q.notify = make(chan struct{})
	q.mu.Unlock()
	close(ch) // wake blocked Receive calls
	return nil
}

// Receive blocks until a task whose notBefore has elapsed is available,
// then returns a 30-second Lease. ctx cancellation is honoured.
func (q *InMemory) Receive(ctx context.Context, _ ReceiveFilter) (Lease, error) {
	for {
		q.mu.Lock()
		now := time.Now()
		for i, p := range q.items {
			if !now.Before(p.notBefore) {
				q.items = append(q.items[:i], q.items[i+1:]...)
				until := now.Add(30 * time.Second)
				q.leases[p.task.ID] = leaseRecord{task: p.task, until: until}
				q.mu.Unlock()
				return Lease{Task: p.task, LeaseUntil: until}, nil
			}
		}
		ch := q.notify
		q.mu.Unlock()

		// Wait for a new publish, a delayed-item check, or ctx cancellation.
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Lease{}, ctx.Err()
		case <-timer.C:
		case <-ch:
			timer.Stop()
		}
	}
}

// Heartbeat extends the lease for taskID.
func (q *InMemory) Heartbeat(_ context.Context, taskID string, extend time.Duration) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if rec, ok := q.leases[taskID]; ok {
		rec.until = time.Now().Add(extend)
		q.leases[taskID] = rec
	}
	return nil
}

// Ack removes the task from the queue after successful completion.
func (q *InMemory) Ack(_ context.Context, taskID string) error {
	q.mu.Lock()
	delete(q.leases, taskID)
	q.mu.Unlock()
	return nil
}

// Nack returns the task to the queue after retryAfter has elapsed.
func (q *InMemory) Nack(_ context.Context, taskID string, retryAfter time.Duration) error {
	q.mu.Lock()
	rec, ok := q.leases[taskID]
	if ok {
		delete(q.leases, taskID)
		q.items = append(q.items, pending{
			task:      rec.task,
			notBefore: time.Now().Add(retryAfter),
		})
	}
	q.mu.Unlock()
	return nil
}

// Len returns the number of tasks waiting for delivery (excludes leased tasks).
func (q *InMemory) Len(_ context.Context) (int, error) {
	q.mu.Lock()
	n := len(q.items)
	q.mu.Unlock()
	return n, nil
}
