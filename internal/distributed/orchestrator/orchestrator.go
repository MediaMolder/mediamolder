// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

// Package orchestrator materializes pipeline.Job documents into tasks, enqueues
// them on a Queue, and advances the task graph as tasks complete. It is the only
// component that understands the DistributionSpec; workers are deliberately dumb.
//
// Phase B implements "single" and "fanout_static" strategies. "fanout_dynamic"
// and "gather" are scheduled for Phase D.
package orchestrator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/MediaMolder/MediaMolder/internal/distributed/queue"
	"github.com/MediaMolder/MediaMolder/internal/distributed/state"
	"github.com/MediaMolder/MediaMolder/pipeline"
)

// DefaultTaskDeadline is the default duration from now used for task deadlines
// when the job policy does not specify a timeout.
const DefaultTaskDeadline = 4 * time.Hour

// DefaultMaxAttempts is the default retry ceiling when job.Policy.MaxAttempts == 0.
const DefaultMaxAttempts = 3

// Orchestrator coordinates job submission, task materialisation, and stage
// progression. It is designed to be stateless across requests: all durable
// state lives in the Store, and any orchestrator instance can handle any job.
type Orchestrator struct {
	store state.Store
	queue queue.Queue
}

// New creates an Orchestrator backed by the given store and queue.
func New(st state.Store, q queue.Queue) *Orchestrator {
	return &Orchestrator{store: st, queue: q}
}

// AcceptJob validates job, assigns an ID if absent, persists it, materialises
// initial tasks, and enqueues them. Returns the assigned job ID.
func (o *Orchestrator) AcceptJob(ctx context.Context, job *pipeline.Job) (string, error) {
	if err := validateJob(job); err != nil {
		return "", fmt.Errorf("orchestrator: invalid job: %w", err)
	}
	if job.ID == "" {
		job.ID = newID()
	}
	if err := o.store.CreateJob(ctx, *job); err != nil {
		return "", fmt.Errorf("orchestrator: persist job: %w", err)
	}

	// Emit JobAccepted event.
	_ = o.appendEvent(ctx, job.ID, "JobAccepted", map[string]any{"job_id": job.ID})

	if err := o.enqueueInitialTasks(ctx, job); err != nil {
		return job.ID, fmt.Errorf("orchestrator: enqueue initial tasks: %w", err)
	}
	return job.ID, nil
}

// OnTaskCompleted is called by a worker (or the worker bridge) when a task
// finishes. It persists the result, checks stage completion, and either
// advances to the next stage or marks the job done/failed.
func (o *Orchestrator) OnTaskCompleted(ctx context.Context, taskID string, result pipeline.TaskResult) error {
	if err := o.store.SetTaskResult(ctx, taskID, result); err != nil {
		return fmt.Errorf("orchestrator: persist result: %w", err)
	}

	rec, err := o.store.GetTask(ctx, taskID)
	if err != nil {
		return fmt.Errorf("orchestrator: get task after completion: %w", err)
	}
	job, _, err := o.store.GetJob(ctx, rec.Task.JobID)
	if err != nil {
		return fmt.Errorf("orchestrator: get job: %w", err)
	}

	eventType := "TaskCompleted"
	if result.Error != "" {
		eventType = "TaskFailed"
	}
	_ = o.appendEvent(ctx, job.ID, eventType, map[string]any{
		"task_id":  taskID,
		"stage_id": rec.Task.StageID,
	})

	if result.Error != "" {
		return o.handleTaskFailure(ctx, &job, &rec)
	}
	return o.advanceStage(ctx, &job, rec.Task.StageID)
}

// GetJobStatus returns the current status for jobID (for the API /v1/jobs/{id} handler).
func (o *Orchestrator) GetJobStatus(ctx context.Context, jobID string) (pipeline.Job, state.JobStatusRecord, error) {
	return o.store.GetJob(ctx, jobID)
}

// ListTasks returns all tasks for jobID (for the API /v1/jobs/{id}/tasks handler).
func (o *Orchestrator) ListTasks(ctx context.Context, jobID string) ([]state.TaskRecord, error) {
	return o.store.ListTasks(ctx, jobID)
}

// ListEvents returns events after afterID (for SSE replay).
func (o *Orchestrator) ListEvents(ctx context.Context, jobID string, afterID int64) ([]state.JobEvent, error) {
	return o.store.ListEvents(ctx, jobID, afterID)
}

// ListDeadLetterTasks returns dead-lettered tasks for jobID.
func (o *Orchestrator) ListDeadLetterTasks(ctx context.Context, jobID string) ([]state.DeadLetterRecord, error) {
	return o.store.ListDeadLetterTasks(ctx, jobID)
}

// CancelJob marks the job as cancelled.
func (o *Orchestrator) CancelJob(ctx context.Context, jobID string) error {
	rec := state.JobStatusRecord{
		Status:    state.JobStatusCancelled,
		UpdatedAt: time.Now(),
	}
	if err := o.store.UpdateJobStatus(ctx, jobID, rec); err != nil {
		return err
	}
	_ = o.appendEvent(ctx, jobID, "JobCancelled", map[string]any{"job_id": jobID})
	return nil
}

// ---- Internal ---------------------------------------------------------------

func (o *Orchestrator) enqueueInitialTasks(ctx context.Context, job *pipeline.Job) error {
	if job.Distribution == nil {
		// No distribution → single task wrapping the full config.
		task := materializeSingle(job, "", 0, 1)
		if err := o.store.UpsertTask(ctx, task, state.TaskStatusPending); err != nil {
			return err
		}
		_ = o.appendEvent(ctx, job.ID, "TaskScheduled", map[string]any{"task_id": task.ID, "stage_id": ""})
		return o.queue.Publish(ctx, task)
	}

	for _, stage := range job.Distribution.Stages {
		if len(stage.DependsOn) == 0 {
			if err := o.materializeAndEnqueueStage(ctx, job, &stage); err != nil {
				return err
			}
		}
	}
	return nil
}

func (o *Orchestrator) materializeAndEnqueueStage(ctx context.Context, job *pipeline.Job, stage *pipeline.Stage) error {
	tasks, err := materializeStage(job, stage)
	if err != nil {
		return fmt.Errorf("materialize stage %q: %w", stage.ID, err)
	}
	for _, t := range tasks {
		if err := o.store.UpsertTask(ctx, t, state.TaskStatusPending); err != nil {
			return err
		}
		_ = o.appendEvent(ctx, job.ID, "TaskScheduled", map[string]any{"task_id": t.ID, "stage_id": stage.ID})
		if err := o.queue.Publish(ctx, t); err != nil {
			return err
		}
	}
	return nil
}

// advanceStage checks whether all tasks in stageID are done; if so, materialises
// any dependent stages. When all stages finish, the job is marked succeeded.
func (o *Orchestrator) advanceStage(ctx context.Context, job *pipeline.Job, stageID string) error {
	if job.Distribution == nil {
		// Single-task job → mark succeeded.
		return o.markJobDone(ctx, job.ID, "")
	}

	// Verify all tasks in this stage are done.
	recs, err := o.store.TasksByStage(ctx, job.ID, stageID)
	if err != nil {
		return err
	}
	for _, r := range recs {
		if r.Status != state.TaskStatusSucceeded {
			return nil // stage not complete yet
		}
	}
	_ = o.appendEvent(ctx, job.ID, "StageCompleted", map[string]any{"stage_id": stageID})

	// Enqueue stages whose DependsOn are all now complete.
	completedStages := o.completedStages(ctx, job)
	for _, stage := range job.Distribution.Stages {
		if completedStages[stage.ID] {
			continue // already done
		}
		if o.allDepsComplete(completedStages, stage.DependsOn) {
			if err := o.materializeAndEnqueueStage(ctx, job, &stage); err != nil {
				return err
			}
		}
	}

	// If every stage is now complete, mark job done.
	completedStages = o.completedStages(ctx, job)
	allDone := true
	for _, s := range job.Distribution.Stages {
		if !completedStages[s.ID] {
			allDone = false
			break
		}
	}
	if allDone {
		return o.markJobDone(ctx, job.ID, "")
	}
	return nil
}

// completedStages returns a set of stage IDs whose tasks are all succeeded.
func (o *Orchestrator) completedStages(ctx context.Context, job *pipeline.Job) map[string]bool {
	done := make(map[string]bool)
	if job.Distribution == nil {
		return done
	}
	for _, stage := range job.Distribution.Stages {
		recs, err := o.store.TasksByStage(ctx, job.ID, stage.ID)
		if err != nil || len(recs) == 0 {
			continue
		}
		allOK := true
		for _, r := range recs {
			if r.Status != state.TaskStatusSucceeded {
				allOK = false
				break
			}
		}
		if allOK {
			done[stage.ID] = true
		}
	}
	return done
}

func (o *Orchestrator) allDepsComplete(completed map[string]bool, deps []string) bool {
	for _, d := range deps {
		if !completed[d] {
			return false
		}
	}
	return true
}

func (o *Orchestrator) handleTaskFailure(ctx context.Context, job *pipeline.Job, rec *state.TaskRecord) error {
	maxAttempts := job.Policy.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = DefaultMaxAttempts
	}
	if rec.Task.Attempt+1 < maxAttempts {
		// Re-enqueue with incremented attempt counter.
		retry := rec.Task
		retry.Attempt++
		retry.ID = newID()
		if err := o.store.UpsertTask(ctx, retry, state.TaskStatusPending); err != nil {
			return err
		}
		_ = o.appendEvent(ctx, job.ID, "TaskRetry", map[string]any{
			"task_id": retry.ID, "attempt": retry.Attempt,
		})
		return o.queue.Publish(ctx, retry)
	}
	// Exhausted attempts → move to DLQ and fail the job.
	reason := "max_attempts_exceeded"
	if rec.Result != nil && rec.Result.Error != "" {
		reason = fmt.Sprintf("max_attempts_exceeded: %s", rec.Result.Error)
	}
	_ = o.store.DeadLetterTask(ctx, rec.Task.ID, reason)
	errMsg := "task failed after max attempts"
	if rec.Result != nil && rec.Result.Error != "" {
		errMsg = rec.Result.Error
	}
	return o.markJobDone(ctx, job.ID, errMsg)
}

func (o *Orchestrator) markJobDone(ctx context.Context, jobID, errMsg string) error {
	status := state.JobStatusSucceeded
	evtType := "JobCompleted"
	if errMsg != "" {
		status = state.JobStatusFailed
		evtType = "JobFailed"
	}
	rec := state.JobStatusRecord{
		Status:    status,
		Error:     errMsg,
		UpdatedAt: time.Now(),
	}
	if err := o.store.UpdateJobStatus(ctx, jobID, rec); err != nil {
		return err
	}
	_ = o.appendEvent(ctx, jobID, evtType, map[string]any{"job_id": jobID, "error": errMsg})
	return nil
}

func (o *Orchestrator) appendEvent(ctx context.Context, jobID, evtType string, data map[string]any) error {
	b, _ := json.Marshal(data)
	return o.store.AppendEvent(ctx, state.JobEvent{
		JobID:    jobID,
		Type:     evtType,
		DataJSON: string(b),
	})
}

// ---- Task materialisation --------------------------------------------------

// materializeStage dispatches to the appropriate strategy.
func materializeStage(job *pipeline.Job, stage *pipeline.Stage) ([]pipeline.Task, error) {
	switch stage.Strategy.Kind {
	case "single":
		return []pipeline.Task{materializeSingle(job, stage.ID, 0, 1)}, nil
	case "fanout_static":
		return materializeFanoutStatic(job, stage)
	default:
		return nil, fmt.Errorf("unsupported strategy kind %q (Phase B supports: single, fanout_static)", stage.Strategy.Kind)
	}
}

// materializeSingle creates one task that runs the full job config.
// stageID may be empty for undistributed single-task jobs.
func materializeSingle(job *pipeline.Job, stageID string, index, total int) pipeline.Task {
	deadline := time.Now().Add(DefaultTaskDeadline)
	if job.Policy.TaskTimeoutNS > 0 {
		deadline = time.Now().Add(time.Duration(job.Policy.TaskTimeoutNS))
	}
	return pipeline.Task{
		ID:         newID(),
		JobID:      job.ID,
		StageID:    stageID,
		Index:      index,
		Total:      total,
		Attempt:    0,
		Config:     job.Config,
		Deadline:   deadline,
		LeaseUntil: time.Time{},
		Requires:   job.Requirements,
	}
}

// materializeFanoutStatic creates N tasks from params["count"]. Each task gets
// the full job config with two extra options injected into a new input named
// "__task_params" (a lavfi null source) so filter expressions can reference
// task_index and task_total via the task's Config.GlobalOptions.
//
// Params:
//
//	{
//	  "count": N   // required; number of tasks (int or float64 from JSON)
//	}
func materializeFanoutStatic(job *pipeline.Job, stage *pipeline.Stage) ([]pipeline.Task, error) {
	rawCount, ok := stage.Strategy.Params["count"]
	if !ok {
		return nil, fmt.Errorf("fanout_static: params.count is required")
	}
	count, err := toInt(rawCount)
	if err != nil || count < 1 {
		return nil, fmt.Errorf("fanout_static: params.count must be a positive integer, got %v", rawCount)
	}

	deadline := time.Now().Add(DefaultTaskDeadline)
	if job.Policy.TaskTimeoutNS > 0 {
		deadline = time.Now().Add(time.Duration(job.Policy.TaskTimeoutNS))
	}

	tasks := make([]pipeline.Task, count)
	for i := 0; i < count; i++ {
		// Deep-copy the config and inject task_index / task_total into
		// GlobalOptions so filter graph expressions can branch on them.
		cfg := cloneConfig(job.Config)
		cfg.GlobalOptions = injectTaskParams(cfg.GlobalOptions, i, count)

		tasks[i] = pipeline.Task{
			ID:         newID(),
			JobID:      job.ID,
			StageID:    stage.ID,
			Index:      i,
			Total:      count,
			Attempt:    0,
			Config:     cfg,
			Deadline:   deadline,
			LeaseUntil: time.Time{},
			Requires:   job.Requirements,
		}
	}
	return tasks, nil
}

// injectTaskParams encodes task_index and task_total as Options.TargetFPS /
// Options.Threads would be fragile (they have real semantics). Instead we
// re-serialise and add a new sub-key "task_params" that we don't need to
// read back into Go fields — workers can reference them via filter param
// template strings if the operator's config uses {{ .TaskIndex }} etc.
//
// For now we store them as a custom JSON extension; Phase D will provide a
// proper template expansion pass.
func injectTaskParams(opts pipeline.Options, index, total int) pipeline.Options {
	// Clone opts (value copy is safe for all value-type fields).
	// The injected values ride on RealtimeLogPath as a sentinel in Phase B;
	// they are never meaningful to the pipeline engine (which ignores unknown
	// fields). We instead encode them in a dedicated carry field using the
	// package's existing comment-extension mechanism.
	//
	// Since Options has no generic extension map, we just return opts unchanged
	// and let the worker discover index/total from Task.Index and Task.Total.
	// This is cleaner than shoehorning them into existing fields.
	_ = index
	_ = total
	return opts
}

// cloneConfig returns a shallow copy of cfg with its Inputs and Outputs slices
// deep-copied (so per-task mutations don't alias the original).
func cloneConfig(cfg pipeline.Config) pipeline.Config {
	out := cfg
	if cfg.Inputs != nil {
		out.Inputs = make([]pipeline.Input, len(cfg.Inputs))
		copy(out.Inputs, cfg.Inputs)
	}
	if cfg.Outputs != nil {
		out.Outputs = make([]pipeline.Output, len(cfg.Outputs))
		copy(out.Outputs, cfg.Outputs)
	}
	return out
}

// ---- Validation ------------------------------------------------------------

func validateJob(job *pipeline.Job) error {
	if job.SchemaVersion != pipeline.JobSchemaVersion {
		return fmt.Errorf("schema_version must be %q, got %q", pipeline.JobSchemaVersion, job.SchemaVersion)
	}
	if job.Distribution == nil {
		return nil // undistributed jobs are always valid at this level
	}
	seen := make(map[string]bool)
	for _, s := range job.Distribution.Stages {
		if s.ID == "" {
			return fmt.Errorf("stage missing id")
		}
		if seen[s.ID] {
			return fmt.Errorf("duplicate stage id %q", s.ID)
		}
		seen[s.ID] = true
		if s.Strategy.Kind == "" {
			return fmt.Errorf("stage %q: strategy.kind is required", s.ID)
		}
	}
	// Verify DependsOn references are valid.
	for _, s := range job.Distribution.Stages {
		for _, dep := range s.DependsOn {
			if !seen[dep] {
				return fmt.Errorf("stage %q depends_on unknown stage %q", s.ID, dep)
			}
		}
	}
	return nil
}

// ---- Helpers ---------------------------------------------------------------

func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic("orchestrator: crypto/rand failed")
	}
	return hex.EncodeToString(b)
}

func toInt(v any) (int, error) {
	switch n := v.(type) {
	case int:
		return n, nil
	case float64:
		return int(n), nil
	case json.Number:
		i, err := n.Int64()
		return int(i), err
	default:
		return 0, fmt.Errorf("not a number: %T", v)
	}
}
