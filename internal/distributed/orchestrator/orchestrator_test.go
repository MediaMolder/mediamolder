// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package orchestrator_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/MediaMolder/MediaMolder/internal/distributed/orchestrator"
	"github.com/MediaMolder/MediaMolder/internal/distributed/queue"
	"github.com/MediaMolder/MediaMolder/internal/distributed/state"
	"github.com/MediaMolder/MediaMolder/job"
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

func baseJob() *job.Job {
	return &job.Job{
		SchemaVersion: job.JobSchemaVersion,
		Config: job.Config{
			SchemaVersion: "1.0",
		},
	}
}

// ---- Undistributed (single-task) job ---------------------------------------

func TestAcceptJob_NoDistribution_EnqueuesOneTask(t *testing.T) {
	orch, q, _ := newTestOrch(t)
	ctx := context.Background()

	jb := baseJob()
	id, err := orch.AcceptJob(ctx, jb)
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

	jb := baseJob()
	id, _ := orch.AcceptJob(ctx, jb)

	tctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	lease, _ := q.Receive(tctx, queue.ReceiveFilter{})
	_ = q.Ack(ctx, lease.Task.ID)

	result := job.TaskResult{
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

	jb := baseJob()
	jb.Policy.MaxAttempts = 1 // no retries
	id, _ := orch.AcceptJob(ctx, jb)

	tctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	lease, _ := q.Receive(tctx, queue.ReceiveFilter{})
	_ = q.Nack(ctx, lease.Task.ID, 0)

	result := job.TaskResult{
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

	jb := baseJob()
	jb.Distribution = &job.DistributionSpec{
		Stages: []job.Stage{
			{
				ID:       "encode",
				Strategy: job.StageStrategy{Kind: "single"},
			},
		},
	}
	_, err := orch.AcceptJob(ctx, jb)
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

	jb := baseJob()
	jb.Distribution = &job.DistributionSpec{
		Stages: []job.Stage{
			{
				ID:       "encode",
				Strategy: job.StageStrategy{Kind: "fanout_static", Params: map[string]any{"count": float64(4)}},
			},
		},
	}
	_, err := orch.AcceptJob(ctx, jb)
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

// TestAcceptJob_FanoutStatic_RejectsHugeCount guards the DoS backstop: a
// remote job submission must not be able to drive an unbounded task
// allocation (each task deep-copies the full config). See orchestrator.MaxFanoutTasks.
func TestAcceptJob_FanoutStatic_RejectsHugeCount(t *testing.T) {
	orch, _, _ := newTestOrch(t)
	ctx := context.Background()

	jb := baseJob()
	jb.Distribution = &job.DistributionSpec{
		Stages: []job.Stage{
			{
				ID:       "encode",
				Strategy: job.StageStrategy{Kind: "fanout_static", Params: map[string]any{"count": float64(orchestrator.MaxFanoutTasks + 1)}},
			},
		},
	}
	if _, err := orch.AcceptJob(ctx, jb); err == nil {
		t.Fatalf("expected error for count > orchestrator.MaxFanoutTasks (%d)", orchestrator.MaxFanoutTasks)
	}
}

func TestAcceptJob_FanoutStatic_MissingCount_Errors(t *testing.T) {
	orch, _, _ := newTestOrch(t)
	ctx := context.Background()

	jb := baseJob()
	jb.Distribution = &job.DistributionSpec{
		Stages: []job.Stage{
			{
				ID:       "encode",
				Strategy: job.StageStrategy{Kind: "fanout_static"}, // no params.count
			},
		},
	}
	_, err := orch.AcceptJob(ctx, jb)
	if err == nil {
		t.Fatal("expected error for missing params.count")
	}
}

// ---- Stage chaining --------------------------------------------------------

func TestStageChaining_SecondStageEnqueuedAfterFirst(t *testing.T) {
	orch, q, st := newTestOrch(t)
	ctx := context.Background()

	jb := baseJob()
	jb.Distribution = &job.DistributionSpec{
		Stages: []job.Stage{
			{ID: "a", Strategy: job.StageStrategy{Kind: "single"}},
			{ID: "b", DependsOn: []string{"a"}, Strategy: job.StageStrategy{Kind: "single"}},
		},
	}
	id, _ := orch.AcceptJob(ctx, jb)

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
	_ = orch.OnTaskCompleted(ctx, leaseA.Task.ID, job.TaskResult{
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
	_ = orch.OnTaskCompleted(ctx, leaseB.Task.ID, job.TaskResult{
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

	jb := baseJob()
	jb.SchemaVersion = "1.0"
	_, err := orch.AcceptJob(ctx, jb)
	if err == nil {
		t.Fatal("expected validation error for wrong schema_version")
	}
}

func TestAcceptJob_UnknownDependsOn_Errors(t *testing.T) {
	orch, _, _ := newTestOrch(t)
	ctx := context.Background()

	jb := baseJob()
	jb.Distribution = &job.DistributionSpec{
		Stages: []job.Stage{
			{ID: "a", DependsOn: []string{"nonexistent"}, Strategy: job.StageStrategy{Kind: "single"}},
		},
	}
	_, err := orch.AcceptJob(ctx, jb)
	if err == nil {
		t.Fatal("expected error for unknown depends_on")
	}
}

// ---- CancelJob -------------------------------------------------------------

func TestCancelJob(t *testing.T) {
	orch, _, st := newTestOrch(t)
	ctx := context.Background()

	jb := baseJob()
	id, _ := orch.AcceptJob(ctx, jb)

	if err := orch.CancelJob(ctx, id); err != nil {
		t.Fatal(err)
	}

	_, statusRec, _ := st.GetJob(ctx, id)
	if statusRec.Status != state.JobStatusCancelled {
		t.Fatalf("want cancelled, got %s", statusRec.Status)
	}
}

// ---- Phase D: fanout_dynamic -----------------------------------------------

// TestFanoutDynamic_WaitsForProducer verifies that accepting a job with a
// fanout_dynamic stage that depends on a single producer stage only enqueues
// the producer task initially.
func TestFanoutDynamic_WaitsForProducer(t *testing.T) {
	orch, q, _ := newTestOrch(t)
	ctx := context.Background()

	manifestPath := t.TempDir() + "/manifest.json"

	jb := baseJob()
	jb.Distribution = &job.DistributionSpec{
		Stages: []job.Stage{
			{ID: "detect", Strategy: job.StageStrategy{Kind: "single"}},
			{
				ID:        "encode",
				DependsOn: []string{"detect"},
				Strategy: job.StageStrategy{
					Kind:   "fanout_dynamic",
					Params: map[string]any{"manifest_uri": "file://" + manifestPath},
				},
			},
		},
	}
	_, err := orch.AcceptJob(ctx, jb)
	if err != nil {
		t.Fatalf("AcceptJob: %v", err)
	}

	n, _ := q.Len(ctx)
	if n != 1 {
		t.Fatalf("want 1 task (producer) initially, got %d", n)
	}

	tctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	lease, _ := q.Receive(tctx, queue.ReceiveFilter{})
	if lease.Task.StageID != "detect" {
		t.Fatalf("want detect task, got stage %q", lease.Task.StageID)
	}
}

// TestFanoutDynamic_SpawnsChildTasksFromManifest verifies that completing the
// producer stage causes the orchestrator to read the manifest and spawn one
// child encode task per segment.
func TestFanoutDynamic_SpawnsChildTasksFromManifest(t *testing.T) {
	orch, q, _ := newTestOrch(t)
	ctx := context.Background()

	tmpDir := t.TempDir()
	manifestPath := tmpDir + "/manifest.json"
	manifestJSON := `{"splitter":"scene_list","input_uri":"file:///src.mp4","segments":[{"index":0,"inpoint":0,"outpoint":12.5},{"index":1,"inpoint":12.5,"outpoint":0}]}`
	if err := os.WriteFile(manifestPath, []byte(manifestJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	jb := baseJob()
	jb.Config.Inputs = []job.Input{{ID: "src", URL: "file:///src.mp4"}}
	jb.Config.Outputs = []job.Output{{ID: "out", URL: "file:///out.mp4"}}
	jb.Distribution = &job.DistributionSpec{
		Stages: []job.Stage{
			{ID: "detect", Strategy: job.StageStrategy{Kind: "single"}},
			{
				ID:        "encode",
				DependsOn: []string{"detect"},
				Strategy: job.StageStrategy{
					Kind:   "fanout_dynamic",
					Params: map[string]any{"manifest_uri": "file://" + manifestPath},
				},
			},
		},
	}
	_, err := orch.AcceptJob(ctx, jb)
	if err != nil {
		t.Fatalf("AcceptJob: %v", err)
	}

	// Receive and complete the detect (producer) task.
	tctx, cancel := context.WithTimeout(ctx, time.Second)
	detectLease, _ := q.Receive(tctx, queue.ReceiveFilter{})
	cancel()
	_ = q.Ack(ctx, detectLease.Task.ID)
	if err := orch.OnTaskCompleted(ctx, detectLease.Task.ID, job.TaskResult{
		StartedAt: time.Now(), FinishedAt: time.Now(),
	}); err != nil {
		t.Fatalf("OnTaskCompleted detect: %v", err)
	}

	// Two encode tasks should now be enqueued (one per segment).
	n, _ := q.Len(ctx)
	if n != 2 {
		t.Fatalf("want 2 encode tasks, got %d", n)
	}

	// Verify concat-demuxer inputs have correct InPoint/OutPoint.
	wantIn := []float64{0.0, 12.5}
	wantOut := []float64{12.5, 0.0}
	received := make([]*job.Task, 2)
	for i := 0; i < 2; i++ {
		tctx2, cancel2 := context.WithTimeout(ctx, time.Second)
		l, _ := q.Receive(tctx2, queue.ReceiveFilter{})
		cancel2()
		cp := l.Task
		received[cp.Index] = &cp
	}
	for i, task := range received {
		if task == nil {
			t.Fatalf("no task for index %d", i)
		}
		if len(task.Config.Inputs) == 0 {
			t.Fatalf("task %d: no inputs", i)
		}
		inp := task.Config.Inputs[0]
		if inp.Kind != "concat" {
			t.Fatalf("task %d: want Kind=concat, got %q", i, inp.Kind)
		}
		if len(inp.ConcatList) != 1 {
			t.Fatalf("task %d: want 1 concat entry, got %d", i, len(inp.ConcatList))
		}
		ce := inp.ConcatList[0]
		if ce.InPoint != wantIn[i] {
			t.Errorf("task %d: InPoint want %.1f got %.1f", i, wantIn[i], ce.InPoint)
		}
		if ce.OutPoint != wantOut[i] {
			t.Errorf("task %d: OutPoint want %.1f got %.1f", i, wantOut[i], ce.OutPoint)
		}
	}
}

// ---- Phase D: gather -------------------------------------------------------

// TestGather_BuildsConcatConfigFromFanoutOutputs runs a full
// detect → fanout_dynamic → gather pipeline and verifies that the gather task
// receives a concat-demuxer input listing all segment outputs in index order.
func TestGather_BuildsConcatConfigFromFanoutOutputs(t *testing.T) {
	orch, q, _ := newTestOrch(t)
	ctx := context.Background()

	tmpDir := t.TempDir()
	manifestPath := tmpDir + "/manifest.json"
	manifestJSON := `{"splitter":"byte_range","input_uri":"file:///src.mp4","segments":[{"index":0,"inpoint":0,"outpoint":10},{"index":1,"inpoint":10,"outpoint":20},{"index":2,"inpoint":20,"outpoint":0}]}`
	if err := os.WriteFile(manifestPath, []byte(manifestJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	jb := baseJob()
	jb.Storage = job.StorageRef{URI: "file://" + tmpDir}
	jb.Config.Inputs = []job.Input{{ID: "src", URL: "file:///src.mp4"}}
	jb.Config.Outputs = []job.Output{{ID: "out", URL: "file:///out.mp4"}}
	jb.Distribution = &job.DistributionSpec{
		Stages: []job.Stage{
			{ID: "detect", Strategy: job.StageStrategy{Kind: "single"}},
			{
				ID:        "encode",
				DependsOn: []string{"detect"},
				Strategy: job.StageStrategy{
					Kind:   "fanout_dynamic",
					Params: map[string]any{"manifest_uri": "file://" + manifestPath},
				},
			},
			{
				ID:        "stitch",
				DependsOn: []string{"encode"},
				Strategy:  job.StageStrategy{Kind: "gather"},
			},
		},
	}
	_, err := orch.AcceptJob(ctx, jb)
	if err != nil {
		t.Fatalf("AcceptJob: %v", err)
	}

	// Complete the detect stage.
	tctx, cancel := context.WithTimeout(ctx, time.Second)
	detectLease, _ := q.Receive(tctx, queue.ReceiveFilter{})
	cancel()
	_ = q.Ack(ctx, detectLease.Task.ID)
	if err := orch.OnTaskCompleted(ctx, detectLease.Task.ID, job.TaskResult{
		StartedAt: time.Now(), FinishedAt: time.Now(),
	}); err != nil {
		t.Fatalf("complete detect: %v", err)
	}

	// Complete the 3 encode tasks (consume all from queue first, then ack).
	var encodeLeases []queue.Lease
	for i := 0; i < 3; i++ {
		tctx2, cancel2 := context.WithTimeout(ctx, time.Second)
		enc, err := q.Receive(tctx2, queue.ReceiveFilter{})
		cancel2()
		if err != nil {
			t.Fatalf("receive encode task %d: %v", i, err)
		}
		encodeLeases = append(encodeLeases, enc)
	}
	for _, enc := range encodeLeases {
		_ = q.Ack(ctx, enc.Task.ID)
		if err := orch.OnTaskCompleted(ctx, enc.Task.ID, job.TaskResult{
			StartedAt: time.Now(), FinishedAt: time.Now(),
		}); err != nil {
			t.Fatalf("complete encode task: %v", err)
		}
	}

	// Stitch (gather) task should now be enqueued.
	tctx3, cancel3 := context.WithTimeout(ctx, time.Second)
	stitchLease, err := q.Receive(tctx3, queue.ReceiveFilter{})
	cancel3()
	if err != nil {
		t.Fatalf("stitch task not enqueued: %v", err)
	}
	if stitchLease.Task.StageID != "stitch" {
		t.Fatalf("want stage stitch, got %q", stitchLease.Task.StageID)
	}

	inp := stitchLease.Task.Config.Inputs
	if len(inp) == 0 {
		t.Fatal("stitch task: no inputs")
	}
	if inp[0].Kind != "concat" {
		t.Fatalf("stitch input Kind: want concat, got %q", inp[0].Kind)
	}
	if len(inp[0].ConcatList) != 3 {
		t.Fatalf("stitch concat list: want 3 entries, got %d", len(inp[0].ConcatList))
	}
	// Segment output URLs must be distinct and ordered.
	seg0URL := inp[0].ConcatList[0].File
	seg2URL := inp[0].ConcatList[2].File
	if seg0URL == seg2URL {
		t.Fatal("concat entries 0 and 2 should have distinct output URLs")
	}
}
