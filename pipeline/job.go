// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import "time"

// JobSchemaVersion is the schema_version value for Job documents.
const JobSchemaVersion = "1.4"

// Job is the top-level Phase B submission document (schema_version "1.4").
// It wraps a pipeline Config and adds distribution, storage, and scheduling
// metadata. A Job with no Distribution block behaves like a Tier 1 job: the
// orchestrator emits exactly one task that runs the full Config on one worker.
type Job struct {
	SchemaVersion string            `json:"schema_version"`
	ID            string            `json:"id,omitempty"`
	Name          string            `json:"name,omitempty"`
	Owner         string            `json:"owner,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`

	// Config is the pipeline graph. Embedded verbatim in every derived Task.
	Config Config `json:"config"`

	// Distribution describes how to split the job into tasks.
	// If absent the orchestrator creates a single task for the whole Config.
	Distribution *DistributionSpec `json:"distribution,omitempty"`

	// Storage is the shared object-storage root used for media and manifests.
	// Required for Tier 2; optional for single-process modes.
	Storage StorageRef `json:"storage,omitempty"`

	// Requirements specifies capabilities every worker must provide.
	Requirements WorkerRequirements `json:"requirements,omitempty"`

	// Policy controls retry and timeout behaviour.
	Policy JobPolicy `json:"policy,omitempty"`
}

// DistributionSpec describes how a Job is split across workers.
type DistributionSpec struct {
	Stages []Stage `json:"stages"`
}

// Stage is one processing step in a distributed Job.
type Stage struct {
	ID        string        `json:"id"`
	DependsOn []string      `json:"depends_on,omitempty"`
	Strategy  StageStrategy `json:"strategy"`
	// Subgraph selects which nodes from the master Config this stage runs.
	// An empty NodeIDs list means "all nodes" (full graph passthrough).
	Subgraph SubgraphRef  `json:"subgraph"`
	Assembly *SubgraphRef `json:"assembly,omitempty"`
}

// StageStrategy selects how a Stage emits tasks.
type StageStrategy struct {
	// Kind is one of "single", "fanout_static", "fanout_dynamic", "gather".
	Kind   string         `json:"kind"`
	Params map[string]any `json:"params,omitempty"`
}

// SubgraphRef selects a subset of nodes from the master Config graph.
type SubgraphRef struct {
	NodeIDs []string `json:"node_ids"`
}

// StorageRef points to an object-storage root for shared media and manifests.
type StorageRef struct {
	URI string `json:"uri,omitempty"`
}

// WorkerRequirements specifies capabilities a worker must provide.
type WorkerRequirements struct {
	// HardwareAccel lists required hardware acceleration types (e.g. "cuda", "videotoolbox").
	HardwareAccel []string `json:"hardware_accel,omitempty"`
	// Codecs lists codec names the worker must support (e.g. "h264_nvenc").
	Codecs          []string `json:"codecs,omitempty"`
	MinFreeDiskBytes int64   `json:"min_free_disk_bytes,omitempty"`
	MinFreeMemBytes  int64   `json:"min_free_mem_bytes,omitempty"`
	// Region constrains which region a worker must be in (e.g. "us-east-1"). Empty means any.
	Region string `json:"region,omitempty"`
	// EstimatedDurationNS is a scheduling hint (nanoseconds) used for presigned URL TTL.
	EstimatedDurationNS int64 `json:"estimated_duration_ns,omitempty"`
}

// JobPolicy controls retry and timeout at the job level.
type JobPolicy struct {
	MaxAttempts           int   `json:"max_attempts,omitempty"`
	RetryBackoffInitialNS int64 `json:"retry_backoff_initial_ns,omitempty"`
	RetryBackoffMaxNS     int64 `json:"retry_backoff_max_ns,omitempty"`
	TaskTimeoutNS         int64 `json:"task_timeout_ns,omitempty"`
}

// Task is a self-contained unit of work dispatched to a single worker.
// Its Config is always a complete, runnable pipeline.Config.
type Task struct {
	ID      string `json:"id"`
	JobID   string `json:"job_id"`
	StageID string `json:"stage_id"`
	// Index is the zero-based position of this task within its stage.
	Index int `json:"index"`
	// Total is the total number of tasks in this stage.
	Total   int    `json:"total"`
	Attempt int    `json:"attempt"`
	Config  Config `json:"config"`

	Deadline   time.Time `json:"deadline"`
	LeaseUntil time.Time `json:"lease_until"`

	Requires WorkerRequirements `json:"requires,omitempty"`

	// TraceContext carries W3C traceparent/tracestate headers injected by the
	// orchestrator so the worker can attach its spans to the job's trace.
	TraceContext map[string]string `json:"trace_context,omitempty"`
}

// ArtifactRef identifies a produced output object.
type ArtifactRef struct {
	URI      string `json:"uri"`
	Size     int64  `json:"size,omitempty"`
	ETag     string `json:"etag,omitempty"`
	Checksum string `json:"checksum,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
}

// ArtifactSlot is a target output location assigned to a task at dispatch time.
type ArtifactSlot struct {
	ID          string `json:"id"`
	URI         string `json:"uri"`
	ManifestURI string `json:"manifest_uri,omitempty"`
}

// TaskResult is recorded by a worker when a task finishes (success or failure).
type TaskResult struct {
	Outputs    []ArtifactRef `json:"outputs,omitempty"`
	Error      string        `json:"error,omitempty"`
	StartedAt  time.Time     `json:"started_at"`
	FinishedAt time.Time     `json:"finished_at"`
}

// WorkerCapabilities describes what a specific worker process can execute.
type WorkerCapabilities struct {
	WorkerID      string   `json:"worker_id"`
	HardwareAccel []string `json:"hardware_accel,omitempty"`
	Codecs        []string `json:"codecs,omitempty"`
	// Region is the deployment region this worker is in (e.g. "us-east-1").
	Region        string `json:"region,omitempty"`
	FreeDiskBytes int64  `json:"free_disk_bytes,omitempty"`
	FreeMemBytes  int64  `json:"free_mem_bytes,omitempty"`
}
