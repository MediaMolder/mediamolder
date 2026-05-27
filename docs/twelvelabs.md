# TwelveLabs Video Understanding

MediaMolder integrates the [TwelveLabs Video Understanding
API](https://docs.twelvelabs.io/v1.3/api-reference/introduction) so your
graphs can **index, analyze, search, and embed** video clips with the
Marengo and Pegasus foundation models — without writing any Go.

Four built-in `go_processor` nodes handle the integration:

| Node | Model | Purpose |
|---|---|---|
| `twelvelabs_indexer` | Marengo + Pegasus | Upload completed clips into a TwelveLabs index. |
| `twelvelabs_analyzer` | Pegasus 1.5 | Generate captions, summaries, and structured chapters per segment. |
| `twelvelabs_searcher` | Marengo 3.0 | Run natural-language search against an index, emit timestamped hits. |
| `twelvelabs_embedder` | Marengo 3.0 | Produce per-clip / per-window video embeddings, inline or to disk. |

## When to use it

- **Editorial / archive workflows** — auto-summarise long clips, generate
  chapter markers, surface "all goals" or "all close-ups" as cut points.
- **Compliance / moderation** — search a rolling index for spoken phrases
  or visual cues; tag matches on the graph event bus.
- **Recommendation / similarity** — embed segments with Marengo and
  persist vectors alongside your existing metadata.

For low-latency on-prem analysis without uploading clips, use
[`vidi_analyzer`](vidi-guide.md) instead.

## Getting an API key

1. Sign up at <https://playground.twelvelabs.io/> (a free tier is
   available).
2. Create an API key on the dashboard.
3. Make it available to MediaMolder via one of the following sources —
   they are tried in this order:

   1. The `--api-key` flag (CLI) or `api_key` processor parameter.
   2. The `TWELVELABS_API_KEY` environment variable.
   3. `~/.config/mediamolder/twelvelabs.json` with the shape
      `{"api_key": "tlk_…"}`.

   The same precedence is used by the CLI, the in-graph processors, and
   the `/api/twelvelabs/*` HTTP routes.

```bash
export TWELVELABS_API_KEY="tlk_…"
```

## Quick start (CLI)

The `mediamolder twelvelabs` subcommand wraps the REST API for ad-hoc
operations:

```bash
# Create an index that supports both models.
mediamolder twelvelabs indexes create \
    --name demo --models marengo3.0,pegasus1.5

# Upload a file and wait for it to be ingested.
mediamolder twelvelabs index --index demo my-clip.mp4

# Ask Pegasus to summarise it (use the video_id printed above).
mediamolder twelvelabs analyze \
    --video-id <video_id> \
    --prompt "Summarise the clip in one sentence."

# Search the index.
mediamolder twelvelabs search --index demo --query "a person walking a dog"

# Generate Marengo embeddings to a file.
mediamolder twelvelabs embed --video my-clip.mp4 --out my-clip.embeddings.json
```

Run `mediamolder twelvelabs help` for the full flag reference.

## HTTP routes

When the GUI server is running (`mediamolder gui`), the same operations
are available over HTTP for browser tools or external scripts:

| Method | Path | Body |
|---|---|---|
| `GET`    | `/api/twelvelabs/ping`             | — |
| `GET`    | `/api/twelvelabs/indexes`          | — |
| `POST`   | `/api/twelvelabs/indexes`          | `{"name": "...", "models": [{"name": "marengo3.0"}]}` |
| `DELETE` | `/api/twelvelabs/indexes/{id}`     | — |
| `POST`   | `/api/twelvelabs/search`           | `{"index_id": "...", "query": "...", "search_options": ["visual","audio"], "threshold": "medium"}` |

API-key resolution follows the same precedence chain as the CLI.

## Graph examples

### Pass-through (whole file)

Upload a file as-is and have Pegasus summarise it:

```json
{
  "schema_version": "1.1",
  "inputs": [{ "id": "src", "url": "my-clip.mp4" }],
  "nodes": [
    { "id": "file",    "type": "file_source",   "input":  "src" },
    { "id": "indexer", "type": "go_processor",  "processor": "twelvelabs_indexer",
      "params": { "index_id": "${TL_INDEX}", "wait_for_ready": true } },
    { "id": "summary", "type": "go_processor",  "processor": "twelvelabs_analyzer",
      "params": { "prompt": "Summarise in one sentence." } }
  ],
  "edges": [
    { "from": "file",    "to": "indexer" },
    { "from": "indexer", "to": "summary", "kind": "events" }
  ]
}
```

### Shot-aware chunking

Detect shot boundaries, cut into one MP4 per shot, and index + caption
each:

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
    { "from": "enc",     "to": "indexer", "kind": "events" },
    { "from": "enc",     "to": "caption", "kind": "events" }
  ]
}
```

The `events`-kind edge from `segment_sink` (`enc`) delivers a
`SegmentCompleted` event whenever a shot file is closed; the engine
auto-registers any compatible downstream `go_processor` as a
`SegmentEventConsumer`. Run with metadata captured to a file:

```bash
mediamolder run graph.json --metadata-out captions.jsonl
```

### Per-shot embeddings to disk

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
    { "from": "enc",   "to": "embed", "kind": "events" }
  ]
}
```

Each `out/embeddings/shot-NNNNN.embeddings.json` looks like:

```json
{
  "model":  "marengo3.0",
  "dim":    1024,
  "vectors": [
    { "scope": "clip", "start_s": 0.0, "end_s": 4.21,
      "vector": [0.012, -0.087, "… 1024 floats …"] }
  ]
}
```

Load and ingest into your own vector store (pgvector, Qdrant, FAISS, …):

```python
import json, glob, numpy as np
rows = []
for path in sorted(glob.glob("out/embeddings/shot-*.embeddings.json")):
    with open(path) as f:
        doc = json.load(f)
    for v in doc["vectors"]:
        rows.append((path, v["start_s"], v["end_s"],
                     np.asarray(v["vector"], dtype="float32")))
```

## Processor reference

All four processors accept the following common parameters in addition
to those listed below:

| Param | Type | Default | Description |
|---|---|---|---|
| `api_key` | string | *(unset)* | TwelveLabs API key (highest precedence). |
| `api_key_env` | string | `TWELVELABS_API_KEY` | Env var holding the key. |
| `base_url` | string | *(production)* | API base URL override (tests). |
| `poll_interval_s` | float | `2` | Initial poll interval (with backoff). |
| `poll_max_interval_s` | float | `30` | Backoff ceiling. |
| `request_timeout_s` | float | *(unbounded)* | Per-request HTTP timeout. |

### `twelvelabs_indexer`

Uploads each completed segment / file into a TwelveLabs index and emits
`{event: "indexed", task_id, video_id, file_path, segment_index, …}` on
the metadata bus.

| Param | Type | Default | Description |
|---|---|---|---|
| `index_id` | string | **required** | Pre-existing TwelveLabs index. |
| `wait_for_ready` | bool | `true` | Block until task reaches `ready`. |
| `max_concurrent` | int | `2` | In-flight uploads per graph. |

### `twelvelabs_analyzer`

Runs a Pegasus analyze call on each completed segment.

| Param | Type | Default | Description |
|---|---|---|---|
| `index_id` | string | **required** | Index holding the uploaded segment. |
| `prompt` | string | `"Describe what happens in this video."` | Pegasus prompt. |
| `temperature` | float | `0.2` | Pegasus sampling temperature. |
| `segments` | bool | `false` | Request structured timestamped chapters. |
| `max_concurrent` | int | `2` | In-flight analyze calls. |

Emits `{event: "analyzed", text, chapters: [...], task_id, video_id, …}`.

### `twelvelabs_searcher`

Runs Marengo search either on a timer (live monitoring of a fixed
query) or once per completed segment.

| Param | Type | Default | Description |
|---|---|---|---|
| `index_id` | string | **required** | Index to search. |
| `query` | string | *(one required)* | Natural-language query. |
| `query_media_url` | string | *(one required)* | Image / audio query URL. |
| `search_options` | []string | `["visual","audio"]` | Modalities. |
| `threshold` | string | `"medium"` | `low` / `medium` / `high`. |
| `page_limit` | int | *(server default)* | Max hits per page. |
| `min_score` | float | `0` | Drop matches below this score. |
| `interval_s` | float | `0` | If > 0, re-run on a timer (otherwise per segment). |

Emits `{event: "search", matches: [...], count, index_id, query, …}`.

### `twelvelabs_embedder`

Generates a Marengo video embedding per segment.

| Param | Type | Default | Description |
|---|---|---|---|
| `model` | string | `"marengo3.0"` | Embedding model. |
| `scopes` | []string | `["clip"]` | `clip` and/or `video` windows. |
| `window_s` | float | `6` | Window length when `scopes` includes `video`. |
| `out_dir` | string | *(unset)* | If set, write one file per input clip. |
| `out_format` | string | `"json"` | `json` or `jsonl`. |
| `max_concurrent` | int | `2` | In-flight embed calls. |

Emits `{event: "embedded", dim, count, embeddings|out_file, task_id, …}`.
When `out_dir` is set, vectors are written to disk and stripped from the
event payload (only `out_file` remains).

## Reading results

Every node emits to `Metadata.Custom["twelvelabs"]` on the event bus.
The `event` field discriminates the payload (`indexed` / `analyzed` /
`search` / `embedded`).

The two common offline consumption patterns:

**1. JSON Lines for offline processing**

```bash
mediamolder run graph.json --metadata-out results.jsonl
```

One JSON object per line, ready for `jq`, `pandas`, or any pipeline
tool.

**2. Sidecar files per segment**

Add a `metadata_file_writer` node downstream of the analyzer / embedder
to emit one JSON file alongside each segment.

## Cost & rate-limit notes

- Indexing is billed per minute of source video; analyze and search are
  billed per request. See <https://twelvelabs.io/pricing>.
- The client honours `429` with exponential backoff up to 5 attempts.
- For high-volume graphs, use `process_every` upstream to throttle the
  rate of segments handed to the indexer, or shrink `max_concurrent`.

## License note

TwelveLabs API usage is governed by their [Terms of
Service](https://twelvelabs.io/terms-of-service). MediaMolder itself
remains LGPL-2.1-or-later; the integration code calls the public REST
API only and ships no proprietary SDK.

## See also

- [Go Processor Nodes](go-processor-nodes.md) — processor interface
  reference.
- [Vidi 2.5 Guide](vidi-guide.md) — on-prem multimodal analysis
  alternative.
- [JSON Config Reference](json-config-reference.md) — graph schema.
- [TwelveLabs Integration Architecture](architecture/twelvelabs_integration.md)
  — design and phased implementation notes.
- [TwelveLabs API docs](https://docs.twelvelabs.io/v1.3/api-reference/introduction)
