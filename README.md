# MediaMolder

A modern, Go-native media processing engine built on open source media libraries.

FFmpeg is an incredible open source project. It is used to process audio, video and images at a global scale, and it's known for its reliability and performance.

FFmpeg has two distinct layers: 
- an **interface / orchestration layer** that provides a Command Line Interface (CLI), parses command strings, builds a media processing graph (pipeline), and runs the pipeline until all processing is completed, and
- a set of **media processing libraries** (libavcodec, libavformat, libavfilter, etc.) that do the actual media processing (container file parsing, analysis, demuxing, decoding, filtering, encoding, and muxing).

### 1. Project Overview
MediaMolder is an independent, open-source media processing engine written in Go. It provides a new interface/orchestration layer, built on top of the same battle-tested libraries that power FFmpeg; replacing the FFmpeg command-line interface with a clean, declarative JSON defining each job. Mediamolder includes a cross-platform graphical user interface that runs in your web browser, letting you create, edit and run media graphs.
![MediaMolder User Interface](docs/images/ABR_x264.png)
It is not a wrapper around the ffmpeg binary; it is a ground-up redesign of the high-level engine that retains full media conversion capability through direct libav* bindings.

Version 1.x should be considered experimental.

### 2. Goals

- **Deliver a modern media processing engine** that improves orchestration, usability, execution, observability, and reliability.
- **Provide a fully declarative, version-controlled configuration model** using JSON pipeline files and native Go structs.
- **Significantly improve usability with an intuitive graphical user interface**,
  including a live **Hardware Capabilities dialog** that shows every available
  GPU/accelerator backend, its supported encode/decode codecs grouped by media
  type, and any unavailable backends with diagnostic messages.
- **Preserve all of FFmpeg’s modern media capabilities** (formats, codecs, filters, devices, bitstream filters) via direct, zero-overhead libav* bindings.
	- Some older (obsolete) features will be deprecated.
- **Support custom processor nodes** inside any media processing pipeline — no rebuilds, no C code, no cryptic filtergraph hacks (see below).
- **Replace error-prone CLI strings and cryptic filtergraphs** with a single, structured, validated JSON defining each job. This...
  - Eliminates command-line escaping nightmares and length limits.
  - Enables programmatic construction, validation, storage in databases, diffing, and versioning.
  - Treats pipelines as data, not opaque strings — making them introspectable and machine-friendly.
- **Improve metadata generation, extraction, and propagation** throughout the processing graph.
- **Offer first-class runtime observability, dynamic control, and resilience**. This is especially important for live streams and long-running jobs (metrics, tracing, graceful restarts, etc.).
- **Achieve near-identical performance to native FFmpeg** — Go’s orchestration layer adds negligible overhead since all heavy lifting stays in the libav* libraries.
- **Make the engine trivially embeddable** as a lightweight Go library in any application.
- **Remain fully LGPL compliant** (see [LICENSING.md](LICENSING.md)).
- **Enable easy migration from the FFmpeg CLI** with a robust FFmpeg command to MediaMolder JSON converter and detailed migration guide (see [FFmpeg Migration Guide](docs/ffmpeg-migration-guide.md)).
- **Manage the project openly and fairly** to attract and retain like-minded contributors who value clean APIs, reliability, and developer experience.

### 3. Non-Goals
- Competing with the FFmpeg project. 
  - There is no intent or desire to fork or manage development of the media processing libraries that power FFmpeg. 
- Rewriting existing codec or filter processing libraries in Go.

## Prerequisites

- **Go 1.23+**
- **FFmpeg 8.1+** (libavcodec 62.x, libavformat 62.x, libavfilter 11.x, libavutil 60.x)
  - Either a system install (via Homebrew, apt, etc.) with `pkg-config` available, **or** a source build in a sibling directory (see static build below)
- **pkg-config** (if using system FFmpeg)
- **Git LFS** (for the media test corpus, when available): `git lfs install`

## Build / Install

See [Build & Packaging](docs/build_and_packaging.md) 
For detailed instructions see [MacOS](docs/build/macos.md), [Windows](docs/build/windows.md) and [Linux](docs/build/linux.md)

## Documentation

### Usage

- [Using MediaMolder (CLI & GUI guide)](docs/using_mediamolder.md)
- [Concepts — Graph Model, Nodes, Edges, Lifecycle](docs/concepts-and-graph-basics.md)
- [FFmpeg Migration Guide](docs/ffmpeg-migration-guide.md)
- [Validation](docs/graph_validation_design.md)
- [JSON Config Reference](docs/json-config-reference.md)
- [Export to FFmpeg CLI](docs/export.md)
- [Visual Editor (GUI)](docs/gui.md)
- [Go Processor Nodes](docs/go-processor-nodes.md)
- [Yolov8 object detection/classification](docs/yolov8-guide.md)

### Code

- [Architecture](docs/architecture/architecture.md)
- [Pipeline State Machine](docs/graph-state-machine.md)
- [Pipeline Instrumentation Roadmap](docs/pipeline-instrumentation-roadmap.md)
- [Clock & Sync](docs/clock-and-sync.md)
- [Event Bus](docs/event-bus.md)
- [Error Handling](docs/error-handling.md)
- [Hardware Acceleration](docs/hardware-acceleration.md)
- [Observability](docs/observability.md) — Prometheus metrics, OpenTelemetry tracing, per-node performance monitoring, `mediamolder perf` CLI
- [Graph Compilation](docs/graph-compilation.md)

### Project

- [Contribution & Governance](docs/contribution_and_governance.md)
- [Project Specification](docs/specification.md)
- [Licensing](LICENSING.md)
