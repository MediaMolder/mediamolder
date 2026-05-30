// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package orchestrator_test

import (
	"context"
	"testing"
	"time"

	"github.com/MediaMolder/MediaMolder/internal/distributed/orchestrator"
	"github.com/MediaMolder/MediaMolder/internal/distributed/queue"
	"github.com/MediaMolder/MediaMolder/internal/distributed/state"
	"github.com/MediaMolder/MediaMolder/pipeline"
)

func newTestOrch(t *testing.T) (*orchestrator.Orchestrator, queue.Queue, state.Store) {
	t.Helper()
	st, err := state.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	q := queue.NewInMemory()
	orch := orchestrator.New(st, q)
	return orch, q, st
}

func baseJob() *pipeline.Job {
	return &pipeline.Job{
		SchemaVersion: pipeline.JobSchemaVersion,
		Config: pipeline.Config{
			SchemaVersion: "1.0",
		},
	}
}

// ---- Undistributed (single-task) job ---------------------------------------

func TestAcceptJob_NoDistribution_EnqueuesOneTask(t *testing.T) {
	orch, q, _ := newTestOrch(t)
	ctx := context.Background()

	job := baseJob()
	id, err := orch.AcceptJob(ctx, job)
	if err != nil {
		t.Fatalf("AcceptJob: %v", err)
	}
	if id == "" {
		t.Fatal("want non-empty job id")
	}

	tctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	lease, err := q.Receive(tctx, queue.ReceiveFilter{})
	if err != nil {
		t.Fatalf("no task in queue: %v", err)
	}
	if lease.Task.JobID != id {
		t.Fatalf("task job_id mismatch: want %q got %q", id, lease.Task.JobID)
	}
}

func TestOnTaskCompleted_NoDistribution_MarksJobSucceeded(t *testing.T) {
	orch, q, st := newTestOrch(t)
	ctx := context.Background()

	job := baseJob()
	id, _ := orch.AcceptJob(ctx, job)

	tctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	lease, _ := q.Receive(tctx, queue.ReceiveFilter{})
	_ = q.Ack(ctx, lease.Task.ID)

	result := pipeline.TaskResult{
		StartedAt:  time.Now().Add(-time.Second),
		FinishedAt: time.Now(),
	}
	if err := orch.OnTaskCompleted(ctx, lease.Task.ID, result); err != nil {
		t.Fatalf("OnTaskCompleted: %v", err)
	}

	_, statusRec, err := st.GetJob(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if statusRec.Status != state.JobStatusSucceeded {
		t.Fatalf("want succeeded, got %s", statusRec.Status)
	}
}

func TestOnTaskCompleted_TaskError_MarksJobFailed(t *testing.T) {
	orch, q, st := newTestOrch(t)
	ctx := context.Background()

	job := baseJob()
	job.Policy.MaxAttempts = 1 // no retries
	id, _ := orch.AcceptJob(ctx, job)

	tctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	lease, _ := q.Receive(tctx, queue.ReceiveFilter{})
	_ = q.Nack(ctx, lease.Task.ID, 0)

	result := pipeline.TaskResult{
		Error:      "pipeline failed",
		StartedAt:  time.Now().Add(-time.Second),
		FinishedAt: time.Now(),
	}
	if err := orch.OnTaskCompleted(ctx, lease.Task.ID, result); err != nil {
		t.Fatalf("OnTaskCompleted: %v", err)
	}

	_, statusRec, _ := st.GetJob(ctx, id)
	if statusRec.Status != state.JobStatusFailed {
		t.Fatalf("want failed, got %s", statusRec.Status)
	}
}

// ---- Distributed: single strategy -----------------------------------------

func TestAcceptJob_SingleStrategy_EnqueuesOneTask(t *testing.T) {
	orch, q, _ := newTestOrch(t)
	ctx := context.Background()

	job := baseJob()
	job.Distribution = &pipeline.DistributionSpec{
		Stages: []pipeline.Stage{
			{
				ID:       "encode",
				Strategy: pipeline.StageStrategy{Kind: "single"},
			},
		},
	}
	_, err := orch.AcceptJob(ctx, job)
	if err != nil {
		t.Fatalf("AcceptJob: %v", err)
	}

	tctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	lease, err := q.Receive(tctx, queue.ReceiveFilter{})
	if err != nil {
		t.Fatalf("no task: %v", err)
	}
	if lease.Task.StageID != "encode" {
		t.Fatalf("want stage encode, got %q", lease.Task.StageID)
	}
	if lease.Task.Index != 0 || lease.Task.Total != 1 {
		t.Fatalf("want index=0 total=1, got index=%d total=%d", lease.Task.Index, lease.Task.Total)
	}
}

// ---- Distributed: fanout_static strategy -----------------------------------

func TestAcceptJob_FanoutStatic_EnqueuesNTasks(t *testing.T) {
	orch, q, _ := newTestOrch(t)
	ctx := context.Background()

	job := baseJob()
	job.Distribution = &pipeline.DistributionSpec{
		Stages: []pipeline.Stage{
			{
				ID:       "encode",
				Strategy: pipeline.StageStrategy{Kind: "fanout_static", Params: map[string]any{"count": float64(4)}},
			},
		},
	}
	_, err := orch.AcceptJob(ctx, job)
	if err != nil {
		t.Fatalf("AcceptJob: %v", err)
	}

	for i := 0; i < 4; i++ {
		tctx, cancel := context.WithTimeout(ctx, time.Second)
		lease, err := q.Receive(tctx, queue.ReceiveFilter{})
		cancel()
		if err != nil {
			t.Fatalf("receive task %d: %v", i, err)
		}
		if lease.Task.Total != 4 {
			t.Fatalf("task %d: want Total=4, got %d", i, lease.Task.Total)
		}
	}
}

func TestAcceptJob_FanoutStatic_MissingCount_Errors(t *testing.T) {
	orch, _, _ := newTestOrch(t)
	ctx := context.Background()

	job := baseJob()
	job.Distribution = &pipeline.DistributionSpec{
		Stages: []pipeline.Stage{
			{
				ID:       "encode",
				Strategy: pipeline.StageStrategy{Kind: "fanout_static"}, // no params.count
			},
		},
	}
	_, err := orch.AcceptJob(ctx, job)
	if err == nil {
		t.Fatal("expected error for missing params.count")
	}
}

// ---- Stage chaining --------------------------------------------------------

func TestStageChaining_SecondStageEnqueuedAfterFirst(t *testing.T) {
	orch, q, st := newTestOrch(t)
	ctx := context.Background()

	job := baseJob()
	job.Distribution = &pipeline.DistributionSpec{
		Stages: []pipeline.Stage{
			{ID: "a", Strategy: pipeline.StageStrategy{Kind: "single"}},
			{ID: "b", DependsOn: []string{"a"}, Strategy: pipeline.StageStrategy{Kind: "single"}},
		},
	}
	id, _ := orch.AcceptJob(ctx, job)

	// Only stage a should be enqueued initially.
	n, _ := q.Len(ctx)
	if n != 1 {
		t.Fatalf("want 1 task initially, got %d", n)
	}

	// Receive and complete stage a.
	tctx, cancel := context.WithTimeout(ctx, time.Second)
	leaseA, _ := q.Receive(tctx, queue.ReceiveFilter{})
	cancel()
	_ = q.Ack(ctx, leaseA.Task.ID)
	_ = orch.OnTaskCompleted(ctx, leaseA.Task.ID, pipeline.TaskResult{
		StartedAt: time.Now(), FinishedAt: time.Now(),
	})

	// Stage b should now be enqueued.
	tctx2, cancel2 := context.WithTimeout(ctx, time.Second)
	leaseB, err := q.Receive(tctx2, queue.ReceiveFilter{})
	cancel2()
	if err != nil {
		t.Fatalf("stage b not enqueued: %v", err)
	}
	if leaseB.Task.StageID != "b" {
		t.Fatalf("want stage b, got %q", leaseB.Task.StageID)
	}

	// Complete stage b.
	_ = q.Ack(ctx, leaseB.Task.ID)
	_ = orch.OnTaskCompleted(ctx, leaseB.Task.ID, pipeline.TaskResult{
		StartedAt: time.Now(), FinishedAt: time.Now(),
	})

	_, statusRec, _ := st.GetJob(ctx, id)
	if statusRec.Status != state.JobStatusSucceeded {
		t.Fatalf("want succeeded, got %s", statusRec.Status)
	}
}

// ---- Validation -----------------------------------------------------------

func TestAcceptJob_InvalidSchemaVersion(t *testing.T) {
	orch, _, _ := newTestOrch(t)
	ctx := context.Background()

	job := baseJob()
	job.SchemaVersion = "1.0"
	_, err := orch.AcceptJob(ctx, job)
	if err == nil {
		t.Fatal("expected validation error for wrong schema_version")
	}
}

func TestAcceptJob_UnknownDependsOn_Errors(t *testing.T) {
	orch, _, _ := newTestOrch(t)
	ctx := context.Background()

	job := baseJob()
	job.Distribution = &pipeline.DistributionSpec{
		Stages: []pipeline.Stage{
			{ID: "a", DependsOn: []string{"nonexistent"}, Strategy: pipeline.StageStrategy{Kind: "single"}},
		},
	}
	_, err := orch.AcceptJob(ctx, job)
	if err == nil {
		t.Fatal("expected error for unknown depends_on")
	}
}

// ---- CancelJob -------------------------------------------------------------

func TestCancelJob(t *testing.T) {
	orch, _, st := newTestOrch(t)
	ctx := context.Background()

	job := baseJob()
	id, _ := orch.AcceptJob(ctx, job)

	if err := orch.CancelJob(ctx, id); err != nil {
		t.Fatal(err)
	}

	_, statusRec, _ := st.GetJob(ctx, id)
	if statusRec.Status != state.JobStatusCancelled {
		t.Fatalf("want cancelled, got %s", statusRec.Status)
	}
}
