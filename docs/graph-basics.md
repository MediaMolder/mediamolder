# Graph Basics — Nodes, Edges, Sources, Sinks, and the FFmpeg CLI Mapping

This document is a tutorial introduction to the MediaMolder processing graph.
It defines the building blocks (nodes, edges, pads, sources, sinks),
explains **when two nodes can be connected with a single edge** and **when a
transform filter has to sit between them**, and maps the same concepts back
to the FFmpeg CLI: *"when can `-c copy` work, when do I need a
`-filter_complex` chain, and what does the order of `-i`, `-map`, `-c:v`,
`-vf` on the command line correspond to in the JSON graph?"*

---

## 0. Definitions

These terms are used consistently throughout the document.

### Graph structure

MediaMolder graphs have two equivalent presentations: the **GUI canvas** (a
visual editor where boxes are wired together with cables) and the **JSON
config** (the on-disk representation the runtime actually consumes). Each
term below is defined from both angles.

- **Graph**
  - *GUI:* the entire canvas — every box, wire, and label visible in the
    editor. Saving the canvas writes the JSON; loading the JSON reconstructs
    the canvas.
  - *JSON:* the whole `graph` object in the config: a directed acyclic graph
    (DAG) of `nodes` connected by `edges`, compiled by `graph.Build` /
    `graph.Compile` and executed by the pipeline runtime.

- **Node**
  - *GUI:* a box on the canvas with a title bar (the node's `id` / `type`),
    one or more input pads on the left, one or more output pads on the
    right, and a properties panel for its parameters. Sources have no input
    pads, sinks have no output pads.
  - *JSON:* one entry in `graph.nodes[]` (or an *implicit* node generated
    from `inputs[]` / `outputs[]` for `source` / `output`). Each node has a
    `type` (`source`, `filter`, `filter_source`, `filter_sink`, `encoder`,
    `copy`, `output`, `go_processor`, `metadata_reader`, `metadata_writer`)
    and a unique `id` referenced by edges.

- **Edge**
  - *GUI:* the line (wire / pipe) drawn between an output pad on one node
    and an input pad on another. Its colour or style indicates the edge
    `type` (video / audio / subtitle / data).
  - *JSON:* one entry in `graph.edges[]`, declared as
    `{ from, to, type }`. The `from` / `to` strings name node IDs (with
    optional pad selectors like `in0:v:0` for source nodes and `out0:v` for
    output nodes). The `type` field labels what kind of media flows along
    the wire (see §1).

- **Pad**
  - *GUI:* the small connector dot or socket on the edge of a node where
    wires attach. Input pads are typically on the left of the box, output
    pads on the right. Hovering a pad shows its name and accepted media
    type; the canvas refuses to drop a video wire onto an audio pad.
  - *JSON:* a typed input or output port on a node, addressed via the pad
    selector suffix on `from` / `to`. Filter nodes can have multiple pads
    (e.g. `overlay` has two video input pads — `[0]` is the background,
    `[1]` is the foreground); selectors target a specific pad. Most
    non-filter nodes have exactly one input pad and one output pad, so the
    selector is omitted.

### Roles a node plays on a given edge

- **Producer** — the node at the `from` end of an edge. It writes frames (or
  packets, for `copy`) onto the edge.
- **Consumer** — the node at the `to` end of an edge. It reads what the
  producer wrote. The same node is a consumer on its inbound edges and a
  producer on its outbound edges.

### Roles a node plays in the whole graph

- **Source** — a node with **zero inbound edges**. Sources originate media:
  - `source` (implicit, from `inputs[i]`) opens a demuxer.
  - `filter_source` runs a libavfilter source filter (`color`, `testsrc2`,
    `sine`, `anullsrc`, `movie`, `amovie`).
- **Sink** — a node with **zero outbound edges**. Sinks consume media:
  - `output` (implicit, from `outputs[i]`) opens a muxer.
  - `filter_sink` runs a libavfilter sink filter (`nullsink`, `anullsink`).
- **Interior node** — anything that has both inbound and outbound edges:
  `filter`, `encoder`, `copy`, `go_processor`, `metadata_reader`,
  `metadata_writer`.

### Node types — what they do

- **`filter`** — wraps one libavfilter (e.g. `scale`, `format`, `overlay`,
  `metadata`). Each `filter` node is its own internal `AVFilterGraph` with
  buffersrc inputs and buffersink outputs; libavfilter's `auto_scale` /
  `auto_resample` inserts converters *inside* one node but **never across**
  nodes. Operates on decoded frames.
- **`filter_source` / `filter_sink`** — degenerate `filter` nodes with no
  inbound or no outbound edges respectively. Used for synthetic generators
  (`testsrc2`, `sine`) and analyser-only pipelines (`ebur128 → anullsink`).
- **`encoder`** — opens an `AVCodecContext` and turns decoded frames into
  packets that the muxer writes. Always sits immediately upstream of an
  `output` pad.
- **`copy`** — a packet pass-through. Forwards demuxer packets to the muxer
  with **no decode and no encode**. Must sit directly between a `source`
  and an `output` (one inbound edge, one outbound edge, no filter in
  between). The CLI equivalent is `-c copy`.
- **`go_processor`** — a Go function that consumes and produces decoded
  frames. Useful for AI inference, custom analysis, or per-frame metadata
  injection that doesn't fit a libavfilter. See
  [go-processor-nodes.md](go-processor-nodes.md).
- **`metadata_reader` / `metadata_writer`** — graph-level container/stream
  metadata + chapter IO (Wave 2 #11). Distinct from per-frame
  `AVFrame->metadata`, which rides inline on `video` and `audio` edges.

### Format vs. type

- **Edge type** — the coarse compatibility label on the edge: `video`,
  `audio`, `subtitle`, `data`. Enforced at graph build time. Mismatch is
  always rejected.
- **Frame format** — the *fine-grained* shape of the data flowing on an
  edge: pixel format / width / height / SAR (video); sample format / sample
  rate / channel layout (audio). The runtime negotiates this via libavfilter's
  format negotiation *inside* a single node, but **between** nodes the
  producer's output format must already be in the consumer's accept list —
  otherwise the user must insert a transform node (`format`, `aformat`,
  `scale`, `aresample`, …).

### Stream-copy vs. transcode

- **Stream-copy** — packet pass-through via a `copy` node. No decoder, no
  encoder. The destination container must accept the source codec.
- **Transcode** — `source → [filter…] → encoder → output`. Decoded frames
  are processed and re-encoded. Pixel/sample format conversion happens here
  (or via explicit `format` / `aformat` nodes when crossing node boundaries).

### Transform filter

A **transform filter** is any `filter` node whose sole job is to convert
a frame format that an upstream node produced into a format the downstream
node accepts. Common examples: `format=pix_fmts=yuv420p`, `aformat=…`,
`scale`, `aresample`, `fps`, `setsar`, `hwupload`, `hwdownload`. They appear
in the graph because MediaMolder treats each `filter` node as its own
libavfilter graph, so libavfilter's auto-converter does not bridge across
nodes.

### Pass-through filter vs. terminal sink

- **`null` / `anull`** — *pass-through filters* (one in, one out). No-op
  used for instrumentation or to isolate a chain. CLI: `-vf null`.
- **`nullsink` / `anullsink`** — *terminal sinks* (one in, zero out).
  Discard frames at the end of an analyser-only branch. Use as the filter
  inside a `filter_sink` node. CLI: `-f null /dev/null`.

---

## The three compatibility rules

The answer falls out of three rules:

1. **Edge type must match the producer's output and the consumer's input.**
2. **The data on the edge must be in a format the consumer can accept.**
3. **The path from a `source` to a sink (output / `filter_sink`) must end in
   *exactly one* of: a `copy` node, an `encoder` node, or a `filter_sink`.**

Everything else — `format`, `aformat`, `aresample`, `scale`, `fps`, `setsar`,
`hwupload` / `hwdownload`, `null` / `anull` — exists to satisfy rule **2** when
the producer and consumer disagree on the *format* of the bytes flowing on an
otherwise type-compatible edge.

---

## 1. Edge Types — the Coarsest Compatibility Check

Every edge declares a `type`:

| `type`     | Carries                                | FFmpeg analogue            |
| ---------- | -------------------------------------- | -------------------------- |
| `video`    | decoded `AVFrame` (pixel data)         | a video stream after decode |
| `audio`    | decoded `AVFrame` (sample data)        | an audio stream after decode |
| `subtitle` | `AVSubtitle` (or text packets)         | a subtitle stream          |
| `data`     | opaque packets (e.g. KLV, timed ID3)   | a data stream              |

The compiler enforces type matching at **graph build time** — connecting a
`video` edge into an `aac` encoder is rejected before the runtime starts.

Cross-media filters (the `avf_*` family: `showwavespic`, `showspectrumpic`,
`avectorscope`, `showvolume`, …) consume one type and produce another. They
declare the produced type via `output_media_type` (auto-filled from a curated
registry — see [graph-compilation.md](graph-compilation.md) and the §1
waveform row in [ffmpeg-coverage-roadmap.md](ffmpeg-coverage-roadmap.md)).

> **Per-frame metadata is not a separate edge type.** It rides inside
> `AVFrame->metadata` and propagates over `video` and `audio` edges
> automatically. The `metadata` and `ametadata` filters manipulate it in place
> as ordinary `filter` nodes — see Wave 7 #39 in the roadmap.

---

## 2. When Direct Wiring Works

Two nodes can be wired with a single edge — *no intermediate filter* — when
**all** of the following hold:

- The edge `type` matches both ends.
- The producer's output frame format is in the consumer's accept list:
  - **video:** pixel format, width, height, color range, color space, SAR.
  - **audio:** sample format, sample rate, channel layout.
  - **subtitle:** text vs. bitmap.
- For `copy`: the producer is a `source` (demuxer) and the destination
  container's muxer accepts the source codec id. No decode happens.
- For `encoder`: the producer's frame format is in the encoder's
  `pix_fmts` / `sample_fmts` / `sample_rates` / `ch_layouts` lists.

The most common direct-wire patterns:

| Pattern                                   | Why it works                                              |
| ----------------------------------------- | --------------------------------------------------------- |
| `source → copy → output`                  | Packets flow straight through; no format negotiation.     |
| `source → encoder → output`               | Decoder picks a default pixel/sample format the encoder accepts (e.g. mp4 H.264 → libx264 both speak `yuv420p`). |
| `source → filter → encoder` (single filter) | libavfilter auto-inserts `auto_scale=1` / `auto_resample=1` if needed; for trivial filters (`null`, `setpts`) no conversion is required. |
| `filter_source → encoder`                 | `testsrc2` defaults to `yuv420p`, `sine` to `flt`/`44100`/`stereo` — both within libx264 / aac defaults. |

In FFmpeg CLI, this is the world of `ffmpeg -i in.mkv -c copy out.mp4` and
`ffmpeg -i in.mp4 -c:v libx264 out.mp4` — no `-vf` / `-af` needed because the
default formats already line up.

---

## 3. When a Transform Filter Is Required

When the producer's format is **not** in the consumer's accept list, an
explicit transform node is needed. libavfilter will *sometimes* auto-insert a
converter (`auto_scale`, `auto_resample`) inside a single filter graph, but
**MediaMolder treats each `filter` node as its own filter graph**, so
auto-insertion does not bridge across nodes — you must place the transform
yourself.

### Video transforms

| Need                                             | Filter to insert                       | FFmpeg CLI form                |
| ------------------------------------------------ | -------------------------------------- | ------------------------------ |
| Pixel format mismatch (e.g. `rgba` → libx264)    | `format=pix_fmts=yuv420p`              | implicit in `-vf format=`      |
| Width / height mismatch                          | `scale=W:H`                            | `-vf scale=`                   |
| Frame rate mismatch (encoder demands CFR)        | `fps=N`                                | `-vf fps=N`                    |
| SAR mismatch                                     | `setsar=1`                             | `-vf setsar=1`                 |
| CPU ↔ GPU memory (CUDA, VAAPI)                   | `hwupload` / `hwdownload`              | `-vf hwupload`                 |
| Color space / range                              | `colorspace`, `zscale`                 | `-vf zscale=`                  |
| "Pass through but isolate timestamps"            | `null` / `setpts`                      | `-vf null`                     |

### Audio transforms

| Need                                             | Filter to insert                       | FFmpeg CLI form                |
| ------------------------------------------------ | -------------------------------------- | ------------------------------ |
| Sample format mismatch (`fltp` → AAC `s16`)      | `aformat=sample_fmts=s16`              | implicit in `-af aformat=`     |
| Sample rate mismatch                             | `aresample=44100`                      | `-af aresample=`               |
| Channel layout mismatch (5.1 → stereo)           | `aformat=channel_layouts=stereo` or `pan=` | `-ac 2` or `-af pan=`      |
| Pass through                                     | `anull`                                | `-af anull`                    |

### Subtitle transforms

Subtitles are usually wired `source → copy → output` or burned into video via
`subtitles=` / `ass=` (which is **a video filter**, not a subtitle filter — it
takes a `video` edge in and returns a `video` edge out, reading the subtitle
file out-of-band).

---

## 4. The "Stream-Copy vs. Re-encode" Decision

The choice between a `copy` node and a `filter → encoder` chain mirrors
FFmpeg's `-c copy` vs. `-c:v libx264`:

| Goal                                  | Graph                                        | CLI                            |
| ------------------------------------- | -------------------------------------------- | ------------------------------ |
| Remux only (change container)         | `source → copy → output`                     | `-c copy`                      |
| Re-encode video, copy audio           | `source(v) → x264 → out`<br>`source(a) → copy → out` | `-c:v libx264 -c:a copy` |
| Trim a stream-copied file             | `source → copy → output` + output `options.ss` / `options.t` | `-ss N -to M -c copy` |

**Constraint on `copy`:** it requires *no decoded frame in front of it*. Once
you insert any `filter`, `go_processor`, or `encoder` upstream, the path is
no longer copyable — the demuxer packet is gone, only frames remain.

---

## 5. Sources and Sinks

Every edge in the graph traces back to a **source** and forward to a **sink**.

### Sources (zero inbound edges)

| Node type        | Origin                                                              |
| ---------------- | ------------------------------------------------------------------- |
| `source` (implicit) | A demuxer — `inputs[i].url`. Referenced via `in0:v:0`, `in0:a:0`. |
| `filter_source`  | A libavfilter source filter: `color`, `testsrc2`, `sine`, `anullsrc`, `movie`, `amovie`. No file demuxer is opened (except for `movie`/`amovie`, which open one internally). |

### Sinks (zero outbound edges)

| Node type        | Destination                                                         |
| ---------------- | ------------------------------------------------------------------- |
| `output` (implicit) | A muxer — `outputs[i].url`. Referenced via `out0:v`, `out0:a`. |
| `filter_sink`    | A libavfilter sink filter: `nullsink`, `anullsink`. Frames are consumed and discarded — used for analyser-only pipelines (e.g. `testsrc2 → ebur128 → anullsink`). |

A graph is legal with **zero `inputs`** if it contains at least one
`filter_source`, and legal with **zero `outputs`** if it contains at least one
`filter_sink`. The validator (`pipeline.validate`) enforces both relaxations.

### "null" filters vs. sinks

There is a distinction worth keeping clear:

- `null` (video) and `anull` (audio) are **pass-through filters** — one input,
  one output, no-op. They sit in the middle of a chain.
- `nullsink` and `anullsink` are **terminal sinks** — one input, no output.
  They drain frames and discard them. Use them as the terminator of a
  `filter_sink` node.

CLI analogue: `null` ≈ `-vf null`; `nullsink` ≈ `-f null /dev/null`.

---

## 6. Mapping FFmpeg CLI Argument Order to MediaMolder JSON

FFmpeg's command line is **positional and stateful** — flags before `-i`
configure the next *input*, flags after `-i` configure the next *output* (or
the global state up to the next `-i`), and `-map` selects which streams from
which inputs flow to which output. MediaMolder's JSON is **declarative** — the
same information is split between `inputs[]`, `graph.nodes[]`, `graph.edges[]`,
and `outputs[]`.

### Cheat-sheet

| CLI fragment                              | JSON home                                                  |
| ----------------------------------------- | ---------------------------------------------------------- |
| `-i path` (Nth occurrence)                | `inputs[N]`                                                |
| Demuxer flags before `-i` (`-f`, `-ss`, `-r`) | `inputs[N].format`, `.options`, `.start_time` …       |
| `-map 0:v:0`                              | An edge `from: "in0:v:0"`                                  |
| `-map 1:a`                                | One edge per audio stream of `in1` (or refer to the specific track index) |
| `-vf chain` (per output)                  | A linear chain of `filter` nodes wired to `out:v`          |
| `-af chain` (per output)                  | A linear chain of `filter` nodes wired to `out:a`          |
| `-filter_complex "spec"`                  | A general subgraph of `filter` (and `filter_source` / `filter_sink`) nodes |
| `-c:v libx264` `-b:v 6M`                  | An `encoder` node with `codec: "libx264"`, `params: {b: "6M"}` |
| `-c copy`                                 | A `copy` node                                              |
| `-disposition:s:0 default`                | `outputs[i].streams[j].disposition: "default"`             |
| Output `path` (last positional)           | `outputs[i].url`                                           |
| Muxer flags (after the last `-c …` and before `path`) | `outputs[i].format`, `.options`               |

### Worked example

CLI:

```
ffmpeg -i in.mkv \
  -filter_complex "[0:v]scale=1280:720,format=yuv420p[v]; [0:a]aresample=48000[a]" \
  -map "[v]" -c:v libx264 -b:v 4M \
  -map "[a]" -c:a aac -b:a 128k \
  out.mp4
```

JSON equivalent (abbreviated):

```json
{
  "inputs":  [{ "id": "in0", "url": "in.mkv" }],
  "graph": {
    "nodes": [
      { "id": "scale", "type": "filter", "filter": "scale",   "params": { "w": "1280", "h": "720" } },
      { "id": "fmt",   "type": "filter", "filter": "format",  "params": { "pix_fmts": "yuv420p" } },
      { "id": "ars",   "type": "filter", "filter": "aresample","params": { "_pos0": "48000" } },
      { "id": "x264",  "type": "encoder","codec":  "libx264", "params": { "b": "4M" } },
      { "id": "aac",   "type": "encoder","codec":  "aac",     "params": { "b": "128k" } }
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
- The CLI's `[v]` and `[a]` named pads are not needed — every JSON node has a
  unique `id` that the edges reference directly.
- The `-c:v` and `-c:a` flags become two separate `encoder` nodes; their
  position relative to the filter chain is encoded by the edges, not by CLI
  argument order.
- `out.mp4` (the trailing positional argument) becomes `outputs[0].url`.
- `format=yuv420p` is **explicit** in the graph because each MediaMolder
  `filter` node is its own libavfilter graph; libavfilter's `auto_scale` does
  not bridge between them. The CLI hides this because the entire
  `-filter_complex` string is a single libavfilter graph where the auto-scale
  is inserted automatically.

---

## 7. Quick Diagnostic Checklist

When a pipeline fails with *"impossible to find a valid input format"* or
*"output pixel format not in encoder accept list"*:

1. **Check the edge `type`.** Mismatch is rejected at build time — fix by
   changing the `type` or by inserting a cross-media filter.
2. **Check the encoder's accept list.** `ffmpeg -h encoder=libx264` shows
   `Supported pixel formats: …`. If the upstream filter's output isn't in the
   list, insert `format=` (video) or `aformat=` (audio).
3. **Check sample rate / channel layout for audio encoders** — AAC accepts
   many rates but the source may be unusual; `aresample=` fixes it.
4. **Check that `copy` has a clean source-to-output path** with nothing in the
   middle. If you see "copy node has decoded input", you've put a filter or
   processor in front of it.
5. **Check that every source has a sink and every sink has a source.** Orphan
   nodes are reported by `graph.Compile` as warnings — see
   [graph-compilation.md](graph-compilation.md).

---

## Cross-references

- [json-config-reference.md](json-config-reference.md) — full schema
- [graph-compilation.md](graph-compilation.md) — build / compile / execute phases
- [ffmpeg-coverage-roadmap.md](ffmpeg-coverage-roadmap.md) — §2.3 (filter graph contract), §6.7 (Wave 7 items)
- [ffmpeg-migration-guide.md](ffmpeg-migration-guide.md) — full CLI → JSON migration patterns
- [go-processor-nodes.md](go-processor-nodes.md) — when to insert a Go processor instead of a libavfilter
