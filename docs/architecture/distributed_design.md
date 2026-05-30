# MediaMolder Remote & Distributed Execution — Design

## 1. Goals & Non-Goals

### Goals
1. **Tier 1 — Remote Single-Server.** A user runs the GUI/CLI on their laptop and submits a
   job to one remote `mediamolder` server (LAN box, EC2 instance, private cloud VM).
   The server executes the **entire job in one process** using the existing
   `pipeline.Engine`. No queue, no workers, no task subdivision.
2. **Tier 2 — Distributed.** One or more clients submit jobs to a fleet of **stateless**
   API/orchestrator nodes. The orchestrator splits a job into tasks per the job's own
   distribution spec, puts tasks on a queue, and one or more workers pull and execute
   them. Multiple API/orchestrator instances can run behind a load balancer for HA.
3. **Same job document.** A job submitted in Tier 1 and Tier 2 uses the same JSON; the
   only difference is whether a `distribution` block is present.
4. **Single-process path is unchanged.** All existing `mediamolder run job.json` behavior
   is preserved bit-for-bit. Remote/distributed code is additive.

### Non-Goals
- **Provisioning / autoscaling** of API nodes or workers. That belongs to a separate
  deployment platform (Terraform, Pulumi, Kubernetes operator, internal control plane).
  MediaMolder only needs to *run well* under such a platform.
- **Reimplementing storage.** Shared storage (S3, GCS, NFS) is configured externally;
  MediaMolder consumes URIs.
- **A built-in scheduler smart enough to bin-pack heterogeneous workers.** That's the
  deployment platform's job. Workers self-advertise capabilities; orchestrator filters.

---

## 2. Three Deployment Topologies

```
Tier 0 — Local (unchanged)
  ┌──────────────┐
  │ mediamolder  │  (GUI + engine in one process)
  └──────────────┘

Tier 1 — Remote Single-Server
  ┌──────────────┐        HTTPS         ┌──────────────────────┐
  │ Client       │ ───── job JSON ────▶ │ mediamolder serve    │
  │ (GUI / CLI)  │ ◀──── events SSE ─── │   --mode=server      │
  └──────────────┘                      │   (one process runs  │
                                        │    entire job)       │
                                        └──────────────────────┘

Tier 2 — Distributed (stateless API + queue + workers)
  ┌──────────┐  ┌──────────┐                ┌──────────────────────────┐
  │ Client A │  │ Client B │   ── HTTPS ──▶ │ Load Balancer            │
  └──────────┘  └──────────┘                └─────────┬────────────────┘
                                                      │
                              ┌───────────────────────┼───────────────────────┐
                              ▼                       ▼                       ▼
                       ┌────────────┐          ┌────────────┐          ┌────────────┐
                       │ orchestr.  │          │ orchestr.  │   ...    │ orchestr.  │
                       │ (stateless)│          │ (stateless)│          │ (stateless)│
                       └─────┬──────┘          └─────┬──────┘          └─────┬──────┘
                             └────────────┬──────────┴────────────┬─────────┘
                                          ▼                       ▼
                                ┌──────────────────┐    ┌──────────────────┐
                                │ State Store      │    │ Task Queue       │
                                │ (Postgres/Dynamo)│    │ (SQS/NATS/Redis) │
                                └──────────────────┘    └────────┬─────────┘
                                                                 │
                                          ┌──────────────────────┼──────────────────────┐
                                          ▼                      ▼                      ▼
                                   ┌────────────┐         ┌────────────┐         ┌────────────┐
                                   │ worker 1   │         │ worker 2   │   ...   │ worker N   │
                                   └────────────┘         └────────────┘         └────────────┘

                              Shared object storage (S3/GCS/NFS) — all media + manifests
```

The same `mediamolder` binary serves all three; mode is chosen by flag:

```
mediamolder run job.json                          # Tier 0
mediamolder serve --mode=server   --addr=:8443    # Tier 1
mediamolder serve --mode=api      --addr=:8443    # Tier 2 (stateless API/orchestrator)
mediamolder serve --mode=worker   --queue=…       # Tier 2 (worker)
```

---

## 3. Job Model (one document for all tiers)

A `Job` wraps the existing `pipeline.Config` and adds metadata the orchestrator needs.

```go
// pipeline/job.go (new)
type Job struct {
    SchemaVersion string          `json:"schema_version"`        // "1.4"
    ID            string          `json:"id"`                    // assigned by server if empty
    Name          string          `json:"name,omitempty"`
    Owner         string          `json:"owner,omitempty"`
    Labels        map[string]string `json:"labels,omitempty"`

    // The pipeline graph. Untouched in Tier 0 / Tier 1.
    Config        Config          `json:"config"`

    // Optional. If absent, the job is a single-task job (Tier 0/1 style)
    // even when submitted to a Tier 2 API — the orchestrator just emits
    // one task that wraps the whole graph.
    Distribution  *DistributionSpec `json:"distribution,omitempty"`

    // Where media + manifests live. Required for Tier 2; optional for Tier 1
    // (server can use its local filesystem).
    Storage       StorageRef      `json:"storage,omitempty"`

    // Optional per-job execution constraints.
    Requirements  WorkerRequirements `json:"requirements,omitempty"`

    // Optional error / retry policy at the job level.
    Policy        JobPolicy       `json:"policy,omitempty"`
}
```

### 3.1 Single submission, three execution paths

| Tier | What the server does with `Job` |
|------|---------------------------------|
| 0    | `pipeline.NewEngine(job.Config).Run()` — direct. |
| 1    | Same, but inside the `serve --mode=server` HTTP handler; events stream over SSE. |
| 2    | Hand the `Job` to the orchestrator, which materializes tasks via `Distribution`. |

A job with **no `Distribution`** submitted to a Tier 2 API is still valid: the orchestrator
generates exactly one `Task` that contains the whole graph, queues it, and a single worker
runs it end-to-end. This lets users adopt the distributed control plane without restructuring
their jobs.

---

## 4. Tier 1 — Remote Single-Server

### 4.1 Process model
- Single `mediamolder serve --mode=server` process.
- Accepts one `POST /v1/jobs` at a time per *job slot*; concurrency knob `--max-jobs N`
  (default `runtime.NumCPU()/2`, configurable; jobs beyond the limit queue in memory
  with bounded backlog).
- Storage: `--workdir /var/lib/mediamolder` holds inputs (if uploaded), outputs,
  logs, manifests. Optional `--storage s3://…` to write outputs to object storage
  (uses the same URI layer as Tier 2 — see §7).
- No external dependencies. No database. State lives in memory + on disk.
- Survives process restart only as far as completed outputs on disk; in-flight jobs
  are lost. This is intentional — Tier 1 is for "my one server", not for HA.

### 4.2 HTTP API (Tier 1 = subset of Tier 2)

```
POST   /v1/jobs                   submit job; returns {id, status_url, events_url}
GET    /v1/jobs/{id}              status snapshot
GET    /v1/jobs/{id}/events       SSE stream of pipeline events (reuses existing event bus)
GET    /v1/jobs/{id}/metrics      Prometheus snapshot for this job
GET    /v1/jobs/{id}/artifacts    list of produced files (URIs + sizes + hashes)
DELETE /v1/jobs/{id}              cancel
GET    /healthz                   liveness
GET    /readyz                    readiness
```

The same routes exist on Tier 2 — clients don't need to know which tier they're talking
to. Tier 2 just adds `/v1/jobs/{id}/tasks` and worker-facing endpoints (§6.3).

### 4.3 Input data
Three options, all driven by the URI scheme in `Input.url`:
1. `file:///abs/path` — server-local file (LAN/NFS use-case).
2. `s3://…`, `gs://…`, `https://…` — fetched by the server before/while running.
3. `upload://<token>` — client uploads via `PUT /v1/uploads/{token}` first, then
   references the token in the job. Streaming-friendly (`Transfer-Encoding: chunked`).

### 4.4 GUI changes
- Settings panel adds a "Backend" selector: `Local | Remote URL`.
- When `Remote URL` is chosen, the **Run** button calls `POST /v1/jobs` instead of
  spawning the local engine. Events panel subscribes to `/events` SSE.
- The graph editor itself is unchanged — it already produces the same JSON.

### 4.5 Security (Tier 1 minimum)
- TLS terminated by the server (`--tls-cert/--tls-key`) or by a reverse proxy.
- Bearer-token auth (`--auth-token-file`) — single shared token is acceptable for
  Tier 1 (one user → one server).
- Path allowlist for `file://` inputs/outputs (`--allow-path`).
- Disable `upload://` unless `--enable-uploads` is set.

---

## 5. Tier 2 — Distributed

### 5.1 Statelessness of API/Orchestrator

**Invariant:** any orchestrator instance can handle any request for any job at any time.
Concretely:

- No job state lives in process memory beyond the lifetime of a single request.
- All durable state lives in the **State Store** (Postgres / DynamoDB / etc.).
- All task hand-off lives in the **Task Queue**.
- Long-lived SSE subscriptions are *advisory* — if the client's TCP connection lands
  on instance A and A dies, the client reconnects and lands on B, which replays events
  from the State Store's event log.

This makes the orchestrator trivially horizontally scalable behind a stateless L7 LB.
The deployment platform can scale orchestrator pods up/down based on request rate or
queue depth without coordination.

### 5.2 Orchestrator responsibilities (per request, not per job)

```
POST /v1/jobs
  1. Validate job JSON.
  2. Assign job_id (UUIDv7).
  3. Persist job + initial state in StateStore (single transaction).
  4. Materialize initial task set (see §5.4).
  5. Enqueue tasks on Queue.
  6. Return 202 with job_id.

Worker callback / task completion (POST /v1/tasks/{id}/result, or queue-side ack)
  1. Persist task result + manifest in StateStore (idempotent on task_id).
  2. Evaluate downstream readiness:
     - Static fan-out: enqueue next layer's tasks if all predecessors done.
     - Dynamic fan-out: parse the producing task's manifest, expand into N child
       tasks, persist them, enqueue.
     - Gather: when all siblings done, enqueue assembly task.
  3. If job's task graph is exhausted → mark job complete.

Periodic reconciliation (any orchestrator, leader-election via DB advisory lock or
similar — optional)
  - Re-enqueue tasks whose lease expired (worker died).
  - Mark jobs as failed when retry budget exhausted.
  - GC old completed jobs per retention policy.
```

Critically, the reconciliation loop is **idempotent and safe to run from any orchestrator**.
Use a short DB-level lock (`SELECT … FOR UPDATE SKIP LOCKED` in Postgres,
conditional writes in DynamoDB) so multiple orchestrators don't double-enqueue.

### 5.3 DistributionSpec

```go
type DistributionSpec struct {
    // Ordered stages. Each stage is a subgraph of the master Config + a strategy.
    Stages []Stage `json:"stages"`
}

type Stage struct {
    ID        string   `json:"id"`
    DependsOn []string `json:"depends_on,omitempty"` // other stage IDs
    Strategy  StageStrategy `json:"strategy"`
    Subgraph  SubgraphRef   `json:"subgraph"`   // node IDs from master Config
    Assembly  *SubgraphRef  `json:"assembly,omitempty"` // for gather strategies
}

type StageStrategy struct {
    Kind   string         `json:"kind"`   // "single" | "fanout_static" | "fanout_dynamic" | "gather"
    Params map[string]any `json:"params"` // strategy-specific
}
```

Built-in strategies (each implemented in `internal/orchestrator/strategies/`):

| Kind | Behavior |
|------|----------|
| `single` | One task running this subgraph. |
| `fanout_static` | N tasks; each gets a parameter slice (e.g. time range, file from a list). Params: `{count, partition}`. |
| `fanout_dynamic` | Run a producer task; parse its manifest; emit M child tasks. Params: `{producer_stage, splitter}` where `splitter` names a registered Go function (`scene_list`, `chapter_list`, `key_frame_list`, `byte_range`, custom). |
| `gather` | Wait for all tasks of `depends_on` stage; assemble using `Assembly` subgraph (e.g. concat demuxer, or a Go processor). |

The scene-detect → encode-per-scene → stitch example becomes:

```jsonc
"distribution": {
  "stages": [
    { "id": "detect", "strategy": {"kind": "single"},
      "subgraph": { "node_ids": ["scene_detect"] } },

    { "id": "encode", "depends_on": ["detect"],
      "strategy": {"kind": "fanout_dynamic",
                   "params": {"producer_stage": "detect", "splitter": "scene_list"}},
      "subgraph": { "node_ids": ["src", "encode_video", "encode_audio", "seg_sink"] } },

    { "id": "stitch", "depends_on": ["encode"],
      "strategy": {"kind": "gather"},
      "subgraph": { "node_ids": ["concat_src", "passthrough", "final_sink"] },
      "assembly": { "node_ids": ["concat_src", "passthrough", "final_sink"] } }
  ]
}
```

### 5.4 Task

```go
type Task struct {
    ID         string          `json:"id"`         // UUIDv7
    JobID      string          `json:"job_id"`
    StageID    string          `json:"stage_id"`
    Attempt    int             `json:"attempt"`
    Config     Config          `json:"config"`     // self-contained pipeline.Config
    Inputs     []ArtifactRef   `json:"inputs"`     // resolved URIs (signed if applicable)
    Outputs    []ArtifactSlot  `json:"outputs"`    // target URIs + manifest path
    Deadline   time.Time       `json:"deadline"`
    LeaseUntil time.Time       `json:"lease_until"`
    Requires   WorkerRequirements `json:"requires,omitempty"` // hw_accel, codecs, gpu, etc.
}
```

A task is **always a complete `pipeline.Config`** — the worker is the existing engine.
This is the property that keeps the worker dumb and the orchestrator the only place
that understands "tasks". It also means a task can be unit-tested by running it as
a Tier 0 job (`mediamolder run task.json`).

### 5.5 Task Queue abstraction

```go
// internal/distributed/queue/queue.go
type Queue interface {
    Publish(ctx context.Context, t Task) error
    // Receive returns one task plus a lease. The worker must call Heartbeat()
    // before LeaseUntil expires, or the task becomes re-deliverable.
    Receive(ctx context.Context, filter ReceiveFilter) (Lease, error)
    Heartbeat(ctx context.Context, taskID string, extend time.Duration) error
    Ack(ctx context.Context, taskID string) error
    Nack(ctx context.Context, taskID string, retryAfter time.Duration) error
}
```

Adapters: `inmemory` (test, single-binary "distributed"), `sqs`, `nats`, `redis_streams`,
`postgres_queue` (LISTEN/NOTIFY + `FOR UPDATE SKIP LOCKED`).

### 5.6 State Store abstraction

```go
// internal/distributed/state/state.go
type Store interface {
    CreateJob(ctx context.Context, j Job) error
    GetJob(ctx context.Context, id string) (Job, JobStatus, error)
    AppendEvent(ctx context.Context, jobID string, e Event) error
    StreamEvents(ctx context.Context, jobID string, since EventCursor) (<-chan Event, error)

    UpsertTask(ctx context.Context, t Task, status TaskStatus) error
    SetTaskResult(ctx context.Context, taskID string, r TaskResult) error
    TasksByStage(ctx context.Context, jobID, stageID string) ([]TaskRecord, error)

    AcquireLeaderLock(ctx context.Context, key string, ttl time.Duration) (Lock, error)
}
```

Adapters: `sqlite` (single-node dev), `postgres` (production default),
`dynamodb` (AWS-native). Schema lives in `internal/distributed/state/migrations/`.

### 5.7 Worker

- `mediamolder serve --mode=worker --queue=… --state=…`
- Loop: `Receive` → resolve input URIs → `pipeline.NewEngine(task.Config).Run()` →
  write outputs to storage → write manifest → `Ack` (or `Nack` on failure).
- Heartbeats are emitted from a goroutine tied to engine progress, not a fixed timer,
  so a stuck encode doesn't keep extending the lease forever
  (configurable: `--heartbeat=progress|timer`).
- Reports task-level events to the State Store *and* the event bus (forwarded to the
  job's SSE stream by any orchestrator).
- Advertises capabilities at startup via `WorkerCapabilities` (codecs, HW accel,
  GPU type, free disk, free RAM). Orchestrator's `ReceiveFilter` honors task
  `Requires` to route GPU tasks to GPU workers, etc.

### 5.8 Failure handling

- **Worker dies mid-task:** lease expires, task re-delivered, attempt counter increments,
  capped by `Policy.MaxAttempts`. Each attempt writes to a fresh manifest path so
  partial outputs from the failed attempt don't poison the gather stage.
- **Orchestrator dies:** another orchestrator picks up via reconciliation. The client's
  SSE reconnects.
- **Storage unavailable:** task fails with a `retryable_storage` error class; backed
  off and re-enqueued. Job-level circuit breaker if > X% of tasks in a window fail
  with the same class.
- **Poison task:** after `MaxAttempts`, task goes to a dead-letter queue / table;
  job marked `failed`; existing successful sibling artifacts kept for forensics.

### 5.9 What is *not* in the orchestrator
- No cron / scheduled jobs (a separate scheduler service can `POST /v1/jobs`).
- No multi-tenant billing.
- No per-user quotas beyond a simple rate limiter (the deployment platform owns this).

---

## 6. Storage & URIs (shared by Tier 1 & Tier 2)

New package `internal/storage/`:

```go
type FS interface {
    Open(ctx context.Context, uri string) (io.ReadCloser, error)
    Create(ctx context.Context, uri string) (io.WriteCloser, error)
    Stat(ctx context.Context, uri string) (FileInfo, error)
    Sign(ctx context.Context, uri string, op Op, ttl time.Duration) (string, error)
    List(ctx context.Context, uriPrefix string) ([]string, error)
}
```

Adapters: `file`, `s3`, `gs`, `azureblob`, `http_get` (read-only).

- All pipeline I/O nodes (file source/sink, HLS/DASH muxer, image2, concat) gain a
  thin URI-resolver shim that maps non-`file://` URIs to a local path via either
  (a) FUSE-like streaming wrapper for `s3://`, or (b) explicit prefetch to a temp
  dir when streaming isn't supported by the libavformat protocol in use.
- Manifest = small JSON sidecar produced by the worker, written next to the output
  artifact, describing: byte size, duration, codecs, frame count, checksum, child
  artifact list (for fan-out producers). The orchestrator only ever reads manifests,
  never the media.

This same layer makes Tier 1 work with `s3://` outputs even without any
distributed pieces.

### 6.1 S3 Presigned URL Generation

**Problem.** A server instance (Tier 1) or orchestrator/worker (Tier 2) may not hold an
IAM identity with direct `s3:GetObject` / `s3:PutObject` permissions on the bucket
containing the job's media. This is common in cross-account setups, or when the server is
provisioned by a deployment platform that separates the *signing identity* from the
execution identity. In these cases `s3://` URIs in the job must be converted to short-TTL
HTTPS presigned URLs before the pipeline engine (or a worker) tries to open them.

**Presigning is opt-in and server-side only.** Credentials used for presigning are never
embedded in job documents, never placed on the queue, and never transmitted to clients.
They are provisioned directly to the server instance by the operator.

Presigning activates **only when a designated credential source is explicitly
configured** (`--s3-presign-credentials` flag or AWS environment variables — see below).
It is never triggered automatically based on access errors at runtime; that would create
confusing failure modes.

**Credential sources** (evaluated in priority order by the `s3` adapter at startup):

1. **AWS SDK environment variables** — `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`,
   `AWS_SESSION_TOKEN`, `AWS_REGION`. These activate presigning if present.
2. **Credentials JSON file** — path provided via `--s3-presign-credentials /path/to/creds.json`:
   ```json
   {
     "access_key_id":     "AKIA…",
     "secret_access_key": "…",
     "session_token":     "…",
     "region":            "us-east-1"
   }
   ```
   The file must be `chmod 600` and owned by the process user. The adapter refuses to load
   it otherwise and exits at startup with a clear diagnostic.
3. **IAM instance/task role** — EC2 Instance Metadata Service, ECS task role, EKS IRSA.
   This is the default when neither of the above is configured; presigning is **not**
   activated by this source alone (direct S3 access is assumed).

**Presigning workflow.** The `PresignResolver` runs against `job.Config` before any
execution begins — not lazily at open time:

```
POST /v1/jobs  with  Input.url  = "s3://bucket/input.mp4"
                     Output.url = "s3://bucket/output/result.mp4"

PresignResolver.Resolve(job.Config, ttl)
  ├── each s3:// input URI  → s3.Sign(ctx, uri, OpGet, ttl) → https://…?X-Amz-…
  └── each s3:// output URI → s3.Sign(ctx, uri, OpPut, ttl) → https://…?X-Amz-…

pipeline.NewEngine(resolvedConfig).Run()
   // engine sees only HTTPS presigned URLs — no s3:// protocol required in libavformat
```

For **Tier 2**, `PresignResolver.Resolve` runs in the orchestrator when materializing
each `Task`, substituting presigned URLs into `Task.Config` before the task is enqueued.
Workers and queue messages never contain `s3://` URIs or signing credentials.

**TTL policy.**
- Default: `task.Deadline + 30 min`.
- For Tier 1 single-process jobs: `--s3-presign-ttl` (default `24h`).
- Hard cap: 7 days (AWS S3 maximum for presigned URLs using temporary/STS credentials).
- A warning is logged if `Job.Requirements.EstimatedDuration` is set and exceeds `TTL − 30 min`.
- If a presigned output URL expires before a task completes (e.g., during a long retry
  cycle), the worker reports a `presign_expired` error class. The orchestrator
  re-presigns and re-enqueues the task **without** incrementing the failure attempt
  counter — it is a scheduling error, not a task error.

**Output presigning.** Presigned `PUT` URLs for outputs are generated at the same time
as input `GET` URLs. For objects > 5 GB, the adapter generates a presigned
`CreateMultipartUpload` URL plus presigned `UploadPart` URLs (default part size: 128 MB,
up to 1 000 parts), surfaced as an `internal/storage/s3multipart` helper used by the
output-sink shim.

**Manifest paths** (Tier 2) are also presigned for `PUT` at task dispatch time, and
for `GET` when the orchestrator reads them during gather/fan-in.

**Security constraints:**
- The credentials file path may not be inside the job workdir or any client-writable
  directory; the adapter rejects the path at startup if it is.
- `secret_access_key` and `session_token` values are never logged, never included in
  events/metrics, and never returned by any API endpoint.
- Presigned URLs are treated as secrets: not stored in the State Store (only in the
  in-flight task record), not logged at INFO level (DEBUG only, with the query string
  redacted to `?X-Amz-…[redacted]`), and not included in error responses sent to the
  submitting client.
- The credentials file is read once at startup and not re-read on SIGHUP (to avoid TOCTOU
  races). Credential rotation requires a process restart; this is intentional for
  auditability.

---

## 7. Schema versioning

- New top-level document type `Job` introduced at `schema_version: "1.4"`.
- Existing `Config` (`schema_version: "1.3"`) is embedded under `job.config` unchanged.
- `schema/v1.4.json` validates both bare `Config` (back-compat) and `Job`.
- `TestSchemaSyncWithGoStructs` extended to cover `Job`, `DistributionSpec`, `Task`.

---

## 8. Observability

- Existing pipeline events (`StateChanged`, `FrameStats`, `Error`, etc.) flow unchanged
  inside the worker.
- New event kinds at the job level: `JobAccepted`, `TaskScheduled`, `TaskStarted`,
  `TaskCompleted`, `TaskFailed`, `StageCompleted`, `JobCompleted`, `JobFailed`.
- Every event carries `job_id`, `task_id` (when applicable), `stage_id`, `attempt`.
- Prometheus: existing metrics gain `job_id`/`task_id` labels behind a cardinality
  switch (`--metrics-cardinality=high|low`). Default `low` aggregates by stage.
- OTEL traces: `Job` span → `Stage` spans → `Task` spans → existing pipeline spans.
  Trace context propagated through the queue payload.

---

## 9. Security model

| Surface | Tier 1 | Tier 2 |
|---------|--------|--------|
| Client → API | TLS + bearer token | TLS + OIDC/JWT or mTLS |
| API → Queue | n/a | IAM role / SASL / mTLS |
| API → State | n/a | DB credentials from secret manager |
| Worker → Queue | n/a | Same as API → Queue |
| Worker → Storage | local FS, optional S3 IAM | IAM role per worker (signed URLs preferred) |
| Job content | Path allowlist + URI scheme allowlist | Same + per-job storage prefix isolation |
| Presign credentials | `--s3-presign-credentials` file (`chmod 600`) or AWS env vars; never in job JSON | Same; credential file path must not be client-writable; credentials never leave the server process |

The orchestrator **never** hands a worker raw long-lived credentials. Worker tasks get
short-TTL signed URLs for inputs (read) and outputs (write) scoped to the job's prefix.

---

## 10. Backwards Compatibility & Migration

| Existing capability | After this design |
|---------------------|-------------------|
| `mediamolder run job.json` | Unchanged. |
| GUI on localhost | Unchanged; "Backend" selector defaults to Local. |
| `pipeline.Config` JSON | Unchanged; now also embeddable as `job.config`. |
| Existing tests | Must remain green. New code lives under `internal/distributed/`, `internal/orchestrator/`, `internal/storage/`, `cmd/mediamolder/serve.go`. |

A job submitted to a Tier 2 API without a `Distribution` block is run as a single task
on a single worker, so users can move to Tier 2 without restructuring jobs.

---

## 11. Phased Implementation

**Phase A — URI storage layer + Tier 1**
1. `internal/storage/` with `file` + `s3` adapters; URI shim in I/O nodes.
2. `PresignResolver` in `internal/storage/s3presign.go`; credential loading from env
   and `--s3-presign-credentials` file; `chmod 600` check; multipart helper.
3. `cmd/mediamolder/serve.go` with `--mode=server`. Bearer auth, SSE events, uploads.
4. GUI "Backend" selector; `mediamolder job submit` CLI.
5. End-to-end test: laptop GUI → EC2 server → S3 input + S3 output (presigned path).
6. Updated README.md, docs/openapi-gui.yaml, docs/openapi-metrics.yaml, /docs/using_mediamolder.md and /docs/gui.md
7. Updated architecture documents in /docs/architecture

**Phase B — Job model + in-memory distributed**
1. `pipeline/job.go` and `schema/v1.4.json`.
2. `internal/distributed/queue` (`inmemory`) + `state` (`sqlite`).
3. `--mode=api` and `--mode=worker` running in one binary against in-memory queue.
4. `single` and `fanout_static` strategies.
5. Updated README.md, docs/openapi-gui.yaml, docs/openapi-metrics.yaml, /docs/using_mediamolder.md and /docs/gui.md
6. Updated architecture documents in /docs/architecture
7. Tier 1 server keeps working; Tier 2 single-host works for dev.

**Phase C — Stateless orchestrator + real queue/state**
1. `postgres` state adapter (with migrations), `sqs` or `nats` queue adapter.
2. Leader-electionless reconciliation loop (advisory locks).
3. Run 2+ API instances behind a local nginx in CI to prove statelessness.
4. Lease + heartbeat + retry + DLQ.
5. Updated README.md, docs/openapi-gui.yaml, docs/openapi-metrics.yaml, /docs/using_mediamolder.md and /docs/gui.md
6. Updated architecture documents in /docs/architecture

**Phase D — Dynamic fan-out & gather**
1. `fanout_dynamic` with `scene_list`, `byte_range`, `chapter_list` splitters.
2. `gather` with concat-demuxer assembly subgraph.
3. End-to-end: split-encode-stitch demo from CLI and GUI.
4. Updated README.md, docs/openapi-gui.yaml, docs/openapi-metrics.yaml, /docs/using_mediamolder.md and /docs/gui.md
5. Updated architecture documents in /docs/architecture

**Phase E — Production polish**
1. Capability-aware routing (GPU, codec, region).
2. OIDC/mTLS auth.
3. OTEL trace propagation across queue.
4. DynamoDB state adapter; signed-URL output writers.
5. Updated README.md, docs/openapi-gui.yaml, docs/openapi-metrics.yaml, /docs/using_mediamolder.md and /docs/gui.md
6. Updated architecture documents in /docs/architecture

Each phase ships independently and leaves the previous tier(s) fully functional.

---

## 12. Open Questions

1. **Queue payload size cap.** SQS hard-limits messages to 256 KB. Large `pipeline.Config`
   payloads (e.g., long concat lists) must be stored in State and referenced by ID in
   the queue message. Decision: always store `Task.Config` in State; queue carries only
   `{job_id, task_id, lease}`.
2. **Streaming vs. prefetch for `s3://` inputs.** Some libavformat demuxers seek heavily
   (MP4 moov atom at end). Default: prefetch when format is in a known-seek-heavy list;
   otherwise stream via the protocol's `http`/`s3` reader if available.
3. **Cross-stage frame edges.** Today's graph can pass frames between nodes. Across
   tasks, only files/manifests cross the boundary. Document this limit; the
   `fanout_dynamic` splitter must always be preceded by a sink that materializes
   the boundary as files + a manifest.
4. **Worker auto-update.** Out of scope here; deployment platform's problem.
5. **Multi-region.** Out of scope; storage + queue + state must all be in the same
   region for v1.

---

## 13. Summary

- **Tier 1** adds a thin HTTP server around the existing engine — no new concepts,
  one binary mode flag, immediately useful for "GUI on laptop, encode on the big box."
- **Tier 2** adds a stateless orchestrator, a pluggable queue, a pluggable state store,
  and a pluggable storage layer. The worker is the existing engine. The orchestrator
  is the only component that understands tasks.
- The same `Job` JSON works in all tiers; `Distribution` is optional and additive.
- Provisioning, scaling, and node lifecycle belong to an external platform; MediaMolder
  is designed to be a well-behaved tenant of one.
