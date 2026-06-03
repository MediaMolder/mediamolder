# Running MediaMolder with a Remote Backend

This guide explains how to offload encoding work to a remote machine — a single
GPU server (Tier 1) or a scalable multi-node cluster (Tier 2) — while driving
jobs from the GUI on your laptop or from the `mediamolder job` CLI.

---

## Contents

1. [Concepts](#1-concepts)
2. [Choosing a deployment tier](#2-choosing-a-deployment-tier)
3. [Tier 1 — single remote server](#3-tier-1--single-remote-server)
   - [Requirements](#31-requirements)
   - [Quick start](#32-quick-start)
   - [TLS](#33-tls)
   - [Authentication](#34-authentication)
   - [S3 inputs and outputs](#35-s3-inputs-and-outputs)
   - [File uploads from the client](#36-file-uploads-from-the-client)
   - [Connecting the GUI](#37-connecting-the-gui)
   - [Connecting the CLI](#38-connecting-the-cli)
   - [Tier 1 server flags](#39-tier-1-server-flags)
4. [Tier 2 — distributed cluster](#4-tier-2--distributed-cluster)
   - [Requirements](#41-requirements)
   - [Architecture overview](#42-architecture-overview)
   - [Minimal single-host cluster](#43-minimal-single-host-cluster)
   - [Production multi-node cluster](#44-production-multi-node-cluster)
   - [AWS-native stack (SQS + DynamoDB)](#45-aws-native-stack-sqs--dynamodb)
   - [OIDC authentication](#46-oidc-authentication)
   - [mTLS client certificates](#47-mtls-client-certificates)
   - [Capability-aware routing](#48-capability-aware-routing)
   - [OTEL distributed tracing](#49-otel-distributed-tracing)
   - [Dead-letter queue](#410-dead-letter-queue)
   - [Tier 2 flags reference](#411-tier-2-flags-reference)
5. [REST API reference](#5-rest-api-reference)
   - [Tier 1 endpoints](#51-tier-1-endpoints)
   - [Job submission example](#52-job-submission-example)
   - [SSE event stream](#53-sse-event-stream)
   - [Tier 2 additional endpoints](#54-tier-2-additional-endpoints)
6. [Security checklist](#6-security-checklist)
7. [Troubleshooting](#7-troubleshooting)

---

## 1. Concepts

MediaMolder has three operating modes, each a superset of the previous:

| Mode | Flag | What runs where |
|------|------|-----------------|
| **Local** | _(default, `mediamolder run`)_ | Everything on your machine |
| **Tier 1** | `--mode=server` | GUI/CLI on your laptop; encode on one remote machine |
| **Tier 2** | `--mode=api` + `--mode=worker` | GUI/CLI on your laptop; orchestrator + N workers anywhere |

The same job JSON you use locally works in every mode — there is nothing to
rewrite. The `Distribution` block added in Tier 2 is purely additive and
optional.

---

## 2. Choosing a deployment tier

**Use Tier 1 when:**
- You have one beefy remote machine (e.g. a workstation with a GPU, an EC2 `g4dn` instance).
- Jobs run sequentially or you want simple parallelism (multiple `--max-jobs`).
- You do not need fault tolerance — if the server process dies, the job is lost.
- Setup time matters: Tier 1 is a single binary, a TLS cert, and a token.

**Use Tier 2 when:**
- You need to split a single job across multiple workers (scene-parallel encoding).
- You need fault tolerance — crashed workers are retried automatically.
- You want to scale workers up or down independently of the API surface.
- You are running on AWS or another cloud with managed queue/state services.

Both tiers are served by the same `mediamolder` binary; only the `--mode` flag
changes.

---

## 3. Tier 1 — single remote server

### 3.1 Requirements

On the remote machine:
- `mediamolder` binary built from source (see [build instructions](build/)).
- Any FFmpeg libraries you need linked at build time (CUDA, VideoToolbox, etc.).
- Network access from your client to the server on the chosen port.
- A TLS certificate (self-signed is fine for a private network; use Let's Encrypt
  or your CA for public-facing servers).

On your laptop / CI:
- `mediamolder` binary (only the CLI; or just `curl` for scripted use).

### 3.2 Quick start

```bash
# ── Remote machine ─────────────────────────────────────────────────────────
# 1. Generate an auth token.
TOKEN=$(openssl rand -hex 32)
echo "$TOKEN" > /etc/mediamolder/token
chmod 600 /etc/mediamolder/token

# 2. Generate a self-signed cert if you don't have one.
openssl req -x509 -newkey rsa:4096 -days 365 -nodes \
  -keyout /etc/mediamolder/server.key \
  -out    /etc/mediamolder/server.crt \
  -subj   "/CN=my-encode-box"

# 3. Start the server.
mediamolder serve \
  --mode=server \
  --addr=:8443 \
  --tls-cert=/etc/mediamolder/server.crt \
  --tls-key=/etc/mediamolder/server.key \
  --auth-token-file=/etc/mediamolder/token
```

```bash
# ── Laptop ─────────────────────────────────────────────────────────────────
# Submit a job from the command line.
mediamolder job submit \
  --backend=https://my-server.example.com:8443 \
  --token="$TOKEN" \
  job.json
```

The server starts in seconds. There is no database, no queue, and no other
dependencies beyond the `mediamolder` binary itself.

### 3.3 TLS

TLS is strongly recommended — auth tokens travel in HTTP headers.

| Scenario | Recommended approach |
|----------|---------------------|
| Private LAN / VPN | Self-signed cert; clients configure `--insecure` or add the cert to their trust store |
| Public internet | Let's Encrypt via your reverse proxy (nginx/caddy); proxy TLS-terminates and forwards to `--addr=127.0.0.1:8080` (plain HTTP locally) |
| Corporate CA | Issue a cert from your CA; distribute the CA bundle to clients |

Omit `--tls-cert` / `--tls-key` to run plain HTTP. Only do this on a trusted
network with no auth token configured (e.g. loopback-only dev).

### 3.4 Authentication

Set `--auth-token-file` to a `chmod 600` file containing a random token.
Every client request must carry `Authorization: Bearer <token>`.

`/healthz` and `/readyz` are always unauthenticated (needed by load-balancer
health checks).

To rotate the token: update the file and restart the server.

### 3.5 S3 inputs and outputs

If your pipeline uses `s3://` URIs the server can presign them on the client's
behalf so workers never need AWS credentials. Set up credentials on the server:

```bash
# Option A — environment variables (simplest).
export AWS_ACCESS_KEY_ID=AKIA...
export AWS_SECRET_ACCESS_KEY=...
export AWS_REGION=us-east-1

mediamolder serve --mode=server ...

# Option B — credentials file (recommended for services; must be mode 0600).
cat > /etc/mediamolder/s3-creds.json <<'EOF'
{
  "access_key_id":     "AKIA...",
  "secret_access_key": "...",
  "region":            "us-east-1"
}
EOF
chmod 600 /etc/mediamolder/s3-creds.json

mediamolder serve --mode=server \
  --s3-presign-credentials=/etc/mediamolder/s3-creds.json \
  --s3-presign-ttl=12h \
  ...
```

The server converts every `s3://` URI in the submitted job to a short-lived
HTTPS presigned URL before execution begins. The credentials never leave the
server process.

### 3.6 File uploads from the client

Enable direct file upload when inputs are local files on your laptop:

```bash
# Server — enable the upload endpoint and restrict work files to /tmp/mm.
mediamolder serve --mode=server \
  --enable-uploads \
  --workdir=/tmp/mm \
  ...
```

```bash
# Client — upload a file, get back a URI, embed it in a pipeline.
UPLOAD_URI=$(mediamolder job upload \
  --backend=https://my-server.example.com:8443 \
  --token="$TOKEN" \
  /Users/me/footage.mp4)

# $UPLOAD_URI is something like server-upload://a3f9... 
# Use it as an input URL in your pipeline JSON.
```

The upload endpoint allocates a single-use token per file; the file is stored
in `--workdir` and deleted when the job completes.

### 3.7 Connecting the GUI

1. Open the GUI (`mediamolder gui` or visit the already-running server URL).
2. Click **Backend** in the toolbar.
3. Enter the server URL (e.g. `https://my-server.example.com:8443`) and token.
4. Click **Connect**. The toolbar button turns blue when connected.

All subsequent **Run** clicks send the job to the remote server. Click
**Backend → Use Local** to revert to local execution.

### 3.8 Connecting the CLI

```bash
# Persist settings in the environment (or a shell alias).
export MEDIAMOLDER_BACKEND=https://my-server.example.com:8443
export MEDIAMOLDER_TOKEN=$TOKEN

mediamolder job submit job.json
mediamolder job status <job-id>
mediamolder job events  <job-id>   # SSE stream printed to stdout
mediamolder job cancel  <job-id>
```

### 3.9 Tier 1 server flags

| Flag | Default | Description |
|------|---------|-------------|
| `--addr` | `:8443` | TCP listen address |
| `--tls-cert` | _(none)_ | PEM certificate; omit for plain HTTP |
| `--tls-key` | _(none)_ | PEM private key |
| `--auth-token-file` | _(none)_ | File containing the Bearer token (must be `chmod 600`) |
| `--max-jobs` | `0` (unlimited) | Maximum number of concurrently running jobs |
| `--workdir` | OS temp dir | Directory for upload temp files |
| `--s3-presign-credentials` | _(none)_ | Path to S3 credentials JSON (must be `chmod 600`) |
| `--s3-presign-ttl` | `24h` | Presigned URL validity window |
| `--enable-uploads` | `false` | Enable `PUT /v1/uploads/{token}` for local file inputs |
| `--allow-path` | _(none)_ | Permit `file://` inputs under this path prefix (repeatable) |

---

## 4. Tier 2 — distributed cluster

### 4.1 Requirements

On every node:
- `mediamolder` binary built from source.
- Network connectivity between API nodes and worker nodes (for heartbeating and
  queue message routing).

Infrastructure (choose one set):

| Component | Local/dev | Production (Postgres) | Production (AWS) |
|-----------|-----------|----------------------|------------------|
| **Queue** | built-in (`inmemory://`) | NATS JetStream | Amazon SQS |
| **State** | SQLite | Postgres 14+ | Amazon DynamoDB |

Workers and API nodes are the same binary — only the `--mode` flag differs.

### 4.2 Architecture overview

```
Client (GUI / CLI)
       │  POST /v1/jobs
       ▼
┌──────────────────┐   Publish   ┌──────────────────┐
│  API server(s)   │────────────▶│  Queue           │
│  --mode=api      │             │  (NATS / SQS /   │
│  (Orchestrator)  │◀────────────│   in-memory)     │
└──────────────────┘   Ack/Nack  └──────────────────┘
       │                                 ▲
       │  Read/Write                     │ Receive
       ▼                                 │
┌──────────────────┐             ┌──────────────────┐
│  State store     │             │  Worker(s)        │
│  (Postgres /     │◀────────────│  --mode=worker   │
│   DynamoDB /     │  UpdateTask │  (Engine)        │
│   SQLite)        │             └──────────────────┘
└──────────────────┘
```

- **API nodes** accept job submissions, materialise tasks, publish them to the
  queue, and serve the status/SSE API.
- **Workers** pull tasks from the queue, run the encode, and write results back
  to the state store.
- **State store** is the single source of truth for job and task state. Both API
  nodes and workers read and write it directly.
- **Queue** is used only to wake up workers; it does not carry task content
  (task config is always fetched from state).

Any number of API nodes and workers can share the same queue and state store.

### 4.3 Minimal single-host cluster

Useful for development, testing the distributed code path, or small single-machine
setups where you want fault tolerance at the task level:

```bash
# One process — embedded workers, in-memory queue, SQLite state.
mediamolder serve \
  --mode=api \
  --addr=:8080 \
  --workers=4 \
  --state=sqlite:///var/lib/mediamolder/state.sqlite3 \
  --auth-token-file=/etc/mediamolder/token
```

This is equivalent to Tier 1 but uses the Tier 2 task model internally: tasks
can be retried on failure, the DLQ is available, and you can inspect per-task
status.

### 4.4 Production multi-node cluster

**Step 1 — Postgres**

Create a database and user:

```sql
CREATE DATABASE mediamolder;
CREATE USER mm WITH PASSWORD 'changeme';
GRANT ALL ON DATABASE mediamolder TO mm;
```

Migrations are applied automatically when the API server starts.

**Step 2 — NATS JetStream** (or skip to step 3 for SQS)

```bash
nats-server -js &   # or use a managed NATS cluster
```

**Step 3 — API servers**

Run at least two for high availability. They elect a reconciler leader via
Postgres advisory lock, so running more than one is always safe.

```bash
mediamolder serve \
  --mode=api \
  --addr=:8080 \
  --workers=0 \                      # API-only; workers run separately
  --state=postgres://mm:changeme@db.example.com/mediamolder?sslmode=require \
  --queue=nats://nats.example.com:4222/mediamolder \
  --reconcile-interval=30s \
  --tls-cert=/etc/ssl/server.crt \
  --tls-key=/etc/ssl/server.key \
  --auth-token-file=/etc/mediamolder/token
```

Put a load balancer (nginx, HAProxy, AWS ALB) in front of the API nodes. The
`/healthz` and `/readyz` endpoints can be used for health checks.

**Step 4 — Workers**

Workers can run on separate machines, containers, or spot instances. Scale them
up and down freely — the queue and state store handle continuity.

```bash
mediamolder serve \
  --mode=worker \
  --workers=8 \
  --state=postgres://mm:changeme@db.example.com/mediamolder?sslmode=require \
  --queue=nats://nats.example.com:4222/mediamolder
```

Workers do not need to be reachable from the API nodes or from clients. They
only need outbound access to the queue, state store, and storage (S3 / NFS /
local disk).

### 4.5 AWS-native stack (SQS + DynamoDB)

No Postgres or NATS required:

```bash
export AWS_ACCESS_KEY_ID=...
export AWS_SECRET_ACCESS_KEY=...
export AWS_REGION=us-east-1

# Create a DynamoDB table with PK (String) + SK (String) as primary key.
aws dynamodb create-table \
  --table-name mediamolder-state \
  --attribute-definitions \
    AttributeName=PK,AttributeType=S \
    AttributeName=SK,AttributeType=S \
  --key-schema \
    AttributeName=PK,KeyType=HASH \
    AttributeName=SK,KeyType=RANGE \
  --billing-mode PAY_PER_REQUEST

# API server.
mediamolder serve \
  --mode=api \
  --addr=:8080 \
  --workers=0 \
  --state=dynamodb://dynamodb.us-east-1.amazonaws.com/mediamolder-state \
  --queue=sqs://sqs.us-east-1.amazonaws.com/123456789012/mediamolder-tasks \
  --reconcile-interval=60s \
  --auth-token-file=/etc/mediamolder/token

# Workers (on ECS, Fargate, or EC2).
mediamolder serve \
  --mode=worker \
  --workers=4 \
  --state=dynamodb://dynamodb.us-east-1.amazonaws.com/mediamolder-state \
  --queue=sqs://sqs.us-east-1.amazonaws.com/123456789012/mediamolder-tasks
```

Credentials are loaded from standard AWS environment variables or from the EC2
instance / ECS task IAM role. No credentials file is required when running on
AWS infrastructure with an appropriate IAM role attached.

### 4.6 OIDC authentication

Replace static bearer tokens with OIDC JWTs (Google, Okta, Auth0, Keycloak,
Azure AD, etc.):

```bash
mediamolder serve \
  --mode=api \
  --oidc-issuer=https://accounts.google.com \
  --oidc-client-id=MY_OAUTH2_CLIENT_ID \
  --addr=:8443 \
  --tls-cert=/etc/ssl/server.crt \
  --tls-key=/etc/ssl/server.key \
  --state=...  --queue=...
```

Every request must carry a valid `Authorization: Bearer <jwt>` header. The
server fetches the JWKS from `{issuer}/.well-known/openid-configuration` on
startup and re-fetches on unknown `kid`. Only RS256-signed tokens are accepted.

`/healthz` and `/readyz` remain unauthenticated.

`--oidc-issuer` and `--auth-token-file` are mutually exclusive — OIDC takes
precedence if both are set.

### 4.7 mTLS client certificates

Require clients to present a certificate signed by your CA. This is useful for
machine-to-machine API access (worker → API, CI/CD pipeline → API) where you
want to eliminate bearer tokens entirely.

```bash
# Generate or obtain a CA bundle and distribute client certs to authorised callers.
mediamolder serve \
  --mode=api \
  --addr=:8443 \
  --tls-cert=/etc/ssl/server.crt \
  --tls-key=/etc/ssl/server.key \
  --mtls-ca=/etc/ssl/client-ca.pem \          # PEM bundle — all trusted client CAs
  --state=...  --queue=...
```

`--mtls-ca` requires `--tls-cert` and `--tls-key`. Client certificates are
verified at the TLS handshake. An invalid or missing client cert is rejected
before any HTTP traffic is read.

mTLS and OIDC can be combined (certificate for transport-layer identity, JWT for
application-layer identity).

### 4.8 Capability-aware routing

Workers advertise the hardware they have. Tasks can declare what they require.
The worker only dequeues tasks whose requirements it can satisfy.

```bash
# GPU worker — advertises CUDA and NVENC codecs, restricted to us-east-1.
mediamolder serve \
  --mode=worker \
  --workers=4 \
  --capabilities=cuda,h264_nvenc,hevc_nvenc \
  --region=us-east-1 \
  --state=...  --queue=...

# CPU-only worker — accepts anything with no region restriction.
mediamolder serve \
  --mode=worker \
  --workers=16 \
  --state=...  --queue=...
```

In the job JSON, set requirements on any stage's distribution block:

```jsonc
{
  "distribution": {
    "stages": [
      {
        "id": "encode",
        "strategy": "single",
        "requires": {
          "hardware_accel": ["cuda"],
          "codecs":         ["h264_nvenc"],
          "region":         "us-east-1"
        }
      }
    ]
  }
}
```

Matching rules:
- A task with no `requires` is accepted by **any** worker.
- `hardware_accel` and `codecs` entries are matched case-insensitively against
  the worker's `--capabilities` list.
- `region` must match the worker's `--region` exactly. If either side has no
  region set, the region constraint is not enforced.

### 4.9 OTEL distributed tracing

Tier 2 propagates OpenTelemetry trace context through the queue automatically.
The orchestrator injects the current span context into each task before
publishing; the worker extracts it before executing the pipeline, so task spans
appear as children of the originating job span in your trace backend.

Configure your OTEL exporter before starting either process:

```bash
export OTEL_EXPORTER_OTLP_ENDPOINT=https://otel.example.com:4317
export OTEL_SERVICE_NAME=mediamolder-api     # or mediamolder-worker
export OTEL_RESOURCE_ATTRIBUTES=deployment.environment=production

mediamolder serve --mode=api ...
```

No MediaMolder flags are required — tracing is active whenever an OTEL SDK is
initialised in the process. Refer to your OTEL SDK initialisation documentation
for exporter-specific setup.

### 4.10 Dead-letter queue

When a task exceeds its `policy.max_attempts` (default 3) it is moved to the
dead-letter queue and the job is marked `failed`. Dead-lettered tasks are
preserved for forensics — their partial output artifacts are not deleted.

Inspect dead-lettered tasks:

```bash
curl -H "Authorization: Bearer $TOKEN" \
  https://api.example.com/v1/jobs/{job-id}/dlq
```

Response is a JSON array of objects with `task_id`, `stage_id`, `reason`,
`attempt`, and `created_at`.

### 4.11 Tier 2 flags reference

| Flag | Default | Description |
|------|---------|-------------|
| `--mode` | `server` | Operating mode: `server` \| `api` \| `worker` |
| `--queue` | `inmemory://` | Queue URI: `inmemory://`, `nats://[user:pass@]host:port[/stream]`, `sqs://sqs.{region}.amazonaws.com/{account}/{queue}` |
| `--state` | `sqlite:///tmp/mediamolder-state.sqlite3` | State-store URI: `sqlite:///path`, `postgres://[user:pass@]host/db[?opts]`, `dynamodb://dynamodb.{region}.amazonaws.com/{table}` |
| `--workers` | `1` | Embedded worker goroutines (`api` mode only) |
| `--reconcile-interval` | `30s` | How often the reconciler scans for expired leases; `0` to disable |
| `--capabilities` | _(none)_ | Comma-separated capability list advertised by this worker (e.g. `cuda,h264_nvenc`) |
| `--region` | _(none)_ | Deployment region advertised by this worker (e.g. `us-east-1`) |
| `--oidc-issuer` | _(none)_ | OIDC issuer URL for JWT validation; mutually exclusive with `--auth-token-file` |
| `--oidc-client-id` | _(none)_ | Expected OIDC audience / client ID |
| `--mtls-ca` | _(none)_ | PEM CA bundle for mTLS client cert verification; requires `--tls-cert`/`--tls-key` |

---

## 5. REST API reference

All `/v1/` endpoints require `Authorization: Bearer <token>` except `/healthz`
and `/readyz`. The SSE events endpoint also accepts `?token=<token>` as a query
parameter because the browser `EventSource` API cannot set custom headers.

### 5.1 Tier 1 endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/v1/jobs` | Submit a job (JSON body = pipeline config) |
| `GET` | `/v1/jobs/{id}` | Get job status |
| `GET` | `/v1/jobs/{id}/events` | SSE stream of job events |
| `GET` | `/v1/jobs/{id}/artifacts` | List output artifacts |
| `DELETE` | `/v1/jobs/{id}` | Cancel a running job |
| `POST` | `/v1/uploads` | Allocate an upload token |
| `PUT` | `/v1/uploads/{token}` | Upload a file (requires `--enable-uploads`) |
| `GET` | `/healthz` | Liveness probe |
| `GET` | `/readyz` | Readiness probe |

### 5.2 Job submission example

```http
POST /v1/jobs HTTP/1.1
Content-Type: application/json
Authorization: Bearer <token>

{ ... pipeline config ... }
```

Response `202 Accepted`:

```json
{
  "id":         "a1b2c3d4",
  "status_url": "https://server:8443/v1/jobs/a1b2c3d4",
  "events_url": "https://server:8443/v1/jobs/a1b2c3d4/events"
}
```

For Tier 2 distributed jobs (schema_version `"1.4"`), include a `distribution`
block with one or more stages:

```json
{
  "schema_version": "1.4",
  "name": "encode-4-segments",
  "config": {
    "schema_version": "1.0",
    "inputs": [...],
    "graph": {...},
    "outputs": [...]
  },
  "distribution": {
    "stages": [
      {
        "id": "encode",
        "strategy": { "kind": "fanout_static", "params": { "count": 4 } }
      }
    ]
  }
}
```

Schema versions `"1.0"`–`"1.2"` (bare graph definitions) are accepted and
wrapped in a single-task job automatically for backward compatibility.

### 5.3 SSE event stream

```bash
curl --no-buffer \
  -H "Authorization: Bearer $TOKEN" \
  "https://server:8443/v1/jobs/a1b2c3d4/events"
```

Each event has the form `event: <type>\ndata: <JSON>\n\n` where `<type>` is one
of:

| Event type | Description |
|------------|-------------|
| `state` | Job or task state transition |
| `metrics` | Encoding progress metrics (fps, bitrate, etc.) |
| `error` | Non-fatal or fatal error detail |
| `log` | Informational log line from the encoder |
| `metadata` | Output file metadata written after completion |
| `done` | Final event; no further events will be sent |

### 5.4 Tier 2 additional endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/jobs/{id}/tasks` | List all tasks with their status and results |
| `GET` | `/v1/jobs/{id}/events` | SSE event log replayed from state store; cursor via `?after=<id>` |
| `GET` | `/v1/jobs/{id}/artifacts` | Aggregate `ArtifactRef` list from all completed tasks |
| `GET` | `/v1/jobs/{id}/dlq` | Dead-lettered tasks for this job (see §4.10) |

---

## 6. Security checklist

Before exposing a MediaMolder server to the network, review each item:

- **[ ] TLS enabled** — `--tls-cert` + `--tls-key` are set. Plain HTTP is only
  acceptable on a trusted loopback interface.
- **[ ] Auth configured** — either `--auth-token-file` (Tier 1/2 static token),
  `--oidc-issuer` (Tier 2 JWT), or `--mtls-ca` (Tier 2 mTLS) is active. Running
  without any authentication is only safe when the process is bound to
  `127.0.0.1`.
- **[ ] `--allow-path` scoped** — if `file://` inputs are permitted, the path
  allowlist should be as narrow as possible. Default is empty (deny all
  `file://` inputs from client jobs).
- **[ ] Uploads disabled or scoped** — `--enable-uploads` exposes a writable
  endpoint. Enable only when necessary; set `--workdir` to a dedicated partition
  with a disk quota.
- **[ ] S3 credentials are not in job JSON** — credentials belong on the server
  via `--s3-presign-credentials` or AWS environment variables. Clients submit
  `s3://` URIs; the server presigns them.
- **[ ] S3 credentials file is `chmod 600`** — the server refuses to start if the
  file permissions are wrong.
- **[ ] Token file is `chmod 600`** — keep the auth token inaccessible to other
  processes.
- **[ ] Workers are not internet-facing** — workers only need outbound access to
  the queue, state store, and storage endpoints. They do not expose an HTTP port.
- **[ ] Firewall the state store and queue** — Postgres, NATS, SQS, and DynamoDB
  should be reachable only from the API and worker nodes, not from the internet.

---

## 7. Troubleshooting

### `connection refused` when submitting a job

Check that the server is listening on the expected port:
```bash
curl -k https://my-server.example.com:8443/healthz
```
If this fails, the server is not running or the port is firewalled.

### `401 Unauthorized`

The bearer token does not match. Verify the token on the server:
```bash
cat /etc/mediamolder/token
```
And compare with the token your client is sending (check `Authorization` header
in verbose output: `mediamolder job submit --verbose ...`).

For OIDC: ensure the token has not expired and the `aud` claim matches
`--oidc-client-id`.

### Jobs stuck in `queued` state

Workers are not consuming from the queue. Check:
1. `mediamolder serve --mode=worker` is running and can reach the queue.
2. Queue URI matches between API node and workers exactly.
3. If using `--capabilities`, verify the task's `requires` fields are satisfied
   by at least one running worker.

### `certificate signed by unknown authority`

Your client does not trust the server's TLS certificate. Either:
- Pass `--insecure` to the CLI (dev/test only).
- Add the server's CA certificate to your OS trust store.
- Use a certificate signed by a public CA (Let's Encrypt).

### DynamoDB `ResourceNotFoundException`

The table does not exist. Create it first:
```bash
aws dynamodb create-table \
  --table-name <your-table> \
  --attribute-definitions AttributeName=PK,AttributeType=S AttributeName=SK,AttributeType=S \
  --key-schema AttributeName=PK,KeyType=HASH AttributeName=SK,KeyType=RANGE \
  --billing-mode PAY_PER_REQUEST
```

### Task retried repeatedly / ends up in DLQ

Check the job events stream for the error:
```bash
mediamolder job events <job-id> --backend=... --token=...
```
Common causes: storage URI unreachable, codec not available on worker, presigned
URL expired (increase `--s3-presign-ttl`), or OOM on the worker node.

### Leaked temporary files

If the server is killed mid-job, upload files in `--workdir` may not be cleaned
up. Add a cron job or systemd timer:
```bash
find /tmp/mm -mtime +1 -delete
```
