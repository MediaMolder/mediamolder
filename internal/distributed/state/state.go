// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

// Package state defines the durable-state abstraction used by orchestrators.
// Phase B ships a SQLite adapter. Phase C will add Postgres and DynamoDB.
package state

import (
	"context"
	"time"

	"github.com/MediaMolder/MediaMolder/pipeline"
)

// JobStatus is the lifecycle state of a job.
type JobStatus string

const (
	JobStatusQueued    JobStatus = "queued"
	JobStatusRunning   JobStatus = "running"
	JobStatusSucceeded JobStatus = "succeeded"
	JobStatusFailed    JobStatus = "failed"
	JobStatusCancelled JobStatus = "cancelled"
)

// TaskStatus is the lifecycle state of an individual task.
type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusSucceeded TaskStatus = "succeeded"
	TaskStatusFailed    TaskStatus = "failed"
)

// JobStatusRecord holds the mutable status of a job.
type JobStatusRecord struct {
	Status    JobStatus `json:"status"`
	Error     string    `json:"error,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TaskRecord holds a task together with its current status and optional result.
type TaskRecord struct {
	Task       pipeline.Task        `json:"task"`
	Status     TaskStatus           `json:"status"`
	Result     *pipeline.TaskResult `json:"result,omitempty"`
	LeaseUntil time.Time            `json:"lease_until,omitempty"`
	UpdatedAt  time.Time            `json:"updated_at"`
}

// DeadLetterRecord holds a task that exhausted its retry budget.
type DeadLetterRecord struct {
	TaskID    string    `json:"task_id"`
	JobID     string    `json:"job_id"`
	StageID   string    `json:"stage_id"`
	TaskJSON  string    `json:"task_json"`
	Reason    string    `json:"reason"`
	Attempt   int       `json:"attempt"`
	CreatedAt time.Time `json:"created_at"`
}

// ReconcilerLocker is an optional interface that Store adapters may implement
// to support distributed advisory locking for the reconciler. If a Store does
// not implement ReconcilerLocker the reconciler assumes single-instance mode
// and skips coordination.
type ReconcilerLocker interface {
	// TryReconcilerLock attempts to acquire an exclusive advisory lock.
	// Returns a non-nil release function, whether the lock was acquired,
	// and any error. The release function must always be called.
	TryReconcilerLock(ctx context.Context) (release func(context.Context), ok bool, err error)
}

// JobEvent is a single entry in the per-job event log.
type JobEvent struct {
	ID        int64     `json:"id"`
	JobID     string    `json:"job_id"`
	Type      string    `json:"type"`
	DataJSON  string    `json:"data"`
	CreatedAt time.Time `json:"created_at"`
}

// Store is the state-store abstraction used by orchestrators.
type Store interface {
	// Job lifecycle.
	CreateJob(ctx context.Context, j pipeline.Job) error
	GetJob(ctx context.Context, id string) (pipeline.Job, JobStatusRecord, error)
	UpdateJobStatus(ctx context.Context, jobID string, s JobStatusRecord) error

	// Event log (append-only; used to rebuild SSE streams).
	AppendEvent(ctx context.Context, e JobEvent) error
	ListEvents(ctx context.Context, jobID string, afterID int64) ([]JobEvent, error)

	// Task management.
	UpsertTask(ctx context.Context, t pipeline.Task, status TaskStatus) error
	SetTaskResult(ctx context.Context, taskID string, r pipeline.TaskResult) error
	GetTask(ctx context.Context, taskID string) (TaskRecord, error)
	TasksByStage(ctx context.Context, jobID, stageID string) ([]TaskRecord, error)
	ListTasks(ctx context.Context, jobID string) ([]TaskRecord, error)

	// Lease management (used by workers and the reconciler).
	RenewTaskLease(ctx context.Context, taskID string, until time.Time) error
	ListExpiredLeases(ctx context.Context) ([]TaskRecord, error)

	// Dead-letter queue.
	DeadLetterTask(ctx context.Context, taskID, reason string) error
	ListDeadLetterTasks(ctx context.Context, jobID string) ([]DeadLetterRecord, error)

	// Close releases underlying resources.
	Close() error
}
