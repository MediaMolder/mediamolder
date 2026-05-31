// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

// Package queue defines the task-queue abstraction used by orchestrators and
// workers. Phase B provides an in-memory adapter; Phase C will add SQS, NATS,
// and Postgres LISTEN/NOTIFY adapters.
package queue

import (
	"context"
	"strings"
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
	// Region is the deployment region this worker is in (e.g. "us-east-1").
	// Empty means "accept tasks regardless of region requirement".
	Region string
}

// TaskSatisfiedBy reports whether task t can be executed by a worker
// described by filter f. It is used by queue adapters to skip tasks whose
// requirements the current worker cannot meet.
func TaskSatisfiedBy(t pipeline.Task, f ReceiveFilter) bool {
	if f.Region != "" && t.Requires.Region != "" && f.Region != t.Requires.Region {
		return false
	}
	// Build a set from the worker's advertised capabilities.
	have := make(map[string]bool, len(f.Capabilities))
	for _, c := range f.Capabilities {
		have[strings.ToLower(c)] = true
	}
	for _, req := range t.Requires.HardwareAccel {
		if !have[strings.ToLower(req)] {
			return false
		}
	}
	for _, req := range t.Requires.Codecs {
		if !have[strings.ToLower(req)] {
			return false
		}
	}
	return true
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
