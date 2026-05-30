// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

// Package queue defines the task-queue abstraction used by orchestrators and
// workers. Phase B provides an in-memory adapter; Phase C will add SQS, NATS,
// and Postgres LISTEN/NOTIFY adapters.
package queue

import (
	"context"
	"time"

	"github.com/MediaMolder/MediaMolder/pipeline"
)

// ReceiveFilter lets a worker restrict which tasks it will accept.
// An empty filter accepts any task.
type ReceiveFilter struct {
	// Capabilities is the set of capability strings the worker advertises.
	// A task is only delivered when the worker satisfies every entry in
	// Task.Requires.HardwareAccel and Task.Requires.Codecs.
	// Empty means "accept any task".
	Capabilities []string
}

// Lease wraps a task received from the queue together with its commit handle.
// The worker must call Ack or Nack before LeaseUntil; otherwise the task
// becomes re-deliverable to another worker.
type Lease struct {
	Task       pipeline.Task
	LeaseUntil time.Time
}

// Queue is the task-queue abstraction.
type Queue interface {
	// Publish enqueues a task for immediate delivery.
	Publish(ctx context.Context, t pipeline.Task) error

	// Receive blocks until a task that satisfies filter is available, then
	// returns a Lease. Blocks until ctx is cancelled when the queue is empty.
	Receive(ctx context.Context, filter ReceiveFilter) (Lease, error)

	// Heartbeat extends the lease for taskID by extend. Workers should call
	// this periodically while processing long tasks.
	Heartbeat(ctx context.Context, taskID string, extend time.Duration) error

	// Ack removes the task from the queue (successful completion).
	Ack(ctx context.Context, taskID string) error

	// Nack returns the task to the queue after retryAfter has elapsed.
	Nack(ctx context.Context, taskID string, retryAfter time.Duration) error

	// Len returns the count of tasks currently waiting for delivery
	// (excludes leased tasks).
	Len(ctx context.Context) (int, error)
}
