// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

// Package reconciler implements a leaderless background loop that detects
// orphaned tasks (running but lease expired) and either re-enqueues them
// or moves them to the dead-letter queue.
//
// When the backing Store implements state.ReconcilerLocker (e.g.
// PostgresStore), every reconciler instance races for the Postgres advisory
// lock; only the winner performs work during that cycle, avoiding double
// re-enqueue. SQLite-backed stores are assumed to be single-instance and
// skip the lock entirely.
package reconciler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/MediaMolder/MediaMolder/internal/distributed/orchestrator"
	"github.com/MediaMolder/MediaMolder/internal/distributed/queue"
	"github.com/MediaMolder/MediaMolder/internal/distributed/state"
)

// DefaultInterval is the reconciliation cycle period when the caller does not
// provide one.
const DefaultInterval = 30 * time.Second

// Reconciler scans for expired leases and re-enqueues or dead-letters tasks.
type Reconciler struct {
	store    state.Store
	queue    queue.Queue
	interval time.Duration
}

// New creates a Reconciler. interval <= 0 uses DefaultInterval.
func New(st state.Store, q queue.Queue, interval time.Duration) *Reconciler {
	if interval <= 0 {
		interval = DefaultInterval
	}
	return &Reconciler{store: st, queue: q, interval: interval}
}

// RunOnce executes a single reconciliation cycle synchronously. It is used
// primarily in tests.
func (r *Reconciler) RunOnce(ctx context.Context) error {
	return r.reconcile(ctx)
}

// Run blocks until ctx is cancelled, running a reconciliation cycle every
// r.interval. It always returns a non-nil error (ctx.Err() when cancelled).
func (r *Reconciler) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := r.reconcile(ctx); err != nil {
				// Log and continue; a single failed cycle is not fatal.
				fmt.Printf("reconciler: cycle error: %v\n", err)
			}
		}
	}
}

// reconcile performs one reconciliation pass. It acquires an advisory lock
// when available, then re-enqueues or dead-letters every expired task.
func (r *Reconciler) reconcile(ctx context.Context) error {
	// Try to acquire exclusive coordination lock (Postgres only).
	var release func(context.Context)
	if locker, ok := r.store.(state.ReconcilerLocker); ok {
		rel, acquired, err := locker.TryReconcilerLock(ctx)
		if err != nil {
			return fmt.Errorf("advisory lock: %w", err)
		}
		if !acquired {
			return nil // another instance is reconciling
		}
		release = rel
		defer release(ctx)
	}

	expired, err := r.store.ListExpiredLeases(ctx)
	if err != nil {
		return fmt.Errorf("list expired leases: %w", err)
	}

	for _, rec := range expired {
		if err := r.handleExpired(ctx, rec); err != nil {
			// Non-fatal per-task error; log and continue.
			fmt.Printf("reconciler: handle expired task %s: %v\n", rec.Task.ID, err)
		}
	}
	return nil
}

// handleExpired decides whether to re-enqueue or dead-letter an expired task.
func (r *Reconciler) handleExpired(ctx context.Context, rec state.TaskRecord) error {
	job, _, err := r.store.GetJob(ctx, rec.Task.JobID)
	if err != nil {
		return fmt.Errorf("get job %s: %w", rec.Task.JobID, err)
	}

	maxAttempts := job.Policy.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = orchestrator.DefaultMaxAttempts
	}

	if rec.Task.Attempt+1 >= maxAttempts {
		if err := r.store.DeadLetterTask(ctx, rec.Task.ID, "max_attempts_exceeded_lease_expired"); err != nil {
			return fmt.Errorf("dead-letter task %s: %w", rec.Task.ID, err)
		}
		rec2 := state.JobStatusRecord{
			Status:    state.JobStatusFailed,
			Error:     fmt.Sprintf("task %s dead-lettered after %d attempts", rec.Task.ID, maxAttempts),
			UpdatedAt: time.Now(),
		}
		_ = r.store.UpdateJobStatus(ctx, rec.Task.JobID, rec2)
		return nil
	}

	// Re-enqueue with a fresh ID and incremented attempt counter.
	retry := rec.Task
	retry.ID = newID()
	retry.Attempt++
	retry.LeaseUntil = time.Time{}
	if err := r.store.UpsertTask(ctx, retry, state.TaskStatusPending); err != nil {
		return fmt.Errorf("upsert retry task: %w", err)
	}
	if err := r.queue.Publish(ctx, retry); err != nil {
		return fmt.Errorf("publish retry task %s: %w", retry.ID, err)
	}
	return nil
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
