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
		- [`metadata_file_writer`](#metadata_file_writer)
	- [Persisting metadata to files](#persisting-metadata-to-files)
		- [CLI: --metadata-out](#cli---metadata-out)
		- [Go API: custom event consumer](#go-api-custom-event-consumer)
	- [Helper functions](#helper-functions)
		- [Letterbox](#letterbox)
		- [ImageToFloat32Tensor](#imagetofloat32tensor)
		- [DrawDetections](#drawdetections)
		- [FrameToRGBA / FrameToFloat32Tensor](#frametorgba--frametofloat32tensor)
		- [When to use what](#when-to-use-what)
		- [Example](#example)
	- [Writing a custom processor](#writing-a-custom-processor)
		- [Step 1: Implement the interface](#step-1-implement-the-interface)
		- [Step 2: Register it](#step-2-register-it)
		- [Step 3: Use it in JSON](#step-3-use-it-in-json)
	- [Metadata and the event bus](#metadata-and-the-event-bus)
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
	- [Schema version](#schema-version)

---

## When to use a go_processor

Use `go_processor` when you need to do something that FFmpeg's built-in filters can't:

- **AI inference** — run an object detection model (YOLO, SSD), speech recogniser (Whisper), or image quality scorer (BRISQUE) on each frame.
- **Stateful analysis** — track objects across frames, detect scene changes, compute running averages.
- **Structured metadata** — emit detections, quality scores, or custom key-value data on the pipeline event bus so other parts of your application can react in real time.
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
```

| Method | When it runs | What to do |
|--------|-------------|------------|
| `Init` | Once, before the first frame arrives. | Read your config from `params`, load models, allocate buffers. Return an error to abort the pipeline. |
| `Process` | Once per frame. | Inspect or modify the frame, run your logic, return the frame (or a new one) plus optional metadata. Return a `nil` frame to drop it. |
| `Close` | Once, when the pipeline shuts down (even after errors). | Release resources, flush buffers, close files. |

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

If your processor produces results (detections, scores, analytics), return them as `*Metadata`. The runtime automatically publishes non-nil metadata on the pipeline event bus so the rest of your application can consume it.

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

The factory is called once per `go_processor` node in the pipeline, so if your JSON config has three nodes using `"my_proc"`, three separate instances are created. Each holds its own state — no shared-state concurrency issues to worry about.

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

### `metadata_file_writer`

A decorator processor that wraps another processor and writes all emitted metadata to a [JSON Lines](https://jsonlines.org/) file. The inner processor's metadata is still returned normally (so it also reaches the event bus).

| Param              | Type   | Default      | Description                                          |
|--------------------|--------|--------------|------------------------------------------------------|
| `output_file`      | string | **(required)**| Path to the `.jsonl` output file                     |
| `inner_processor`  | string | **(required)**| Name of a registered processor to wrap               |
| *(other params)*   |        |              | Forwarded to the inner processor's `Init()`          |

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

Frames where the inner processor returns no metadata produce no output line.

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
  ├─ preprocessing:  Letterbox / ImageToFloat32Tensor  → feed to AI model
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

## Metadata and the event bus

Whenever your `Process()` method returns a non-nil `*Metadata`, the runtime automatically posts it to the pipeline's event bus. You don't need to do anything special — just return the metadata and it's published.

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
mediamolder run --metadata-out detections.jsonl pipeline.json
```

Use `-` to write to stdout (useful for piping):

```bash
mediamolder run --metadata-out - pipeline.json 2>/dev/null | jq '.metadata.detections[]'
```

This captures metadata from **all** `go_processor` nodes in the pipeline. Each line includes `node_id` so you can filter by source.

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

- **`Init()` error** → pipeline creation aborts (same as an invalid filter).
- **`Process()` error** → respects the node's `error_policy`:
  - `"abort"` (default): pipeline stops immediately.
  - `"skip"`: frame is dropped, processing continues.
  - `"retry"`: frame is re-submitted up to `max_retries` times.
  - `"fallback"`: reroute to `fallback_node`.
- **`Close()`** is called unconditionally during shutdown, even after errors.

---

## Lifecycle

A processor goes through three phases, always in this order:

```
Pipeline starts
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
- **Close is guaranteed.** Even if `Process()` returns an error or the pipeline is cancelled, `Close()` still runs.

---

## Performance tips

- **Return the same frame pointer** if you didn't modify it. Creating a new frame when you only needed to read it wastes memory and CPU on copying.
- **Use the provided helpers** (`ImageToFloat32Tensor`, `Letterbox`) instead of writing your own preprocessing. They're tested, correct, and safe for concurrent use.
- **Drop frames by returning nil.** `return nil, md, nil` tells the runtime to consume the frame. No error, no forwarding — the frame just stops here.
- **Batch if your model wants it.** If GPU inference is faster on N frames at once, accumulate frames in a buffer inside `Process()` and emit results when the batch is full.
- **Check `ctx.Context` for cancellation.** If your processing is slow (e.g. large model inference), periodically check `ctx.Context.Done()` so the pipeline can shut down promptly.
- **Keep `Init()` fast.** It runs before the pipeline starts, so slow model loading delays everything. For very large models, consider lazy-loading on the first `Process()` call.

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

This example shows how you'd wire a custom YOLO object detector into a pipeline. The processor itself is Go code you write; the JSON just tells MediaMolder where it sits in the graph:

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

The `yolo_v8_detector` name must be registered in your Go code before the pipeline runs:

```go
processors.Register("yolo_v8_detector", func() processors.Processor {
    return &YOLODetector{}
})
```

Inside `YOLODetector.Process()`, you'd use `ImageToFloat32Tensor` to prepare the frame, run your ONNX model, then return the detections as `*Metadata`.

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

If your pipeline JSON includes any `go_processor` node, set `"schema_version": "1.1"` at the top level. Existing pipelines that only use `filter`, `encoder`, `source`, and `sink` nodes continue to work unchanged with `"1.0"`. The parser accepts both versions.
