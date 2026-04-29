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
| `copy_ts`        | bool     | no       | Preserve original demuxer timestamps end-to-end (FFmpeg `-copyts`). Suppresses the implicit `-ts_offset` after `-ss` and switches output-side `ss`/`to` to absolute timeline values. |

Use `"1.1"` when the graph contains `go_processor` nodes. Use `"1.2"` when the graph stores editor-side data under `graph.ui` (e.g. node positions written by `mediamolder gui`); v1.2 is otherwise a strict superset of v1.1, and the runtime accepts all three versions interchangeably. Stream-copy nodes (`type: "copy"`) work under any of the three versions.

## Input

| Field      | Type   | Required | Description                                    |
|------------|--------|----------|------------------------------------------------|
| `id`       | string | yes      | Unique identifier, referenced in edge `from`   |
| `url`      | string | yes      | File path or URL                               |
| `kind`     | string | no       | `"file"` (default) or `"lavfi"` (open URL through libavfilter virtual demuxer; FFmpeg `-f lavfi`). |
| `streams`  | array  | yes      | Stream selections                              |
| `options`  | object | no       | Demuxer options (key-value). Includes per-input timing flags `ss` (start), `t` (duration), `to` (end), accepting seconds or `HH:MM:SS[.ms]`. These restrict the demuxer so every downstream stage sees only the trimmed window. Surfaced as the **Timing** section on the Input form in the GUI. |
| `map_metadata` | bool | no   | Copy this input's container-level metadata onto outputs that don't set their own (FFmpeg `-map_metadata`). |
| `map_chapters` | bool | no   | Copy this input's chapter table onto outputs that don't set their own (FFmpeg `-map_chapters`). First input wins. |
| `stream_loop`  | int  | no   | Number of *additional* times to rewind on EOF. `0` = no loop, `N>0` plays N+1 times, `-1` = infinite. PTS continues monotonically across iterations. (FFmpeg `-stream_loop`). |
| `itsoffset`    | float | no  | Shift every demuxed PTS/DTS by this many seconds (may be negative). Composes additively with the `-ss` ts_offset. (FFmpeg `-itsoffset`). |
| `read_rate`    | float | no  | Pace packet reads to (read_rate × realtime). `1.0` = native-rate (FFmpeg `-re`); `2.0` = 2× realtime. `0` (default) disables pacing. |
| `read_rate_initial_burst` | float | no | Seconds of media-time at the start to read at full speed before pacing kicks in. Defaults to 0.5 when `read_rate` is non-zero. |
| `read_rate_catchup` | float | no | Multiplier used to recover from pacing lag (must be ≥ `read_rate`). Defaults to `read_rate × 1.05` when unset. |

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
| `codec_tag_video`    | string | no | FourCC override for video (`-tag:v`, e.g. `"hvc1"`). Must be exactly 4 ASCII chars. |
| `codec_tag_audio`    | string | no | FourCC override for audio (`-tag:a`).    |
| `codec_tag_subtitle` | string | no | FourCC override for subtitles (`-tag:s`). |
| `max_frames_video` | int  | no | Cap muxed video packets (FFmpeg `-frames:v` / `-vframes`). |
| `max_frames_audio` | int  | no | Cap muxed audio packets (FFmpeg `-frames:a` / `-aframes`). |
| `fps_mode`         | string | no | `passthrough` (default) / `vfr` / `cfr` / `drop`. Reconciles encoder PTS with target framerate. (FFmpeg `-fps_mode`). |
| `audio_sync`       | int  | no | Inject `aresample=async=N` in front of the audio encoder. `1` = trim/pad start only; `>1` = continuous soft compensation (1000 is typical). (FFmpeg legacy `-async`). |
| `shortest`         | bool | no | Stop muxing when the shortest stream feeding this output ends. (FFmpeg `-shortest`). |
| `max_file_size`    | int64 | no | Cap container size in bytes (FFmpeg `-fs`). |
| `metadata`         | object | no | Container-level metadata (`-metadata key=value`). Replaces input-mapped metadata when set. |
| `streams`          | array | no | Per-stream metadata + disposition overrides; see [StreamSpec](#streamspec). |
| `chapters`         | array | no | Explicit chapter table; replaces input-mapped chapters when set. |
| `kind`             | string | no | `"file"` (default) or `"tee"` for fan-out muxing; see [Tee outputs](#tee-outputs). |
| `targets`          | array | no | Required when `kind == "tee"`; see [TeeTarget](#teetarget). |
| `encoder_params_video`    | object | no | Codec-specific options forwarded to the implicit video encoder (`crf`, `preset`, …). |
| `encoder_params_audio`    | object | no | Codec-specific options forwarded to the implicit audio encoder. |
| `encoder_params_subtitle` | object | no | Codec-specific options forwarded to the implicit subtitle encoder. |
| `options`        | object | no       | Muxer options. Includes per-output timing flags `ss` (start), `t` (duration), `to` (end), accepting seconds or `HH:MM:SS[.ms]`. These restrict what the muxer writes (the full source still flows through the graph), which is the typical place to trim a stream-copy job. Surfaced as the **Timing** section on the Output form in the GUI. |

### StreamSpec

Per-stream metadata + disposition addressed in FFmpeg-style `s:<type>:<index>` form. Mirrors `-metadata:s:<type>:<idx>` and `-disposition:s:<type>:<idx>`.

| Field        | Type   | Required | Description |
|--------------|--------|----------|-------------|
| `type`       | string | yes      | `"v"`, `"a"`, `"s"`, or `"d"`. |
| `index`      | int    | yes      | 0-based position within the chosen type, in muxer-add order. |
| `metadata`   | object | no       | `av_dict_set` per key — typically `{"language": "eng"}`. |
| `disposition`| string | no       | `+`-separated AV_DISPOSITION_* names (e.g. `"default+forced"`). |

### Tee outputs

When `kind == "tee"`, the output's `url` and `format` are ignored and `targets` is required. The runtime opens libavformat's built-in `tee` muxer, which fans out one encoded packet stream to N container slaves with no re-encoding. Per-target metadata/disposition is **not** supported by libavformat (slaves clone the parent context's metadata); use `Output.Metadata` / `Output.Streams` to set values shared by every target. Per-target stream selection (`select`) and bitstream-filter chains (`bsfs`) are supported.

### TeeTarget

| Field          | Type   | Required | Description |
|----------------|--------|----------|-------------|
| `url`          | string | yes      | Slave URL (file path or scheme). |
| `format`       | string | no       | Force container format (`f=`); usually required since auto-detection from `url` may fail. |
| `select`       | string | no       | Comma-separated FFmpeg stream specifiers (e.g. `"v"`, `"a:0"`, `"v,a:0"`). Default = all streams. |
| `bsfs`         | string | no       | Bitstream filter chain (e.g. `"h264_mp4toannexb"`). Append `/v` etc. via `bsfs_v`/`bsfs_a` for stream-specific chains via the `options` map. |
| `onfail`       | string | no       | `"abort"` (default) or `"ignore"` — slave-failure policy. |
| `use_fifo`     | bool   | no       | Wrap slave in the `fifo` muxer (extra buffering thread). |
| `fifo_options` | string | no       | `;`-separated `key=value` options forwarded to the fifo muxer when `use_fifo` is set. |
| `options`      | object | no       | Additional `[opt=val:opt=val]` pairs prepended to the slave URL. |

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
