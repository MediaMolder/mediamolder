# FFmpeg

FFmpeg is an incredible open source project. It is used to process audio, video and images at a global scale, and it's known for its reliability and performance.

FFmpeg has two distinct layers: 
- an **interface / orchestration layer** that provides a Command Line Interface (CLI), parses command strings, builds a media processing graph, and runs the graph, and
- a collection of **media processing libraries** (libavcodec, libavformat, libavfilter, etc.) that do the actual media processing (container file parsing, analysis, demuxing, decoding, filtering, encoding, and muxing).

# MediaMolder

**MediaMolder** is a modern, open-source media processing engine, written in Go. 
It is a ground-up redesign of the interface and orchestration layers of FFmpeg. The 
goal is to match FFmpeg's functional requirements, while delivering significant 
improvements in non-functional requirements such as usability, observability, 
maintainability, extensibility, portability and security. While you might initially 
think of MediaMolder as "FFmpeg for Dummies"... its advanced capabilities make it 
more like "FFmpeg for Smarties".

MediaMolder is built on the same proven libav\* libraries (libavcodec, libavformat,
libavfilter, x264, x265, etc.) that power FFmpeg. It is **not** a wrapper around the 
`ffmpeg` binary. The entire orchestration layer — graph construction, scheduling, 
error handling, hardware wiring, metadata propagation — is rewritten from scratch in 
Go, with direct zero-copy bindings to the same libav\* libraries that FFmpeg uses.

---

## Why use MediaMolder?

### Visual editor

FFmpeg runs media processing graphs, but until now you were forced to visualize 
those graphs in your head. MediaMolder can import your FFmpeg command-line, 
enabling you to view, edit, validate, and run your graph with detailed performance 
metrics. The MediaMolder Graphical User Interface (GUI) is a fluid, 
drag-and-drop graph editor that runs in your web browser. The GUI is launched
from the mediamolder binary by the `gui` subcommand (run `./mediamolder gui`). 
![MediaMolder User Interface](docs/images/ABR_x264.png)

- Build encode graphs by dragging filters, encoders, sources, and sinks onto
  a canvas and wiring them by stream type. Mismatched types (video → audio
  input) are rejected at the handle level.
- The Inspector surfaces typed forms for every node: encoder rate-control
  modes, HLS/DASH delivery wizards, bitstream-filter chains, chapter and
  container metadata editors, per-stream disposition and language overrides,
  audio channel routing.
- Hover any edge to see every technical property MediaMolder can infer for
  that stream (resolution, pixel format, frame rate, colour space, codec,
  bitrate, sample rate, channel layout) — seeded from a probe of the source
  file and propagated forward through the graph.
- **FFmpeg ->** parses any `ffmpeg` command line and drops the equivalent
  graph onto the canvas. **-> FFmpeg** shows you the FFmpeg command-line for
  any MediaMolder graph.
- The **Run panel** shows live per-node metrics — packets, rate, error count,
  mean frame latency, and *unblocked performance* (the rate each node achieves
  while actively processing, idle and stall time excluded).
- MediaMolder graphs are saved as JSON files that can be run by passing the 
  JSON to the MediaMolder binary as a single command-line argument.
- MediaMolder saves the position of every node in your graph layout, and it
  saves the technical metadata of the source media if the source files are 
  defined in the job.
- The properties panel includes extended help for most parameters, explaining
  the effect of each option, the default value, and the valid range. Parameters
  that accept a list of values (e.g. `hwaccel`) show a dropdown menu of valid
  options.  


### Safe by default

**MediaMolder validates your graph before the first frame is touched.**

`mediamolder validate` (and the GUI's inline annotations) run a
static + probe-assisted analysis pass that catches every class of problem that
would cause FFmpeg to crash silently or produce unusable output hours into a
job: graph topology errors, codec/container incompatibilities, pixel-format
mismatches, hardware boundary violations, HDR without tone-mapping, interlaced
sources without a deinterlacer, VFR streams without an fps filter, odd
dimensions rejected by encoders, and more. Every issue is reported in a single
pass with a human-readable message, an ERROR/WARNING/INFO severity, and the
exact node and edge where the problem occurs.

Where the fix is unambiguous, the GUI offers **one-click automated remediation**
— auto-insert `yadif`/`bwdif` for interlaced sources, `tonemap`/`zscale` for
HDR→SDR conversions, `fps`/`format`/`scale` adapters at incompatible
boundaries, `hwupload`/`hwdownload` at hardware device transitions. You see the
problem and its fix before committing any compute time.


### Observable at every level

MediaMolder was designed for long-running and production jobs where "check
after it finishes" is not an option. 

- **Per-node performance tracking** (`NodePerfTracker`) records each node's
  active, idle, and stalled fractions, windowed FPS vs. target, stall count
  and duration, per-frame processing latency, and — for decoder nodes —
  the libavcodec thread pool fill (`threads_busy`). The bottleneck node and
  its constraint are always visible.
- **Prometheus metrics** for every node and graph: 20+ gauges, counters,
  and histograms covering frames, errors, bitrate, frame latency, FPS,
  queue fill, CPU core estimates, and thread visibility.
- **`/perf` and `/perf/stream`** HTTP endpoints expose the per-node snapshot
  as JSON on demand or as a 2 Hz Server-Sent Events stream for dashboards.
- **`mediamolder perf`** renders a live colour-coded terminal table — green
  when nodes meet their FPS target, amber/red when they fall behind — with
  no extra tooling required.
- **OpenTelemetry** span wiring: every graph run and every handler goroutine
  emits a child span so your existing distributed trace shows exactly where
  decode/filter/encode time goes.

### Extensible in pure Go

Custom processing logic — object detection, AI filters, scene detection,
subtitle generation, business-specific metadata — slots into any graph as a
first-class node, written as an ordinary Go struct that implements the
`processors.Processor` interface. No C, no rebuilds, no filtergraph string
hacks. The engine schedules, monitors, and error-handles custom nodes
identically to built-in nodes. For example, you can add a custom Yolo-v8
object-detection node to a graph and it will run directly inside your media
graph. See [Yolo-V8 Guide](docs/yolov8-guide.md)

### Hardware acceleration — any platform, properly

 MediaMolder makes hardware acceleration *safe and understandable*.

- A **Hardware Capabilities dialog** probes all available backends at startup
  and displays each GPU's marketing name, supported encode/decode codecs
  grouped by media type, capability notes (max resolution, 10-bit, B-frames,
  concurrent session limits), and a diagnostic message for any backend that
  failed to open.
- Per-input, per-stream hardware decode control with a live scope hint in the
  Inspector: *"HW decode: video (prores_ap4x) · SW fallback: audio"* — so
  you know exactly what goes to the GPU before you run.
- **Automatic hardware filter mapping:** assign a CUDA device to a `scale`
  node, tick *Auto-map to hardware filter*, and the runtime promotes it to
  `scale_cuda` and inserts `hwupload`/`hwdownload` at device boundaries.
- **Apple ProRes RAW hardware decode** via VideoToolbox — including
  ProRes RAW HQ and ProRes 4444 XQ — codecs that FFmpeg's VideoToolbox
  binding does not expose.

### Production-grade infrastructure

- **Declarative, version-controlled graphs.** JSON files are diffable,
  database-storable, reliably generated programmatically, and fully schema-
  validated (v1.0/v1.1). The graph layout (node positions) round-trips through 
  the GUI without polluting the runtime config.
- **Full timing control.** `-ss`/`-t`/`-to` at input *and* output scope, a
  faithful Go port of FFmpeg's demuxer trim logic, `av_parse_time` string
  parsing, and per-encoder time-base control.
- **Graph state machine** with live pause/resume, graceful cancellation via
  `context.Context`, per-node error policies, and a structured event bus.
  Suitable for live streams and unattended overnight jobs alike.
- **Trivially embeddable.** The CLI and GUI are thin consumers of a clean Go
  API. Drop the engine into any service or CI/CD graph with a single import.


### Drop-in FFmpeg migration

`mediamolder convert-cmd` turns any FFmpeg command line into a validated JSON
config in one step: rate-control flags, per-stream maps, stream-copy nodes,
tee/HLS/DASH muxers, bitstream filters, hardware devices, cover-art and
attachment handling, `-map_metadata`/`-map_chapters`, two-pass encoding, and
more — all converted with high fidelity and covered by round-trip regression
tests. The generated graph runs immediately; the Inspector shows every option
the conversion inferred so you can review and adjust.
See [FFmpeg Migration Guide](docs/ffmpeg-migration-guide.md)

---

MediaMolder gives you 100% of FFmpeg's media processing capabilities —
every codec, filter, hardware backend, and container format — with a graph
model that validates before it runs, shows you what's happening while it runs,
and tells you exactly what went wrong when it doesn't.


## Prerequisites

- [**Go 1.23+**](https://go.dev/dl/)
- [**FFmpeg 8.1+**](https://ffmpeg.org/download.html) (libavcodec 62.x, libavformat 62.x, libavfilter 11.x, libavutil 60.x)
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
- [Validation](docs/architecture/graph_validation_design.md)
- [JSON Config Reference](docs/json-config-reference.md)
- [Export to FFmpeg CLI](docs/architecture/export.md)
- [Visual Editor (GUI)](docs/gui.md)
- [Go Processor Nodes](docs/go-processor-nodes.md)
- [Scene Detection (PySceneDetect port)](docs/scene-detection.md)
- [Yolov8 object detection/classification](docs/yolov8-guide.md)

### Code

- [Architecture](docs/architecture/architecture.md)
- [graph State Machine](docs/graph-state-machine.md)
- [graph Instrumentation Roadmap](docs/pipeline-instrumentation-roadmap.md)
- [Clock & Sync](docs/clock-and-sync.md)
- [Event Bus](docs/architecture/event-bus.md)
- [Error Handling](docs/error-handling.md)
- [Hardware Acceleration](docs/hardware-acceleration.md)
- [Observability](docs/observability.md) — Prometheus metrics, OpenTelemetry tracing, per-node performance monitoring, `mediamolder perf` CLI
- [Graph Compilation](docs/graph-compilation.md)

### Project

- [MediaMolder Project](docs/mediamolder_project.md)
- [Contribution & Governance](docs/contribution_and_governance.md)
- [Project Specification](docs/specification.md)
- [Benchmarks](docs/benchmarks.md) — `mediamolder hwbench` user tool + Go graph CI benchmarks
- [Licensing](LICENSING.md)
