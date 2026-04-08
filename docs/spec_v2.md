**MediaMolder: Project Specification**  
**Version:** 1.0 (Draft)  
**License:** LGPL-2.1 or later  
**Language:** Go (top-level orchestration and public APIs)  
**Underlying libraries:** Direct linking to libavcodec, libavformat, libavfilter, libavutil, libswscale, libswresample, and any hardware acceleration backends (same as FFmpeg)  
**Repository:** github.com/MediaMolder/MediaMolder (proposed)  
**CLI binary:** `MediaMolder`

### 1. Project Overview
MediaMolder is a **new, independent open-source media processing engine** written primarily in Go. It reuses the battle-tested libav* C libraries for all heavy lifting (demuxing, decoding, filtering, encoding, muxing, hardware acceleration) but replaces the entire command-line-driven, string-based architecture of FFmpeg with a clean, modern, Go-native design.

The goal is **maximum usability and operational reliability** while preserving 100 % of FFmpeg’s functional capabilities. It is **not** a wrapper or fork of the FFmpeg CLI; it is a ground-up redesign of the high-level pipeline layer.

### 2. Primary Objectives
- Eliminate command-line escaping hell and cryptic filtergraph strings.
- Provide a declarative, structured, version-controlled configuration model (**JSON as the primary command payload**, in-memory Go structs).
- Deliver first-class runtime observability, dynamic control, and resilience for long-running jobs.
- Make the engine trivially embeddable as a library in any Go program.
- Achieve developer ergonomics that attract a much larger contributor base than C-based FFmpeg.
- Maintain identical media capabilities (formats, codecs, filters, devices, bitstream filters, etc.) through direct libav* bindings.
- Remain fully LGPL compliant.
- Provide a function to parse any compliant FFMPEG command-line string, converting to a compliant JSON command payload

### 3. Non-Goals
- Pure-Go implementation of codecs/filters (performance and compatibility reasons).
- Replacing any libav* library.
- GUI or web UI (those can be built on top).

### 4. High-Level Architecture
```
+-------------------+     +---------------------+
|   Public API /    |<--->|  Control Plane      |
|   Library Layer   |     | (HTTP/CLI)          |
+-------------------+     +---------------------+
           |                       |
           v                       v
+-------------------+     +---------------------+
|   Pipeline Engine |<--->|  Observability      |
|   (Go)            |     |  & Metrics          |
+-------------------+     +---------------------+
           |
           v
+-------------------+
|   Binding Layer   |  (cgo + thin Go wrappers)
|   (libav* C API)  |
+-------------------+
           |
           v
       libavcodec / libavformat / libavfilter / ...
```

- **Pipeline Engine** (pure Go): Builds, validates, schedules, and supervises the graph.
- **Binding Layer**: Idiomatic Go wrappers over libav* (inspired by but independent of existing projects such as go-astiav). Uses cgo only where necessary; exposes Go types, channels, errors, and context cancellation.
- **Control Plane**: Live introspection and modification (pause, seek, reconfigure filters, add/remove outputs, etc.).
- **Execution Model**: Event-driven, multi-lane scheduler using Go goroutines + channels for packet/frame flow. Back-pressure is native.

### 5. Technical Stack
- **Language**: Go 1.23+ (modules, generics, structured concurrency via errgroup/context).
- **Build**: cgo enabled; static or dynamic linking of FFmpeg libs (pkg-config or vendored).
- **Configuration**: Viper + custom JSON Schema validation (**JSON as primary command payload**).
- **Serialization**: Native Go structs that can be marshaled to/from JSON (with optional YAML/TOML round-tripping).
- **Observability**: OpenTelemetry + Prometheus exporter (metrics, traces, logs).
- **Control API**: HTTP/JSON REST (JSON payloads).
- **Logging**: Zerolog or slog with structured context.
- **Testing**: Full integration tests against real media files + property-based testing.

### 6. Core Components (detailed)

#### 6.1 Binding Layer (`MediaMolder/av`)
- Thin, manually maintained Go wrappers for every public libav* API needed.
- Provides:
  - `av.FormatContext`, `av.CodecContext`, `av.FilterGraph`, `av.Frame`, `av.Packet`, etc.
  - Automatic memory management via finalizers + explicit `Close()` where needed.
  - Go error types with rich context (`av.Err` wrapping AVERROR codes).
  - Channel-based streaming APIs (`FrameChan`, `PacketChan`).
  - Hardware device context helpers (CUDA, VAAPI, QSV, etc.).
- All filter parameters are native Go structs (no string escaping).

#### 6.2 Pipeline Definition (`MediaMolder/pipeline`)
- **Pipeline** struct containing:
  - Inputs (array of `Input` with URL, format options, stream selection)
  - Graph (directed acyclic graph of nodes + explicit edges)
  - Outputs (array of `Output` with muxer, codec, metadata)
  - Global options (thread count, hardware accel, metadata, timestamps)
- Nodes are typed:
  - `SourceNode` (demux+decode)
  - `FilterNode` (any libavfilter)
  - `EncoderNode`
  - `SinkNode` (mux)
- Supports filter chains, multi-input/multi-output filters, split/merge, and labels via Go API only.
- Validation at build time (type checking of ports: video/audio/subtitle/data) using JSON Schema.

#### 6.3 Graph Engine (`MediaMolder/graph`)
- Builds libavfilter graph from the declarative Go model (loaded from JSON command payload).
- Supports **static** and **dynamic** graphs:
  - Dynamic: add/remove/replace nodes at runtime via control plane.
  - Hot-reconfiguration of filter parameters (e.g., change drawtext string live).

#### 6.4 Runtime Scheduler (`MediaMolder/runtime`)
- Work-stealing scheduler with dedicated lanes per output stream.
- Native Go channels for frame/packet flow with configurable buffering.
- Automatic back-pressure and flow control.
- Watchdog timers for stalled stages.
- Graceful drain on shutdown.

#### 6.5 Control & Observability Plane
- Per-pipeline HTTP/JSON control server (or embedded).
- Endpoints: `Pause`, `Resume`, `Seek`, `Reconfigure`, `AddOutput`, `InjectEvent`, `GetMetrics`, `GetGraphSnapshot`.
- Built-in metrics: fps, bitrate, latency per node, buffer levels, CPU/GPU usage, errors.
- Structured events for every major lifecycle step.

### 7. Configuration Example (JSON – Primary Command Payload)
```json
{
  "version": "v1",
  "inputs": [
    {
      "id": "main",
      "url": "input.mkv",
      "stream_selection": "0:v:0,0:a:0"
    }
  ],
  "graph": {
    "nodes": [
      {
        "id": "scale",
        "type": "filter",
        "filter": "scale",
        "params": {
          "width": 1280,
          "height": 720,
          "flags": "bicubic"
        }
      },
      {
        "id": "drawtext",
        "type": "filter",
        "filter": "drawtext",
        "params": {
          "text": "Live at {{localtime}}",
          "fontfile": "/path/to/font.ttf"
        }
      }
    ]
  },
  "outputs": [
    {
      "id": "hls",
      "url": "output.m3u8",
      "format": "hls",
      "codec_video": "libx264",
      "codec_audio": "aac",
      "options": {
        "hls_time": 4,
        "hls_list_size": 0
      }
    }
  ]
}
```

### 8. Reliability Features
- Stage-level isolation (separate goroutine groups with panic recovery).
- Granular error policies per node (skip, retry, fallback, abort).
- Transactional file output (write to `.tmp` then atomic rename).
- Automatic node restart on transient failures.
- Context cancellation propagates cleanly through the entire pipeline.
- Comprehensive crash reports with graph snapshot.

### 9. Library Usage Example
```go
p, err := MediaMolder.NewPipeline(ctx, config)
if err != nil { ... }

err = p.Start()
defer p.Close()

// Live control
go controlLoop(p)

// Blocking run until finished
err = p.Wait()
```

### 10. CLI Tool (`MediaMolder`)
- `MediaMolder run config.json [--metrics-addr=:9090] [--control-addr=:8080]`
- `MediaMolder inspect config.json`
- `MediaMolder list-filters`, `list-codecs`, etc.
- Real-time progress and JSON status output.

### 11. Build & Packaging
- Official binaries for Linux/macOS/Windows (statically linked where possible).
- Docker images with common FFmpeg library sets.
- pkg-config based build for custom libav* installations.
- Clear documentation on how to vendor or point to a specific FFmpeg build.

### 12. Development Roadmap (Phased)
**Phase 0 (MVP)**: Core binding layer + simple input→filter→output pipeline.  
**Phase 1**: Full declarative graph (JSON primary), CLI, basic control plane.  
**Phase 2**: Observability, dynamic reconfiguration, reliability features.  
**Phase 3**: Hardware accel parity, advanced filters, bitstream filters, subtitles.  
**Phase 4**: Production hardening, community tools, language bindings (Python, Rust via FFI).

### 13. Contribution & Governance
- Standard GitHub flow + CLA (LGPL compatible).
- High emphasis on documentation and examples.
- Automated integration test suite against a large media corpus.
- Encourages contributions in Go; C changes only in the binding layer when absolutely necessary.

This specification gives the project a clean, modern foundation that directly solves the usability and reliability pain points of the current FFmpeg core while retaining every media capability through proven libraries. The Go top layer dramatically lowers the barrier for new contributors and makes embedding powerful media pipelines into larger applications trivial. JSON as the command payload ensures maximum portability, strict validation, and seamless integration with modern tooling and APIs.

### 14. Possible Future Improvements
- **gRPC Control API**: Add a gRPC server (with protobuf definitions) as an alternative to the HTTP/JSON control plane. gRPC offers strongly-typed service contracts, bidirectional streaming for real-time metrics/events, and efficient binary serialization — well-suited for high-frequency control interactions and language-agnostic client generation.
- **Language bindings**: Python, Rust, and other language bindings via FFI or gRPC client stubs.
- **Web UI / Dashboard**: A browser-based interface for pipeline monitoring and control built on top of the HTTP or gRPC APIs.