# MediaMolder GUI

The `mediamolder gui` subcommand serves a browser-based visual editor for
building, validating, and running MediaMolder JSON pipelines. It is bundled
into the same single binary as the CLI — no separate install or web server is
required.

## Quick start

```sh
# Build the binary with the embedded production frontend.
make build-gui

# Launch the editor (opens http://127.0.0.1:8080 by default).
./mediamolder gui
```

Useful flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--host`     | `127.0.0.1` | Interface to bind. Use `0.0.0.0` to expose on the LAN. |
| `--port`     | `8080`      | TCP port. |
| `--no-open`  | `false`     | Do not auto-open a browser tab. |
| `--examples` | `testdata/examples` | Directory whose `*.json` files are listed in the **Example** dropdown. Set to `""` to disable. |
| `--dev`      | `false`     | Skip the embedded frontend; expects you to run `npm run dev` separately. |

## Anatomy

```
┌──────────────────────────────────────────────────────────────┐
│ Toolbar  [Example ▾] [Auto layout] [New] [Import] [Export]   │
│          [Run] / [Stop] [Show log]                           │
├────────────┬─────────────────────────────────────┬───────────┤
│            │                                     │           │
│  Palette   │            Canvas                   │ Inspector │
│  (search   │   (React Flow with stream-typed     │  (form    │
│   filters, │    handles + edges)                 │   for the │
│   codecs,  │                                     │  selected │
│   processors)                                    │   node)   │
│            │                                     │           │
└────────────┴─────────────────────────────────────┴───────────┘
                                       ┌──────────────┐
                                       │  Run panel   │
                                       │  (status,    │
                                       │   per-node   │
                                       │   metrics,   │
                                       │   error log) │
                                       └──────────────┘
```

### Palette

Populated at runtime from `GET /api/nodes`, which lists every libavfilter,
libavcodec encoder, demuxer/muxer, and registered Go processor available in
the binary you are running. Drag any entry onto the canvas to spawn a
configured node.

### Canvas

* Each node exposes one source and one target handle per stream type
  (video / audio / subtitle / data). Handles only accept connections of the
  same type — incompatible drags are rejected.
* Edges are colour-coded by stream type.
* Node positions are persisted into the saved JSON under `graph.ui.positions`
  (schema v1.2) so reopening a job preserves the layout. The runtime ignores
  this block — it is metadata for the editor only.
* `Backspace` / `Delete` removes the selected node (input fields are not
  hijacked).

### Inspector

The right-hand panel shows a typed form for the selected node. Codec, filter,
and processor parameters surface as editable fields; arbitrary key/value pairs
can be added for less common options.

### Run panel

Click **Run** to execute the current graph. The frontend POSTs the job to
`/api/run`, then opens an `EventSource` against `/api/events/{jobId}` to
receive a stream of typed events:

| Event      | Payload                                                                 |
|------------|-------------------------------------------------------------------------|
| `state`    | `{from, to}` — pipeline state transitions (Ready → Playing → ...).     |
| `metrics`  | `{State, Elapsed, Nodes:[{NodeID, Frames, FPS, Errors, ...}]}` snapshot.|
| `error`    | `{node_id, stage, error}` — per-node failures.                          |
| `log`      | `{message}` — informational entries (e.g. EOS).                         |
| `metadata` | `pipeline.ProcessorMetadata` events from custom processors.             |
| `done`     | `{status: "succeeded"\|"failed"\|"canceled", error}` — terminal event.  |

Live data is merged back into each node on the canvas: frame counts and FPS
appear as badges, and any node that has logged an error is outlined in red.
**Stop** cancels the underlying `context.Context` so the run unwinds cleanly.

## HTTP API

All endpoints are unauthenticated and intended for `localhost` use. Bind
explicitly to `127.0.0.1` (the default) if untrusted users share the host.

| Method | Path                          | Purpose                                               |
|--------|-------------------------------|-------------------------------------------------------|
| `GET`  | `/api/health`                 | Liveness probe.                                       |
| `GET`  | `/api/nodes`                  | Catalogue of available filters/codecs/processors.     |
| `GET`  | `/api/examples`               | List of bundled example job JSONs.                    |
| `GET`  | `/examples/{file}`            | Static serve of the examples directory.               |
| `POST` | `/api/validate`               | Parse + structurally validate a posted JobConfig.     |
| `POST` | `/api/run`                    | Start a run; returns `{job_id}`.                      |
| `POST` | `/api/cancel/{jobId}`         | Cancel an in-flight run.                              |
| `GET`  | `/api/events/{jobId}`         | Server-Sent Events stream for the run.                |

### Why SSE rather than WebSockets?

Progress streaming is one-way (server → client), so SSE is sufficient and
considerably simpler:

* `EventSource` is built into every modern browser; no client library needed.
* No additional Go module dependency.
* Auto-reconnect and event framing are handled by the protocol.

The job manager keeps the most recent 64 events per run in memory so a client
that connects mid-run still sees prior `error`/`state` events.

## Development workflow

```sh
# Terminal 1: backend in dev mode (skips the embedded frontend).
make gui-dev

# Terminal 2: Vite dev server with hot reload + /api proxy.
make frontend-dev
```

Open <http://127.0.0.1:5173>. Edits to `frontend/src/**` reload instantly;
edits to Go code require restarting the backend.

To produce a single shippable binary with the production frontend embedded:

```sh
make build-gui
```

## Schema impact

The GUI persists node positions under `graph.ui.positions` keyed by node ID
(schema v1.2). Older `schema_version: "1.0"` and `"1.1"` jobs load and run
unchanged; the editor will add the `ui` block on save. Pipelines authored
without the GUI never need to include it.

## Security considerations

* The GUI server has no authentication. Treat it as a developer tool and bind
  it to a trusted interface.
* `/api/run` accepts any JobConfig the local pipeline package can parse,
  including file paths the binary has access to. Do not expose the port on
  untrusted networks.
* The job manager retains the 16 most recent finished runs (events + metrics)
  in memory; older runs are garbage-collected.
