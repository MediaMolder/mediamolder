// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/MediaMolder/MediaMolder/internal/distributed/state"
	"github.com/MediaMolder/MediaMolder/pipeline"
)

func openTestStore(t *testing.T) state.Store {
	t.Helper()
	st, err := state.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func sampleJob() pipeline.Job {
	return pipeline.Job{
		SchemaVersion: pipeline.JobSchemaVersion,
		ID:            "job-1",
		Name:          "test-job",
		Config: pipeline.Config{
			SchemaVersion: "1.0",
		},
	}
}

func sampleTask(jobID, stageID, taskID string, index int) pipeline.Task {
	return pipeline.Task{
		ID:      taskID,
		JobID:   jobID,
		StageID: stageID,
		Index:   index,
		Total:   2,
	}
}

func TestSQLite_CreateAndGetJob(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	j := sampleJob()
	if err := st.CreateJob(ctx, j); err != nil {
		t.Fatal(err)
	}

	got, statusRec, err := st.GetJob(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != j.ID {
		t.Fatalf("want id %q, got %q", j.ID, got.ID)
	}
	if statusRec.Status != state.JobStatusQueued {
		t.Fatalf("want queued, got %s", statusRec.Status)
	}
}

func TestSQLite_UpdateJobStatus(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	j := sampleJob()
	_ = st.CreateJob(ctx, j)

	rec := state.JobStatusRecord{
		Status:    state.JobStatusRunning,
		UpdatedAt: time.Now(),
	}
	if err := st.UpdateJobStatus(ctx, j.ID, rec); err != nil {
		t.Fatal(err)
	}

	_, got, _ := st.GetJob(ctx, j.ID)
	if got.Status != state.JobStatusRunning {
		t.Fatalf("want running, got %s", got.Status)
	}
}

func TestSQLite_UpsertAndGetTask(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	_ = st.CreateJob(ctx, sampleJob())

	task := sampleTask("job-1", "stage-a", "task-1", 0)
	if err := st.UpsertTask(ctx, task, state.TaskStatusPending); err != nil {
		t.Fatal(err)
	}

	rec, err := st.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Task.ID != task.ID {
		t.Fatalf("want task %q, got %q", task.ID, rec.Task.ID)
	}
	if rec.Status != state.TaskStatusPending {
		t.Fatalf("want pending, got %s", rec.Status)
	}
}

func TestSQLite_SetTaskResult(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	_ = st.CreateJob(ctx, sampleJob())
	task := sampleTask("job-1", "stage-a", "task-2", 0)
	_ = st.UpsertTask(ctx, task, state.TaskStatusPending)

	result := pipeline.TaskResult{
		StartedAt:  time.Now().Add(-time.Second),
		FinishedAt: time.Now(),
	}
	if err := st.SetTaskResult(ctx, task.ID, result); err != nil {
		t.Fatal(err)
	}

	rec, err := st.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != state.TaskStatusSucceeded {
		t.Fatalf("want succeeded, got %s", rec.Status)
	}
	if rec.Result == nil {
		t.Fatal("want non-nil result")
	}
}

func TestSQLite_TasksByStage(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	_ = st.CreateJob(ctx, sampleJob())
	for i, id := range []string{"t-a1", "t-a2"} {
		_ = st.UpsertTask(ctx, sampleTask("job-1", "stage-a", id, i), state.TaskStatusPending)
	}
	_ = st.UpsertTask(ctx, sampleTask("job-1", "stage-b", "t-b1", 0), state.TaskStatusPending)

	recs, err := st.TasksByStage(ctx, "job-1", "stage-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("want 2 tasks for stage-a, got %d", len(recs))
	}
}

func TestSQLite_AppendAndListEvents(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	_ = st.CreateJob(ctx, sampleJob())

	for _, typ := range []string{"JobAccepted", "TaskScheduled", "TaskCompleted"} {
		_ = st.AppendEvent(ctx, state.JobEvent{
			JobID:    "job-1",
			Type:     typ,
			DataJSON: `{}`,
		})
	}

	evts, err := st.ListEvents(ctx, "job-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(evts) != 3 {
		t.Fatalf("want 3 events, got %d", len(evts))
	}

	// Cursor pagination.
	cursor := evts[1].ID
	tail, err := st.ListEvents(ctx, "job-1", cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != 1 {
		t.Fatalf("want 1 event after cursor, got %d", len(tail))
	}
	if tail[0].Type != "TaskCompleted" {
		t.Fatalf("want TaskCompleted, got %s", tail[0].Type)
	}
}

func TestSQLite_GetJobNotFound(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	_, _, err := st.GetJob(ctx, "does-not-exist")
	if err == nil {
		t.Fatal("expected error for missing job")
	}
}
