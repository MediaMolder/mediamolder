# MediaMolder

A modern, Go-native media processing engine built on open source media libraries.

FFmpeg is an incredible open source project, with two distinct layers: 
- an **interface / orchestration layer** that provides a Command Line Interface (CLI), parses command strings, builds a media processing graph (pipeline), and runs the pipeline until all processing is completed, and
- a set of **media processing libraries** (libavcodec, libavformat, libavfilter, etc.) that do the actual media processing (container file parsing, analysis, demuxing, decoding, filtering, encoding, and muxing).

### 1. Project Overview
MediaMolder is an independent, open-source media processing engine written in Go. It provides a new orchestration layer on top of the same battle-tested libraries that power FFmpeg; replacing the CLI's command-line-driven, string-based architecture with a clean, declarative JSON pipeline model. It is not a wrapper around the ffmpeg binary; it is a ground-up redesign of the high-level engine that retains full media conversion capability through direct libav* bindings.

Version 1.x should be considered experimental.

### 2. Project Goals

- **Deliver a modern media processing engine** that improves orchestration, usability, execution, observability, and reliability.
- **Preserve 100% of FFmpeg’s media capabilities** (formats, codecs, filters, devices, bitstream filters) via direct, zero-overhead libav* bindings.
- **Support custom processor nodes** inside any media processing pipeline — no rebuilds, no C code, no cryptic filtergraph hacks (see below).
- **Provide a fully declarative, version-controlled configuration model** using JSON pipeline files and native Go structs.
- **Replace error-prone CLI strings and cryptic filtergraphs** with a single, structured, validated JSON defining each job. This...
  - Eliminates command-line escaping nightmares and length limits.
  - Enables programmatic construction, validation, storage in databases, diffing, and versioning.
  - Treats pipelines as data, not opaque strings — making them introspectable and machine-friendly.
- **Greatly improve metadata generation, extraction, and propagation** throughout the processing graph.
- **Offer first-class runtime observability, dynamic control, and resilience**. This is especially important for live streams and long-running jobs (metrics, tracing, graceful restarts, etc.).
- **Achieve near-identical performance to native FFmpeg** — Go’s orchestration layer adds negligible overhead since all heavy lifting stays in the libav* libraries.
- **Make the engine trivially embeddable** as a lightweight Go library in any application.
- **Remain fully LGPL compliant** (see [LICENSING.md](LICENSING.md)).
- **Lower the barrier to adoption** with a robust FFmpeg CLI → MediaMolder JSON migration parser and detailed migration guide (see [FFmpeg Migration Guide](docs/ffmpeg-migration-guide.md)).
- **Manage the project openly and fairly** to attract and retain like-minded contributors who value clean APIs, reliability, and developer experience.

#### How MediaMolder compares to FFmpeg when you need custom functionality in your media pipeline

| Aspect                          | FFmpeg (traditional)                                                                 | MediaMolder                                                                 |
|---------------------------------|--------------------------------------------------------------------------------------|-----------------------------------------------------------------------------|
| Adding custom logic (AI, OpenCV, metadata enrichment, etc.) | Write a custom `AVFilter` in C, register it in libavfilter, then **rebuild FFmpeg from source** (including linking external libs like OpenCV or ONNX Runtime). | Write a pure-Go struct that implements a simple `Processor` interface. Register it once at startup. |
| Integration effort              | High: C expertise, FFmpeg build system, dependency hell, custom configure flags (`--enable-libopencv`, etc.). | Low: Standard Go code + `go get`. Use existing Go libraries (GoCV, ONNX Runtime Go bindings, etc.). |
| Distribution & maintenance      | You ship a custom FFmpeg binary (or static build). Versioning and updates become painful. | Your custom nodes are compiled directly into your Go application/binary. One artifact, trivial updates. |
| Runtime flexibility             | Static. Custom filters are baked in at compile time. Runtime registration is not officially supported. | Fully dynamic: nodes can be enabled/disabled via JSON config. Live pipelines can swap or hot-reload logic. |
| Performance                     | Excellent (native C), but the rebuild tax is heavy.                                      | Near-native: heavy lifting still happens in libav* bindings; your Go node sits in the orchestration layer. |

#### What you actually have to do in FFmpeg today
- Follow the official `doc/writing_filters.txt`.
- Implement `AVFilter`, `AVFilterPad`, frame processing callbacks, etc.
- Modify FFmpeg’s build scripts to link your external library (OpenCV, TensorFlow, etc.).
- Recompile the entire libavfilter (or the whole FFmpeg suite).
- For AI/OpenCV workflows, most teams end up maintaining a private fork or using external tools (e.g., piping frames to a separate Python process), which destroys performance and reliability.

#### MediaMolder makes this trivial
You simply implement something like:

```go
type CustomAINode struct {
    ModelPath string
    // ...
}

func (n *CustomAINode) Process(ctx context.Context, frame *av.Frame) (*av.Frame, error) {
    // Run inference with your Go AI library, enrich metadata, draw overlays, etc.
    return frame, nil
}
```

Then register it once:

```go
mediamolder.RegisterProcessor("ai-inference", func() mediaprocessor.Processor { return &CustomAINode{} })
```

And declare it in your JSON pipeline exactly like any built-in node:

```json
{
  "nodes": [
    { "type": "decoder" },
    { "type": "ai-inference", "model": "yolov8.onnx", "confidence": 0.6 },
    { "type": "encoder" }
  ]
}
```

This opens the door to:
- **AI / computer vision** (object detection, segmentation, pose estimation, super-resolution)
- **Metadata enrichment** (scene classification, OCR, custom EXIF, ML-based quality scoring)
- **Business logic** (watermarking with dynamic data, ad insertion, compliance checks)
- **Hardware-specific nodes** (GPU kernels, edge TPU, custom DSP)

All while staying fully inside the same declarative JSON pipeline and benefiting from MediaMolder’s observability, resilience, and embeddability.

This is the single biggest productivity leap over raw FFmpeg for teams building production media platforms.


### 3. Non-Goals
- Competing with the FFmpeg project. 
  - There is no intent or desire to fork or manage development of the media processing libraries that power FFmpeg. 
- Rewriting existing codec or filter processing libraries in Go.

---

## Prerequisites

- **Go 1.23+**
- **FFmpeg 8.1+** (libavcodec 62.x, libavformat 62.x, libavfilter 11.x, libavutil 60.x)
  - Either a system install (via Homebrew, apt, etc.) with `pkg-config` available, **or** a source build in a sibling directory (see static build below)
- **pkg-config** (if using system FFmpeg)
- **Git LFS** (for the media test corpus, when available): `git lfs install`

---

## Installation

### From source

```sh
git clone https://github.com/MediaMolder/mediamolder.git
cd mediamolder
```

**Using system FFmpeg (via pkg-config):**
```sh
go build ./...
```

**Using a local FFmpeg source build (static linking):**

Place your FFmpeg source tree as a sibling directory (i.e. `../ffmpeg` relative to the mediamolder checkout), then build with the `ffstatic` tag:
```sh
go build -tags=ffstatic ./...
```

**Install the CLI:**
```sh
go install ./cmd/mediamolder
```

---

## Quickstart

Create a job JSON file. See [docs/json-config-reference.md](docs/json-config-reference.md)≠≠
Some examples are below, with additional example job JSONs in [testdata/examples](testdata/examples/)

`transcode.json`:
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
        "params": { "w": "1280", "h": "720" }
      }
    ],
    "edges": [
      { "from": "src:v:0",  "to": "scale",  "type": "video" },
      { "from": "scale",    "to": "out:v",   "type": "video" },
      { "from": "src:a:0",  "to": "out:a",   "type": "audio" }
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

Run it:
```sh
mediamolder run transcode.json
```

Run with JSON progress output:
```sh
mediamolder run --json transcode.json
```

Inspect the resolved pipeline graph without running:
```sh
mediamolder inspect transcode.json
```

Convert an FFmpeg command to MediaMolder JSON:
```sh
mediamolder convert-cmd "ffmpeg -i input.mp4 -vf scale=1280:720 -c:v libx264 -c:a aac output.mp4"
```

List available codecs, filters, or formats:
```sh
mediamolder list-codecs
mediamolder list-filters
mediamolder list-formats
mediamolder list-processors
mediamolder list-codecs --json   # JSON output
```

### Go processor node example

Custom Go code can run as a first-class node in the processing graph.
Register a processor, then reference it from JSON:

```json
{
  "schema_version": "1.1",
  "inputs": [
    { "id": "src", "url": "input.mp4", "streams": [{"input_index": 0, "type": "video", "track": 0}] }
  ],
  "graph": {
    "nodes": [
      {
        "id": "count",
        "type": "go_processor",
        "processor": "frame_counter",
        "params": { "log_every": 100 }
      }
    ],
    "edges": [
      { "from": "src:v:0", "to": "count:default", "type": "video" },
      { "from": "count:default", "to": "out:v", "type": "video" }
    ]
  },
  "outputs": [
    { "id": "out", "url": "output.mp4", "codec_video": "libx264" }
  ]
}
```

See the [Go Processor Nodes](docs/go-processor-nodes.md) guide for the full API, built-in processors, and how to write your own.

### Multi-input overlay example

```json
{
  "schema_version": "1.0",
  "inputs": [
    { "id": "bg", "url": "background.mp4", "streams": [{"input_index": 0, "type": "video", "track": 0}] },
    { "id": "fg", "url": "overlay.png", "streams": [{"input_index": 0, "type": "video", "track": 0}] }
  ],
  "graph": {
    "nodes": [
      { "id": "ov", "type": "filter", "filter": "overlay", "params": {"x": 10, "y": 10} }
    ],
    "edges": [
      { "from": "bg:v:0", "to": "ov:default", "type": "video" },
      { "from": "fg:v:0", "to": "ov:overlay", "type": "video" },
      { "from": "ov:default", "to": "out:v", "type": "video" }
    ]
  },
  "outputs": [
    { "id": "out", "url": "composited.mp4", "codec_video": "libx264" }
  ]
}
```

### Multi-output (adaptive bitrate) example

```json
{
  "schema_version": "1.0",
  "inputs": [
    { "id": "src", "url": "input.mp4", "streams": [{"input_index": 0, "type": "video", "track": 0}] }
  ],
  "graph": {
    "nodes": [
      { "id": "split", "type": "filter", "filter": "split" },
      { "id": "hd", "type": "filter", "filter": "scale", "params": {"w": "1920", "h": "1080"} },
      { "id": "sd", "type": "filter", "filter": "scale", "params": {"w": "640", "h": "480"} }
    ],
    "edges": [
      { "from": "src:v:0", "to": "split:default", "type": "video" },
      { "from": "split:out0", "to": "hd:default", "type": "video" },
      { "from": "split:out1", "to": "sd:default", "type": "video" },
      { "from": "hd:default", "to": "out_hd:v", "type": "video" },
      { "from": "sd:default", "to": "out_sd:v", "type": "video" }
    ]
  },
  "outputs": [
    { "id": "out_hd", "url": "output_1080p.mp4", "codec_video": "libx264" },
    { "id": "out_sd", "url": "output_480p.mp4", "codec_video": "libx264" }
  ]
}
```

---

## Documentation

- [JSON Config Reference](docs/json-config-reference.md)
- [Go Processor Nodes](docs/go-processor-nodes.md)
- [FFmpeg Migration Guide](docs/ffmpeg-migration-guide.md)
- [Pipeline State Machine](docs/pipeline-state-machine.md)
- [Clock & Sync](docs/clock-and-sync.md)
- [Event Bus](docs/event-bus.md)
- [Error Handling](docs/error-handling.md)
- [Hardware Acceleration](docs/hardware-acceleration.md)
- [Observability](docs/observability.md)
- [Graph Compilation](docs/graph-compilation.md)
- [Pipeline Instrumentation Roadmap](docs/pipeline-instrumentation-roadmap.md)
- [Build & Packaging](docs/build_and_packaging.md)
- [Contribution & Governance](docs/contribution_and_governance.md)
- [Project Specification](docs/specification.md)
- [Licensing](LICENSING.md)

---

## Architecture

### Processing Pipeline

A pipeline flows through five phases:

1. **Build** — Parse JSON config into a validated DAG (`graph.Build`). Catches structural errors (missing nodes, cycles).
2. **Compile** — Analyze the graph for stage grouping, dead-branch detection, disconnected-source warnings, and per-edge buffer sizing (`graph.Compile`). See [Graph Compilation](docs/graph-compilation.md).
3. **Open resources** — Demuxers, decoders, filters, encoders, and muxers are created in topological order.
4. **Execute** — The scheduler launches one goroutine per node, connected by buffered channels sized per-edge by the compiler. Each node processes frames independently.
5. **Finalize** — Outputs are flushed and atomically renamed.

### Performance Monitoring

MediaMolder instruments the runtime to help identify bottlenecks:

- **Channel backpressure monitoring** — A sampler goroutine periodically polls the fill level of every inter-node channel. High fill ratios indicate a downstream node can't keep up. Available via `Pipeline.EdgeStats()`.
- **Per-node processing latency** — Every handler (source, filter, encoder, sink, Go processor) records per-frame latency. Available via `Pipeline.Metrics()` snapshots (`AvgLatency`, `MaxLatency`).
- **Adaptive buffer sizing** — The compiler assigns per-edge channel buffer sizes based on node kinds (e.g., 16 after sources for burst absorption, 4 for encoder→sink). See [Graph Compilation](docs/graph-compilation.md).

Both monitoring mechanisms add zero overhead to the data path — backpressure uses periodic sampling (not channel wrapping), and latency uses lock-free atomics.

```go
// Identify the bottleneck edge:
for _, es := range pipe.EdgeStats().Snapshot() {
    if es.Fill > 0.8 {
        fmt.Printf("backpressure: %s → %s (%.0f%% full, %d stalls)\n",
            es.FromNode, es.ToNode, es.Fill*100, es.Stalls)
    }
}

// Check per-node latency:
for _, ns := range pipe.Metrics().Snapshot().Nodes {
    fmt.Printf("%s: avg=%s max=%s\n", ns.NodeID, ns.AvgLatency, ns.MaxLatency)
}
```

See [Observability](docs/observability.md) for Prometheus metrics, OpenTelemetry tracing, and Grafana dashboard configuration.

---

## License

LGPL-2.1-or-later. See [LICENSE](LICENSE) and [LICENSING.md](LICENSING.md) for details.
