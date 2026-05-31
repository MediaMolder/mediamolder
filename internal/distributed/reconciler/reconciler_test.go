// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package reconciler_test

import (
	"context"
	"testing"
	"time"

	"github.com/MediaMolder/MediaMolder/internal/distributed/queue"
	"github.com/MediaMolder/MediaMolder/internal/distributed/reconciler"
	"github.com/MediaMolder/MediaMolder/internal/distributed/state"
	"github.com/MediaMolder/MediaMolder/pipeline"
)

func makeStore(t *testing.T) state.Store {
	t.Helper()
	st, err := state.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func seedJob(t *testing.T, st state.Store, policy pipeline.JobPolicy) string {
	t.Helper()
	ctx := context.Background()
	job := pipeline.Job{
		ID:     "job-1",
		Policy: policy,
	}
	if err := st.CreateJob(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}
	return job.ID
}

func seedExpiredTask(t *testing.T, st state.Store, jobID, taskID string, attempt int) {
	t.Helper()
	ctx := context.Background()
	task := pipeline.Task{
		ID:      taskID,
		JobID:   jobID,
		Attempt: attempt,
	}
	// Insert as running with lease already expired.
	if err := st.UpsertTask(ctx, task, state.TaskStatusRunning); err != nil {
		t.Fatalf("upsert task: %v", err)
	}
	expired := time.Now().Add(-2 * time.Minute)
	if err := st.RenewTaskLease(ctx, taskID, expired); err != nil {
		t.Fatalf("renew lease to past: %v", err)
	}
}

// TestReconciler_ReenqueuesExpiredTask verifies that an expired task with
// remaining attempts is re-enqueued with an incremented attempt counter.
func TestReconciler_ReenqueuesExpiredTask(t *testing.T) {
	st := makeStore(t)
	q := queue.NewInMemory()
	ctx := context.Background()

	jobID := seedJob(t, st, pipeline.JobPolicy{MaxAttempts: 3})
	seedExpiredTask(t, st, jobID, "task-1", 0)

	r := reconciler.New(st, q, time.Hour)
	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// The queue should have received the retry task.
	if n, err := q.Len(ctx); err != nil {
		t.Fatalf("q.Len: %v", err)
	} else if n != 1 {
		t.Fatalf("expected 1 queued retry, got %d", n)
	}

	// The new task should have attempt=1.
	ctx2, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	lease, err := q.Receive(ctx2, queue.ReceiveFilter{})
	if err != nil {
		t.Fatalf("q.Receive: %v", err)
	}
	received := lease.Task
	if received.Attempt != 1 {
		t.Errorf("retry attempt = %d, want 1", received.Attempt)
	}
	if received.ID == "task-1" {
		t.Errorf("retry should have a new ID, got task-1")
	}
}

// TestReconciler_DeadLettersExhaustedTask verifies that a task at maxAttempts
// is moved to the DLQ instead of being re-enqueued.
func TestReconciler_DeadLettersExhaustedTask(t *testing.T) {
	st := makeStore(t)
	q := queue.NewInMemory()
	ctx := context.Background()

	jobID := seedJob(t, st, pipeline.JobPolicy{MaxAttempts: 2})
	seedExpiredTask(t, st, jobID, "task-2", 1) // attempt=1, maxAttempts=2 → exhausted

	r := reconciler.New(st, q, time.Hour)
	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// Nothing should be in the queue.
	if n, err := q.Len(ctx); err != nil {
		t.Fatalf("q.Len: %v", err)
	} else if n != 0 {
		t.Errorf("expected 0 queued tasks after DLQ, got %d", n)
	}

	// The DLQ should have one record.
	dlq, err := st.ListDeadLetterTasks(ctx, jobID)
	if err != nil {
		t.Fatalf("ListDeadLetterTasks: %v", err)
	}
	if len(dlq) != 1 {
		t.Fatalf("expected 1 DLQ record, got %d", len(dlq))
	}
	if dlq[0].TaskID != "task-2" {
		t.Errorf("DLQ task ID = %q, want task-2", dlq[0].TaskID)
	}
}

// TestReconciler_IgnoresHealthyTasks verifies that tasks with future leases
// are left untouched.
func TestReconciler_IgnoresHealthyTasks(t *testing.T) {
	st := makeStore(t)
	q := queue.NewInMemory()
	ctx := context.Background()

	jobID := seedJob(t, st, pipeline.JobPolicy{MaxAttempts: 3})
	task := pipeline.Task{ID: "task-live", JobID: jobID}
	if err := st.UpsertTask(ctx, task, state.TaskStatusRunning); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Future lease — should NOT be picked up.
	future := time.Now().Add(10 * time.Minute)
	if err := st.RenewTaskLease(ctx, "task-live", future); err != nil {
		t.Fatalf("renew lease: %v", err)
	}

	r := reconciler.New(st, q, time.Hour)
	if err := r.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if n, err := q.Len(ctx); err != nil {
		t.Fatalf("q.Len: %v", err)
	} else if n != 0 {
		t.Errorf("expected 0 queued tasks, got %d", n)
	}
}
