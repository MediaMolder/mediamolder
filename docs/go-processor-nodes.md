# Go Processor Nodes

The `go_processor` node type lets you insert **custom Go code** into a MediaMolder processing graph. Each frame (video or audio) arriving at a `go_processor` node is handed to your Go function, where you can inspect it, modify it, replace it, drop it, or attach metadata — then pass it along to the next node in the graph.

Everything runs in-process: no subprocesses, no network calls, no Python. Your processor is just a Go struct with three methods.

## Contents

- [Go Processor Nodes](#go-processor-nodes)
	- [Contents](#contents)
	- [When to use a go\_processor](#when-to-use-a-go_processor)
	- [JSON config](#json-config)
	- [Go interface](#go-interface)
		- [ProcessorContext](#processorcontext)
		- [Metadata](#metadata)
	- [Registration](#registration)
	- [Built-in processors](#built-in-processors)
		- [`null`](#null)
		- [`frame_counter`](#frame_counter)
		- [`frame_info`](#frame_info)
		- [`scene_change`](#scene_change)
		- [`scene_change_content`](#scene_change_content)
		- [`scene_change_adaptive`](#scene_change_adaptive)
		- [`scene_change_threshold`](#scene_change_threshold)
		- [`scene_change_hash`](#scene_change_hash)
		- [`scene_change_histogram`](#scene_change_histogram)
		- [`metadata_file_writer`](#metadata_file_writer)
		- [`sei_hello`](#sei_hello)
		- [`vidi_analyzer`](#vidi_analyzer)
		- [`twelvelabs_indexer`](#twelvelabs_indexer)
		- [`twelvelabs_analyzer`](#twelvelabs_analyzer)
		- [`twelvelabs_searcher`](#twelvelabs_searcher)
		- [`twelvelabs_embedder`](#twelvelabs_embedder)
		- [`whisper_stt`](#whisper_stt)
		- [`face_detect`](#face_detect)
		- [`sequence_editor`](#sequence_editor)
	- [Helper functions](#helper-functions)
		- [Letterbox](#letterbox)
		- [ImageToFloat32Tensor](#imagetofloat32tensor)
		- [DrawDetections](#drawdetections)
		- [FrameToRGBA / FrameToFloat32Tensor](#frametorgba--frametofloat32tensor)
		- [When to use what](#when-to-use-what)
		- [Example](#example)
	- [Writing a custom processor](#writing-a-custom-processor)
	- [Writing a FrameSource processor](#writing-a-framesource-processor)
		- [Step 1: Implement the interface](#step-1-implement-the-interface)
		- [Step 2: Register it](#step-2-register-it)
		- [Step 3: Use it in JSON](#step-3-use-it-in-json)
	- [Metadata and the event bus](#metadata-and-the-event-bus)
	- [Persisting metadata to files](#persisting-metadata-to-files)
		- [CLI: --metadata-out](#cli---metadata-out)
		- [metadata\_file\_writer processor](#metadata_file_writer-processor)
		- [Go API: custom event consumer](#go-api-custom-event-consumer)
	- [Error handling](#error-handling)
	- [Lifecycle](#lifecycle)
	- [Performance tips](#performance-tips)
	- [Examples](#examples)
		- [Passthrough with logging](#passthrough-with-logging)
		- [Frame counting with periodic metadata](#frame-counting-with-periodic-metadata)
		- [Chained processors (filter → processor → encoder)](#chained-processors-filter--processor--encoder)
		- [Custom AI processor](#custom-ai-processor)
	- [YOLOv8 built-in processor (optional)](#yolov8-built-in-processor-optional)
		- [Building with ONNX support](#building-with-onnx-support)
		- [JSON config](#json-config-1)
		- [Parameters](#parameters)
		- [What it does](#what-it-does)
	- [Vidi 2.5 multimodal analysis](#vidi-25-multimodal-analysis)
	- [TwelveLabs video understanding](#twelvelabs-video-understanding)
	- [Schema version](#schema-version)

---

## When to use a go_processor

Use `go_processor` when you need to do something that FFmpeg's built-in filters can't:

- **AI inference** — run an object detection model (YOLO, SSD), speech recogniser (Whisper), or image quality scorer (BRISQUE) on each frame.
- **Stateful analysis** — track objects across frames, detect scene changes, compute running averages.
- **Structured metadata** — emit detections, quality scores, or custom key-value data on the event bus so other parts of your application can react in real time.
- **Conditional forwarding** — drop frames that don't meet criteria (content gating, deduplication, silence removal).

Use a regular `"filter"` node for things FFmpeg already does well (scaling, colour conversion, overlays, audio mixing, etc.). Filters are faster because they run inside libavfilter's optimised C code.

---

## JSON config

A `go_processor` node has the same structure as other nodes, plus a required `processor` field:

```json
{
  "id": "my_node",
  "type": "go_processor",
  "processor": "registered_name",
  "params": {
    "key": "value"
  },
  "error_policy": { "policy": "abort" }
}
```

| Field          | Type   | Required | Description                                |
|----------------|--------|----------|--------------------------------------------|
| `id`           | string | yes      | Unique node ID                             |
| `type`         | string | yes      | Must be `"go_processor"`                   |
| `processor`    | string | yes      | Name passed to `processors.Register()`     |
| `params`       | object | no       | Arbitrary key/value passed to `Init()`     |
| `error_policy` | object | no       | Same error policy as any other node        |

Edges to/from `go_processor` nodes use the same syntax as filters:

```json
{ "from": "src:v:0",       "to": "my_node:default", "type": "video" }
{ "from": "my_node:default", "to": "enc:default",    "type": "video" }
```

When `go_processor` nodes are present, set `"schema_version": "1.1"`.

---

## Go interface

Every processor implements three methods:

```go
type Processor interface {
    Init(params map[string]any) error
    Process(frame *av.Frame, ctx ProcessorContext) (*av.Frame, *Metadata, error)
    Close() error
}

type FrameLookahead interface {
  LookbackFrames() int
}

type FrameSource interface {
  Run(ctx context.Context, send func(*av.Frame) error) error
}
```

| Method | When it runs | What to do |
|--------|-------------|------------|
| `Init` | Once, before the first frame arrives. | Read your config from `params`, load models, allocate buffers. Return an error to abort the graph. |
| `Process` | Once per frame. | Inspect or modify the frame, run your logic, return the frame (or a new one) plus optional metadata. Return a `nil` frame to drop it. |
| `Close` | Once, when the graph shuts down (even after errors). | Release resources, flush buffers, close files. |

`FrameLookahead` is optional. Implement it when a processor confirms metadata
for an earlier frame after seeing future frames. The runtime delays downstream
delivery by `LookbackFrames()` frames so metadata routing and forced-IDR marks
can target the event frame instead of the confirmation frame.

`FrameSource` is optional. Implement it when a processor **generates its own frames** rather than processing inbound ones.  A `go_processor` that also implements `FrameSource` may have **zero inbound AV edges** in the graph; the runtime calls `Run()` instead of the `Process()` loop and feeds the produced frames to all downstream channels.  The `send` callback takes ownership of each frame — do not close frames you have sent.  `sequence_editor` is the built-in example.  See [Writing a FrameSource processor](#writing-a-framesource-processor) for implementation notes.

### ProcessorContext

Every call to `Process()` includes a context struct with information about the current frame:

```go
type ProcessorContext struct {
    StreamID   string          // which stream this frame belongs to, e.g. "v:0"
    MediaType  av.MediaType    // video, audio, or subtitle
    PTS        int64           // presentation timestamp (in stream timebase units)
    FrameIndex uint64          // how many frames this node has seen so far (starts at 0)
    Context    context.Context // standard Go context — check this for cancellation
}
```

You can use `MediaType` to handle video and audio frames differently, and `FrameIndex` for logic that depends on position (e.g. "skip the first 100 frames").

### Metadata

If your processor produces results (detections, scores, analytics), return them as `*Metadata`. The runtime automatically publishes non-nil metadata on the event bus so the rest of your application can consume it.

```go
type Metadata struct {
    Detections   []Detection    // objects found in this frame
    QualityScore float64        // e.g. BRISQUE score, SSIM, custom metric
    Custom       map[string]any // anything else — counters, flags, labels
}

type Detection struct {
    Label      string     // what was detected, e.g. "person", "car"
    Confidence float64    // model confidence, 0.0–1.0
    BBox       [4]float64 // bounding box in pixel coords: [x1, y1, x2, y2]
    TrackID    int        // optional object tracking ID across frames
}
```

Return `nil` for metadata if your processor has nothing to report for a given frame.

---

## Registration

Before you can reference a processor by name in JSON, you need to register it. Registration maps a string name to a factory function that creates new instances.

The typical place to do this is in an `init()` function, which runs automatically at startup:

```go
import "github.com/MediaMolder/MediaMolder/processors"

func init() {
    processors.Register("my_proc", func() processors.Processor {
        return &MyProcessor{}
    })
}
```

The factory is called once per `go_processor` node in the graph, so if your JSON config has three nodes using `"my_proc"`, three separate instances are created. Each holds its own state — no shared-state concurrency issues to worry about.

To see which processors are available at runtime:

```sh
mediamolder list-processors
```

---

## Built-in processors

MediaMolder ships with these processors out of the box:

### `null`

Passthrough — forwards every frame unmodified. Useful for testing and as a starting template.

```json
{ "id": "noop", "type": "go_processor", "processor": "null" }
```

### `frame_counter`

Counts frames and emits metadata with the running total.

| Param      | Type | Default | Description                        |
|------------|------|---------|------------------------------------|
| `log_every`| int  | 1       | Emit metadata every N frames       |

```json
{
  "id": "counter",
  "type": "go_processor",
  "processor": "frame_counter",
  "params": { "log_every": 100 }
}
```

Metadata emitted:

```json
{ "custom": { "frame_count": 100 } }
```

### `frame_info`

A read-only analysis processor that passes frames through unchanged while emitting metadata about each frame's properties: dimensions, pixel format, PTS, frame index, and stream ID. Useful for diagnostics, logging, and verifying that frames arrive as expected.

| Param       | Type | Default | Description                                 |
|-------------|------|---------|---------------------------------------------|
| `log_every` | int  | 1       | Emit metadata every N frames                |

```json
{
  "id": "info",
  "type": "go_processor",
  "processor": "frame_info",
  "params": { "log_every": 30 }
}
```

Metadata emitted:

```json
{ "custom": { "width": 1920, "height": 1080, "pix_fmt": 0, "pts": 3003, "frame_index": 30, "stream_id": "v:0" } }
```

### `scene_change`

Detects scene changes between consecutive frames using the same algorithm as FFmpeg's [`scdet`](https://ffmpeg.org/ffmpeg-filters.html#scdet) filter:

- **Content change (MAFD + diff)** — computes the Mean Absolute Frame Difference on the luma channel. For YUV formats (the common case for decoded H.264/H.265), this reads the Y plane **directly** — zero pixel-format conversion, zero allocation. For RGB or packed formats, falls back to a GRAY8 conversion via libswscale. The final score is `min(mafd, |mafd − prev_mafd|)`, which suppresses false positives from gradual pans, zooms, and fades while catching hard cuts — identical to FFmpeg's scdet algorithm.
- **PTS gap** — flags a scene change when the PTS delta between consecutive frames exceeds a threshold (useful for detecting stream discontinuities or spliced content).

The frame is always passed through unchanged. Internally, the processor clones each frame (via `av_frame_clone`, reference-counted — no pixel copy) to compare against the next.

| Param           | Type    | Default | Description                                         |
|-----------------|---------|---------|-----------------------------------------------------|
| `threshold`     | float64 | 10      | 0–100, min scdet score to trigger (same scale as FFmpeg `scdet=threshold=10`)  |
| `pts_threshold` | int64   | 0       | Min PTS gap to flag (0 = disabled)                  |

```json
{
  "id": "scene_detect",
  "type": "go_processor",
  "processor": "scene_change",
  "params": { "threshold": 10, "pts_threshold": 90000 }
}
```

Metadata emitted (only on detected scene changes):

```json
{ "custom": { "scene_change": true, "reasons": ["content_change"], "mafd": 42.1, "score": 38.7, "frame_index": 142 } }
```

### `scene_change_content`

Port of [PySceneDetect](https://github.com/Breakthrough/PySceneDetect)'s `ContentDetector`.
Converts each frame to HSV, computes a weighted per-channel mean pixel distance, and
triggers a cut when the score exceeds `threshold`. Supports Canny-edge weighting for higher
accuracy on complex scenes. See [docs/scene-detection.md](scene-detection.md#scene_change_content) for full details.

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| `threshold` | float64 | 27.0 | Weighted HSV delta; lower = more sensitive |
| `min_scene_len` | int/string | 15 | Min scene length: frames, `"0.6s"`, or timecode |
| `luma_only` | bool | false | Use luma channel only |
| `filter_mode` | string | `"merge"` | `"merge"` or `"suppress"` adjacent-cut handling |
| `kernel_size` | int | 0 | Canny dilation kernel size; 0 = auto |
| `frame_rate` | float64 | 25.0 | Stream frame rate |

```json
{
  "id": "detect",
  "type": "go_processor",
  "processor": "scene_change_content",
  "params": { "threshold": 27.0, "min_scene_len": "0.6s" }
}
```

### `scene_change_adaptive`

Port of PySceneDetect's `AdaptiveDetector`. Wraps `scene_change_content` and normalises
each frame's content score against a rolling window mean, making it robust to sustained
high-motion segments that would otherwise saturate the content score.
See [docs/scene-detection.md](scene-detection.md#scene_change_adaptive) for full details.
Because the adaptive detector confirms a cut after the rolling window is full,
it implements `FrameLookahead`; the runtime compensates for `window_width` so
segment cuts and encoder IDR marks land on the detected scene boundary frame.

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| `threshold` | float64 | 3.0 | Adaptive ratio threshold |
| `min_scene_len` | int/string | 15 | Min scene length |
| `window_width` | int | 2 | Rolling-window half-width |
| `min_content_val` | float64 | 15.0 | Minimum raw content score required |
| `luma_only` | bool | false | Use luma channel only |
| `frame_rate` | float64 | 25.0 | Stream frame rate |

```json
{
  "id": "detect",
  "type": "go_processor",
  "processor": "scene_change_adaptive",
  "params": { "threshold": 3.0, "min_scene_len": "0.6s", "min_content_val": 15.0 }
}
```

### `scene_change_threshold`

Port of PySceneDetect's `ThresholdDetector`. Detects fades to/from black (or white) by
tracking per-frame mean brightness. Emits a cut at the midpoint of each fade transition
(configurable via `fade_bias`).
See [docs/scene-detection.md](scene-detection.md#scene_change_threshold) for full details.

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| `threshold` | float64 | 12.0 | Mean brightness threshold (0–255) |
| `min_scene_len` | int/string | 15 | Min scene length |
| `method` | string | `"floor"` | `"floor"` = fade to black; `"ceiling"` = fade to white |
| `fade_bias` | float64 | 0.0 | −1 = start of fade-out, +1 = start of fade-in, 0 = midpoint |
| `add_final_scene` | bool | false | Always emit a cut at the last frame |
| `frame_rate` | float64 | 25.0 | Stream frame rate |

```json
{
  "id": "detect",
  "type": "go_processor",
  "processor": "scene_change_threshold",
  "params": { "threshold": 12.0, "method": "floor" }
}
```

### `scene_change_hash`

Port of PySceneDetect's `HashDetector`. Computes a perceptual DCT hash of each frame
and measures Hamming distance to the previous frame. Robust to minor colour-grading
and compression artefact changes.
See [docs/scene-detection.md](scene-detection.md#scene_change_hash) for full details.

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| `threshold` | float64 | 0.395 | Normalised Hamming distance (0–1) |
| `min_scene_len` | int/string | 15 | Min scene length |
| `size` | int | 16 | DCT low-frequency block edge length |
| `lowpass` | int | 2 | Resize multiplier (`size × lowpass` pixels per side) |
| `frame_rate` | float64 | 25.0 | Stream frame rate |

```json
{
  "id": "detect",
  "type": "go_processor",
  "processor": "scene_change_hash",
  "params": { "threshold": 0.395, "size": 16, "lowpass": 2 }
}
```

### `scene_change_histogram`

Port of PySceneDetect's `HistogramDetector`. Compares adjacent-frame luma histograms
using the Pearson correlation coefficient. Fastest of the five go-scene-detect processors;
ideally used as a coarse pre-filter.
See [docs/scene-detection.md](scene-detection.md#scene_change_histogram) for full details.

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| `threshold` | float64 | 0.05 | Histogram divergence (1 − Pearson correlation) |
| `min_scene_len` | int/string | 15 | Min scene length |
| `bins` | int | 256 | Number of histogram bins |
| `frame_rate` | float64 | 25.0 | Stream frame rate |

```json
{
  "id": "detect",
  "type": "go_processor",
  "processor": "scene_change_histogram",
  "params": { "threshold": 0.05, "bins": 256 }
}
```

### `metadata_file_writer`

A sink processor that writes metadata events to a [JSON Lines](https://jsonlines.org/) file. Supports two usage modes:

**Events-wiring mode** (recommended for the GUI and new graphs): omit `inner_processor` and connect an **`events`** edge from any `go_processor` node to this node. The engine opens the output file and routes every event directly — no video path change required.

**Wrapper mode** (legacy / JSON-authored graphs): set `inner_processor` to the name of another registered processor. `metadata_file_writer` wraps it, intercepts its metadata, writes it to the file, and forwards both the frame and metadata to the caller. Deprecated for new graphs; prefer events-wiring.

| Param              | Type   | Default      | Description |
|--------------------|--------|--------------|-------------|
| `output_file`      | string | **(required)**| Path to the output file |
| `output_format`    | string | `"jsonl"`    | Output format: `"jsonl"`, `"csv"`, or `"timecodes"` |
| `inner_processor`  | string | *(optional)* | Wrapper mode only: name of a registered processor to wrap |
| *(other params)*   |        |              | Wrapper mode only: forwarded to the inner processor's `Init()` |

**Events-wiring mode example** (GUI / no inner processor):

```json
{
  "nodes": [
    { "id": "detect", "type": "go_processor", "processor": "scene_change_content",
      "params": { "threshold": 27.0 } },
    { "id": "cut_log", "type": "go_processor", "processor": "metadata_file_writer",
      "params": { "output_file": "/tmp/cuts.jsonl" } }
  ],
  "edges": [
    { "from": "detect",  "to": "cut_log", "type": "events" }
  ]
}
```

In the GUI, drag from the **pink (events) handle** on the right of a `go_processor` node to the pink handle on the left of a `metadata_file_writer` node. The events wire is drawn as a pink dashed line.

**Wrapper mode example** (legacy):

```json
{
  "id": "detect_and_log",
  "type": "go_processor",
  "processor": "metadata_file_writer",
  "params": {
    "output_file": "detections.jsonl",
    "inner_processor": "yolo_v8",
    "model": "/models/yolov8n.onnx",
    "labels_file": "/models/coco.names",
    "conf": 0.5
  }
}
```

Each line in the output file is a JSON object:

```json
{"frame_index":0,"pts":0,"metadata":{"detections":[{"label":"person","confidence":0.92,"bbox":[120,45,380,510]}]}}
{"frame_index":5,"pts":5000,"metadata":{"detections":[{"label":"car","confidence":0.87,"bbox":[400,200,700,450]}]}}
```

Frames where the processor returns no metadata produce no output line.

---

### `sei_hello`

An example processor that attaches a `user_data_unregistered` SEI payload to
every video frame via `av.Frame.AddSEIUnregisteredSideData`. H.264 and HEVC
encoders that honour SEI side data (libx265 by default; libx264 with
`udu_sei=1`) serialise the payload into the output bitstream.

Non-video frames are passed through untouched.

| Param  | Type   | Default   | Description                                       |
|--------|--------|-----------|---------------------------------------------------|
| `text` | string | `"hello"` | Payload appended after the 16-byte UUID in the SEI NAL |

```json
{
  "id": "stamp_sei",
  "type": "go_processor",
  "processor": "sei_hello",
  "params": { "text": "hello" }
}
```

> **Note:** `seiHelloUUID` is an ASCII placeholder, not an RFC 4122 UUID.
> Production processors should use a proper UUID to avoid bitstream collisions
> with other SEI producers.

### `vidi_analyzer`

Sends batches of decoded video frames to a running [Vidi 2.5](https://github.com/bytedance/vidi) Python inference service and publishes the structured results (captions, bounding boxes, timestamps, edit plans, QA answers) as `Metadata` on the event bus. No build tags required — all dependencies are standard library plus the `av` package.

Requires a separate Python service. See the full [Vidi 2.5 Guide](vidi-guide.md) for service setup, task descriptions, and performance tips.

| Param           | Type   | Default                | Description |
|-----------------|--------|------------------------|-------------|
| `service_url`   | string | **(required)**         | Base URL of the Vidi inference service |
| `query`         | string | `"describe the scene"` | Natural-language prompt sent with every batch |
| `task`          | string | `"captioning"`         | `captioning` \| `grounding` \| `qa` \| `editing` |
| `buffer_frames` | int    | `8`                    | Frames to accumulate before each `/infer` call |
| `process_every` | int    | `1`                    | Only process every Nth video frame |
| `jpeg_quality`  | int    | `75`                   | JPEG quality for frame encoding (1–100) |
| `timeout_s`     | float  | `30`                   | Per-request HTTP timeout in seconds |

```json
{
  "id": "vidi",
  "type": "go_processor",
  "processor": "vidi_analyzer",
  "params": {
    "service_url":   "http://localhost:8000",
    "query":         "identify and timestamp every scene change",
    "task":          "grounding",
    "buffer_frames": 8
  }
}
```

### `twelvelabs_indexer`

Uploads each completed segment / file into a [TwelveLabs](https://twelvelabs.io) index (Marengo + Pegasus). Emits an `indexed` event with the resulting `task_id` and `video_id`.

### `twelvelabs_analyzer`

Runs Pegasus analyze on each completed segment to emit captions, summaries, and optional structured chapter markers.

### `twelvelabs_searcher`

Runs a Marengo natural-language search against an index — on a timer or per segment — and publishes timestamped matches.

### `twelvelabs_embedder`

Generates Marengo video embeddings per clip (and optionally per fixed window), inline on the event bus or to per-segment `json` / `jsonl` files.

All four nodes share an API-key resolution chain (`api_key` param → `TWELVELABS_API_KEY` env → `~/.config/mediamolder/twelvelabs.json`) and emit to `Metadata.Custom["twelvelabs"]`. See the full [TwelveLabs Guide](twelvelabs.md) for parameters, graph examples, and the CLI / HTTP surface.

### `whisper_stt`

Transcribes an audio stream to timestamped text locally with [whisper.cpp](https://github.com/ggml-org/whisper.cpp) — offline, no network. It accumulates the (auto-resampled 16 kHz mono) audio during processing and runs a single transcription pass at end-of-stream; the audio passes through unchanged. Each segment is emitted as `Metadata` on the event bus, and an optional sidecar transcript (SRT / VTT / JSON / TXT) is written. Compiled only with the **`with_whisper`** build tag; you supply `libwhisper` and a ggml model (MediaMolder ships neither).

| Param             | Type   | Default        | Description |
|-------------------|--------|----------------|-------------|
| `model`           | string | **(required)** | Path to a ggml/gguf Whisper model |
| `language`        | string | `"auto"`       | Source language hint (`auto` detects) |
| `task`            | string | `"transcribe"` | `transcribe` \| `translate` (to English) |
| `beam_size`       | int    | `0`            | `0`/`1` greedy; `>1` beam search |
| `word_timestamps` | bool   | `false`        | Request token-level timestamps |
| `threads`         | int    | `NumCPU()`     | Inference threads |
| `initial_prompt`  | string | `""`           | Context/biasing prompt |
| `output_file`     | string | `""`           | Sidecar path; empty = events only |
| `output_format`   | string | `"srt"`        | `srt` \| `vtt` \| `json` \| `txt` |

```json
{
  "id": "stt",
  "type": "go_processor",
  "processor": "whisper_stt",
  "params": {
    "model":         "/models/ggml-base.en.bin",
    "language":      "en",
    "output_file":   "/tmp/out.srt",
    "output_format": "srt"
  }
}
```

See the full [Whisper Speech-to-Text Guide](whisper-stt-guide.md) for build
instructions, model selection, and output details.

---

### `face_detect`

Detects faces (YOLOv8-face) in each video frame, aligns each to the canonical 112×112, and optionally embeds it (SFace) for recognition/clustering. The frame passes through unchanged; each face is emitted as a `Detection` (box + score, for generic overlay consumers) plus the richer `face.Record` slice (landmarks + optional 128-d embedding) under `Metadata.Custom["faces"]`. Compiled only with the **`with_onnx`** build tag; models are loaded as data and SHA-256-verified from `MEDIAMOLDER_FACE_MODELS` (see `scripts/fetch-face-models.sh`).

| Param           | Type   | Default   | Description |
|-----------------|--------|-----------|-------------|
| `every`         | int    | `1`       | Analyse every Nth video frame |
| `conf`          | float  | `0.5`     | Detector confidence threshold (0 = package default) |
| `embeddings`    | bool   | `false`   | Also compute the 128-d SFace embedding per face |
| `models_dir`    | string | `""`      | Override `MEDIAMOLDER_FACE_MODELS` |
| `ort_lib`       | string | `""`      | ONNX Runtime library path (else auto-discovered / `ONNXRUNTIME_SHARED_LIBRARY_PATH`) |
| `output_file`   | string | `""`      | Absolute path; write detections to this sidecar directly |
| `output_format` | string | `"jsonl"` | Sidecar format: `jsonl` \| `csv` \| `timecodes` |

Set `output_file` and `face_detect` writes its own sidecar — no extra node — so a complete face-detection job is just an input wired into one node (an analysis-only graph: no encoder, no muxer, `"outputs": []`):

```json
{
  "id": "faces",
  "type": "go_processor",
  "processor": "face_detect",
  "params": { "every": 1, "conf": 0.5, "embeddings": true, "output_file": "/abs/faces.jsonl" }
}
```

A still image is a single-frame video stream, so the same node handles images and video. Alternatively, omit `output_file` and wire an `events` edge into a [`metadata_file_writer`](#metadata_file_writer). See the full [Face Detection Guide](face-detection-guide.md) and the [design](architecture/face-detection.md).

---

### `sequence_editor`

A basic NLE-style timeline / sequence generator (a **FrameSource**
`go_processor`). You declare a fixed output video format and one or more
**tracks**; each track holds clips placed at explicit sequence times. At any
output time the clip on the highest-priority (highest-index) track that
covers that time is decoded, converted to the sequence format, stamped with a
continuous sequence PTS, and emitted; uncovered times render black. This
gives cuts, inserts, multi-cam selects, and layered content via track
priority. The sequence timebase is continuous and independent of the
sources' timebases, so output PTS is strictly increasing at a constant rate.

Transitions between adjacent clips on a track support `dissolve` (a linear
cross-fade) and the full libavfilter `xfade` set (wipes, slides, fades, …) —
see the `transition` field below.

#### When to use `sequence_editor`

- Multi-track / layered timelines (upper track replaces lower where present)
- Precise placement: clips at specific sequence times, with gaps or overlaps
- A fixed output format that sources are scaled/retimed into
- Dissolve or any `xfade` transition between adjacent clips on a track

#### Params

```json
{
  "format": {
    "width": 1920,
    "height": 1080,
    "pix_fmt": "yuv420p",
    "frame_rate": 29.97,
    "time_base": [1, 90000],
    "length_sec": 130
  },
  "tracks": [
    {
      "id": "V1",
      "type": "video",
      "clips": [
        { "input_id": "video_a", "source_in": 0,  "source_out": 10.5, "timeline_in": 0,  "transition": { "type": "dissolve", "duration": 0.5 } },
        { "input_id": "video_b", "source_in": 10, "source_out": 20.5, "timeline_in": 10, "transition": { "type": "dissolve", "duration": 0.5 } },
        { "input_id": "video_a", "source_in": 20, "source_out": 30,   "timeline_in": 20 }
      ]
    }
  ],
  "sequence_log": "/tmp/seq.jsonl"
}
```

| Field (in `format`) | Type | Description |
|---|---|---|
| `width` / `height` | int | Output resolution; sources are scaled to it. |
| `pix_fmt` | string | Output pixel format (e.g. `yuv420p`). |
| `frame_rate` | number | Constant output frame rate. |
| `time_base` | [int,int] | Continuous sequence timebase (e.g. `[1, 90000]`). |
| `length_sec` | number | Exact sequence length; overrides the computed duration. |

| Field (in `tracks[].clips[]`) | Type | Description |
|---|---|---|
| `url` *or* `input_id` / `media_id` | string | Source. `input_id`/`media_id` reference a graph Input node and are resolved to a `url` by the engine before `Init()`. |
| `source_in` | number | Source time (seconds) mapped to `timeline_in`. |
| `source_out` | number | Source stop time; clip duration = `source_out − source_in` (must be > 0, and includes any transition overlap). |
| `timeline_in` | number | Where the clip begins on the output timeline (seconds). |
| `transition` | object | Optional `{ "type": "<name>", "duration": <sec> }` transition into the next clip. `dissolve` is a linear cross-fade (the `blend` filter — *not* xfade's dithered dissolve); every other name is a **libavfilter `xfade` transition** (`fade`, `wipeleft/right/up/down`, `slideleft/right/…`, `circleopen/close`, `fadeblack/white/grays`, `radial`, `zoomin`, `hblur`, …) composited per-window via an xfade graph. Transitions are within-track (between two adjacent clips on the same track); they do not compose across track layers. Unsupported names are rejected at load time. |

`sequence_log` (optional): a path to write one JSON-Lines record per output
frame describing what the renderer did (winning track/clip, source time
fetched, hold vs. fresh content) — useful for debugging timeline math.

Runnable examples:
[`61_sequence_editor_dissolves.json`](../testdata/examples/61_sequence_editor_dissolves.json)
(dissolve) and
[`62_sequence_editor_wipe.json`](../testdata/examples/62_sequence_editor_wipe.json)
(xfade `wipeleft` + `slideright`).

**Note**: `sequence_editor` is a **FrameSource** — it
opens its sources internally and has **no inbound AV edge**. Reference
sources via `input_id`/`media_id` on top-level Input nodes (the engine
resolves them to URLs); those Input nodes need no edges into the sequence
node.

---

## Helper functions

The `processors` package includes utility functions that handle common preprocessing and visualisation tasks you'd otherwise have to write yourself. These are **not called automatically** — you call them inside your `Process()` method whenever you need them.

```go
import "github.com/MediaMolder/MediaMolder/processors"
```

### Letterbox

```go
func Letterbox(src image.Image, targetW, targetH int) *image.RGBA
```

Resizes an image to fit inside `targetW × targetH` **without stretching**. The aspect ratio is preserved and any remaining space is filled with black bars — exactly like a widescreen film on a 4:3 screen. Most AI models require a fixed square input (e.g. 640×640), so this is typically the first preprocessing step.

### ImageToFloat32Tensor

```go
func ImageToFloat32Tensor(img image.Image, targetSize int) []float32
```

Takes any Go `image.Image`, letterboxes it to `targetSize × targetSize`, then converts the pixels into a flat `[]float32` array in **NCHW channel-first layout** (three separate planes: R, G, B) with values normalised to [0, 1]. This is the exact format expected by ONNX Runtime, TensorRT, and most inference frameworks — you can pass the slice directly to your model.

### DrawDetections

```go
func DrawDetections(img *image.RGBA, dets []Detection)
```

Draws a red bounding-box rectangle onto the image for each detection. BBox coordinates are in pixels. Useful for debugging or producing annotated video output.

### FrameToRGBA / FrameToFloat32Tensor

```go
func FrameToRGBA(frame *av.Frame) (*image.RGBA, error)
func FrameToFloat32Tensor(frame *av.Frame, targetSize int) ([]float32, error)
```

These convert an `*av.Frame` directly to an image or tensor in one call. Under the hood, `FrameToRGBA` uses `av.Frame.ToRGBA()` which delegates to libswscale — any pixel format FFmpeg can handle (YUV420P, NV12, RGB24, etc.) is supported. `FrameToFloat32Tensor` calls `FrameToRGBA`, then letterboxes and normalises into `[3, H, W]` NCHW float32 layout.

> **Note**: Hardware-surface frames (CUDA, VAAPI) must be transferred to system memory first — see `HWDecoderContext.ReceiveFrame()` with `AutoTransfer`.

### When to use what

These helpers are tools you call **inside** `Process()`, at whatever stage makes sense:

```
frame arrives
  │
  ├─ preprocessing:  Letterbox / FrameToFloat32Tensor   → feed to AI model
  │
  ├─ your logic:     run inference, compute scores, make decisions
  │
  ├─ postprocessing: DrawDetections                    → annotate output
  │
  └─ return frame + metadata
```

You can use none, some, or all of them — they're entirely optional.

### Example

```go
func (p *MyDetector) Process(frame *av.Frame, ctx processors.ProcessorContext) (*av.Frame, *processors.Metadata, error) {
    // 1. Preprocess: frame → model-ready tensor (handles any pixel format)
    tensor, err := processors.FrameToFloat32Tensor(frame, 640)
    if err != nil {
        return nil, nil, err
    }

    // 2. Run inference (your model, your framework)
    detections := p.model.Detect(tensor)

    // 3. Optional: draw boxes for visual debugging
    //    rgba, _ := processors.FrameToRGBA(frame)
    //    processors.DrawDetections(rgba, detections)

    return frame, &processors.Metadata{Detections: detections}, nil
}
```

See `processors/helpers.go` for implementation details.

---

## Writing a custom processor

### Step 1: Implement the interface

```go
package mypkg

import (
    "github.com/MediaMolder/MediaMolder/av"
    "github.com/MediaMolder/MediaMolder/processors"
)

type BrightnessChecker struct {
    threshold float64
}

func (p *BrightnessChecker) Init(params map[string]any) error {
    p.threshold = 0.5
    if v, ok := params["threshold"].(float64); ok {
        p.threshold = v
    }
    return nil
}

func (p *BrightnessChecker) Process(frame *av.Frame, ctx processors.ProcessorContext) (*av.Frame, *processors.Metadata, error) {
    if ctx.MediaType != av.MediaTypeVideo {
        return frame, nil, nil // pass non-video through
    }

    // Example: compute average brightness from frame data.
    // (Real implementation would read frame pixel data.)
    brightness := 0.7 // placeholder

    md := &processors.Metadata{
        QualityScore: brightness,
        Custom: map[string]any{
            "above_threshold": brightness >= p.threshold,
        },
    }

    return frame, md, nil
}

func (p *BrightnessChecker) Close() error { return nil }
```

### Step 2: Register it

```go
func init() {
    processors.Register("brightness_checker", func() processors.Processor {
        return &BrightnessChecker{}
    })
}
```

### Step 3: Use it in JSON

```json
{
  "schema_version": "1.1",
  "inputs": [
    {
      "id": "src",
      "url": "input.mp4",
      "streams": [{ "input_index": 0, "type": "video", "track": 0 }]
    }
  ],
  "graph": {
    "nodes": [
      {
        "id": "bright",
        "type": "go_processor",
        "processor": "brightness_checker",
        "params": { "threshold": 0.4 }
      }
    ],
    "edges": [
      { "from": "src:v:0", "to": "bright:default", "type": "video" },
      { "from": "bright:default", "to": "out:v", "type": "video" }
    ]
  },
  "outputs": [
    { "id": "out", "url": "output.mp4", "codec_video": "libx264" }
  ]
}
```

---

## Writing a FrameSource processor

A `FrameSource` processor generates its own frames instead of processing inbound ones.  This pattern is ideal for anything that reads files directly (clip assemblers, test-pattern generators, deck-ingest adapters) and must keep memory proportional to the working set rather than the full timeline.

### When to use FrameSource vs Processor

| Use `Processor` when… | Use `FrameSource` when… |
|---|---|
| Frames arrive from a graph input node | The node opens its own files |
| You transform, analyse, or annotate each frame | You emit frames from scratch or from a controlled read |
| You need to consume exactly one inbound AV edge | Zero inbound AV edges make sense for this node |

### Implementation skeleton

```go
type MySource struct {
    clips []string
}

func (s *MySource) Init(params map[string]any) error {
    // parse params["clips"] etc.
    return nil
}

// Process is required by the Processor interface but must never be called.
func (s *MySource) Process(_ *av.Frame, _ processors.ProcessorContext) (*av.Frame, *processors.Metadata, error) {
    return nil, nil, fmt.Errorf("MySource: Process() called on a FrameSource — runtime bug")
}

func (s *MySource) Close() error { return nil }

// Run implements processors.FrameSource.
func (s *MySource) Run(ctx context.Context, send func(*av.Frame) error) error {
    for _, clip := range s.clips {
        if err := ctx.Err(); err != nil {
            return err
        }
        // open clip, decode, send frames ...
        f, err := decodeOneFrame(clip)
        if err != nil {
            return err
        }
        if err := send(f); err != nil { // send takes ownership; do not close f after this
            return err
        }
    }
    return nil
}

func init() {
    processors.Register("my_source", func() processors.Processor { return &MySource{} })
}
```

### Engine behaviour

- The engine detects `FrameSource` via a Go interface assertion at graph initialisation time.
- `Run()` is called instead of `Process()` when `len(inboundAVEdges) == 0`.
- Frames produced by `send()` are forwarded to all downstream channels exactly as if they had come from a `Process()` return value.
- Context cancellation is propagated via `ctx`; check `ctx.Err()` inside your read loop.
- Input nodes referenced by `input_id` in the processor's params are resolved to URLs before `Init()` is called; the `in` seek point is derived from `options.ss` on the input when no explicit `in` param is given.

---

## Metadata and the event bus

Whenever your `Process()` method returns a non-nil `*Metadata`, the runtime automatically posts it to the event bus. You don't need to do anything special — just return the metadata and it's published.

On the consuming side, any part of your application can listen for these events:

```go
for ev := range pipeline.Events() {
    switch e := ev.(type) {
    case processors.ProcessorMetadataEvent:
        fmt.Printf("node=%s frame=%d detections=%d\n",
            e.NodeID, e.FrameIndex, len(e.Metadata.Detections))
    }
}
```

The event struct tells you which node produced the metadata, which frame it was for, and carries the full `Metadata` you returned:

```go
type ProcessorMetadataEvent struct {
    NodeID     string     // which go_processor node emitted this
    FrameIndex uint64     // which frame (zero-based)
    PTS        int64      // presentation timestamp of that frame
    Metadata   *Metadata  // your detections, scores, custom data
}
```

This is how you wire processors into a larger system — for example, logging detections to a file, updating a real-time dashboard, triggering alerts, or feeding results into a database.

---

## Persisting metadata to files

There are three ways to capture metadata on disk, depending on your use case:

### CLI: --metadata-out

The simplest approach. Pass `--metadata-out <path>` to the `run` command and all `ProcessorMetadata` events are written as JSON Lines:

```bash
mediamolder run --metadata-out detections.jsonl job.json
```

Use `-` to write to stdout (useful for piping):

```bash
mediamolder run --metadata-out - job.json 2>/dev/null | jq '.metadata.detections[]'
```

This captures metadata from **all** `go_processor` nodes in the graph. Each line includes `node_id` so you can filter by source.

### metadata_file_writer processor

For per-node file output configured entirely in JSON (no CLI flags needed), use the built-in [`metadata_file_writer`](#metadata_file_writer) processor. It wraps another processor, runs it, and writes its metadata to a file. See the [built-in processors](#metadata_file_writer) section above.

### Go API: custom event consumer

For library users who want full control (database writes, webhooks, custom formats):

```go
eng, _ := pipeline.NewPipeline(cfg)

go func() {
    for ev := range eng.Events() {
        md, ok := ev.(pipeline.ProcessorMetadata)
        if !ok || md.Metadata == nil {
            continue
        }
        // Write to database, send webhook, etc.
        b, _ := json.Marshal(md)
        fmt.Println(string(b))
    }
}()

eng.Run(ctx)
```

---

## Error handling

- **`Init()` error** → graph creation aborts (same as an invalid filter).
- **`Process()` error** → respects the node's `error_policy`:
  - `"abort"` (default): the graph stops immediately.
  - `"skip"`: frame is dropped, processing continues.
  - `"retry"`: frame is re-submitted up to `max_retries` times.
  - `"fallback"`: reroute to `fallback_node`.
- **`Close()`** is called unconditionally during shutdown, even after errors.

---

## Lifecycle

A processor goes through three phases, always in this order:

```
Graph starts
    │
    ├─ processors.Get("name")   →  factory creates a new instance of your struct
    │
    ├─ processor.Init(params)   →  you read config, load models, allocate buffers
    │
    ├─ processor.Process(frame) →  called once per frame, potentially thousands of times
    │   processor.Process(frame)
    │   processor.Process(frame)
    │   ...
    │
    └─ processor.Close()        →  you release resources; always called, even after errors
```

Important guarantees:

- **One instance per node.** If your JSON has two `go_processor` nodes both using `"my_proc"`, each gets its own struct instance with its own state.
- **Serial calls.** `Process()` is never called concurrently on the same instance. You don't need mutexes for per-node state.
- **Ordering preserved.** Frames arrive in decode order. If frame 42 arrives before frame 43, your `Process()` sees them in that order.
- **Close is guaranteed.** Even if `Process()` returns an error or the graph is cancelled, `Close()` still runs.

---

## Performance tips

- **Return the same frame pointer** if you didn't modify it. Creating a new frame when you only needed to read it wastes memory and CPU on copying.
- **Use the provided helpers** (`FrameToFloat32Tensor`, `Letterbox`) instead of writing your own preprocessing. They're tested, correct, and safe for concurrent use.
- **Drop frames by returning nil.** `return nil, md, nil` tells the runtime to consume the frame. No error, no forwarding — the frame just stops here.
- **Batch if your model wants it.** If GPU inference is faster on N frames at once, accumulate frames in a buffer inside `Process()` and emit results when the batch is full.
- **Check `ctx.Context` for cancellation.** If your processing is slow (e.g. large model inference), periodically check `ctx.Context.Done()` so the graph can shut down promptly.
- **Keep `Init()` fast.** It runs before the graph starts, so slow model loading delays everything. For very large models, consider lazy-loading on the first `Process()` call.

---

## Examples

### Passthrough with logging

```json
{
  "schema_version": "1.1",
  "inputs": [
    {
      "id": "src",
      "url": "input.mp4",
      "streams": [{ "input_index": 0, "type": "video", "track": 0 }]
    }
  ],
  "graph": {
    "nodes": [
      { "id": "pass", "type": "go_processor", "processor": "null" }
    ],
    "edges": [
      { "from": "src:v:0", "to": "pass:default", "type": "video" },
      { "from": "pass:default", "to": "out:v", "type": "video" }
    ]
  },
  "outputs": [
    { "id": "out", "url": "output.mp4", "codec_video": "libx264" }
  ]
}
```

### Frame counting with periodic metadata

```json
{
  "schema_version": "1.1",
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
        "id": "counter",
        "type": "go_processor",
        "processor": "frame_counter",
        "params": { "log_every": 50 }
      }
    ],
    "edges": [
      { "from": "src:v:0", "to": "counter:default", "type": "video" },
      { "from": "counter:default", "to": "out:v", "type": "video" },
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

### Chained processors (filter → processor → encoder)

```json
{
  "schema_version": "1.1",
  "inputs": [
    {
      "id": "src",
      "url": "input.mp4",
      "streams": [{ "input_index": 0, "type": "video", "track": 0 }]
    }
  ],
  "graph": {
    "nodes": [
      {
        "id": "scale",
        "type": "filter",
        "filter": "scale",
        "params": { "w": "1280", "h": "720" }
      },
      {
        "id": "analyse",
        "type": "go_processor",
        "processor": "frame_counter",
        "params": { "log_every": 1 }
      }
    ],
    "edges": [
      { "from": "src:v:0", "to": "scale:default", "type": "video" },
      { "from": "scale:default", "to": "analyse:default", "type": "video" },
      { "from": "analyse:default", "to": "out:v", "type": "video" }
    ]
  },
  "outputs": [
    { "id": "out", "url": "output_720p.mp4", "codec_video": "libx264" }
  ]
}
```

### Custom AI processor

This example shows how you'd wire a custom YOLO object detector into a graph. The processor itself is Go code you write; the JSON just tells MediaMolder where it sits in the graph:

```json
{
  "schema_version": "1.1",
  "inputs": [
    {
      "id": "cam",
      "url": "rtsp://camera.local/stream",
      "streams": [{ "input_index": 0, "type": "video", "track": 0 }]
    }
  ],
  "graph": {
    "nodes": [
      {
        "id": "detect",
        "type": "go_processor",
        "processor": "yolo_v8_detector",
        "params": {
          "model": "/models/yolov8n.onnx",
          "conf": 0.5,
          "device": "cuda:0",
          "labels_file": "/models/coco.names"
        }
      }
    ],
    "edges": [
      { "from": "cam:v:0", "to": "detect:default", "type": "video" },
      { "from": "detect:default", "to": "out:v", "type": "video" }
    ]
  },
  "outputs": [
    { "id": "out", "url": "detected.mp4", "codec_video": "libx264" }
  ]
}
```

The `yolo_v8_detector` name must be registered in your Go code before the graph runs:

```go
processors.Register("yolo_v8_detector", func() processors.Processor {
    return &YOLODetector{}
})
```

Inside `YOLODetector.Process()`, you'd use `FrameToFloat32Tensor` to prepare the frame, run your ONNX model, then return the detections as `*Metadata`.

---

## Vidi 2.5 multimodal analysis

The `vidi_analyzer` processor integrates [Vidi 2.5](https://github.com/bytedance/vidi) — a 9B-parameter multimodal LMM — as a first-class graph node. It batches decoded video frames, encodes them as JPEG, and POSTs them to a FastAPI inference service. Results are published as structured `Metadata`.

See the dedicated [Vidi 2.5 Guide](vidi-guide.md) for full setup instructions, Python service template, task reference, and performance tips.

---

## TwelveLabs video understanding

The `twelvelabs_*` processors connect MediaMolder graphs to the [TwelveLabs Video Understanding API](https://docs.twelvelabs.io/v1.3/api-reference/introduction) (Marengo + Pegasus). They cover four common operations:

- `twelvelabs_indexer` — upload completed segments / files into an index.
- `twelvelabs_analyzer` — Pegasus captions / summaries / chapters.
- `twelvelabs_searcher` — Marengo natural-language search.
- `twelvelabs_embedder` — Marengo video embeddings (inline or to disk).

The nodes consume `events`-kind edges from a `segment_sink` (or any node that emits `SegmentCompleted`) and post to the REST API in the background, then emit structured results on the event bus. The `mediamolder twelvelabs` CLI and the `/api/twelvelabs/*` HTTP routes wrap the same client for ad-hoc operations.

Full guide: [docs/twelvelabs.md](twelvelabs.md).

---

## YOLOv8 built-in processor (optional)

When built with the `with_onnx` build tag, MediaMolder includes a ready-to-use `yolo_v8` processor that runs YOLOv8 object detection via [ONNX Runtime](https://onnxruntime.ai/).

### Building with ONNX support

You need the ONNX Runtime shared library installed on your system. Then:

```bash
go build -tags with_onnx ./cmd/mediamolder
```

Set `ONNXRUNTIME_SHARED_LIBRARY_PATH` to the library location, or pass it via the `ort_lib` param.

### JSON config

```json
{
  "id": "detect",
  "type": "go_processor",
  "processor": "yolo_v8",
  "params": {
    "model": "/models/yolov8n.onnx",
    "conf": 0.5,
    "iou": 0.45,
    "input_size": 640,
    "num_classes": 80,
    "labels_file": "/models/coco.names",
    "device": "cuda"
  }
}
```

### Parameters

| Param         | Type   | Default      | Description                                              |
|---------------|--------|--------------|----------------------------------------------------------|
| `model`       | string | (required)   | Path to the YOLOv8 `.onnx` model file                   |
| `conf`        | float  | 0.5          | Minimum confidence threshold for detections              |
| `iou`         | float  | 0.45         | IoU threshold for NMS (non-maximum suppression)          |
| `input_size`  | int    | 640          | Model input dimension (640 for YOLOv8n/s/m/l/x)         |
| `num_classes` | int    | 80           | Number of classes the model detects (80 for COCO)        |
| `labels_file` | string | —            | Newline-separated file mapping class index to label name |
| `input_name`  | string | `"images"`   | ONNX input tensor name                                   |
| `output_name` | string | `"output0"`  | ONNX output tensor name                                  |
| `ort_lib`     | string | (env var)    | Path to onnxruntime shared library                       |
| `device`      | string | `"cpu"`      | `"cpu"` or `"cuda"` for GPU acceleration                 |
| `process_every`| int   | `1`          | Run inference every N-th frame; others pass through      |

### What it does

1. Letterboxes the frame to `input_size × input_size` and converts it to a `[1, 3, H, W]` float32 tensor.
2. Runs ONNX inference using pre-allocated tensors (zero allocation per frame).
3. Parses the YOLOv8 transposed output `[1, 4+num_classes, num_predictions]`.
4. Applies greedy NMS to remove duplicate detections.
5. Maps bounding boxes back from model coordinates to original frame pixel coordinates (reversing the letterbox transform).
6. Returns the frame unchanged plus `*Metadata` containing the detections.

The post-processing code (`ParseYOLOv8Output`, `NMS`, `IoU`) lives in `processors/yolov8.go` with no external dependencies, so it compiles and is testable without ONNX Runtime installed.

---

## Schema version

If your graph JSON includes any `go_processor` node, set `"schema_version": "1.1"` at the top level. Existing graphs that only use `filter`, `encoder`, `source`, and `sink` nodes continue to work unchanged with `"1.0"`. The parser accepts both versions.
