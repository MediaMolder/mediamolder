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

Phase B adds two new operating modes that together provide in-process distributed
job execution backed by an in-memory queue and a SQLite state store. This is the
recommended setup for single-host development and small-scale production.

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

All four workers share the in-memory queue with the API server, so no external
infrastructure is needed for development.

### `--mode=worker` — Worker-only

In a future multi-host deployment you can run API and worker processes separately.
Until Phase C (real queue adapter), both processes must be in the same binary; this
flag is provided for future compatibility:

```bash
mediamolder serve \
  --mode=worker \
  --workers=8 \
  --state=sqlite:///var/lib/mediamolder/state.sqlite3
```

### New flags (api + worker modes)

| Flag | Default | Description |
|------|---------|-------------|
| `--mode` | `server` | Operating mode: `server` \| `api` \| `worker` |
| `--queue` | `inmemory://` | Queue URI. Phase B supports `inmemory://` only. |
| `--state` | `sqlite:///tmp/mediamolder-state.sqlite3` | State-store URI. Phase B supports `sqlite:///path`. |
| `--workers` | `1` | Embedded worker goroutines (api mode only). |

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
