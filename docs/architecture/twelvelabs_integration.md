# TwelveLabs Integration — Technical Design

Status: **In progress** · Owner: AI/ML processor track · Target branch: `twelvelabs`
References: [TwelveLabs v1.3 API](https://docs.twelvelabs.io/v1.3/api-reference/introduction) · [`vidi_analyzer`](../vidi-guide.md) (existing reference pattern) · [Go Processor Nodes](../go-processor-nodes.md)

Implementation progress:
- ✅ Phase 1 — REST client skeleton (`internal/twelvelabs`).
- ✅ Phase 2 — indexes + tasks API surface.
- ✅ Phase 3 — analyze + search + embed API surface.
- ✅ Phase 4 — `segment_on_metadata` validation in `pipeline`.
- ✅ Phase 5 — `twelvelabs_indexer` processor + engine wiring for
  `SegmentEventConsumer` / `AsyncMetadataProcessor` (Flows B/C).
- ✅ Phase 6 — `twelvelabs_analyzer`, `twelvelabs_searcher`, and
  `twelvelabs_embedder` processors (shared `processors/twelvelabs_common.go`).
- ✅ Phase 7 — `mediamolder twelvelabs` CLI subcommand
  (`cmd/mediamolder/cmd_twelvelabs.go`).
- ⏳ Phases 8–10 — `/api/twelvelabs/*` routes, GUI palette/Settings card,
  end-to-end recipes + user guide, observability.

---

## 1. Goals

Add first-class TwelveLabs Video Understanding support to MediaMolder so that
graphs can:

1. **Index** existing files or per-shot chunks produced by a MediaMolder graph
   into a TwelveLabs index (Marengo / Pegasus models) without writing custom Go.
2. **Analyze** clips with Pegasus to produce captions, summaries, chapters and
   structured timestamped segments, emitted as `Metadata` on the event bus.
3. **Search** an existing index by natural-language or visual query from inside
   a graph (e.g. mark in/out points based on semantic matches).
4. **Embed** clips or segments with Marengo and persist embeddings to disk or
   downstream nodes.

Non-goals for v1:

- Real-time streaming uploads (TwelveLabs requires a complete file). Live
  sources will tee to a rolling-segment file and submit each completed
  segment.
- Hosting/serving TwelveLabs results back to a web UI (covered by the
  existing event-bus + GUI live-metadata layer).
- Re-implementing TwelveLabs SDKs in Go — we call the REST API directly.

## 2. TwelveLabs API surface (v1.3) — what we actually use

| Capability | Endpoint | Method | Notes |
|---|---|---|---|
| Manage indexes | `/indexes` | `GET/POST/DELETE` | Models: `marengo3.0`, `pegasus1.5`; model_options: `visual`, `audio`. |
| Create index task (upload + ingest) | `/tasks` | `POST` (multipart) | `index_id`, `video_file` *or* `video_url`. Returns `task_id`. |
| Poll task | `/tasks/{id}` | `GET` | Status: `pending` → `validating` → `indexing` → `ready` \| `failed`. |
| Sync analyze | `/analyze` | `POST` | Pegasus; ≤ 1 h. Streaming SSE supported. |
| Async analyze | `/analyze/tasks` | `POST` | Pegasus; ≤ 2 h; supports video segmentation. |
| Search index | `/search` | `POST` | Marengo. Returns timestamped clips with confidence. |
| Embed (v2) | `/embed/tasks` (video), `/embed` (text/image/audio) | `POST` | Marengo embeddings. |

Auth: `x-api-key: $TWELVELABS_API_KEY` header on every request. Base URL:
`https://api.twelvelabs.io/v1.3`.

Errors: standard HTTP codes plus `{code, message, docs_url}` JSON body.
`429` requires exponential backoff (honoured by our client).

## 3. Architecture

TwelveLabs operates on **whole video files** (MP4 / MOV / WebM), not on raw
decoded frames. The integration therefore never sits in the decoded-frame
path of a graph. Instead, three usage patterns are supported:

### Flow A — Pass-through file indexing (most common)

A source file (or remote URL) is handed to TwelveLabs as-is, with no
decode/re-encode cycle. This is the cheapest and highest-quality path.

```
  ┌────────────────────────────── MediaMolder graph ──────────────────────────────┐
  │                                                                               │
  │   file_source (input.mp4) ──► twelvelabs_indexer ──► twelvelabs_analyzer      │
  │           │                          │                       │                │
  │           │ (file path)              │                       │                │
  │           ▼                          ▼                       ▼                │
  │   (no decode)         internal/twelvelabs (client)    Metadata on event bus   │
  │                                      │                                        │
  └──────────────────────────────────────┼────────────────────────────────────────┘
                                         ▼
                              api.twelvelabs.io/v1.3
                              (Marengo + Pegasus models)
```

### Flow B — Shot-aware chunking, then per-shot indexing

Detect shot boundaries with the existing `scene_change*` processor, cut the
source into one MP4 per shot via the existing `segment_sink`, and submit
each completed shot file to TwelveLabs. The decode/encode path exists only
to produce the per-shot files; the indexer never sees raw frames.

```
  ┌──────────────────────────────────── MediaMolder graph ───────────────────────────────────┐
  │                                                                                          │
  │   source ─► decode ─► scene_change ─► encode ─► segment_sink (shot-NNN.mp4)              │
  │                                                       │                                  │
  │                                                       │ (file path per completed shot)   │
  │                                                       ▼                                  │
  │                                          twelvelabs_indexer ─► twelvelabs_analyzer       │
  │                                                       │                  │               │
  │                                                       ▼                  ▼               │
  │                                          internal/twelvelabs (client)  Metadata          │
  │                                                       │                                  │
  └───────────────────────────────────────────────────────┼──────────────────────────────────┘
                                                          ▼
                                               api.twelvelabs.io/v1.3
```

### Flow C — Live tee to rolling segments

For live inputs, the graph teees to a rolling-segment muxer (e.g. 6 s MP4
segments). Each completed segment file fires the same event that drives
the indexer in Flow B.

```
  source (live) ─► decode ─► encode ─► segment_sink (seg-NNNNN.mp4) ─► twelvelabs_indexer
```

Results from all three flows land on the standard event bus and surface
via `--metadata-out`, the GUI live panel, or downstream graph nodes.

### Code layers

- **`internal/twelvelabs/`** — pure-Go REST client, no MediaMolder imports.
  Reusable from CLI commands, processors, and tests.
- **`processors/twelvelabs_*.go`** — three thin `go_processor` nodes
  (`twelvelabs_indexer`, `twelvelabs_analyzer`, `twelvelabs_searcher`) that
  wrap the client and bridge to `ProcessorContext` / `Metadata`.
- **`cmd/mediamolder/cmd_twelvelabs.go`** — ad-hoc CLI subcommand for
  one-shot operations against an existing file (no graph required).

### 3.1 Input contract

All three flows feed the indexer a **completed file path** via the
existing event channel that already connects `segment_sink` to
downstream processors (the same mechanism `metadata_file_writer` uses).
Flow A uses `file_source` for a single one-shot event; Flows B and C use
`segment_sink` for a stream of events. The CLI subcommand bypasses the
graph and takes a path directly.

### 3.2 Concurrency & lifecycle

- Each processor owns one `*twelvelabs.Client` with a configurable
  `http.Client` (timeout, transport).
- Long-running operations (indexing, async analyze) use a **worker
  goroutine** that polls the task endpoint on a backoff schedule
  (1s → 2s → 4s → … → 30s cap, honouring `Retry-After`).
- The worker shuts down cleanly via the `Stop(ctx)` lifecycle hook; in-flight
  HTTP requests are cancelled through the processor's `ProcessorContext.Context`.
- In Flow B, the indexer must accept files **out of order** (encode + mux
  latency means shot N+1's file may close before shot N's if shots have
  varying length). `video_id` is keyed by source filename, not by arrival
  order, so downstream analyzer events stay correctly attributed.

## 4. Go client — `internal/twelvelabs`

```go
package twelvelabs

type Client struct {
    BaseURL    string         // default https://api.twelvelabs.io/v1.3
    APIKey     string         // from TWELVELABS_API_KEY or config
    HTTP       *http.Client   // default 60s timeout
    UserAgent  string         // "mediamolder/<version>"
}

// Indexes
func (c *Client) CreateIndex(ctx context.Context, name string, models []ModelSpec) (*Index, error)
func (c *Client) ListIndexes(ctx context.Context) ([]Index, error)
func (c *Client) DeleteIndex(ctx context.Context, id string) error

// Indexing tasks (upload)
func (c *Client) CreateIndexTask(ctx context.Context, indexID string, src TaskSource) (*Task, error)
func (c *Client) GetTask(ctx context.Context, id string) (*Task, error)
func (c *Client) WaitForTask(ctx context.Context, id string, opts WaitOpts) (*Task, error)

// Analyze (Pegasus)
type AnalyzeRequest struct {
    VideoID  string            // for indexed video; or
    VideoURL string            // for one-shot URL
    Prompt   string
    Stream   bool
    TempC    float32
    Segments bool              // request structured timestamped segments
}
func (c *Client) Analyze(ctx context.Context, req AnalyzeRequest) (*AnalyzeResult, error)
func (c *Client) AnalyzeStream(ctx context.Context, req AnalyzeRequest, fn func(AnalyzeChunk) error) error

// Search (Marengo)
type SearchRequest struct {
    IndexID      string
    Query        string             // text query
    QueryMediaURL string            // image/audio query
    SearchOptions []string          // ["visual","audio"]
    Threshold    string             // "low"|"medium"|"high"
    PageLimit    int
}
type SearchResult struct {
    VideoID    string
    StartS     float64
    EndS       float64
    Score      float64
    Confidence string
}
func (c *Client) Search(ctx context.Context, req SearchRequest) ([]SearchResult, error)

// Embed (Marengo v2)
func (c *Client) EmbedVideo(ctx context.Context, src EmbedSource, opts EmbedOpts) (*EmbedTask, error)
func (c *Client) EmbedText(ctx context.Context, text string, opts EmbedOpts) ([]Embedding, error)
```

Internal helpers:

- `do(ctx, method, path, body)` — adds `x-api-key`, JSON marshal, retry on
  429 / 5xx with jittered exponential backoff (max 5 attempts).
- `uploadMultipart(ctx, path, fields, fileField, reader)` — for file
  uploads; streams from `io.Reader` so we never load the whole MP4 in RAM.
- `errorResponse` mirrors `{code, message, docs_url}`; surfaced as
  `*twelvelabs.APIError` so callers can type-assert.

No external dependencies beyond the Go stdlib. SSE for streaming analyze is
parsed with a tiny line-buffered scanner (no third-party library).

## 5. Graph processors

Each processor is registered exactly like `vidi_analyzer`:

```go
func init() {
    Register("twelvelabs_indexer",  func() Processor { return &TwelveLabsIndexer{} })
    Register("twelvelabs_analyzer", func() Processor { return &TwelveLabsAnalyzer{} })
    Register("twelvelabs_searcher", func() Processor { return &TwelveLabsSearcher{} })
    Register("twelvelabs_embedder", func() Processor { return &TwelveLabsEmbedder{} })
}
```

### 5.1 `twelvelabs_indexer`

Watches for completed-file events — either a single `file_source` (Flow A)
or a stream of per-shot/per-segment files from an upstream `segment_sink`
(Flows B and C) — and uploads each finished file to a TwelveLabs index.

Params:

| Name | Type | Default | Description |
|---|---|---|---|
| `api_key_env` | string | `TWELVELABS_API_KEY` | Env var name holding the key. |
| `api_key` | string | *(unset)* | Inline override (discouraged). |
| `index_id` | string | *(required)* | Pre-existing index to upload to. |
| `models` | []string | `["marengo3.0"]` | Used only when `auto_create_index` is true. |
| `auto_create_index` | bool | false | Create the index on first run if missing. |
| `wait_for_ready` | bool | true | Block worker until task reaches `ready`. |
| `poll_interval_s` | float | 2 | Initial poll interval. |
| `max_concurrent` | int | 2 | In-flight upload tasks per graph. |

Emits `Metadata.Custom`:

```json
{
  "twelvelabs": {
    "event":      "indexed",
    "task_id":    "67abf2…",
    "video_id":   "abc123…",
    "file":       "/tmp/shot-00042.mp4",
    "shot_index": 42,
    "duration_s": 6.0,
    "status":     "ready"
  }
}
```

`shot_index` is present only when the upstream node is `segment_sink`
(Flows B and C). Failures populate `twelvelabs_error`; the graph never stops.

### 5.2 `twelvelabs_analyzer`

After each segment is indexed (or for an existing `video_id`), runs Pegasus
analyze with a configurable prompt.

Params:

| Name | Type | Default | Description |
|---|---|---|---|
| (auth + index params from indexer) | | | |
| `prompt` | string | `"Describe what happens in this video."` | Pegasus prompt. |
| `mode` | string | `"sync"` | `sync` (≤1h) or `async` (≤2h, with segments). |
| `segments` | bool | false | Request structured timestamped chapters. |
| `stream` | bool | false | Emit partial Metadata as SSE chunks arrive. |
| `temperature` | float | 0.2 | Pegasus sampling. |

Emits per segment:

```json
{
  "twelvelabs": {
    "event":   "analyzed",
    "video_id": "abc123",
    "prompt":   "…",
    "text":     "A wide shot of a stadium during sunset; the crowd cheers …",
    "chapters": [
      {"start_s": 0.0,  "end_s": 12.4, "title": "Opening wide shot"},
      {"start_s": 12.4, "end_s": 28.1, "title": "Goal celebration"}
    ]
  }
}
```

`chapters` also lands in `Metadata.Detections` with `BBox = nil` and
`Label = chapter.Title` so existing metadata sinks can render them.

### 5.3 `twelvelabs_searcher`

Runs a Marengo search every N seconds (or on a custom trigger event) and
emits matches as Metadata. Can also gate downstream nodes:

- `match_to_event`: emit only the highest-scoring hit per query.
- `min_score`: drop matches below threshold.
- `into_cuts`: convert hits into `Metadata.Custom["cuts"]` consumed by a
  downstream cut/trim filter (e.g. a future `segment_select` filter).

Params:

| Name | Type | Default | Description |
|---|---|---|---|
| `index_id` | string | *(required)* | Index to search. |
| `query` | string | *(required)* | Natural-language query. |
| `search_options` | []string | `["visual","audio"]` | |
| `threshold` | string | `"medium"` | Marengo confidence floor. |
| `interval_s` | float | 0 | If > 0, re-run on a timer. |
| `into_cuts` | bool | false | See above. |

### 5.4 `twelvelabs_embedder`

Requests Marengo video embeddings for each completed file (Flow A) or
shot/segment file (Flows B and C). Embeddings are returned as fixed-length
`float32` vectors per time window; the processor emits them on the event
bus and optionally writes them straight to disk so users can ingest them
into their own vector DB.

Params:

| Name | Type | Default | Description |
|---|---|---|---|
| (auth params from indexer) | | | |
| `model` | string | `"marengo3.0"` | Embedding model. |
| `scopes` | []string | `["clip"]` | `clip` (one vector per file) and/or `video` (per time window). |
| `window_s` | float | 6 | Window length when `scopes` includes `video`. |
| `out_dir` | string | *(unset)* | If set, write one `<file>.embeddings.json` per input file. |
| `out_format` | string | `"json"` | `json`, `jsonl`, or `npy` (NumPy `.npy` per file). |
| `mode` | string | `"sync"` | `sync` (in-line) or `async` (task-polled). |

Emits `Metadata.Custom`:

```json
{
  "twelvelabs": {
    "event":      "embedded",
    "video_id":   "abc123",
    "file":       "/tmp/shot-00042.mp4",
    "shot_index": 42,
    "model":      "marengo3.0",
    "dim":        1024,
    "embeddings": [
      { "scope": "clip",  "start_s": 0.0, "end_s": 6.0, "vector": [0.012, -0.087, "… 1024 floats …"] },
      { "scope": "video", "start_s": 0.0, "end_s": 6.0, "vector": ["…"] }
    ],
    "out_file":   "/data/embeddings/shot-00042.embeddings.json"
  }
}
```

If `out_dir` is set, the vector array is **also** written to disk in the
chosen format and the `embeddings[].vector` field is omitted from the
Metadata event (only `out_file` remains) to keep the event bus light.

### 5.5 Result formats & storage

Every processor emits to the same place: `Metadata.Custom["twelvelabs"]`
on the event bus. From there, results are surfaced through MediaMolder's
existing metadata machinery — nothing TwelveLabs-specific.

**Canonical shape.** Each event has a `"event"` discriminator and a
stable set of fields per type:

| `event` | Required fields | Optional fields |
|---|---|---|
| `indexed`  | `video_id`, `file`, `status`, `duration_s` | `task_id`, `shot_index` |
| `analyzed` | `video_id`, `text`         | `prompt`, `chapters[]`, `segments[]` |
| `search`   | `query`, `matches[]`       | `index_id` |
| `embedded` | `video_id`, `model`, `dim` | `embeddings[]`, `out_file`, `shot_index` |

All timestamps are `float` seconds. Failures replace the payload with
`{ "event": "error", "reason": "…", "http_status": 4xx }` and the graph
continues.

**Storage paths.** Users pick whichever sink fits:

| Sink | How to enable | Output |
|---|---|---|
| JSON Lines | `mediamolder run … --metadata-out results.jsonl` | One JSON object per event, in arrival order. |
| Sidecar per file | `metadata_file_writer` node with `pattern: "{file}.tl.json"` | One JSON file alongside each segment. |
| MP4 chapters | `mp4_chapter_writer` node (existing) consuming `chapters[]` | `chap` track in the output MP4. |
| WebVTT chapters | `webvtt_writer` node (existing) | `.vtt` file with `CHAPTER` cues. |
| ID3 tags / `mov_text` | Existing `metadata_mux` mode | `comment`/`description` tags carrying caption text. |
| Embedding vectors | `twelvelabs_embedder.out_dir` + `out_format` | `.json` / `.jsonl` / `.npy` per source file. |
| GUI live panel | None (always on when GUI is connected) | Cards rendered by `MetadataChip.tsx`. |

For downstream Go consumers:

```go
if tl, ok := metadata.Custom["twelvelabs"].(map[string]any); ok {
    switch tl["event"] {
    case "analyzed":
        caption := tl["text"].(string)
    case "embedded":
        // either inline vectors…
        for _, e := range tl["embeddings"].([]any) {
            vec := e.(map[string]any)["vector"].([]any) // []float64
            _ = vec
        }
        // …or a path to the on-disk file
        if path, ok := tl["out_file"].(string); ok {
            // load from path
            _ = path
        }
    }
}
```

Muxing analyze results back into the **output video** uses the existing
`chapters[]` → `mp4_chapter_writer` or `webvtt_writer` chain; no new
muxer code is required. Search hits can be converted to cut points by
setting `twelvelabs_searcher.into_cuts: true`, which writes
`Metadata.Custom["cuts"]` consumed by the existing `segment_select`
filter (planned) or any downstream node that reads `cuts`.

## 6. CLI integration

A single subcommand wraps the client for ad-hoc use:

```bash
mediamolder twelvelabs index   --index <id> input.mp4
mediamolder twelvelabs analyze --video-id <id> --prompt "Summarise this clip."
mediamolder twelvelabs search  --index <id> --query "a person walking a dog"
mediamolder twelvelabs embed   --video input.mp4 --out embeddings.json
mediamolder twelvelabs indexes list
mediamolder twelvelabs indexes create --name demo --models marengo3.0,pegasus1.5
```

Implementation: `cmd/mediamolder/cmd_twelvelabs.go`, using the same
`internal/twelvelabs` client. Flags map 1:1 to client structs. Output is
JSON by default, with `--format=table` for human-readable.

Authentication precedence: `--api-key` flag → `TWELVELABS_API_KEY` env
→ `~/.config/mediamolder/twelvelabs.json` (last so secrets stay out of
the repo).

## 7. GUI integration

### 7.1 Node palette

Three new entries under a new **AI ▸ TwelveLabs** group in the React Flow
palette, each registered through the existing `goProcessorRegistry.ts`
catalog:

- `twelvelabs_indexer`
- `twelvelabs_analyzer`
- `twelvelabs_searcher`

Each carries an icon, colour (`#5B6CFF`, matching TwelveLabs brand), and
a param schema rendered by the existing Inspector form. No new form widgets
are required — all params are strings, numbers, bools, or string-lists,
already supported.

### 7.2 Settings → Integrations

Add a **TwelveLabs** card to the Settings page (`frontend/src/pages/Settings.tsx`):

- API key field (`type=password`, stored in the existing browser
  `localStorage` per-host secrets store, mirrored to the backend's
  per-user config file on save).
- "Test connection" button → `GET /api/twelvelabs/ping` → backend calls
  `ListIndexes` and returns success/failure.
- Index picker (dropdown populated from `GET /api/twelvelabs/indexes`).

### 7.3 Live metadata panel

`twelvelabs` metadata events surface automatically in the existing live-
event panel. We add a small renderer (`MetadataChip.tsx`) so that:

- `analyzed.text` → expandable card with the prompt + response,
- `chapters` → timeline tick marks under the preview scrubber,
- `search` matches → list with seek-to-timestamp buttons.

No schema-version bump is required — `Metadata.Custom` is already
free-form.

### 7.4 Backend HTTP routes

`internal/api/twelvelabs.go`:

- `GET    /api/twelvelabs/ping`
- `GET    /api/twelvelabs/indexes`
- `POST   /api/twelvelabs/indexes` (create)
- `DELETE /api/twelvelabs/indexes/{id}`
- `POST   /api/twelvelabs/search`  (proxy for GUI free-text search)

All routes load the API key from the same precedence chain as the CLI.

## 8. Graph schema

No new top-level fields. The three processors are plain `go_processor`
entries.

### 8.1 Flow A — pass-through file indexing

```json
{
  "schema_version": "1.1",
  "inputs":  [{ "id": "src", "url": "input.mp4" }],
  "nodes": [
    { "id": "file",    "type": "file_source",   "input":  "src" },
    { "id": "indexer", "type": "go_processor",
      "processor": "twelvelabs_indexer",
      "params": { "index_id": "${TL_INDEX}", "wait_for_ready": true } },
    { "id": "summary", "type": "go_processor",
      "processor": "twelvelabs_analyzer",
      "params": { "prompt": "Summarise the action in one sentence." } }
  ],
  "edges": [
    { "from": "file",    "to": "indexer" },
    { "from": "indexer", "to": "summary" }
  ]
}
```

No decode, no encode — the source MP4 is uploaded byte-for-byte.

### 8.2 Flow B — shot-aware chunking

```json
{
  "schema_version": "1.1",
  "inputs":  [{ "id": "src", "url": "input.mp4" }],
  "outputs": [
    { "id": "shots", "url": "out/shot-%05d.mp4",
      "segment_format": "mp4", "segment_on_metadata": "scene_change" }
  ],
  "nodes": [
    { "id": "dec",     "type": "source",       "input":  "src" },
    { "id": "scene",   "type": "go_processor",
      "processor": "scene_change_adaptive",
      "params": { "threshold": 0.3 } },
    { "id": "enc",     "type": "sink",         "output": "shots" },
    { "id": "indexer", "type": "go_processor",
      "processor": "twelvelabs_indexer",
      "params": { "index_id": "${TL_INDEX}", "wait_for_ready": true } },
    { "id": "summary", "type": "go_processor",
      "processor": "twelvelabs_analyzer",
      "params": { "prompt": "One-sentence caption of this shot." } }
  ],
  "edges": [
    { "from": "dec",     "to": "scene" },
    { "from": "scene",   "to": "enc" },
    { "from": "enc",     "to": "indexer" },
    { "from": "indexer", "to": "summary" }
  ]
}
```

The `enc → indexer` edge uses the existing "completed file" event channel
already wired between `segment_sink` and downstream processors (same
mechanism `metadata_file_writer` uses). `segment_on_metadata` is a new
`segment_sink` mode that closes a segment whenever a named `Metadata`
event (here, `scene_change`) arrives on the bus — see §13 commit #4a.

### 8.3 Flow C — live rolling segments

Identical to §8.2 but with a live input and time-based segmentation
(`"segment_time": 6`) instead of `segment_on_metadata`.

### 8.4 Flow D — per-shot embeddings to disk

Detect shots, emit one MP4 per shot, request a 1024-d Marengo embedding
per shot, and write each vector to `out/embeddings/shot-NNNNN.embeddings.json`:

```json
{
  "schema_version": "1.1",
  "inputs":  [{ "id": "src", "url": "input.mp4" }],
  "outputs": [
    { "id": "shots", "url": "out/shot-%05d.mp4",
      "segment_format": "mp4", "segment_on_metadata": "scene_change" }
  ],
  "nodes": [
    { "id": "dec",      "type": "source",       "input":  "src" },
    { "id": "scene",    "type": "go_processor", "processor": "scene_change_adaptive" },
    { "id": "enc",      "type": "sink",         "output": "shots" },
    { "id": "embed",    "type": "go_processor",
      "processor": "twelvelabs_embedder",
      "params": {
        "model":      "marengo3.0",
        "scopes":     ["clip"],
        "out_dir":    "out/embeddings",
        "out_format": "json"
      } }
  ],
  "edges": [
    { "from": "dec",   "to": "scene" },
    { "from": "scene", "to": "enc" },
    { "from": "enc",   "to": "embed" }
  ]
}
```

Resulting layout:

```
out/shot-00000.mp4
out/shot-00000.embeddings.json    ← { "model": "marengo3.0", "dim": 1024,
                                       "vectors": [{ "start_s":0,"end_s":4.2,
                                                     "vector":[… 1024 floats …] }] }
out/shot-00001.mp4
out/shot-00001.embeddings.json
…
```

Users ingest the resulting JSON / JSONL / NPY files into their own
vector store (pgvector, Qdrant, Pinecone, FAISS, …). A first-party
database integration is out of scope for v1.

## 9. Testing strategy

### 9.1 Unit tests — `internal/twelvelabs/`

- `client_test.go` — every method against `httptest.Server` fixtures.
- `retry_test.go` — 429 with `Retry-After`, 5xx burst, context cancel.
- `multipart_test.go` — streaming upload with `io.Pipe`, asserts no
  whole-file buffering.
- `sse_test.go` — Pegasus stream chunk parsing.
- `errors_test.go` — `APIError` propagation.

### 9.2 Processor tests — `processors/twelvelabs_*_test.go`

Mirroring `vidi_analyzer_test.go`:

- Init param validation (missing `index_id`, bad URL, missing key).
- Process passthrough (video and audio frames forwarded unchanged).
- Indexer (Flow A): receives a single `file_source` event → calls mock
  server → emits `indexed` metadata.
- Indexer (Flow B): receives N out-of-order shot-complete events →
  uploads in parallel up to `max_concurrent` → all emit `indexed`
  metadata with the correct `shot_index`.
- Analyzer: streams SSE chunks → emits per-chunk metadata when
  `stream=true`.
- Searcher: timed trigger fires search → emits matches.
- Error paths: 401 → permanent failure, marked in `twelvelabs_error`;
  429 → backoff, eventual success.
- Registration check (`registry_test.go`).

### 9.3 Schema / docs sync

- `TestSchemaSyncWithGoStructs` already covers processor JSON params via
  the open-ended `params map[string]any` field — no schema changes needed.
- Add an entry to `docs/go-processor-nodes.md` TOC + `Built-in processors`
  list (one line per processor) and a `Twelve Labs multimodal analysis`
  section linking to `docs/twelvelabs.md`.

### 9.4 Integration tests

Tagged `//go:build integration` and skipped by default:

```bash
go test -tags=integration -run TestTwelveLabsLive ./internal/twelvelabs/
```

Requires `TWELVELABS_API_KEY` and a small fixture (`testdata/clip.mp4`,
< 5 MB). CI runs nightly only.

### 9.5 Frontend tests

- `Inspector.test.tsx` — param form renders for each processor.
- `Settings.test.tsx` — API key save + test-connection mock.
- `MetadataChip.test.tsx` — renders `analyzed` and `search` payloads.
- Vitest snapshot for the palette entries.

## 10. Security

- API key never written to logs, traces, or graph JSON exports.
- The graph JSON references `${TL_INDEX}` and `${TWELVELABS_API_KEY}`
  via the existing env-var substitution layer; storing the literal key
  in a graph is rejected by `pipeline.NormalizeConfig` with a warning.
- Upload bodies are streamed, never buffered, to avoid leaking
  multi-GB clips into process memory on `/proc/<pid>/maps`.
- TLS: stdlib `http.Client` with default cert verification; no override.

## 11. Observability

- Prometheus metrics under `mediamolder_twelvelabs_*`:
  - `requests_total{endpoint,status}`
  - `request_duration_seconds{endpoint}` (histogram)
  - `tasks_in_flight{processor}`
  - `task_wait_seconds{terminal_status}` (histogram)
  - `retries_total{reason}`
- OpenTelemetry spans for each REST call (`twelvelabs.<method>`), parented
  to the processor's tick span.
- Structured logs under `slog` attribute group `twelvelabs`.

## 12. Failure modes

| Failure | Effect | Mitigation |
|---|---|---|
| API key missing/invalid | Processor logs once, sets `twelvelabs_error`, becomes passthrough | Detect at `Init`, fail loud if `strict=true` param. |
| 429 rate limit | Retry with backoff up to `max_retries` (5) | Surface `retries_total`. |
| Network partition | Same as 5xx — backoff | `tasks_in_flight` gauge alarms. |
| Index deleted mid-run | Task creation fails 404 | Auto-recreate when `auto_create_index=true`. |
| Graph shutdown during upload | `ctx` cancelled, multipart `io.Reader` closes | Verified by `TestIndexer_StopMidUpload`. |
| Pegasus timeout (sync >1h) | Returns 4xx | Auto-fall back to async mode if `mode=auto`. |

## 13. Implementation plan

Ordered list of commits. Each ends green on `go test ./... && (cd frontend && npm test)`.

1. ✅ **`feat(internal/twelvelabs): REST client skeleton`**
   - `internal/twelvelabs/{client,types,errors,retry}.go` + unit tests.
   - No MediaMolder imports.
2. ✅ **`feat(internal/twelvelabs): indexes + tasks endpoints`**
   - `CreateIndex`, `ListIndexes`, `DeleteIndex`, `CreateIndexTask`,
     `GetTask`, `WaitForTask`. Streaming multipart upload.
3. ✅ **`feat(internal/twelvelabs): analyze + search + embed`**
   - Pegasus sync/async, SSE streaming, Marengo search, embed v2.
4. ✅ **`feat(pipeline): segment_on_metadata for segment_sink`** *(prerequisite for Flow B)*
   - Adds a new `segment_sink` output mode that closes a segment when a
     named `Metadata` event arrives on the bus.
   - Schema bump documented in `schema/v1.1.json`.
   - Tests cover scene-change-triggered cutting on a synthetic input.
5. ✅ **`feat(processors): twelvelabs_indexer`**
   - Listens for completed-file events from `file_source` (Flow A) or
     `segment_sink` (Flows B/C), uploads, emits metadata.
   - Mirrors `vidi_analyzer` structure (Init, Process, Stop, Register).
6. **`feat(processors): twelvelabs_analyzer + twelvelabs_searcher + twelvelabs_embedder`**
   - Share helper `tlClient(p)` to build the client from params.
   - Embedder writes JSON / JSONL / NPY per file when `out_dir` is set.
7. **`feat(cmd): mediamolder twelvelabs subcommand`**
   - `cmd/mediamolder/cmd_twelvelabs.go` with `index|analyze|search|embed|indexes` verbs.
8. **`feat(api): /api/twelvelabs/* routes`**
   - Backend HTTP endpoints + tests.
9. **`feat(gui): TwelveLabs nodes + Settings card + MetadataChip`**
   - React Flow palette entries, Inspector schemas, Settings panel,
     metadata renderer.
10. **`docs(twelvelabs): user guide + cross-references`**
    - `docs/twelvelabs.md` (final version of draft below).
   - Add to `docs/go-processor-nodes.md` TOC + built-ins list.
   - Add to `README.md` processors paragraph + docs index.
   - `CHANGELOG.md` entry.
11. **`test(twelvelabs): integration suite + CI nightly`**
    - `//go:build integration` test against live API; nightly workflow.

## 14. Documentation updates

- `README.md` — one-line mention; link to `docs/twelvelabs.md`.
- `docs/twelvelabs.md` — full user guide (first draft in §15 below).
- `docs/go-processor-nodes.md` — TOC entries, three built-in list rows,
  top-level "TwelveLabs multimodal analysis" section linking to the guide.
- `docs/architecture/architecture.md` — add TwelveLabs as an external
  system in the C4 context diagram.
- `CHANGELOG.md` — under "Unreleased / Added".
- `docs/json-config-reference.md` — note the three new processor names in
  the `go_processor.processor` enum.

---

## 15. Draft user guide — `docs/twelvelabs.md`

> The block below is the **first draft** of the public user guide. It will be
> committed verbatim (minus this header) as `docs/twelvelabs.md` in
> commit #10 of the plan.

````markdown
# TwelveLabs Video Understanding

MediaMolder integrates the [TwelveLabs Video Understanding API](https://docs.twelvelabs.io/v1.3/api-reference/introduction)
so your graphs can **index, analyze, and search** video clips with the
Marengo and Pegasus foundation models — without writing any Go.

Three built-in `go_processor` nodes handle the integration:

| Node | Model | Purpose |
|---|---|---|
| `twelvelabs_indexer` | Marengo + Pegasus | Upload completed clips into a TwelveLabs index. |
| `twelvelabs_analyzer` | Pegasus 1.5 | Generate captions, summaries, and structured chapters. |
| `twelvelabs_searcher` | Marengo 3.0 | Run natural-language search against an index, emit timestamped hits. |
| `twelvelabs_embedder` | Marengo 3.0 | Produce per-clip / per-window video embeddings, optionally to disk. |

## When to use it

- **Editorial / archive workflows** — auto-summarise long clips, generate
  chapter markers, surface "all goals", "all close-ups", etc., as cut
  points.
- **Compliance / moderation** — search a rolling index for spoken phrases
  or visual cues; tag matches in the graph event bus.
- **Recommendation / similarity** — embed segments with Marengo and
  persist vectors alongside your existing metadata.

For low-latency on-prem analysis without uploading clips, use
[`vidi_analyzer`](vidi-guide.md) instead.

## Getting an API key

1. Sign up at <https://playground.twelvelabs.io/> (free tier available).
2. Create an API key on the dashboard.
3. Export it before running MediaMolder:

```bash
export TWELVELABS_API_KEY="tlk_…"
```

Or save it once via the GUI: **Settings → Integrations → TwelveLabs**.

## Quick start (CLI)

Index a file and ask Pegasus to summarise it:

```bash
mediamolder twelvelabs indexes create --name demo --models marengo3.0,pegasus1.5
mediamolder twelvelabs index   --index demo my-clip.mp4
mediamolder twelvelabs analyze --video-id <id-from-previous-output> \
    --prompt "Summarise the clip in one sentence."
```

Search:

```bash
mediamolder twelvelabs search --index demo --query "a person walking a dog"
```

## Graph examples

### Pass-through (whole file)

Upload `my-clip.mp4` as-is and have Pegasus summarise it:

```json
{
  "schema_version": "1.1",
  "inputs":  [{ "id": "src", "url": "my-clip.mp4" }],
  "nodes": [
    { "id": "file",    "type": "file_source",   "input":  "src" },
    { "id": "indexer", "type": "go_processor",  "processor": "twelvelabs_indexer",
      "params": { "index_id": "${TL_INDEX}", "wait_for_ready": true } },
    { "id": "summary", "type": "go_processor",  "processor": "twelvelabs_analyzer",
      "params": { "prompt": "Summarise in one sentence." } }
  ],
  "edges": [
    { "from": "file",    "to": "indexer" },
    { "from": "indexer", "to": "summary" }
  ]
}
```

### Shot-aware chunking

Detect shot boundaries, cut into one MP4 per shot, index and caption each:

```json
{
  "schema_version": "1.1",
  "inputs":  [{ "id": "src", "url": "my-clip.mp4" }],
  "outputs": [
    { "id": "shots", "url": "out/shot-%05d.mp4",
      "segment_format": "mp4", "segment_on_metadata": "scene_change" }
  ],
  "nodes": [
    { "id": "dec",     "type": "source",       "input":  "src" },
    { "id": "scene",   "type": "go_processor", "processor": "scene_change_adaptive" },
    { "id": "enc",     "type": "sink",         "output": "shots" },
    { "id": "indexer", "type": "go_processor", "processor": "twelvelabs_indexer",
      "params": { "index_id": "${TL_INDEX}" } },
    { "id": "caption", "type": "go_processor", "processor": "twelvelabs_analyzer",
      "params": { "prompt": "One-sentence caption of this shot." } }
  ],
  "edges": [
    { "from": "dec",     "to": "scene" },
    { "from": "scene",   "to": "enc" },
    { "from": "enc",     "to": "indexer" },
    { "from": "indexer", "to": "caption" }
  ]
}
```

Run with metadata captured to a file:

```bash
mediamolder run graph.json --metadata-out captions.jsonl
```

## Processor reference

### `twelvelabs_indexer`

| Param | Type | Default | Description |
|---|---|---|---|
| `index_id` | string | **required** | Pre-existing TwelveLabs index. |
| `auto_create_index` | bool | `false` | Create the index on first run. |
| `models` | []string | `["marengo3.0"]` | Models to enable when auto-creating. |
| `wait_for_ready` | bool | `true` | Block until task reaches `ready`. |
| `poll_interval_s` | float | `2` | Initial poll interval (with backoff). |
| `max_concurrent` | int | `2` | In-flight uploads per graph. |
| `api_key_env` | string | `TWELVELABS_API_KEY` | Env var holding the key. |

Emits `Metadata.Custom["twelvelabs"] = { "event": "indexed", … }` per
finished segment.

### `twelvelabs_analyzer`

| Param | Type | Default | Description |
|---|---|---|---|
| `prompt` | string | `"Describe what happens in this video."` | Pegasus prompt. |
| `mode` | string | `"sync"` | `sync` (≤ 1h) or `async` (≤ 2h). |
| `segments` | bool | `false` | Request structured timestamped chapters. |
| `stream` | bool | `false` | Emit Metadata as SSE chunks arrive. |
| `temperature` | float | `0.2` | Pegasus sampling. |

Emits `Metadata.Custom["twelvelabs"] = { "event": "analyzed", "text": …,
"chapters": [...] }`. Chapters also appear in `Metadata.Detections`.

### `twelvelabs_searcher`

| Param | Type | Default | Description |
|---|---|---|---|
| `index_id` | string | **required** | Index to search. |
| `query` | string | **required** | Natural-language query. |
| `search_options` | []string | `["visual","audio"]` | Modalities. |
| `threshold` | string | `"medium"` | `low` / `medium` / `high`. |
| `interval_s` | float | `0` | If > 0, re-run on a timer. |
| `into_cuts` | bool | `false` | Convert hits into `Metadata.Custom["cuts"]`. |

### `twelvelabs_embedder`

| Param | Type | Default | Description |
|---|---|---|---|
| `model` | string | `"marengo3.0"` | Embedding model. |
| `scopes` | []string | `["clip"]` | `clip` and/or `video` windows. |
| `window_s` | float | `6` | Window length when `scopes` includes `video`. |
| `out_dir` | string | *(unset)* | If set, write one file per input clip. |
| `out_format` | string | `"json"` | `json`, `jsonl`, or `npy`. |
| `mode` | string | `"sync"` | `sync` or `async`. |

Emits `Metadata.Custom["twelvelabs"] = { "event": "embedded", "dim": 1024,
"embeddings": [...] }`. When `out_dir` is set, vectors are written to
disk and stripped from the event payload (only `out_file` remains).

## Embeddings example

Detect shot boundaries, save one MP4 per shot, and write one
`shot-NNNNN.embeddings.json` per shot for ingestion into your own
vector store:

```json
{
  "schema_version": "1.1",
  "inputs":  [{ "id": "src", "url": "input.mp4" }],
  "outputs": [
    { "id": "shots", "url": "out/shot-%05d.mp4",
      "segment_format": "mp4", "segment_on_metadata": "scene_change" }
  ],
  "nodes": [
    { "id": "dec",   "type": "source",       "input":  "src" },
    { "id": "scene", "type": "go_processor", "processor": "scene_change_adaptive" },
    { "id": "enc",   "type": "sink",         "output": "shots" },
    { "id": "embed", "type": "go_processor", "processor": "twelvelabs_embedder",
      "params": {
        "model":      "marengo3.0",
        "scopes":     ["clip"],
        "out_dir":    "out/embeddings",
        "out_format": "json"
      } }
  ],
  "edges": [
    { "from": "dec",   "to": "scene" },
    { "from": "scene", "to": "enc" },
    { "from": "enc",   "to": "embed" }
  ]
}
```

One-shot CLI equivalent:

```bash
mediamolder twelvelabs embed --video my-clip.mp4 --out my-clip.embeddings.json
```

Each `.embeddings.json` looks like:

```json
{
  "model": "marengo3.0",
  "dim":   1024,
  "vectors": [
    { "scope": "clip", "start_s": 0.0, "end_s": 4.21,
      "vector": [0.012, -0.087, "… 1024 floats …"] }
  ]
}
```

Load into Python (or any language) and push to your vector DB:

```python
import json, numpy as np, glob
rows = []
for path in sorted(glob.glob("out/embeddings/shot-*.embeddings.json")):
    with open(path) as f:
        doc = json.load(f)
    for v in doc["vectors"]:
        rows.append((path, v["start_s"], v["end_s"], np.asarray(v["vector"], dtype="float32")))
# rows → pgvector / Qdrant / FAISS…
```

Use `out_format: "npy"` instead for direct NumPy memory-maps when
handling millions of shots.

## Reading results

Every node emits to `Metadata.Custom["twelvelabs"]` on the event bus.
The `event` field discriminates the payload (`indexed` / `analyzed` /
`search` / `embedded` / `error`).

Three common ways to consume results:

**1. JSON Lines for offline processing**

```bash
mediamolder run graph.json --metadata-out results.jsonl
```

One JSON object per line, ready for `jq`, `pandas`, or any pipeline tool.

**2. Sidecar files per segment**

Add a `metadata_file_writer` node with `pattern: "{file}.tl.json"` to
emit one JSON file alongside each segment. Useful when downstream tools
expect a sidecar-per-clip layout (NLEs, asset managers).

**3. Muxed into the output video**

Pipe `chapters[]` from `twelvelabs_analyzer` into the existing
`mp4_chapter_writer` (MP4 `chap` track) or `webvtt_writer` (`.vtt` file)
so captions and chapter markers travel with the video. Text-only
summaries can ride along as ID3 / `mov_text` tags via `metadata_mux`.

**4. Embeddings to your own vector DB**

Set `twelvelabs_embedder.out_dir`; pick `json`, `jsonl`, or `npy` per
your ingest pipeline (see the embeddings example above).

**5. Go consumers**

```go
if tl, ok := metadata.Custom["twelvelabs"].(map[string]any); ok {
    switch tl["event"] {
    case "analyzed":
        fmt.Println("caption:", tl["text"])
    case "embedded":
        if path, ok := tl["out_file"].(string); ok {
            // load vectors from disk
            _ = path
        }
    }
}
```

## Cost & rate-limit notes

- Indexing is billed per minute of source video; analyze and search are
  billed per request. See <https://twelvelabs.io/pricing>.
- The client honours `429` with backoff up to 5 attempts. Use
  `mediamolder_twelvelabs_retries_total` to monitor.
- For high-volume graphs, use `process_every` upstream to throttle
  the rate of segments handed to the indexer.

## License note

TwelveLabs API usage is governed by their
[Terms of Service](https://twelvelabs.io/terms-of-service). MediaMolder
itself remains LGPL-2.1-or-later; the integration code calls the public
REST API only and ships no proprietary SDK.

## See also

- [Go Processor Nodes](go-processor-nodes.md) — processor interface reference.
- [Vidi 2.5 Guide](vidi-guide.md) — on-prem multimodal analysis alternative.
- [JSON Config Reference](json-config-reference.md) — graph schema.
- [TwelveLabs API docs](https://docs.twelvelabs.io/v1.3/api-reference/introduction)
````
