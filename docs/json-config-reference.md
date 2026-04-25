# JSON Config Reference

MediaMolder pipelines are defined as JSON files conforming to schema v1.0, v1.1, or v1.2.

## Top-level structure

| Field            | Type     | Required | Description                              |
|------------------|----------|----------|------------------------------------------|
| `schema_version` | string   | yes      | `"1.0"`, `"1.1"`, or `"1.2"`             |
| `inputs`         | array    | yes      | Input sources                            |
| `graph`          | object   | yes      | Processing graph (nodes + edges)         |
| `outputs`        | array    | yes      | Output sinks                             |
| `global_options` | object   | no       | Global pipeline options                  |

Use `"1.1"` when the graph contains `go_processor` nodes. Use `"1.2"` when the graph stores editor-side data under `graph.ui` (e.g. node positions written by `mediamolder gui`); v1.2 is otherwise a strict superset of v1.1, and the runtime accepts all three versions interchangeably. Stream-copy nodes (`type: "copy"`) work under any of the three versions.

## Input

| Field      | Type   | Required | Description                                    |
|------------|--------|----------|------------------------------------------------|
| `id`       | string | yes      | Unique identifier, referenced in edge `from`   |
| `url`      | string | yes      | File path or URL                               |
| `streams`  | array  | yes      | Stream selections                              |
| `options`  | object | no       | Demuxer options (key-value)                     |

### StreamSelect

| Field         | Type   | Required | Description                        |
|---------------|--------|----------|------------------------------------|
| `input_index` | int    | yes      | Index into the input's streams     |
| `type`        | string | yes      | `"video"`, `"audio"`, `"subtitle"`, `"data"` |
| `track`       | int    | yes      | Zero-based track number            |

## Graph

| Field   | Type  | Required | Description         |
|---------|-------|----------|---------------------|
| `nodes` | array | yes      | Processing nodes    |
| `edges` | array | yes      | Directed connections|

### NodeDef

| Field          | Type   | Required | Description                                                                |
|----------------|--------|----------|----------------------------------------------------------------------------|
| `id`           | string | yes      | Unique node identifier                                                     |
| `type`         | string | yes      | `"filter"`, `"encoder"`, `"copy"`, `"source"`, `"sink"`, `"go_processor"`   |
| `filter`       | string | no       | Filter name (for filter nodes)                                             |
| `processor`    | string | no       | Registered Go processor name (required for `go_processor` nodes)           |
| `params`       | object | no       | Parameters (key-value)                                                     |
| `error_policy` | object | no       | Error handling policy                                                      |

For `"encoder"` nodes, every key in `params` other than `codec`, `width`, `height`, `bitrate`, `threads`, and `thread_type` is forwarded verbatim to the encoder via `av_dict_set` → `avcodec_open2`. This is how codec-specific options like `preset`, `crf`, `maxrate`, `bufsize`, or `x264-params` reach libavcodec. `b` is accepted as an alias for `bitrate`; `g` maps to GOP size.

For `"copy"` nodes, no `params` are required — the inbound edge type tells the runtime which input stream to forward. See [Stream-copy nodes](#stream-copy-nodes-type-copy).

### EdgeDef

| Field  | Type   | Required | Description                                          |
|--------|--------|----------|------------------------------------------------------|
| `from` | string | yes      | Source endpoint: `"nodeID"`, `"nodeID:port"`, or `"inputID:type:track"` |
| `to`   | string | yes      | Destination endpoint (same format)                   |
| `type` | string | yes      | `"video"`, `"audio"`, `"subtitle"`, `"data"`         |

### Edge reference formats

- `"inputID:v:0"` — video track 0 from input
- `"inputID:a:1"` — audio track 1 from input
- `"nodeID:default"` — default port on a filter node
- `"nodeID:overlay"` — named port (e.g., overlay filter's second input)
- `"outputID:v"` — video input to output muxer

## Output

| Field         | Type   | Required | Description            |
|---------------|--------|----------|------------------------|
| `id`          | string | yes      | Unique output ID       |
| `url`         | string | yes      | File path or URL       |
| `format`      | string | no       | Container format       |
| `codec_video`    | string | no       | Video encoder name     |
| `codec_audio`    | string | no       | Audio encoder name     |
| `codec_subtitle` | string | no       | Subtitle encoder name  |
| `bsf_video`      | string | no       | Video bitstream filter |
| `bsf_audio`      | string | no       | Audio bitstream filter |
| `options`        | object | no       | Muxer options          |

## GlobalOptions

| Field         | Type   | Required | Description                                                                 |
|---------------|--------|----------|-----------------------------------------------------------------------------|
| `threads`     | int    | no       | Default codec thread count for all decoders/encoders. 0 = FFmpeg auto.      |
| `thread_type` | string | no       | Default threading model: `"frame"`, `"slice"`, `"frame+slice"`, or omit for auto. |
| `hw_accel`    | string | no       | Hardware acceleration backend                                               |
| `hw_device`   | string | no       | Hardware device name/path                                                   |
| `realtime`    | bool   | no       | Pace output to wall-clock time                                              |

Per-node `params.threads` and `params.thread_type` override the global values for individual codecs. See [Threading Architecture](threading-architecture.md).

## ErrorPolicy

| Field           | Type   | Required | Description                              |
|-----------------|--------|----------|------------------------------------------|
| `policy`        | string | yes      | `"abort"`, `"skip"`, `"retry"`, `"fallback"` |
| `max_retries`   | int    | no       | Max retry attempts (default: 3)          |
| `fallback_node` | string | no       | Node ID to reroute to on failure         |

## Example

```json
{
  "schema_version": "1.0",
  "inputs": [
    {
      "id": "src",
      "url": "input.mp4",
      "streams": [
        { "input_index": 0, "type": "video", "track": 0 },
        { "input_index": 0, "type": "audio", "track": 0 }
      ]
    }
  ],
  "graph": {
    "nodes": [
      {
        "id": "scale",
        "type": "filter",
        "filter": "scale",
        "params": { "w": 1280, "h": 720 }
      }
    ],
    "edges": [
      { "from": "src:v:0", "to": "scale:default", "type": "video" },
      { "from": "scale:default", "to": "out:v", "type": "video" },
      { "from": "src:a:0", "to": "out:a", "type": "audio" }
    ]
  },
  "outputs": [
    {
      "id": "out",
      "url": "output.mp4",
      "codec_video": "libx264",
      "codec_audio": "aac"
    }
  ]
}
```

## Stream-copy nodes (`type: "copy"`)

A copy node forwards demuxer packets straight to the muxer with no decode and no encode. Use it when the source codec is already what the destination container should carry — typical "swap container" or "merge tracks losslessly" jobs.

- The runtime adds the output stream by copying the input stream's `AVCodecParameters` directly (no encoder context is opened for that stream).
- Packet timestamps are rescaled per packet from the demuxer's `time_base` to the muxer's `time_base`, so VFR sources and container-imposed timebases (e.g. MP4 audio at 1/15360) are handled.
- The destination container must accept the source codec; the muxer clears `codec_tag` so a container-appropriate FourCC is selected.
- A copy node has exactly **one input and one output**, and its input must come directly from a source node (no filter/processor in front — those imply a decoded frame path).

### Example — swap container without re-encoding

```json
{
  "schema_version": "1.2",
  "inputs": [
    { "id": "in", "url": "clip.mkv",
      "streams": [
        { "input_index": 0, "type": "video", "track": 0 },
        { "input_index": 0, "type": "audio", "track": 0 }
      ] }
  ],
  "graph": {
    "nodes": [
      { "id": "cv", "type": "copy" },
      { "id": "ca", "type": "copy" }
    ],
    "edges": [
      { "from": "in:v:0", "to": "cv",     "type": "video" },
      { "from": "cv",     "to": "out:v",  "type": "video" },
      { "from": "in:a:0", "to": "ca",     "type": "audio" },
      { "from": "ca",     "to": "out:a",  "type": "audio" }
    ]
  },
  "outputs": [ { "id": "out", "url": "clip.mp4" } ]
}
```

Mix freely with encoders — e.g. re-encode the video while copying the audio:

```json
"edges": [
  { "from": "in:v:0", "to": "x264",   "type": "video" },
  { "from": "x264",   "to": "out:v",  "type": "video" },
  { "from": "in:a:0", "to": "ca",     "type": "audio" },
  { "from": "ca",     "to": "out:a",  "type": "audio" }
]
```

## go_processor Nodes (schema v1.1)

The `go_processor` node type enables **custom Go per-frame processing** — for AI inference, quality analysis, tracking, metadata injection, or any transformation that doesn't fit neatly into a libavfilter.

- `processor` must match a name registered via `processors.Register(...)`.
- `params` are passed directly to the processor's `Init()` method.
- Frames flow as `*av.Frame`; the processor may modify, replace, or drop them.
- Non-nil `Metadata` returned by `Process()` is published on the pipeline event bus.

See [Go Processor Nodes](go-processor-nodes.md) for the full guide.

### Example — frame counter

```json
{
  "schema_version": "1.1",
  "inputs": [
    {
      "id": "src",
      "url": "input.mp4",
      "streams": [
        { "input_index": 0, "type": "video", "track": 0 }
      ]
    }
  ],
  "graph": {
    "nodes": [
      {
        "id": "counter",
        "type": "go_processor",
        "processor": "frame_counter",
        "params": { "log_every": 100 }
      }
    ],
    "edges": [
      { "from": "src:v:0", "to": "counter:default", "type": "video" },
      { "from": "counter:default", "to": "out:v", "type": "video" }
    ]
  },
  "outputs": [
    {
      "id": "out",
      "url": "output.mp4",
      "codec_video": "libx264"
    }
  ]
}
```
