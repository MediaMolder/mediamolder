# Go Processor Nodes

The `go_processor` node type lets you insert **custom Go code** as a first-class node in a MediaMolder processing graph. Processors receive decoded `*av.Frame` values (video or audio) directly from upstream nodes, and their output feeds into downstream filters, encoders, or other processors — all within the same process, with zero external dependencies.

## Contents

- [When to use a go_processor](#when-to-use-a-go_processor)
- [JSON config](#json-config)
- [Go interface](#go-interface)
- [Registration](#registration)
- [Built-in processors](#built-in-processors)
- [Writing a custom processor](#writing-a-custom-processor)
- [Metadata and the event bus](#metadata-and-the-event-bus)
- [Error handling](#error-handling)
- [Lifecycle](#lifecycle)
- [Performance tips](#performance-tips)
- [Examples](#examples)
- [Schema version](#schema-version)

---

## When to use a go_processor

Use `go_processor` when:

- You need per-frame logic that libavfilter does not provide (AI inference, custom analytics, metadata injection).
- You want to run stateful algorithms across frames (object tracking, scene-change detection, running averages).
- You want to emit structured metadata (detections, quality scores) on the pipeline event bus.
- You want to drop or conditionally forward frames (content gating, deduplication).

Use a regular `"filter"` node for anything that libavfilter already does well (scaling, colour conversion, overlays, audio mixing, etc.).

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

Every processor implements the `processors.Processor` interface defined in the `processors` package:

```go
package processors

type Processor interface {
    // Init is called once during graph construction, before the first frame.
    Init(params map[string]any) error

    // Process is called for every frame on the node's input.
    // Return the (possibly modified) frame and optional metadata.
    // Return nil frame to drop it entirely.
    Process(frame *av.Frame, ctx ProcessorContext) (*av.Frame, *Metadata, error)

    // Close is called once on pipeline shutdown.
    Close() error
}
```

### ProcessorContext

```go
type ProcessorContext struct {
    StreamID   string          // e.g. "v:0" — the node ID
    MediaType  av.MediaType    // video, audio, subtitle
    PTS        int64           // presentation timestamp
    FrameIndex uint64          // zero-based frame counter
    Context    context.Context // carries cancellation
}
```

### Metadata

```go
type Metadata struct {
    Detections   []Detection    `json:"detections,omitempty"`
    QualityScore float64        `json:"quality_score,omitempty"`
    Custom       map[string]any `json:"custom,omitempty"`
}

type Detection struct {
    Label      string     `json:"label"`
    Confidence float64    `json:"confidence"`
    BBox       [4]float64 `json:"bbox"` // x1, y1, x2, y2
    TrackID    int        `json:"track_id,omitempty"`
}
```

---

## Registration

Register a processor factory before the pipeline starts — typically in `init()` or `main()`:

```go
import "github.com/MediaMolder/MediaMolder/processors"

func init() {
    processors.Register("my_proc", func() processors.Processor {
        return &MyProcessor{}
    })
}
```

The factory returns a **new instance** per pipeline node. This means different nodes can safely hold independent state.

List registered processors from the CLI:

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

When `Process()` returns non-nil `*Metadata`, the runtime publishes a `ProcessorMetadataEvent` on the pipeline event bus:

```go
type ProcessorMetadataEvent struct {
    NodeID     string
    FrameIndex uint64
    PTS        int64
    Metadata   *Metadata
}
```

Consumers read events from the bus:

```go
for ev := range pipeline.Events() {
    switch e := ev.(type) {
    case processors.ProcessorMetadataEvent:
        fmt.Printf("node=%s frame=%d detections=%d\n",
            e.NodeID, e.FrameIndex, len(e.Metadata.Detections))
    }
}
```

This enables real-time dashboards, JSONL logging, or downstream decision-making based on processor output.

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

```
Pipeline created
    │
    ▼
processors.Get("name")       ← factory creates a fresh instance
    │
    ▼
processor.Init(params)        ← called once, before any frames
    │
    ▼
┌─────────────────────────┐
│ processor.Process(frame) │  ← called once per input frame
│       ... repeat ...     │
└─────────────────────────┘
    │
    ▼
processor.Close()             ← called once on shutdown
```

- Each `go_processor` node gets its own instance from the factory — safe for concurrent pipelines.
- `Process()` is called **serially** for a given node (no concurrent calls). Ordering is guaranteed.
- Stateful processors (trackers, running averages) can safely store state in struct fields.

---

## Performance tips

- **Avoid unnecessary copies.** If you don't modify the frame, return the same pointer.
- **Drop frames explicitly.** Return `(nil, md, nil)` to drop a frame without error.
- **Batch internally.** If your workload benefits from batching (e.g., GPU inference on N frames), accumulate inside `Process()` and emit results when the batch is full.
- **Respect cancellation.** Check `ctx.Context` for long-running operations to avoid blocking pipeline shutdown.
- **Keep Init() fast.** Heavy model loading in `Init()` blocks pipeline startup. Consider lazy initialization on the first `Process()` call if appropriate.

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

### Custom processor with AI inference (user-implemented)

This example shows how a user-provided YOLO detector would be wired:

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

The `yolo_v8_detector` processor would be registered in the user's application:

```go
processors.Register("yolo_v8_detector", func() processors.Processor {
    return &YOLODetector{}
})
```

---

## Schema version

- Pipelines using only `filter`, `encoder`, `source`, `sink` nodes continue to use `"schema_version": "1.0"`.
- Pipelines with any `go_processor` node should use `"schema_version": "1.1"`.
- The parser accepts both versions; `"1.0"` configs remain fully backward-compatible.
