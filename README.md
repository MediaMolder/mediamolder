# MediaMolder

**The modern media processing engine that gives you 100% of FFmpeg's power and performance — with a visual editor, bulletproof validation, live observability, pure-Go extensibility, and an intelligent real-time adaptive controller.**

[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8.svg)](https://go.dev)
[![License](https://img.shields.io/badge/License-LGPL--2.1-blue.svg)](LICENSE)
[![FFmpeg Powered](https://img.shields.io/badge/Powered_by-libav*-black.svg)](https://ffmpeg.org)

**MediaMolder is a ground-up redesign of FFmpeg’s interface and orchestration layers.** It uses the same battle-tested `libav*` libraries (libavcodec, libavformat, libavfilter, x264, x265, etc.) but replaces complex, error-prone command-line strings with graphs defined in JSON files.

---

### MediaMolder Advantages

| Feature                          | **MediaMolder**                                      | FFmpeg                      | GStreamer                  |
|----------------------------------|------------------------------------------------------|-----------------------------|----------------------------|
| **Graph visualization**         | **Drag-and-drop browser GUI**                        | None                       | Manual Graphviz            |
| **Pre-run validation**          | **Static + probe-assisted, one-click auto-fix**      | None           | Limited                    |
| **Live observability**          | **Per-node metrics, Prometheus, OTEL, live terminal** | None                       | Basic                      |
| **Real-time adaptive controller** | **Full adaptive loop (threads + presets + frame drop + jitter buffers)** | None | Limited |
| **Extensibility**               | **Pure Go `Processor` interface**                    | C filters (rebuild required) | C plugins                |
| **In-graph video editing**      | **Multi-track timeline node — cuts, transitions, audio crossfades** | Manual filter graphs | Separate GES library |
| **Hardware acceleration**       | **Probed, auto-mapped, safe across platforms**       | Opaque & error-prone       | Complex                    |
| **Declarative config**          | **Versioned JSON + GUI round-trip**                  | Command strings            | Pipeline code              |
| **FFmpeg command migration**    | **One-command `convert-cmd` with round-trip**        | N/A                        | Manual                     |
| **Production readiness**        | **Pause/resume, real-time controller, embeddable**   | Good for scripts           | Good for pipelines         |
| **Remote & distributed execution** | **Same JSON runs locally, on a remote GPU server, or across a fault-tolerant cluster** | Separate tool required | Not built-in |

Every codec, filter, container, and hardware backend FFmpeg offers, with significantly better usability, safety, observability, and *real-time reliability*.

When a graph decodes and re-encodes video, MediaMolder follows FFmpeg's encoder
boundary behavior: source frame types are not reused as encoder commands.
Keyframes/IDRs are inserted by explicit graph controls such as
`force_key_frames` and processor-generated scene-change markers.

### Real Time Encoding with Proper Metrics

MediaMolder GUI — Adaptive Bitrate x264 encode with real-time controller active![Video Encoding Real Time Controller](/docs/images/real-time-controller.png)

- Import any `ffmpeg` command line instantly
- Drag filters, encoders, sources, and sinks onto a canvas
- Set parameter values in an inspector panel with extended help for each parameter
- Validate your job to catch problems before you run it
	- MediaMolder can suggest and implement fixes to common problems
- Hover edges for full stream metadata
- Live performance metrics while running
- In real-time mode, the **Real-time controller panel** shows detailed statistics  (threads, presets, frame drops)
- Export back to the equivalent FFmpeg command line 

## Real-Time Controller

Activate with `--realtime` (CLI) or `global_options.realtime: true` (JSON) and MediaMolder turns on an **adaptive closed-loop control system** that enables reliable live video encoding**.

- **Adaptive control loop** (500 ms ticks) continuously monitors the performance of every encoder
- **Three-tier adaptation**:
  1. Scale encoder threads (graceful restart, within CPU budget)
  2. Increments presets faster/slower (GOP-boundary switching, quality recovery when load drops) to optimize speed vs. quality
  3. Graceful frame drop (as a last resort)
- **Configurable encoder input buffer** (~4 s) + **rolling output buffer** (~4 s) absorbs upstream and downstream jitter (TCP stalls, HLS segment hiccups, SRT bursts, etc.)
- **Live status badges** in GUI + `mediamolder watch` + HTTP/SSE API
- Perfect for live streaming, HLS/DASH playout, broadcast, and any long-running job that must stay on pace


### Graphical User Interface / Visual editor

FFmpeg runs media processing graphs, but until now you would have to visualize 
those graphs in your head. MediaMolder can import your FFmpeg command-line, and the GUI 
enables you to view, edit, validate, and run your graph with detailed performance 
metrics. The MediaMolder Graphical User Interface (GUI) is a fluid, 
drag-and-drop graph editor that runs in your web browser. The GUI is launched
from the mediamolder binary by the `gui` subcommand. For details, see [gui.md](docs/gui.md)

- Build encode graphs by dragging filters, encoders, sources, and sinks onto
  a canvas and wiring them by stream type. Mismatched types (video → audio
  input) are rejected at the handle level.

- The Inspector displays typed forms for every node: encoder rate-control
  modes, HLS/DASH delivery wizards, bitstream-filter chains, chapter and
  container metadata editors, per-stream disposition and language overrides,
  audio channel routing.
- When you select an input file, it is probed to determine its technical parameters.
- Hover any edge (wire) to see every technical property MediaMolder can infer for
  that stream (resolution, pixel format, frame rate, colour space, codec,
  bitrate, sample rate, channel layout) — seeded from a probe of the source
  file and propagated forward through the graph.
- **FFmpeg ->** parses any `ffmpeg` command line and drops the equivalent
  graph onto the canvas. 
- **-> FFmpeg** shows you the equivalent FFmpeg command-line for
  your MediaMolder graph (warning if your graph contains MediaMolder-exclusive 
  capabilities, like custom Go Processor Nodes).
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
  that accept an enumerated list of values (e.g. `hwaccel`) are controlled by 
  a dropdown menu that lets you select a valid value.  


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
identically to built-in nodes. For more details, see [go-processor-nodes.md](docs/go-processor-nodes.md).

Processors may also implement the optional `FrameSource` sub-interface to **generate their own frames** rather than processing inbound ones — the built-in `sequence_editor` timeline node is one example (see [Video editing built in](#video-editing-built-in)).

You can add a custom Yolo-v8 object-detection node to a graph and it will 
run directly inside your media graph. See [Yolo-V8 Guide](docs/yolov8-guide.md)

For multimodal scene understanding — captions, temporal grounding, edit plans, QA — the built-in `vidi_analyzer` node connects any graph to a [Vidi 2.5](https://github.com/bytedance/vidi) inference service. See [Vidi 2.5 Guide](docs/vidi-guide.md)

For cloud-hosted video understanding — index, search, caption, and embed clips via the [TwelveLabs](https://twelvelabs.io) Marengo and Pegasus models — the `twelvelabs_indexer`, `twelvelabs_analyzer`, `twelvelabs_searcher`, and `twelvelabs_embedder` nodes are built in. See [TwelveLabs Guide](docs/twelvelabs.md)

For local, offline speech-to-text, the built-in `whisper_stt` node transcribes an audio stream to timestamped subtitles (SRT/VTT/JSON/TXT) with [whisper.cpp](https://github.com/ggml-org/whisper.cpp). See [Whisper Speech-to-Text Guide](docs/whisper-stt-guide.md)

For true camera-RAW develop (NEF/CR2/CR3/ARW/RAF/ORF/RW2/PEF/SRW/DNG) to a full, demosaicked 8-bit sRGB image via [LibRaw](https://www.libraw.org/) — not the camera's embedded JPEG preview, and not libav's black RAW render — use the `mediamolder raw-decode` command or the built-in `raw_decode` node inside a graph. Deterministic develop (camera white balance, sRGB, AHD); LibRaw is bundled from pinned source and linked statically. See [Camera-RAW Decode Guide](docs/raw-decode-guide.md)

### Video editing built in

Assemble clips into a finished video — cuts, trims, wipes, dissolves, layering,
and audio crossfades — entirely inside a job graph, with no external NLE. The
built-in **`sequence_editor`** node is a multi-track timeline that opens its own
sources and emits a finished stream: place clips on tracks at explicit times, add
transitions composited by a [native Go engine](docs/architecture/transitions.md)
(the full `xfade` set), and mix audio from the same clips with crossfades
auto-coupled to each cut. Build it as a spreadsheet-style table in the GUI or as
declarative JSON. See the [Video Editing Guide](docs/video-editing-guide.md).

### Hardware acceleration — any platform, properly

 MediaMolder makes hardware acceleration *safe and understandable*. See [hardware-acceleration.md](docs/hardware-acceleration.md)

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


### Remote and distributed execution

MediaMolder is built from the ground up to run jobs anywhere — on your laptop,
on a single remote GPU box, or across a horizontally-scaled cluster — without
changing a line of your graph JSON.

**Three deployment tiers, one binary, one job format:**

| Tier | How to start | Best for |
|------|-------------|----------|
| **Local** | `mediamolder run job.json` | Development, single-machine encodes |
| **Tier 1 — remote server** | `mediamolder serve --mode=server` | Run jobs on a more powerful machine; GUI/CLI stays on your laptop |
| **Tier 2 — distributed cluster** | `--mode=api` + `--mode=worker` | Scene-parallel encoding, fault tolerance, cloud autoscaling |

**Why this matters for production workloads:**

- **Zero job-format migration.** The same JSON config file that runs locally
  is submitted verbatim to a remote server or cluster. The `Distribution`
  block for Tier 2 fan-out is purely additive — existing jobs run unmodified.
- **Fault-tolerant task execution.** In Tier 2, every encode task is retried
  automatically up to a configurable `max_attempts` before being moved to an
  inspectable dead-letter queue. A crashed worker never loses a job.
- **Scene-parallel encoding.** The `fanout_dynamic` strategy splits a source
  into segments at scene boundaries, encodes them in parallel across workers,
  then stitches the outputs with the `gather` strategy — dramatically reducing
  wall-clock time for long-form content without changing the output.
- **Capability-aware routing.** Workers advertise their hardware (`cuda`,
  `h264_nvenc`, `hevc_nvenc`, …) and region. Tasks declare what they require.
  GPU tasks are automatically routed to GPU workers; CPU-only workers pick up
  everything else. No manual queue partitioning needed.
- **S3 presigning at the server boundary.** Submit jobs with `s3://` URIs;
  the server converts them to short-lived HTTPS presigned URLs before execution
  starts. Workers never hold AWS credentials, and credentials never travel in
  job JSON.
- **Local file upload.** Enable `--enable-uploads` to let GUI users and CLI
  scripts push local files directly to the server before submitting the job.
  Files are stored in a scoped `--workdir` and deleted on job completion.
- **Pluggable state and queue backends.** Run in-memory + SQLite for local
  development; swap to Postgres + NATS JetStream or DynamoDB + SQS for
  multi-instance cloud deployments with no code changes.
- **Strong authentication built in.** Choose a static bearer token
  (`--auth-token-file`), OIDC JWTs from any standards-compliant provider
  (`--oidc-issuer`), or mTLS client certificates (`--mtls-ca`). All three
  compose freely; `/healthz` and `/readyz` remain unauthenticated for
  load-balancer probes.
- **Distributed tracing end-to-end.** Tier 2 propagates OpenTelemetry span
  context through the queue automatically — task spans appear as children of
  the originating job span in your trace backend, giving full visibility from
  HTTP request to encoded frame.
- **GUI-first remote workflow.** Click **Backend** in the toolbar, enter a
  server URL and token, and every subsequent **Run** is sent to the remote
  machine. Switch back to local with one click.

For setup instructions see [Remote Backend Guide](docs/remote-backend-guide.md).

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

See [Build & Packaging](docs/build-and-packaging.md)

For detailed instructions see [MacOS](docs/build/macos.md), [Windows](docs/build/windows.md) and [Linux](docs/build/linux.md)

## Documentation

### Usage

- [Using MediaMolder (CLI)](docs/using-mediamolder.md)
- [Visual Editor (GUI)](docs/gui.md)
- [Remote Backend Guide](docs/remote-backend-guide.md) — run your jobs on a single remote server or a distributed cluster with API + workers
- [Concepts — Graph Model, Nodes, Edges, Lifecycle](docs/concepts-and-graph-basics.md)
- [JSON Config Reference](docs/json-config-reference.md)
- [FFmpeg Migration Guide](docs/ffmpeg-migration-guide.md)
- [Export to FFmpeg CLI](docs/architecture/export.md)
- [Validation](docs/architecture/graph_validation_design.md)
- [Video Editing Guide](docs/video-editing-guide.md) — assemble clips into a finished video: multi-track timelines, cuts/trims, dissolves and the full transition set with audio crossfades (`sequence_editor`)
- [Go Processor Nodes](docs/go-processor-nodes.md) — `Processor` interface, `FrameSource` interface, built-in processors (`sequence_editor`, scene detectors, TwelveLabs, Vidi, …), writing custom nodes
- [Scene Detection](docs/scene-detection.md) — seven detectors: go-scene-detect ports (content, adaptive, threshold, hash, histogram), FFmpeg scdet, and the motion-compensated `scene_change_mc` (frame-accurate dissolve/fade detection)
- [Yolov8 object detection/classification](docs/yolov8-guide.md)
- [Vidi 2.5 multimodal analysis](docs/vidi-guide.md)
- [TwelveLabs video understanding](docs/twelvelabs.md)
- [Whisper speech-to-text](docs/whisper-stt-guide.md) — local, offline transcription to SRT/VTT/JSON/TXT (`whisper_stt`)
- [Camera-RAW Decode](docs/raw-decode-guide.md) — develop NEF/CR2/CR3/ARW/RAF/ORF/RW2/PEF/SRW/DNG to 8-bit sRGB via bundled LibRaw (`raw-decode` CLI + `raw_decode` node)
- [Real-Time Controller](docs/realtime-controller.md) — adaptive control loop, encoder preset stepping, output buffers, `mediamolder watch`, HTTP API

### Code

- [Architecture](docs/architecture/architecture.md)
- [Graph State Machine](docs/architecture/graph-state-machine.md)
- [Graph Instrumentation Roadmap](docs/roadmap/graph-instrumentation-roadmap.md)
- [Clock & Sync](docs/architecture/clock-and-sync.md)
- [Event Bus](docs/architecture/event-bus.md)
- [Error Handling](docs/architecture/error-handling.md)
- [Hardware Acceleration](docs/hardware-acceleration.md)
- [Observability](docs/architecture/observability.md) — Prometheus metrics, OpenTelemetry tracing, per-node performance monitoring, `mediamolder perf` CLI
- [Graph Compilation](docs/architecture/graph-compilation.md)
- [Video Transitions](docs/architecture/transitions.md) — native Go transition engine (wipes, slides, fades, circles, …) that replaces libavfilter `xfade`
- [Camera-RAW Decode](docs/architecture/raw-decode.md) — LibRaw develop boundary, the three decode intents, determinism scoping, static-link rationale

### Project

- [MediaMolder Project](mediamolder_project.md)
- [Contribution & Governance](contribution_and_governance.md)
- [Project Specification](docs/architecture/specification.md)
- [Benchmarks](docs/architecture/benchmarks.md) — `mediamolder hwbench` user tool + Go graph CI benchmarks
- [Licensing](LICENSING.md)

## Star History

<a href="https://www.star-history.com/?repos=MediaMolder%2Fmediamolder&type=date&legend=top-left">
 <picture>
   <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/chart?repos=MediaMolder/mediamolder&type=date&theme=dark&legend=top-left" />
   <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/chart?repos=MediaMolder/mediamolder&type=date&legend=top-left" />
   <img alt="Star History Chart" src="https://api.star-history.com/chart?repos=MediaMolder/mediamolder&type=date&legend=top-left" />
 </picture>
</a>