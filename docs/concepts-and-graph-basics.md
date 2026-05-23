# Graph Basics — Nodes, Edges, and the MediaMolder Processing Model

This document is a tutorial introduction to every fundamental concept in
MediaMolder: what a graph is, how nodes and edges work, how data flows through
the engine, and how the same ideas are expressed both in a JSON job file,
(which can be run by being passed to the `mediamolder` binary through the 
Command Line Interface, or CLI) and as a visual graph in the canvas of the 
MediaMolder Graphical User Interface.

It also maps everything back to the FFmpeg CLI:
*"when can `-c copy` work, when do I need a `-filter_complex` chain, and what
does the order of `-i`, `-map`, `-c:v`, `-vf` on the command line correspond
to in the JSON graph?"*

---

## Contents

1. [The graph model](#1-the-graph-model)
2. [The JSON job file](#2-the-json-job-file)
3. [Nodes](#3-nodes)
   - [Source nodes](#31-source-nodes)
   - [Filter nodes](#32-filter-nodes)
   - [Encoder nodes](#33-encoder-nodes)
   - [Sink nodes](#34-sink-nodes)
   - [Copy nodes](#35-copy-nodes)
   - [Go processor nodes](#36-go-processor-nodes)
   - [Other node types](#37-other-node-types)
4. [Edges](#4-edges)
   - [Stream types](#41-stream-types)
   - [Port reference syntax](#42-port-reference-syntax)
   - [Multi-port nodes](#43-multi-port-nodes)
5. [Compatibility — when direct wiring works](#5-compatibility--when-direct-wiring-works)
   - [The three rules](#51-the-three-rules)
   - [When a transform filter is required](#52-when-a-transform-filter-is-required)
   - [Stream-copy vs. transcode](#53-stream-copy-vs-transcode)
6. [Sources and sinks in detail](#6-sources-and-sinks-in-detail)
7. [FFmpeg CLI → JSON mapping](#7-ffmpeg-cli--json-mapping)
8. [Data flow and execution](#8-data-flow-and-execution)
   - [Performance monitoring](#8a-performance-monitoring)
9. [Graph lifecycle](#9-graph-lifecycle)
10. [Validation](#10-validation)
11. [Dynamic reconfiguration](#11-dynamic-reconfiguration)
12. [Full worked example](#12-full-worked-example)
13. [Diagnostic checklist](#13-diagnostic-checklist)
14. [Cross-references](#14-cross-references)

---

## 1. The graph model

Every MediaMolder job is described as a **directed acyclic graph (DAG)** — a
network of processing stages connected by typed data flows.

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

- **Directed** — data always travels one way along an edge, from producer to
  consumer. There is no bidirectional flow on a single connection.
- **Acyclic** — there are no loops. A node cannot, directly or indirectly,
  feed data back to itself. The engine rejects graphs with cycles before any
  media is touched.
- **Graph** — nodes are the processing stages; edges are the connections
  between them.

MediaMolder graphs have two equivalent representations:

| Perspective | What you see |
|---|---|
| **GUI canvas** | Boxes (nodes) wired together with cables (edges) on a drag-and-drop canvas. Saving the canvas writes the JSON; loading the JSON reconstructs the canvas. |
| **JSON job file** | A structured text file with `inputs`, `graph`, and `outputs` sections, passed to `mediamolder run` on the command line. |

Both representations are identical in expressive power. The GUI generates
the JSON; the JSON is what the runtime actually executes.

---

## 2. The JSON job file

A job file is a single JSON document with three top-level sections:

```json
{
  "schema_version": "1.0",
  "inputs":  [ ... ],
  "graph":   { "nodes": [...], "edges": [...] },
  "outputs": [ ... ]
}
```

- **`inputs`** — the media files, devices, or network streams to read. Each
  entry becomes a *source node* in the graph automatically.
- **`graph`** — the processing topology: an explicit list of intermediate
  nodes and the edges that connect everything.
- **`outputs`** — the files, streams, or devices to write. Each entry becomes
  a *sink node* in the graph automatically.

Inputs and outputs are declared separately because they carry extra
configuration (URLs, codecs, muxer settings) that doesn't belong to the graph
topology itself. Internally they participate in the graph exactly like any
other node.

To run a job:

```sh
mediamolder run job.json
```

To validate the job without touching any media:

```sh
mediamolder validate job.json
```

To pretty-print the fully-resolved config (useful for debugging):

```sh
mediamolder inspect job.json
```

---

## 3. Nodes

A node is a single processing stage. Every node has:

- A unique **`id`** string — used by edges to reference it
- A **type** that determines what it does

In the GUI, a node is a **box** on the canvas: the `id` and type appear in the
title bar, parameters are edited in the properties panel on the right, and the
small connector dots on the left and right edges of the box are its input and
output **pads**.

---

### 3.1 Source nodes

A source node reads a media file, device, or network stream and produces
decoded media for downstream nodes. It combines support for reading different
container file formats (e.g. MP4, MKV), demuxing / extracting streams and decoding 
(turning compressed packets into raw data) into one stage. What "decoded" means depends on stream type:

- **Video** — raw uncompressed frames (e.g. YUV 4:2:0 pixels)
- **Audio** — raw uncompressed PCM samples (e.g. `fltp` stereo at 48 kHz)
- **Subtitle** — decoded text or bitmap events (ASS, SRT, PGS, …)
- **Data** — opaque packets (timecodes, SCTE-35, metadata tracks) passed through unchanged

Source nodes are **implicitly created** from the `inputs` array — you do not
write them in `graph.nodes`. Each input's `id` becomes the source node's id.

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

This creates a source node `"src"` with two output streams: `"src:v:0"` and
`"src:a:0"`. The `track` number is the index *within that type* — useful when
a file has multiple audio tracks (`"src:a:0"`, `"src:a:1"`, …).

In the GUI, source nodes appear at the left edge of the canvas with no input
pads, only output pads — one per declared stream.

There is also a **`filter_source`** node type for synthetic generators that
produce media without reading a file: `color`, `testsrc2`, `sine`, `anullsrc`,
`movie`, `amovie`. These are fully explicit `graph.nodes` entries.

---

### 3.2 Filter nodes

A filter node transforms, analyses, combines, or generates frames. It wraps a
single **libavfilter** filter — the same filters available in FFmpeg's `-vf`,
`-af`, and `-filter_complex` options — configured via a structured `params`
object instead of a string.

```json
{
  "id": "resize",
  "type": "filter",
  "filter": "scale",
  "params": { "w": "1280", "h": "720" }
}
```

Filter nodes can:
- **Transform** — resolution (`scale`), frame rate (`fps`), pixel format
  (`format`), volume (`volume`), sample rate (`aresample`), …
- **Generate** — constant video (`color`), silence (`aevalsrc`), test
  signals (`testsrc`), …
- **Combine** — merge streams (`overlay`, `amerge`, `vstack`, …)
- **Split** — duplicate a stream to multiple consumers (`split`, `asplit`)
- **Analyse** — measure loudness (`ebur128`), detect scene changes (`scdet`), …
- **Pass through** — `null` (video) and `anull` (audio) are no-ops, useful
  for instrumentation or timestamp manipulation

All parameter values are strings (libavfilter accepts them as strings
internally). Use `mediamolder list-filters` to see the full catalogue.

> **Important:** MediaMolder treats each `filter` node as its own independent
> libavfilter graph. libavfilter's `auto_scale` / `auto_resample` converters
> work *inside* a single node but do **not** bridge across nodes. If the
> producer's output format doesn't match what the next node accepts, you must
> insert an explicit transform node between them (see §5.2).

---

### 3.3 Encoder nodes

An encoder node compresses raw frames into a specific coded format using a
**libavcodec** encoder. It always sits between filter nodes and the sink.

**Implicit** — the most common case. Set `codec_video` or `codec_audio` on
an output and MediaMolder synthesises the encoder node automatically:

```json
"outputs": [{
  "id": "out",
  "url": "output.mp4",
  "codec_video": "libx264",
  "codec_audio": "aac"
}]
```

**Explicit** — when you need precise control over encoder parameters:

```json
{
  "id": "enc",
  "type": "encoder",
  "params": { "codec": "libx264", "preset": "slow", "crf": "18" }
}
```

All codec parameters — preset, rate-control mode, CRF/CQ, bitrate, keyframe
interval, pixel format, etc. — live in `params`.

Use `mediamolder list-codecs` to see available encoders.

---

### 3.4 Sink nodes

A sink node muxes encoded streams into an output container (MP4, MKV, TS,
HLS, RTMP, …) and writes or streams the result. It is the last stage in every
branch of the graph.

Like source nodes, sink nodes are **implicitly created** from the `outputs`
array — one per entry. Each output's `id` is the sink node's id.

```json
"outputs": [{
  "id": "out",
  "url": "result.mp4",
  "codec_video": "libx264",
  "codec_audio": "aac"
}]
```

Edges that write to this sink use `"out:v"` (video) and `"out:a"` (audio) as
the `to` endpoint. In the GUI, sinks appear at the right edge of the canvas
with only input pads, no output pads.

There is also a **`filter_sink`** type for analyser-only pipelines where you
want to process frames but not write a file. Use `nullsink` (video) or
`anullsink` (audio) as the filter inside a `filter_sink` node to discard
frames at the end of a branch.

---

### 3.5 Copy nodes

A copy node is a **packet pass-through** — it forwards compressed packets
directly from a demuxer to a muxer with no decode and no encode. This is the
JSON equivalent of FFmpeg's `-c copy`.

```json
{ "id": "passthru", "type": "copy" }
```

Constraints:
- The producer must be a `source` (demuxer) node — once any filter or encoder
  is upstream, decoded frames are in play and copy is no longer possible.
- The destination container's muxer must accept the source codec.

---

### 3.6 Go processor nodes

A `go_processor` node runs a Go function as a first-class stage in the graph.
It consumes decoded frames, optionally modifies or analyses them, and produces
frames for downstream nodes. Useful for AI inference, custom analysis, or
per-frame metadata injection that doesn't fit a libavfilter.

```json
{
  "id": "detect",
  "type": "go_processor",
  "processor": "yolov8_detector",
  "params": { "model": "yolov8n.onnx", "confidence": "0.5" }
}
```

See [go-processor-nodes.md](go-processor-nodes.md) for the registration API.

---

### 3.7 Other node types

| Type | Purpose |
|---|---|
| `metadata_reader` | Read container-level metadata and chapters into the graph |
| `metadata_writer` | Write container-level metadata and chapters to an output |

---

## 4. Edges

An edge is a connection from one node's output to another node's input. Each
edge:

- Has a **`from`** endpoint (producer)
- Has a **`to`** endpoint (consumer)
- Carries exactly **one stream type** (`"video"`, `"audio"`, `"subtitle"`,
  `"data"`, or `"events"`)

```json
{ "from": "src:v:0", "to": "resize", "type": "video" }
```

The engine validates every edge at build time. Connecting a video output port
to an audio input port is a fatal error caught before any file is opened.

In the GUI, an edge is the **wire** drawn between two pads. The wire's colour
indicates its type (video/audio/subtitle/data/events). The canvas prevents you
from dropping a wire onto an incompatible pad.

---

### 4.1 Stream types

| `type` | Carries | Notes |
|---|---|---|
| `video` | Decoded `AVFrame` (raw pixel data) | Uncompressed between source, filters, and encoder |
| `audio` | Decoded `AVFrame` (raw PCM samples) | Uncompressed between source, filters, and encoder |
| `subtitle` | `AVSubtitle` events (text or bitmap) | Often passed through without decode |
| `data` | Opaque packets (timecodes, KLV, SCTE-35, …) | Passed through unchanged |
| `events` | Structured metadata objects emitted by `go_processor` nodes | **No libav\* involvement.** Rendered as a pink dashed wire in the GUI. Routes processor output (scene cuts, detections, …) to a `metadata_file_writer` sink. Does not carry video frames; never touches any libav\* library. |

> **Per-frame metadata** is not a separate edge type. It rides inside
> `AVFrame->metadata` and propagates automatically over `video` and `audio`
> edges. The `metadata` and `ametadata` filters manipulate it in place as
> ordinary `filter` nodes.

Cross-media filters (the `avf_*` family: `showwavespic`, `showspectrumpic`,
`avectorscope`, …) consume one type and produce another; their output type is
declared via `output_media_type` in the node catalog.

---

### 4.2 Port reference syntax

An endpoint string identifies both the node and which of its ports to use.

| Format | Meaning | Example |
|---|---|---|
| `"id"` | The node's single default port | `"resize"` |
| `"id:port"` | A named port on the node | `"overlay:overlay"` |
| `"id:type:track"` | A typed track from a source node | `"src:v:0"`, `"src:a:1"` |
| `"id:v"` or `"id:a"` | The video or audio input of a sink | `"out:v"`, `"out:a"` |

The **type letter** in source references: `v` (video), `a` (audio), `s`
(subtitle), `d` (data). The **track number** is the zero-based index among
streams of that type in the input's `streams` array.

Examples:
- `"src:v:0"` — video track 0 from source `src`
- `"src:a:1"` — audio track 1 from source `src`
- `"sp:out0"` — first output port of a `split` node
- `"ov:overlay"` — the `overlay` input pad of an `overlay` filter node
- `"out:v"` — the video input of sink `out`

In the GUI the same pad is the small connector dot on the node's edge; you can
hover it to see its name and accepted type.

---

### 4.3 Multi-port nodes

Some filters accept more than one input or produce more than one output. The
port name in the edge selects the specific pad.

**Multi-input: `overlay`** composites two video streams — the first is the
background (default), the second is the foreground.

```
[background] ──────────────► overlay ──► [result]
[foreground] ──► overlay:overlay ──►
```

```json
{ "from": "bg:v:0",  "to": "ov",         "type": "video" },
{ "from": "fg:v:0",  "to": "ov:overlay", "type": "video" }
```

**Multi-output: `split`** duplicates one stream to multiple consumers.

```
[source] ──► split ──► split:out0 ──► [encoder HD]
                  └──► split:out1 ──► [encoder SD]
```

```json
{ "from": "src:v:0", "to":  "sp",      "type": "video" },
{ "from": "sp:out0", "to":  "enc_hd",  "type": "video" },
{ "from": "sp:out1", "to":  "enc_sd",  "type": "video" }
```

When a node has exactly one input or one output, use the bare node id or
`"default"` as the port name — both mean the same thing.

---

## 5. Compatibility — when direct wiring works

### 5.1 The three rules

Two nodes can be wired with a single edge (no intermediate filter) when **all**
of the following hold:

1. **The edge `type` matches both ends** — a `video` edge cannot connect to an
   audio pad.
2. **The producer's output frame format is accepted by the consumer** —
   - *Video:* pixel format, width, height, color range, color space, SAR.
   - *Audio:* sample format, sample rate, channel layout.
3. **The path ends correctly** — every branch from a `source` to a sink must
   terminate in *exactly one* of: a `copy` node, an `encoder` node, or a
   `filter_sink`.

The most common direct-wire patterns:

| Pattern | Why it works |
|---|---|
| `source → copy → output` | Packets pass through; no format negotiation needed |
| `source → encoder → output` | Decoder picks a default format the encoder accepts (e.g. H.264 → libx264 both use `yuv420p`) |
| `source → filter → encoder` | libavfilter handles format negotiation *inside* the filter node |
| `filter_source → encoder` | Synthetic sources (`testsrc2`, `sine`) default to formats encoders accept |

---

### 5.2 When a transform filter is required

When the producer's format is **not** in the consumer's accept list, insert an
explicit transform node. libavfilter's auto-converter works only *inside* a
single filter node — it does not bridge across nodes.

**Video transforms:**

| Need | Filter to insert | FFmpeg CLI form |
|---|---|---|
| Pixel format mismatch (e.g. `rgba` → libx264) | `format=pix_fmts=yuv420p` | `-vf format=` |
| Width / height mismatch | `scale=W:H` | `-vf scale=` |
| Frame rate mismatch (encoder requires CFR) | `fps=N` | `-vf fps=N` |
| SAR mismatch | `setsar=1` | `-vf setsar=1` |
| CPU ↔ GPU memory (CUDA, VAAPI) | `hwupload` / `hwdownload` | `-vf hwupload` |
| Color space / range conversion | `colorspace`, `zscale` | `-vf zscale=` |

**Audio transforms:**

| Need | Filter to insert | FFmpeg CLI form |
|---|---|---|
| Sample format mismatch (`fltp` → AAC `s16`) | `aformat=sample_fmts=s16` | `-af aformat=` |
| Sample rate mismatch | `aresample=48000` | `-af aresample=` |
| Channel layout mismatch (5.1 → stereo) | `aformat=channel_layouts=stereo` or `pan=` | `-ac 2` |

**Subtitle:** Usually wired `source → copy → output`, or burned into video via
the `subtitles=` / `ass=` *video* filter (it takes a video edge in and out,
reading the subtitle file out-of-band).

---

### 5.3 Stream-copy vs. transcode

| Goal | Graph | CLI |
|---|---|---|
| Remux only (change container) | `source → copy → output` | `-c copy` |
| Re-encode video, copy audio | `source(v) → filter → encoder → out`<br>`source(a) → copy → out` | `-c:v libx264 -c:a copy` |
| Trim a stream-copied file | `source → copy → output` + output `options.ss` / `options.t` | `-ss N -to M -c copy` |

**Constraint on `copy`:** once any filter, go_processor, or encoder sits
upstream of a copy node, the original packet is gone — only decoded frames
exist. The path can no longer be stream-copied.

---

## 6. Sources and sinks in detail

### Sources (zero inbound edges)

| Node type | Origin |
|---|---|
| `source` (implicit) | A demuxer — `inputs[i].url`. Referenced via `"in0:v:0"`, `"in0:a:0"`. |
| `filter_source` | A libavfilter source filter: `color`, `testsrc2`, `sine`, `anullsrc`, `movie`, `amovie`. |

A graph is valid with **zero `inputs`** if it contains at least one
`filter_source` node.

### Sinks (zero outbound edges)

| Node type | Destination |
|---|---|
| `output` (implicit) | A muxer — `outputs[i].url`. Referenced via `"out0:v"`, `"out0:a"`. |
| `filter_sink` | A libavfilter sink filter: `nullsink`, `anullsink`. Frames are consumed and discarded — for analyser-only pipelines. |

A graph is valid with **zero `outputs`** if it contains at least one
`filter_sink` node (e.g. a loudness analysis job).

### Pass-through filters vs. terminal sinks

This distinction matters when building analyser pipelines:

- `null` (video) and `anull` (audio) — **pass-through filters** (one in, one
  out, no-op). Used for instrumentation or to isolate timestamps. CLI: `-vf null`.
- `nullsink` and `anullsink` — **terminal sinks** (one in, zero out). Drain
  and discard frames at the end of a branch. Used inside a `filter_sink` node.
  CLI: `-f null /dev/null`.

---

## 7. FFmpeg CLI → JSON mapping

FFmpeg's command line is **positional and stateful** — flags before `-i`
configure the next input, flags after configure the next output, and `-map`
selects streams. MediaMolder's JSON is **declarative** — these commands or parameters
are explicitly declared in the relevant JSON objects in `inputs[]`, `graph.nodes[]`,
 `graph.edges[]`, and `outputs[]`.

### Cheat-sheet

| CLI fragment | JSON home |
|---|---|
| `-i path` (Nth occurrence) | `inputs[N]` |
| Demuxer flags before `-i` (`-f`, `-ss`, `-r`) | `inputs[N].format`, `.options`, `.start_time`, … |
| `-map 0:v:0` | An edge `from: "in0:v:0"` |
| `-map 1:a` | One edge per audio stream of `in1` |
| `-vf chain` (per output) | A linear chain of `filter` nodes wired to `out:v` |
| `-af chain` (per output) | A linear chain of `filter` nodes wired to `out:a` |
| `-filter_complex "spec"` | A general subgraph of `filter` / `filter_source` / `filter_sink` nodes |
| `-c:v libx264` `-b:v 6M` | An `encoder` node with `codec: "libx264"`, `params: {b: "6M"}` |
| `-c copy` | A `copy` node |
| `-disposition:s:0 default` | `outputs[i].streams[j].disposition: "default"` |
| Output `path` (final positional) | `outputs[i].url` |
| Muxer flags (before the output path) | `outputs[i].format`, `.options` |

### Working example

**CLI:**
```sh
ffmpeg -i in.mkv \
  -filter_complex "[0:v]scale=1280:720,format=yuv420p[v]; [0:a]aresample=48000[a]" \
  -map "[v]" -c:v libx264 -b:v 4M \
  -map "[a]" -c:a aac -b:a 128k \
  out.mp4
```

**JSON equivalent:**
```json
{
  "schema_version": "1.0",
  "inputs": [{ "id": "in0", "url": "in.mkv" }],
  "graph": {
    "nodes": [
      { "id": "scale", "type": "filter",  "filter": "scale",    "params": { "w": "1280", "h": "720" } },
      { "id": "fmt",   "type": "filter",  "filter": "format",   "params": { "pix_fmts": "yuv420p" } },
      { "id": "ars",   "type": "filter",  "filter": "aresample","params": { "_pos0": "48000" } },
      { "id": "x264",  "type": "encoder", "codec":  "libx264",  "params": { "b": "4M" } },
      { "id": "aac",   "type": "encoder", "codec":  "aac",      "params": { "b": "128k" } }
    ],
    "edges": [
      { "from": "in0:v:0", "to": "scale", "type": "video" },
      { "from": "scale",   "to": "fmt",   "type": "video" },
      { "from": "fmt",     "to": "x264",  "type": "video" },
      { "from": "x264",    "to": "out:v", "type": "video" },
      { "from": "in0:a:0", "to": "ars",   "type": "audio" },
      { "from": "ars",     "to": "aac",   "type": "audio" },
      { "from": "aac",     "to": "out:a", "type": "audio" }
    ]
  },
  "outputs": [{ "id": "out", "url": "out.mp4", "format": "mp4" }]
}
```

What changed from CLI to JSON:

- `-i in.mkv` → `inputs[0]`.
- The CLI's named pads (`[v]`, `[a]`) are not needed — each JSON node has a
  unique `id` that edges reference directly.
- `-c:v` and `-c:a` become two explicit `encoder` nodes; their position in the
  chain is encoded by the edges, not by argument order.
- `out.mp4` becomes `outputs[0].url`.
- `format=yuv420p` is **explicit** in the JSON because each MediaMolder
  `filter` node is its own libavfilter graph. In the FFmpeg CLI the entire
  `-filter_complex` string is one libavfilter graph, so `auto_scale` is
  inserted automatically. In the JSON, you must insert it yourself.

---

## 8. Data flow and execution

When MediaMolder runs a graph, each node is assigned its own **goroutine
group**. Nodes communicate through **Go channels**: a producer writes frames
to its output channel; the consumer reads from it.

```
[Source goroutine] ──channel──► [Filter goroutine] ──channel──► [Encoder goroutine] ──channel──► [Sink goroutine]
```

Key properties:

**Back-pressure is automatic.** If a downstream node falls behind, its input
channel fills up. When full, the upstream node blocks — slow consumers
propagate naturally backward through the graph without any throttling code.

**Stages run in parallel.** A four-stage chain (source → filter → encoder →
sink) has all four stages active simultaneously on different frames. While the
encoder compresses frame N, the filter transforms frame N+1 and the source
decodes frame N+2.

**Dedicated output lanes.** Each sink has its own goroutine group. A slow
network upload on a live-stream output does not stall an archive write. Each
output path has independent channel buffering.

**Graceful shutdown.** Ctrl-C (or the Stop button in the GUI) sends a context
cancellation to all goroutines. Each stage finishes its current frame,
flushes buffered frames downstream, then exits. Output files are finalised
cleanly — no truncation.

**Channel buffer size.** Each inter-node channel buffers up to 8 frames by
default, smoothing over transient speed differences between stages.

---

## 8a. Performance monitoring

MediaMolder tracks per-node performance continuously while a pipeline is running. The data is available via the `/perf` HTTP endpoint, the `/perf/stream` SSE endpoint (used by the GUI canvas overlay), and the `mediamolder perf` CLI subcommand.

### Per-node snapshot

Every 500 ms each node records a `NodePerfSnapshot` containing:

| Field | Description |
|---|---|
| `ActiveFrac` | Fraction of time spent doing codec or I/O work (0.0–1.0) |
| `IdleFrac` | Fraction of time waiting for the next input frame |
| `StalledFrac` | Fraction of time blocked because the output channel is full |
| `FPS` | Actual output frame rate over a sliding window |
| `FPSTarget` | Target frame rate set via `fps_target` on the node |
| `FPSDeficit` | `FPSTarget − FPS`; positive means the node is falling behind |
| `ThreadsConfigured` | Thread count granted by libavcodec at codec open time |
| `ThreadsBusy` | Live count of threads actively working (−1 if unavailable) |
| `FrameLatencyMean` | Mean time from input-frame receive to output-frame emit |

### Diagnosing bottlenecks

| Pattern | Diagnosis |
|---|---|
| High `ActiveFrac` + high `FPSDeficit` + `ThreadsBusy ≈ ThreadsConfigured` | Thread-limited bottleneck — add threads or use a faster preset |
| High `ActiveFrac` + high `FPSDeficit` + `ThreadsBusy` much less than `ThreadsConfigured` | Sequential bottleneck — more threads won't help; switch preset |
| High `StalledFrac` | Downstream is the bottleneck — look at the next node |
| High `IdleFrac` | Upstream is the bottleneck — look at the previous node |

### `fps_target`

Set `fps_target` on a source or encoder node (Inspector → **fps_target** field, or `"fps_target": 30` in JSON) to define the node's throughput target. The performance overlay uses this to compute and display `FPSDeficit`.

### Real-time mode

When `global_options.realtime` is `true` (or `--realtime` is passed to `mediamolder run`, or the GUI **Real-time** checkbox is checked), an adaptive control loop activates. It reads per-node snapshots every 500 ms and:

1. **Increases encoder thread counts** (graceful codec restart) when a node is thread-limited and within the global CPU thread budget.
2. **Enables frame-drop mode** (1 in 4 frames dropped at the source) once the thread budget is exhausted and deficit exceeds 1 fps.
3. **Emits `RealTimeViolation` events** when convergence is not possible (sequential bottleneck, hardware limit reached, etc.).

Hardware-accelerated encoder nodes are exempt from the CPU thread budget.

See [docs/using-mediamolder.md §5.11](using-mediamolder.md#511-real-time-mode) for full usage details.

---

## 9. Graph lifecycle

Every graph run follows a four-state lifecycle:

```
NULL ──► READY ──► PAUSED ──► PLAYING
  ▲        │         │           │
  └────────┴─────────┴───────────┘
              (any state → NULL)
```

| State | What exists | What runs |
|---|---|---|
| `NULL` | Only the Go struct in memory | Nothing — no libav* contexts open |
| `READY` | Inputs probed, codecs resolved, filter graphs validated, all libav* contexts allocated | Nothing — resources are open but no data moves |
| `PAUSED` | Everything in `READY`, plus the first frame read into each stage | Stages have pre-rolled data but sinks are not writing |
| `PLAYING` | Fully active | All stages running; sinks writing output |

**Typical path:** `NULL → READY → PAUSED → PLAYING → NULL`

`mediamolder run` takes the graph from `NULL` all the way to `PLAYING`
automatically, then back to `NULL` when the job finishes. The GUI shows the
current state in the toolbar and lets you Pause, Resume, or Stop while a job
is running. The state machine is exposed through the Go library API for
applications that need finer control (seek-then-inspect, pre-roll, etc.).

**Error handling.** If any stage returns an error in `PLAYING`, the engine
transitions to `NULL`, draining in-flight frames. The exit code reflects
whether the error was fatal.

---

## 10. Validation

Before any media I/O occurs, the engine validates the graph. Validation runs
in two phases, both available via `mediamolder validate`:

### Phase A — Static analysis (no file I/O)

Runs entirely from the JSON config; no files are opened. Checks:

- Duplicate node IDs
- Unknown edge references (node id that doesn't exist)
- Self-loops and cycles (detected with Kahn's algorithm)
- Edge type mismatches (video output → audio input)
- Codec/container incompatibilities (e.g. VP9 in an MP4 container)
- Missing required fields (`id`, `url`, codec, …)
- Filter arity errors (a filter requiring two inputs receiving only one)

Phase A alone is suitable for CI pre-checks that don't have access to the
actual media files:

```sh
mediamolder validate --no-probe job.json
```

### Phase B — Probe-assisted analysis

Opens each input with `avformat_find_stream_info`. Catches runtime issues
that only the actual media can reveal:

- Interlaced input without a deinterlace filter (`yadif` needed)
- HDR input to an SDR output without tone-mapping (`zscale`+`tonemap` needed)
- VFR input to a fixed-rate encoder (`fps` filter needed)
- Pixel format mismatch (e.g. 10-bit source to an 8-bit-only encoder)
- Sample rate or channel layout mismatch

```sh
mediamolder validate job.json          # Phase A + B
mediamolder validate --json job.json   # JSON report output
mediamolder validate --strict job.json # Exit 1 on WARNINGs too
```

In the GUI, validation runs automatically when you click **Validate** in the
toolbar. Issues appear as inline annotations on the affected nodes and edges,
with suggested fixes in the right panel.

---

## 11. Dynamic reconfiguration

MediaMolder supports changing the graph *while it is running* (`PLAYING`
state).

**Parameter changes** (e.g. the `drawtext` string, `volume` level) are applied
between frames — the engine drains the current frame, calls `av_opt_set`, and
resumes with no frames dropped.

**Structural changes** (add/remove a node) require a brief quiesce: the
affected sub-graph pauses, in-flight frames drain, the change is applied, and
data flow resumes. The caller receives an acknowledgement when the change is
live.

**Codec changes mid-stream are not supported.** Remove the output and add a
new one with the desired codec. This starts a new output file or segment.

**Adding an output at runtime** is supported via `Pipeline.AddOutput()`. The
new output taps into the existing decoded frame stream; no seek or restart is
required.

---

## 12. Full worked example

The job below takes a 4K HDR video, tone-maps it to SDR, scales it to 1080p,
deinterlaces it, overlays a watermark, then writes two simultaneous outputs —
an H.264 archive and an HLS live stream.

```
[src:v:0] ──► tonemap ──► scale ──► yadif ──► overlay ──► split ──► enc_h264 ──► [archive:v]
                                                 ▲              └──────────────────► [hls:v]
[logo:v:0] ──────────────────────────────────────┘

[src:a:0] ────────────────────────────────────────────────────────────────────────► [archive:a]
                                                                                └──► [hls:a]
```

**Node roles:**

| Node | Kind | Role |
|---|---|---|
| `src` | Source | Demux + decode the 4K HDR input |
| `logo` | Source | Demux + decode the PNG watermark |
| `tonemap` | Filter | `zscale` + `tonemap` — HDR → SDR conversion |
| `scale` | Filter | Resize 4K → 1920×1080 |
| `yadif` | Filter | Deinterlace |
| `overlay` | Filter | Composite the watermark over the video |
| `split` | Filter | Duplicate the video stream for two outputs |
| `enc_h264` | Encoder | Compress to H.264 for the archive |
| `archive` | Sink | Mux to MKV file |
| `hls` | Sink | Mux to HLS playlist |

**Job JSON (abbreviated):**

```json
{
  "schema_version": "1.0",
  "inputs": [
    { "id": "src",  "url": "input_4k_hdr.mp4" },
    { "id": "logo", "url": "watermark.png" }
  ],
  "graph": {
    "nodes": [
      { "id": "tonemap", "type": "filter",  "filter": "zscale",  "params": { "t": "linear" } },
      { "id": "scale",   "type": "filter",  "filter": "scale",   "params": { "w": "1920", "h": "1080" } },
      { "id": "yadif",   "type": "filter",  "filter": "yadif",   "params": {} },
      { "id": "overlay", "type": "filter",  "filter": "overlay", "params": { "x": "W-w-10", "y": "H-h-10" } },
      { "id": "split",   "type": "filter",  "filter": "split",   "params": { "outputs": "2" } },
      { "id": "enc_h264","type": "encoder", "codec":  "libx264", "params": { "preset": "slow", "crf": "18" } }
    ],
    "edges": [
      { "from": "src:v:0",    "to": "tonemap",          "type": "video" },
      { "from": "tonemap",    "to": "scale",             "type": "video" },
      { "from": "scale",      "to": "yadif",             "type": "video" },
      { "from": "yadif",      "to": "overlay",           "type": "video" },
      { "from": "logo:v:0",   "to": "overlay:overlay",  "type": "video" },
      { "from": "overlay",    "to": "split",             "type": "video" },
      { "from": "split:out0", "to": "enc_h264",          "type": "video" },
      { "from": "enc_h264",   "to": "archive:v",         "type": "video" },
      { "from": "split:out1", "to": "hls:v",             "type": "video" },
      { "from": "src:a:0",    "to": "archive:a",         "type": "audio" },
      { "from": "src:a:0",    "to": "hls:a",             "type": "audio" }
    ]
  },
  "outputs": [
    { "id": "archive", "url": "archive.mkv" },
    { "id": "hls",     "url": "stream.m3u8", "format": "hls" }
  ]
}
```

**What happens at runtime:**

1. **Build** — `graph.Build()` resolves node IDs, validates edge types, runs
   topological sort.
2. **READY** — libavformat opens both inputs; decoder and filter contexts are
   allocated; HLS and MKV muxers are opened.
3. **PLAYING** — `src` decodes frames and pushes to `tonemap`. Each stage
   processes in its own goroutine. `split` writes the same frame into two
   channels simultaneously — one for `enc_h264` → archive, one directly to
   HLS. A momentary HLS network stall does not block the archive path.
4. **End of stream** — `src` exhausts the input and sends EOS. Each stage
   drains buffered frames in order. Muxers write trailers. Both output files
   are finalised cleanly.
5. **NULL** — All libav* contexts freed, process exits with code 0.

To run this job from the CLI:
```sh
mediamolder run watermark_transcode.json
```

To validate it first:
```sh
mediamolder validate watermark_transcode.json
```

---

## 13. Diagnostic checklist

When a job fails with *"impossible to find a valid input format"*, *"output
pixel format not in encoder accept list"*, or similar:

1. **Check the edge `type`.** Type mismatch is caught at build time — fix by
   correcting `type` or inserting a cross-media filter.
2. **Check the encoder's accepted formats.** `ffmpeg -h encoder=libx264` shows
   `Supported pixel formats: …`. If the upstream format isn't listed, insert
   `format=` (video) or `aformat=` (audio).
3. **Check sample rate / channel layout for audio encoders.** AAC accepts many
   rates but an unusual source may need `aresample=`.
4. **Check that `copy` has a clean source-to-output path** with nothing decoded
   in front of it. A filter or processor upstream means decoded frames are in
   play — the copy path is broken.
5. **Check that every source has a sink and every sink has a source.** Orphan
   nodes are reported by `graph.Compile` as warnings.
6. **Run `mediamolder validate` before `run`.** Phase A catches structural
   errors instantly. Phase B catches format mismatches that require opening the
   input files.

---

## 14. Cross-references

- [using-mediamolder.md](using-mediamolder.md) — full CLI and GUI user guide
- [json-config-reference.md](json-config-reference.md) — complete JSON schema reference
- [graph-validation-design.md](architecture/graph_validation_design.md) — validation issue taxonomy
- [graph-compilation.md](architecture/graph-compilation.md) — build / compile / execute internals
- [ffmpeg-migration-guide.md](ffmpeg-migration-guide.md) — full CLI → JSON migration patterns
- [go-processor-nodes.md](go-processor-nodes.md) — Go processor API
- [hardware-acceleration.md](hardware-acceleration.md) — `hwupload` / `hwdownload` and HW encoder setup
