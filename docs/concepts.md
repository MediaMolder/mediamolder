# Core Concepts

This document explains the fundamental building blocks of MediaMolder: what a graph is, how nodes and edges work, how data flows, and how the engine manages a job from start to finish. No prior knowledge of FFmpeg internals is needed.

---

## Contents

1. [The graph model](#1-the-graph-model)
2. [Nodes](#2-nodes)
   - [Source nodes](#21-source-nodes)
   - [Filter nodes](#22-filter-nodes)
   - [Encoder nodes](#23-encoder-nodes)
   - [Sink nodes](#24-sink-nodes)
3. [Edges](#3-edges)
   - [Stream types](#31-stream-types)
   - [Port reference syntax](#32-port-reference-syntax)
   - [Multi-port nodes](#33-multi-port-nodes)
4. [Data flow and execution](#4-data-flow-and-execution)
5. [Graph lifecycle](#5-graph-lifecycle)
6. [Validation](#6-validation)
7. [Dynamic reconfiguration](#7-dynamic-reconfiguration)
8. [Clock and A/V synchronisation](#8-clock-and-av-synchronisation)
9. [Putting it all together](#9-putting-it-all-together)

---

## 1. The graph model

Every MediaMolder job is described as a **directed acyclic graph (DAG)** — a network of processing stages connected by typed data flows.

```
[Input file]
     │  video
     ▼
  [scale]
     │  video
     ▼
 [Encoder]
     │  video
     ▼
[Output file]
```

Three properties define a DAG and are enforced at build time:

- **Directed** — data always travels one way along an edge, from producer to consumer. There is no "both directions" on a single connection.
- **Acyclic** — there are no loops. A node cannot, directly or indirectly, feed data back to itself. The engine rejects graphs with cycles before any media is touched.
- **Graph** — nodes are the processing stages; edges are the connections between them.

The graph is stored in a JSON file under the `"graph"` key alongside `"inputs"` and `"outputs"`:

```json
{
  "schema_version": "1.0",
  "inputs":  [ ... ],
  "graph":   { "nodes": [...], "edges": [...] },
  "outputs": [ ... ]
}
```

Inputs and outputs are declared separately because they carry extra configuration (URLs, codecs, muxer settings) that doesn't belong to the graph topology itself. Internally, each input becomes a **source node** and each output becomes a **sink node** — they participate in the graph just like everything else.

---

## 2. Nodes

A node is a single processing stage. Every node has:

- A unique **`id`** string — used to reference it in edges
- A **kind** that determines what it does

There are four kinds of nodes, each with its own configuration schema.

---

### 2.1 Source nodes

A source node reads a media file, device, or network stream and produces decoded media for downstream nodes. It combines two libav* operations that always go together: **demuxing** (reading and parsing a container file, identifying and extracting the individual streams) and **decoding** (turning compressed packets into usable data). What "decoded" means depends on the stream type:

- **Video** — uncompressed, raw video frames (typically, raw pixels in whatever format the source YUV 4:2:0)
- **Audio** — packets of uncompressed PCM audio samples (e.g. fltp stereo at 48 kHz)
- **Subtitles** — decoded text or bitmap events (ASS, SRT, PGS, …); passed through largely as-is since most subtitle processing happens at the muxer
- **Data** — opaque packets (timecodes, SCTE-35 markers, metadata tracks); passed through unchanged

Source nodes are implicitly created from the `inputs` array — you don't write them in `graph.nodes`. Each input's `id` becomes the source node's id.

```json
"inputs": [
  {
    "id": "src",
    "url": "interview.mp4",
    "streams": [
      { "input_index": 0, "type": "video", "track": 0 },
      { "input_index": 1, "type": "audio", "track": 0 }
    ]
  }
]
```

This produces a source node with id `"src"` that emits two streams: a video stream addressable as `"src:v:0"` and an audio stream addressable as `"src:a:0"`. The `track` number is the index *within that type* — useful when an input has multiple audio tracks (`"src:a:0"`, `"src:a:1"`, …).

---

### 2.2 Filter nodes

A filter node transforms, analyses, or generates image or video frames, or audio samples. It wraps a single **libavfilter** filter — the same filters available in FFmpeg's `-vf`, `-af`, and `-filter_complex` options — but configured with a structured `params` object instead of a string.

```json
{
  "id": "resize",
  "type": "filter",
  "filter": "scale",
  "params": { "w": "1280", "h": "720" }
}
```

Filter nodes can:
- **Transform** — change resolution (`scale`), frame rate (`fps`), pixel format (`format`), volume (`volume`), sample rate (`aresample`), …
- **Generate** — produce a constant video source (`color`), silence (`aevalsrc`), test signals (`testsrc`), …
- **Combine** — merge multiple streams into one (`overlay`, `amerge`, `vstack`), …
- **Split** — duplicate a stream to multiple consumers (`split`, `asplit`), …
- **Analyse** — measure loudness (`ebur128`), detect scene changes (`scdet`), …
- **Copy** - take the encoded stream from the source node, passing it through unchanged

All filter parameter values are strings in JSON (libavfilter accepts them as strings internally). Use `mediamolder list-filters` to see the full catalogue.

---

### 2.3 Encoder nodes

An encoder node compresses raw frames into a specific coded format using a **libavcodec** encoder. It sits between filter nodes and the sink.

Encoder nodes can be declared explicitly or implicitly:

**Implicit** — the most common case. Set `codec_video` or `codec_audio` directly on an output and MediaMolder synthesises the encoder node automatically:

```json
"outputs": [{ "id": "out", "url": "output.mp4", "codec_video": "libx264", "codec_audio": "aac" }]
```

**Explicit** — when you need precise control over encoder parameters, declare the encoder in `graph.nodes` and wire it yourself:

```json
{
  "id": "enc",
  "type": "encoder",
  "params": { "codec": "libx264", "preset": "slow", "crf": "18" }
}
```

All codec parameters (preset, rate-control mode, CRF/CQ, bitrate, keyframe interval, etc.) live in `params`. Use `mediamolder list-codecs` to see available encoders.

---

### 2.4 Sink nodes

A sink node muxes encoded streams into an output container (MP4, MKV, TS, HLS, RTMP, …) and writes or streams the result. It is the last stage in every branch of the graph.

Like source nodes, sink nodes are implicitly created — one per entry in the `outputs` array. Each output's `id` is the sink node's id.

```json
"outputs": [
  {
    "id": "out",
    "url": "result.mp4",
    "codec_video": "libx264",
    "codec_audio": "aac"
  }
]
```

Edge endpoints that write to this sink use `"out:v"` (video) and `"out:a"` (audio).

---

## 3. Edges

Edges are the connections between nodes. Each edge:

- Has a **`from`** endpoint (producer)
- Has a **`to`** endpoint (consumer)
- Carries exactly **one stream type** (`"video"`, `"audio"`, `"subtitle"`, or `"data"`)

```json
{ "from": "src:v:0", "to": "resize", "type": "video" }
```

The engine validates every edge at build time. Connecting a video output port to an audio input port is rejected immediately.

---

### 3.1 Stream types

| Type | Carries | Typical path |
|---|---|---|
| `video` | Decoded video frames (raw pixels) | Source → filters → encoder → sink |
| `audio` | Decoded audio frames (raw PCM) | Source → filters → encoder → sink |
| `subtitle` | Text or bitmap subtitle packets | Source → (optional BSF) → sink |
| `data` | Opaque data streams (timecodes, metadata tracks) | Source → sink |

Video and audio frames travel as raw, uncompressed data between source, filter, and encoder nodes. They are only compressed again at the encoder stage. Subtitle and data streams typically pass through without decoding.

---

### 3.2 Port reference syntax

An endpoint string identifies both the node and which of its ports to use.

| Format | Meaning | Example |
|---|---|---|
| `"id"` | The node's default port | `"resize"` |
| `"id:port"` | A named port on the node | `"overlay:overlay"` |
| `"id:type:track"` | A typed track from a source node | `"src:v:0"`, `"src:a:1"` |
| `"id:v"` or `"id:a"` | The video or audio input of a sink | `"out:v"`, `"out:a"` |

The **type letter** in source references is a single character: `v` (video), `a` (audio), `s` (subtitle), `d` (data). The **track number** is the zero-based index among streams of that type declared in the input's `streams` array.

Examples:
- `"src:v:0"` — video track 0 from source `src`
- `"src:a:1"` — audio track 1 from source `src` (second audio stream)
- `"split:out0"` — first output port of a `split` filter node
- `"overlay:overlay"` — the `overlay` input pad of an `overlay` filter (the second video input)
- `"out:v"` — the video input of sink `out`

---

### 3.3 Multi-port nodes

Some filters have more than one input or more than one output. The port name in the edge reference selects which pad to connect to.

**Multi-input example** — `overlay` composites two video streams:

```
[background source] ──────► overlay:default ──► [result]
[foreground source] ──► overlay:overlay ──►
```

```json
{ "from": "bg:v:0", "to": "ov:default", "type": "video" },
{ "from": "fg:v:0", "to": "ov:overlay", "type": "video" }
```

**Multi-output example** — `split` duplicates one stream into two:

```
[source] ──► split ──► split:out0 ──► [encoder HD]
                  └──► split:out1 ──► [encoder SD]
```

```json
{ "from": "src:v:0",  "to":   "sp",       "type": "video" },
{ "from": "sp:out0",  "to":   "enc_hd",   "type": "video" },
{ "from": "sp:out1",  "to":   "enc_sd",   "type": "video" }
```

When a node has only one input or one output, use the bare node id or `"default"` as the port name — both mean the same thing.

---

## 4. Data flow and execution

When MediaMolder runs a graph, each node is assigned its own **goroutine group** (a set of Go goroutines supervised by an `errgroup`). Nodes communicate through **Go channels**: a producer writes frames to its output channel; the consumer reads from it.

```
[Source goroutine] --channel--> [Filter goroutine] --channel--> [Encoder goroutine] --channel--> [Sink goroutine]
```

Key properties of this model:

**Back-pressure is automatic.** If a downstream node falls behind, its input channel fills up. When the channel is full, the upstream node blocks rather than producing more frames. Slow consumers propagate slowness backwards through the graph naturally, without any explicit throttling code.

**Stages run in parallel.** Because each node runs independently, a four-stage chain (source → filter → encoder → sink) has all four stages active simultaneously, each working on a different frame. This is a pipeline in the concurrency sense: while the encoder is compressing frame N, the filter is transforming frame N+1 and the source is decoding frame N+2.

**Dedicated output lanes.** Each sink gets its own goroutine group. If you have two outputs (say, an archive file and a live stream), a slow network upload for the live stream does not stall the archive write. The channels provide independent buffering per output path.

**Graceful shutdown.** When a job is cancelled (Ctrl-C, or the `Stop` button in the GUI), a context cancellation signal propagates through all goroutines. Each stage finishes the frame it is currently working on, flushes any buffered frames to its downstream, then exits. Outputs are finalised cleanly — no truncated files.

**Channel buffer size.** Each inter-node channel buffers up to 8 frames by default. This smooths over transient speed mismatches between stages (e.g., a filter that occasionally takes longer on one frame) without consuming excessive memory.

---

## 5. Graph lifecycle

Every graph run follows a four-state lifecycle:

```
NULL ──► READY ──► PAUSED ──► PLAYING
  ▲        │         │           │
  └────────┴─────────┴───────────┘
              (any → NULL)
```

| State | What exists | What runs |
|---|---|---|
| `NULL` | Only the Go struct in memory | Nothing — no libav* contexts |
| `READY` | Inputs probed, codecs resolved, filter graph validated, all libav* contexts allocated | Nothing — resources are open but no data moves |
| `PAUSED` | Everything in READY, plus the first frame has been read into each stage | Stages have pre-rolled data but sinks are not writing |
| `PLAYING` | Fully active | All stages running; sinks writing output |

**Typical run path:** `NULL → READY → PAUSED → PLAYING → NULL`

You don't have to step through states manually. `mediamolder run` takes the graph from `NULL` all the way to `PLAYING` automatically, then back to `NULL` when the job finishes. The state machine is exposed through the Go library API for applications that need finer control (e.g., seek-then-inspect, pre-roll, pause-and-resume).

**Error handling.** If any stage returns an error while in `PLAYING`, the engine transitions to `NULL`, draining in-flight data. The exit code reflects whether the error was fatal.

---

## 6. Validation

Before any media I/O occurs, the engine validates the graph. Validation runs in two phases (also available as the `mediamolder validate` command):

### Phase A — Static analysis

Performed at build time using only the JSON config. No files are opened. Catches:

- **Duplicate node IDs** — two nodes with the same `id`
- **Unknown references** — an edge points to a node id that doesn't exist
- **Self-loops** — an edge from a node back to itself
- **Cycles** — a node (directly or indirectly) feeds back to an earlier node; detected using [Kahn's algorithm](https://en.wikipedia.org/wiki/Topological_sorting#Kahn's_algorithm)
- **Type mismatches** — a video output port connected to an audio input port
- **Codec/container incompatibilities** — e.g. VP9 video in an MP4 container
- **Missing required fields** — `id`, `url`, codec, etc.
- **Filter arity errors** — a filter that requires two inputs receiving only one

### Phase B — Probe-assisted analysis

Performed after opening each input with `avformat_find_stream_info`. Catches runtime issues that can only be detected from the actual media:

- **Interlaced input without a deinterlace filter** — the `yadif` filter is needed
- **HDR input without tone-mapping** — the `zscale` + `tonemap` filter chain is needed for SDR output
- **VFR input to a fixed-rate encoder** — an `fps` filter is needed
- **Pixel format mismatch** — e.g. 10-bit source to an 8-bit-only encoder
- **Sample rate or channel layout mismatch** — `aresample` or `aformat` needed

Phase B is skipped when `--no-probe` is passed. Phase A alone is suitable for CI pre-checks that don't have access to the actual media files.

---

## 7. Dynamic reconfiguration

MediaMolder supports changing the graph *while it is running* (`PLAYING` state), subject to the following rules:

### Parameter changes

Changing a filter's parameters (e.g. the text string in `drawtext`, the volume level in `volume`) is applied **between frames** — the engine drains the current frame from the affected filter, applies the new parameter via libavfilter's `av_opt_set`, and resumes. No frames are dropped.

### Structural changes (add/remove nodes)

Adding or removing a node requires a brief **quiesce step**:

1. The engine stops accepting new packets into the affected sub-graph.
2. All in-flight frames in that sub-graph are drained to the nearest sink or buffer point.
3. The structural change is applied (node added, removed, or replaced).
4. The engine resumes from where data flow stopped.

The caller receives an acknowledgement when the change is live.

### Codec changes

Changing the codec on an existing output **mid-stream is not supported**. To change codecs, remove the output and add a new one with the desired codec. This starts a new output file/segment.

### Adding an output at runtime

A new output (sink + encoder) can be hot-added while the graph is playing via `Pipeline.AddOutput()`. The new output taps into the existing decoded frame stream; no seek or restart is required.

---

## 8. Clock and A/V synchronisation

Every running graph has a single **reference clock**. All stages schedule their work relative to this clock.

**Clock selection:**
- If all inputs are files, the reference clock is the system monotonic clock and the graph runs as fast as the hardware allows (no real-time pacing).
- If any input is a live source (RTMP, RTSP, SRT, capture device), that source provides the clock. The graph paces output to match real-time.
- When multiple live sources are present, the first live source is the master clock by default. Other sources are slaved to it.
- Setting `"realtime": true` in `global_options` forces real-time pacing even for file inputs (useful when the output is a live destination such as RTMP).

**A/V sync:**
Audio and video streams sharing an output are kept synchronised against the reference clock. If audio or video drifts beyond a tolerance window (default: ±40 ms), the engine either inserts a brief silence (audio short) or drops a frame (video late) to re-align. This maintains lip-sync without audible glitches for typical content.

**PTS tracking:**
Every frame carries a presentation timestamp (PTS) in microseconds. The clock package translates these to wall-clock durations and back, handling wrap-around and discontinuities from live sources.

**Seek:**
`Pipeline.Seek(target)` pauses the graph, flushes all stage buffers, seeks all inputs to the nearest keyframe before `target`, and moves to `PAUSED`. Call `Resume()` or set state to `PLAYING` to continue from the new position.

---

## 9. Putting it all together

Here is an annotated example that ties everything together. The job takes a 4K HDR video, tone-maps it to SDR, scales it to 1080p, deinterlaces it, overlays a watermark, then produces two outputs simultaneously — an H.264 archive and an HLS live stream.

```
[src:v:0] ──► tonemap ──► scale ──► yadif ──► split ──►──► enc_h264 ──► [archive:v]
                                                      └──► [hls:v]

[src:a:0] ──────────────────────────────────────────────────────────────► [archive:a]
                                                                       └──► [hls:a]

[logo:v:0] ──► overlay (second input)
```

**Nodes and their roles:**

| Node | Kind | Role |
|---|---|---|
| `src` | Source | Demux + decode the 4K HDR input |
| `logo` | Source | Demux + decode the PNG watermark |
| `tonemap` | Filter | `zscale` + `tonemap` — HDR → SDR |
| `scale` | Filter | Resize 4K → 1920×1080 |
| `yadif` | Filter | Deinterlace |
| `overlay` | Filter | Composite the watermark over the video |
| `split` | Filter | Duplicate the video for two outputs |
| `enc_h264` | Encoder | Compress to H.264 for the archive |
| `archive` | Sink | Mux to an MKV file |
| `hls` | Sink | Mux to an HLS playlist |

**What happens at runtime:**

1. **Build**: `graph.Build()` resolves all node IDs, validates edge types, runs topological sort. Order: `src → tonemap → scale → yadif → overlay → split → enc_h264 → archive / hls`.
2. **READY**: libavformat opens the input; libavcodec allocates decoder contexts; libavfilter chains are configured; HLS/archive muxers are opened.
3. **PLAYING**: Each node runs in its own goroutine. `src` decodes frames and pushes them into the `tonemap` channel. `tonemap` processes and pushes to `scale`. And so on. `split` writes the same frame into two channels simultaneously — one for `enc_h264` and one directly to `hls`. The `archive` and `hls` sinks run independently; a momentary stall on the HLS network write does not block `enc_h264`.
4. **End of stream**: When `src` exhausts the input, it sends an EOS signal. Each stage drains its buffered frames in order. Muxers write their trailers. Both output files are finalised.
5. **NULL**: All libav* contexts are freed. The run exits cleanly.
