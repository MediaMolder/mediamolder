# Remote Server Setup (Tier 1)

`mediamolder serve` runs a lightweight HTTP server that accepts pipeline job
submissions from the GUI or `mediamolder job` CLI. This is the recommended
deployment pattern when you need to run encode jobs on a remote machine (e.g.
an EC2 instance with a GPU) while using the browser GUI on your laptop.

## Quick start

```bash
# On the remote machine — generate a random token and start the server
TOKEN=$(openssl rand -hex 32)
echo "$TOKEN" > /etc/mediamolder/token     # optional — can also pass inline
chmod 600 /etc/mediamolder/token

mediamolder serve \
  --addr=:8443 \
  --tls-cert=/etc/mediamolder/server.crt \
  --tls-key=/etc/mediamolder/server.key \
  --auth-token-file=/etc/mediamolder/token
```

```bash
# On your laptop — submit a job from the GUI by clicking Backend in the toolbar,
# entering the server URL and token, then clicking Run as usual.

# Or from the CLI:
mediamolder job submit \
  --backend=https://my-server.example.com:8443 \
  --token="$TOKEN" \
  pipeline.json
```

## Flags

| Flag | Default | Description |
|---|---|---|
| `--addr` | `:8443` | TCP listen address |
| `--tls-cert` | _(none)_ | PEM certificate; if omitted, plain HTTP |
| `--tls-key` | _(none)_ | PEM private key |
| `--auth-token-file` | _(none)_ | File containing the Bearer token (must be readable) |
| `--max-jobs` | `0` (unlimited) | Concurrent job limit |
| `--workdir` | OS temp dir | Directory for upload temp files |
| `--s3-presign-credentials` | _(none)_ | Path to S3 credentials JSON (must be mode 0600) |
| `--s3-presign-ttl` | `24h` | Presigned URL validity window |
| `--enable-uploads` | `false` | Enable `PUT /v1/uploads/{token}` for local file inputs |
| `--allow-path` | _(none)_ | Permit `file://` inputs under this path (repeatable) |

## API

All `/v1/` endpoints require `Authorization: Bearer <token>` except `/healthz`
and `/readyz`. The SSE events endpoint also accepts `?token=<token>` as a query
parameter because the browser `EventSource` API cannot set custom headers.

| Method | Path | Description |
|---|---|---|
| `POST` | `/v1/jobs` | Submit a job (JSON body = pipeline config) |
| `GET` | `/v1/jobs/{id}` | Get job status |
| `GET` | `/v1/jobs/{id}/events` | SSE stream of job events |
| `GET` | `/v1/jobs/{id}/artifacts` | List output artifacts |
| `DELETE` | `/v1/jobs/{id}` | Cancel a running job |
| `POST` | `/v1/uploads` | Allocate an upload token |
| `PUT` | `/v1/uploads/{token}` | Upload a file (requires `--enable-uploads`) |
| `GET` | `/healthz` | Liveness probe |
| `GET` | `/readyz` | Readiness probe |

### Submit a job

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

### Stream events

Connect with `EventSource` or `curl --no-buffer`:

```bash
curl -H "Authorization: Bearer $TOKEN" \
     "https://server:8443/v1/jobs/a1b2c3d4/events"
```

Each event has the form `event: <type>\ndata: <JSON>\n\n` where `<type>` is one
of `state`, `metrics`, `error`, `log`, `metadata`, or `done`.

## S3 inputs and outputs

The server can transparently convert `s3://` URIs to presigned HTTPS URLs before
running the pipeline so the encode worker never needs direct AWS credentials at
runtime. See [S3 Presigning](architecture/distributed_design.md#61-s3-presigned-url-generation).

### Credential sources (checked in order)

1. Environment variables: `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`,
   `AWS_SESSION_TOKEN`, `AWS_REGION`.
2. JSON file passed via `--s3-presign-credentials` (must be mode 0600):
   ```json
   {
     "access_key_id":     "AKIA…",
     "secret_access_key": "…",
     "session_token":     "…",
     "region":            "us-east-1"
   }
   ```

Presigning is **not** activated by EC2 instance roles alone; it only activates
when credentials are found in one of the above sources.

## File inputs with `upload://`

When `--enable-uploads` is set, clients can push local files to the server:

```bash
# 1. Allocate an upload slot
TOKEN_RESP=$(curl -sX POST -H "Authorization: Bearer $BEARER" https://server:8443/v1/uploads)
UPLOAD_URL=$(echo $TOKEN_RESP | jq -r .upload_url)
URI=$(echo $TOKEN_RESP | jq -r .uri)   # "upload://<token>"

# 2. Upload the file
curl -X PUT -H "Authorization: Bearer $BEARER" \
     --data-binary @local_file.mp4 "$UPLOAD_URL"

# 3. Reference the URI in the job config's input.url
```

## `mediamolder job` CLI

```
mediamolder job submit  --backend=URL --token=TOKEN  config.json
mediamolder job status  --backend=URL --token=TOKEN  <job-id>
mediamolder job cancel  --backend=URL --token=TOKEN  <job-id>
mediamolder job artifacts --backend=URL --token=TOKEN <job-id>
```

The `MEDIAMOLDER_TOKEN` environment variable is used when `--token` is omitted.

---

## Distributed mode (`--mode=api` / `--mode=worker`)

Two operating modes provide distributed job execution. Phase B uses an in-memory
queue and SQLite state store (single-host, dev/small-scale). Phase C adds
production-grade Postgres and NATS / SQS adapters for true multi-instance
stateless deployments.

### `--mode=api` — API server + embedded workers

Start a single binary that accepts Job documents, materialises tasks, and executes
them internally using N embedded workers:

```bash
mediamolder serve \
  --mode=api \
  --addr=:8080 \
  --workers=4 \
  --state=sqlite:///var/lib/mediamolder/state.sqlite3 \
  --auth-token-file=/etc/mediamolder/token
```

#### Multi-instance with Postgres + NATS

Run two (or more) API instances sharing a Postgres state store and a NATS
JetStream queue. Any instance can accept jobs; any worker can execute them.

```bash
# Instance 1
mediamolder serve \
  --mode=api \
  --addr=:8080 \
  --workers=4 \
  --state=postgres://mm:mm@db.example.com/mediamolder?sslmode=require \
  --queue=nats://nats.example.com:4222/mediamolder \
  --reconcile-interval=30s

# Instance 2 (identical flags, different port or behind a load balancer)
mediamolder serve \
  --mode=api \
  --addr=:8081 \
  --workers=4 \
  --state=postgres://mm:mm@db.example.com/mediamolder?sslmode=require \
  --queue=nats://nats.example.com:4222/mediamolder \
  --reconcile-interval=30s
```

Schema migrations are applied automatically on startup.

#### Multi-instance with Postgres + SQS

```bash
export AWS_ACCESS_KEY_ID=...
export AWS_SECRET_ACCESS_KEY=...
export AWS_REGION=us-east-1

mediamolder serve \
  --mode=api \
  --workers=4 \
  --state=postgres://mm:mm@db.example.com/mediamolder?sslmode=require \
  --queue=sqs://sqs.us-east-1.amazonaws.com/123456789012/mediamolder-tasks \
  --reconcile-interval=30s
```

### `--mode=worker` — Worker-only

Run workers separately from API servers (requires a shared queue):

```bash
mediamolder serve \
  --mode=worker \
  --workers=8 \
  --state=postgres://mm:mm@db.example.com/mediamolder?sslmode=require \
  --queue=nats://nats.example.com:4222/mediamolder
```

### Dead-letter queue

When a task exhausts its retry budget (`policy.max_attempts`, default 3) the
reconciler moves it to the DLQ. Inspect dead-lettered tasks:

```bash
curl http://localhost:8080/v1/jobs/{job-id}/dlq
```

Returns a JSON array of `DeadLetterRecord` objects with `task_id`, `reason`,
`attempt`, and `created_at`.

### Distributed-mode flags

| Flag | Default | Description |
|------|---------|-------------|
| `--mode` | `server` | Operating mode: `server` \| `api` \| `worker` |
| `--queue` | `inmemory://` | Queue URI: `inmemory://`, `nats://[user:pass@]host:port[/stream]`, `sqs://sqs.{region}.amazonaws.com/{account}/{queue}` |
| `--state` | `sqlite:///tmp/mediamolder-state.sqlite3` | State-store URI: `sqlite:///path` or `postgres://[user:pass@]host/db[?opts]` |
| `--workers` | `1` | Embedded worker goroutines (api mode only). |
| `--reconcile-interval` | `30s` | How often the reconciler scans for expired leases. Set to `0` to disable. |

### Submitting a Job (schema_version "1.4")

The `POST /v1/jobs` endpoint accepts both:

- **Bare `pipeline.Config`** (schema_version "1.0"–"1.2"): wrapped in a single-task Job for
  backward compatibility.
- **`pipeline.Job`** (schema_version "1.4"): full distributed job with optional
  `distribution` block.

```bash
curl -sX POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  --data @job.json \
  http://localhost:8080/v1/jobs
```

Example `job.json` with a `fanout_static` strategy:

```json
{
  "schema_version": "1.4",
  "name": "encode-4-segments",
  "config": { "schema_version": "1.0", "inputs": [...], "graph": {...}, "outputs": [...] },
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

### Additional Tier 2 endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/jobs/{id}/tasks` | List all tasks for a job with their status and results |
| `GET` | `/v1/jobs/{id}/events` | SSE event log (replayed from SQLite, cursor via `?after=<id>`) |
| `GET` | `/v1/jobs/{id}/artifacts` | Aggregate `ArtifactRef` list from all completed tasks |


---

## Phase D — Dynamic fan-out & gather

Phase D adds two new strategy kinds: `fanout_dynamic` and `gather`. Together
they implement a split-encode-stitch pattern where a **producer stage** analyses
the source and writes a *split manifest*, the **fanout_dynamic stage** spawns one
encode task per segment, and the **gather stage** stitches the outputs into a
final file.

### `split_manifest_writer` processor

Add this go_processor node to the producer stage pipeline. It writes a
`SplitManifest` JSON file on `Close()`.

| Param | Required | Description |
|---|---|---|
| `splitter` | yes | `scene_list` or `byte_range` |
| `output_file` | yes | Absolute path to write the JSON manifest |
| `input_uri` | no | Source URI recorded in the manifest (defaults to job `config.inputs[0].url`) |
| `fps` | for `byte_range` | Frames per second; used to convert frame index to seconds |
| `count` | for `byte_range` | Number of equal-duration segments to produce |
| `threshold` | for `scene_list` | Scene-change score thresho|  (0| `threshold` | for `scene_list` | Sce*: | `threshold` | for `scene_list` | Scene-change score thresho|  (0| `threshun| `threshold` | for `scene_list` | Scene-change score thresho|  (0| `thresh**`byt| `threshold` | for `scene_list` | Scene-change score thresho|  (0| `threshots wit| `threshold` | for `scene_list` | Scene-change score thresho|  (0| `thres": "| `thresho "d| `threshold` | for `scene_listtegy": {| `threshold` | for `scene_lis
    "params": {
      "manifest_uri": "f      "manifest_uri": "f      "m  }
  }
}}}}}}}}}}}}}}}}}}}}}}}}}}}}}}}}}fil}}}}}}URI pointing to the JSON file written by the
producer stage's `split_manifest_writer` node. The orchestrator reads the
manifestmanifestmanifestmanifestmanifestmanifestmanifestmanifestmanifestmanifestmanifestmanifestmanichild tamanifestmanifestmanifestmanifestmanifestmanifestmanifestmanifestmanifestmanifestm the segmanifestmanifestmanifestmanifestmanifestmanifestmanifestmanifestmanifestma:

```
{storage.uri}/segs/{stageID}/{index:04d}.mkv
```

### `gather` strategy

```json
{
  "id": "stitch",
  "depends_on": ["encode"],
  "strategy": { "kind": "gather" }
}
```

When all tasks in every `depends_on` stage succeed, the orchestrator:

1. Retrieves all completed tasks from the first `depends_on` stage, sorted by
   `Task.Index`.
2. Collects `Config.Outputs[0].URL` from each task.
3. Builds a single task whose first input uses the concat demuxer
   (`Kind: "concat"`) listing all segment outputs in order.
4. The4. The4. The4. The4. The4. The4. The4. The4 from the job (the final file).

An optional `source_stage` param overrides which stage's outputs are gatherAn optional `source_stage` param overrides which stage's outputs are gd exAn optional `source_stage` param overrides which stage's outputs are gatherAn optional `source_stage` param overrides which stage's outputs are gd exAn optional `source_stage` param overrides which stage's outputs are gatherAn optional `source_stage` param overrides whicmedAnmolder-demo
mediamolder serve &
curl -s -X POST -H "Content-Type: application/json" \
  --data @testdata/demo-split-encode-stitch.json \
  http://localhost:8080/v1/jobs
```
