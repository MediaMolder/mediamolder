# Detailed Build Plan

*Derived from the [Project Specification](spec_v3.md) and [Development Roadmap](roadmap.md).*

Each task is numbered within its phase (e.g., P0.1). Dependencies reference other task IDs. Tasks within a phase are roughly sequential unless noted as parallelizable.

---

## Phase 0 — MVP

**Goal**: Core binding layer + simple single-input → single-filter → single-output pipeline, driven by a JSON config file.

**Exit criteria**: Successfully transcode a single-stream video file (YUV or Y4M input → H.264 output) with at least one video filter (scale) applied, driven by a JSON config file. Output passes SSIM ≥ 0.99 against equivalent `ffmpeg` output. Benchmarks show < 10% overhead vs. `ffmpeg`.

### P0.1 — Project Scaffolding
- Initialize Go module (`github.com/MediaMolder/MediaMolder`).
- Set up directory structure: `av/`, `pipeline/`, `graph/`, `runtime/`, `cmd/mediamolder/`, `schema/`, `internal/`, `docs/`.
- Configure CI (GitHub Actions): Go build + test on Linux, macOS, Windows.
- Add cgo build with pkg-config for FFmpeg 8.1 detection.
- Add `Makefile` with targets: `build`, `test`, `lint`, `bench`.
- Add `.gitattributes` for Git LFS (media test corpus).
- Add LGPL-2.1 `LICENSE` file and `LICENSING.md` guide.
- Write `README.md` covering:
  - Project overview and goals (what MediaMolder is and why it exists).
  - Prerequisites: Go 1.23+, FFmpeg 8.1+ shared libraries, pkg-config.
  - Installation: `go install` and building from source.
  - Quickstart: a minimal JSON config and `mediamolder run` invocation.
  - Links to `LICENSING.md`, `docs/`, and the project spec.
- Write stub `docs/` files for each package (`av/`, `pipeline/`, `graph/`, `runtime/`) with one-line purpose descriptions — to be expanded as each package is implemented.
- **Deliverable**: Empty project builds, CI is green on all 3 platforms, and `README.md` is complete enough for a new contributor to clone, build, and understand the project.

### P0.2 — FFmpeg Library Detection & Version Check
- Implement pkg-config probe for libavcodec, libavformat, libavfilter, libavutil, libswscale, libswresample.
- Check library versions at init time; fail with clear error if below FFmpeg 8.1 minimums (libavcodec 62.x, libavformat 62.x, libavfilter 11.x, libavutil 60.x).
- Support `-tags=static` build tag for static linking.
- **Deliverable**: `go build` succeeds when FFmpeg 8.1+ is installed; fails with actionable message otherwise.
- **Depends on**: P0.1

### P0.3a — Analyse & Port `json_command_patches` C Implementation
- Checkout and apply the `json_command_patches` branch from the FFmpeg repo to understand the full scope of `fftools/ffmpeg_json.c` (~1,200 lines).
- Catalogue the three capabilities: JSON→argv builder, CLI→JSON generator, JSON→shell-command printer.
- Identify which logic can be ported directly to Go for use in `mediamolder/compat/ffcli` (the argv grouper, option parser, and filtergraph string tokenizer are the most valuable parts).
- Extract the 8 example JSON files from `doc/examples/json_cmd_*.json` into `testdata/ffmpeg-json-examples/` as compatibility layer test inputs.
- Extract and adapt the FATE tests from patch 5 as initial test cases for `mediamolder/compat/ffcli`.
- Document the schema difference: `json_command_patches` JSON is a thin CLI-arg wrapper; MediaMolder JSON is a declarative graph. Both are accepted by `mediamolder convert-cmd`.
- **Deliverable**: A design note (added to `docs/ffmpeg-migration-guide.md` stub) describing the porting plan, schema mapping table, and the list of reusable test cases. No Go code yet.
- **Depends on**: P0.1

### P0.3 — Core Binding Layer: Base Types
- Implement `av.Err` wrapping AVERROR codes with human-readable messages.
- Implement `av.Frame` (alloc, unref, close) with explicit `Close()` and `io.Closer`.
- Implement `av.Packet` (alloc, unref, close) with explicit `Close()`.
- Implement leak detector (`-tags=avleakcheck`): tracks alloc/free counts, logs unclosed resources at process exit.
- Establish cgo safety patterns: no C pointers stored in Go heap, all C access scoped within cgo call blocks.
- **Deliverable**: `av.Frame`, `av.Packet`, `av.Err` usable in Go tests. Leak detector reports unclosed resources.
- **Depends on**: P0.2

### P0.4 — Core Binding Layer: Demuxing
- Implement `av.FormatContext` for input (open, probe, read packet, close).
- Implement stream info inspection (codec type, codec ID, dimensions, sample rate, channel layout).
- Implement structured stream selection: given an input config with `streams: [{ input_index, type, track }]`, resolve to libav* stream indices.
- **Deliverable**: Can open a media file, enumerate streams, and read raw packets in a Go test.
- **Depends on**: P0.3

### P0.5 — Core Binding Layer: Decoding
- Implement `av.CodecContext` for decoders (find decoder, open, send packet, receive frame, flush, close).
- Wire decoder to demuxer: read packet → send to decoder → receive decoded frame.
- Handle EOF and flush correctly.
- **Deliverable**: Can decode video frames from a media file in a Go test.
- **Depends on**: P0.4

### P0.6 — Core Binding Layer: Filtering
- Implement `av.FilterGraph` (create, parse, configure, close).
- Implement buffer source (push frames in) and buffer sink (pull frames out).
- Support a single filter (`scale`) with Go struct parameters (no string escaping).
- **Deliverable**: Can push decoded frames through a scale filter and receive scaled frames in a Go test.
- **Depends on**: P0.5

### P0.7 — Core Binding Layer: Encoding
- Implement `av.CodecContext` for encoders (find encoder, open, send frame, receive packet, flush, close).
- Support H.264 encoding via `libx264` with basic options (preset, bitrate).
- **Deliverable**: Can encode filtered frames to H.264 packets in a Go test.
- **Depends on**: P0.6

### P0.8 — Core Binding Layer: Muxing
- Implement `av.FormatContext` for output (alloc, add stream, write header, write packet, write trailer, close).
- Implement transactional file output: write to `.tmp` file, atomic rename on success.
- **Deliverable**: Can mux encoded packets to an output file (e.g., MP4) in a Go test.
- **Depends on**: P0.7

### P0.9 — Pipeline Config: JSON Schema v1.0
- Define Go structs for `pipeline.Config`: `Input`, `Output`, `GraphDef`, `NodeDef`, `EdgeDef`, `StreamSelection`.
- Implement JSON marshaling/unmarshaling with strict validation (unknown fields rejected).
- Implement `schema_version` check.
- Write JSON Schema file (`schema/v1.0.json`) matching the Go structs.
- **Deliverable**: Can parse the example JSON from spec §9 into Go structs and validate it.
- **Depends on**: P0.1

### P0.10 — Pipeline Engine: Simple Linear Pipeline
- Implement `pipeline.Pipeline` struct that takes a `pipeline.Config`.
- Build a linear pipeline: SourceNode → FilterNode → EncoderNode → SinkNode.
- Wire stages with Go channels (packet/frame flow).
- Implement goroutine-per-stage execution with `errgroup`.
- Implement `Pipeline.Start()` (blocking run to completion) and `Pipeline.Close()` (teardown).
- Implement context cancellation propagation.
- **Deliverable**: Can run a complete transcode (demux → decode → scale → encode → mux) from a JSON config.
- **Depends on**: P0.4, P0.5, P0.6, P0.7, P0.8, P0.9

### P0.11 — Initial Test Corpus & Integration Tests
- Curate 10–20 FATE sample files covering: H.264 video in MP4/MKV, AAC audio, basic edge cases.
- Store in Git LFS under `testdata/`.
- Write integration tests: run pipeline, re-demux output, check stream count/codec/duration.
- Implement SSIM comparison tool (call `ffmpeg` to compute SSIM between output and reference).
- **Deliverable**: Integration tests pass; output SSIM ≥ 0.99 vs. equivalent `ffmpeg` command.
- **Depends on**: P0.10

### P0.12 — Initial Benchmark Suite
- Implement benchmark: 1080p H.264→H.264 transcode with scale filter, `medium` preset.
- Measure throughput (fps), peak memory, startup time.
- Compare against equivalent `ffmpeg` CLI command.
- Record results in CI (track over time).
- **Deliverable**: Benchmark shows < 10% throughput overhead vs. `ffmpeg`.
- **Depends on**: P0.10

### P0.13 — AddressSanitizer CI Job
- Add CI job that builds and runs tests with `CGO_CFLAGS=-fsanitize=address`.
- Fix any memory errors found.
- **Deliverable**: ASan CI job passes clean.
- **Depends on**: P0.11

---

## Phase 1 — Full Graph, CLI, State Machine, Clock/Sync

**Goal**: Multi-input, multi-output declarative graph pipelines from JSON. Full CLI tool. Pipeline state machine and clock/sync for file-based inputs.

**Exit criteria**: Multi-input, multi-output pipelines with complex filter graphs (overlay, concat, split) work from JSON. `mediamolder run`, `inspect`, and `convert-cmd` CLI commands operational. Pipeline state machine (NULL→READY→PAUSED→PLAYING) fully implemented with event bus. Clock/sync model working for file-based inputs; A/V sync within ±40ms. `Pipeline.SetState()`, `Pipeline.Seek()`, and `Pipeline.GetMetrics()` Go API methods functional.

### P1.1 — Binding Layer: Full Filter Support
- Generalize `av.FilterGraph` to support any libavfilter filter (not just scale).
- Implement multi-input filters (e.g., `overlay`, `amix`) and multi-output filters (e.g., `split`, `asplit`).
- Filter parameters are Go structs marshaled from JSON `params` map; validated against libavfilter's option introspection.
- **Deliverable**: Can build and run complex filter graphs (overlay, concat, split) in Go tests.
- **Depends on**: P0.6

### P1.2 — Binding Layer: Audio Decode/Encode
- Implement audio decoder support (AAC, Opus, FLAC, MP3, etc.).
- Implement audio encoder support (AAC, Opus, FLAC).
- Implement audio resampling via libswresample (sample rate, channel layout, sample format conversion).
- **Deliverable**: Can decode and re-encode audio streams in Go tests.
- **Depends on**: P0.5, P0.7

### P1.3 — Graph Engine: DAG Construction
- Implement full directed acyclic graph construction from `GraphDef` (nodes + edges).
- Resolve edge references (`"main:v:0"` → source node video output pad 0).
- Validate edge type compatibility (video↔video, audio↔audio).
- Detect cycles and reject them.
- Support multiple inputs and multiple outputs.
- **Deliverable**: Can construct and validate complex multi-input/multi-output graphs from JSON.
- **Depends on**: P0.9, P0.10

### P1.4 — Runtime: Multi-Lane Scheduler
- Extend runtime to support multiple concurrent output lanes (each output sink gets its own goroutine group).
- Implement stream splitting: one source feeds multiple consumers via fan-out channels.
- Ensure a slow output doesn't stall others.
- **Deliverable**: Multi-output pipelines work; slow output doesn't block fast output.
- **Depends on**: P0.10, P1.3

### P1.5 — Pipeline State Machine
- Implement `pipeline.State` enum: `NULL`, `READY`, `PAUSED`, `PLAYING`.
- Implement `Pipeline.SetState()` with sequential transition enforcement (NULL→READY→PAUSED→PLAYING).
- Implement auto-transition (e.g., `Start()` transitions NULL→READY→PAUSED→PLAYING automatically).
- Implement `any → NULL` teardown with best-effort drain.
- Implement `PLAYING → PAUSED` (suspend data flow) and `PAUSED → PLAYING` (resume).
- Return `ErrInvalidStateTransition` for illegal transitions.
- Wire `Pipeline.Start()`, `Pause()`, `Resume()`, `Close()` as convenience wrappers around `SetState()`.
- **Deliverable**: State machine fully functional with all transitions tested. Integration tests exercise every valid and invalid transition.
- **Depends on**: P0.10

### P1.6 — Event Bus
- Implement `pipeline.Event` interface and concrete types: `StateChanged`, `Error`, `EOS`, `StreamStart`.
- Implement `Pipeline.Events() <-chan pipeline.Event` — buffered channel (default: 256).
- Implement non-blocking post semantics: if consumer is slow, count missed events in metrics and emit `BufferOverflow` warning.
- Emit `StateChanged` events on every state transition (includes previous state, new state, duration).
- Emit `Error` events for every `PipelineError`.
- Emit `EOS` when the pipeline reaches end of stream.
- **Deliverable**: Event bus functional; state transitions and errors observable via channel.
- **Depends on**: P1.5

### P1.7 — Clock & Synchronization (File Mode)
- Implement `clock.Pipeline` with monotonic system clock as default.
- Implement PTS/DTS tracking in source nodes: frames carry correct timestamps from demuxer.
- Implement timestamp passthrough in filter and encoder nodes.
- Implement A/V sync in mux nodes: interleave audio and video packets by DTS.
- Implement sync tolerance check: warn if A/V drift exceeds ±40ms.
- File inputs run as fast as possible (no wall-clock pacing) by default.
- **Deliverable**: Multi-stream (video + audio) transcode produces correctly synchronized output. A/V drift < ±40ms measured by re-demuxing and comparing PTS alignment.
- **Depends on**: P0.10, P1.2

### P1.8 — Seek Support
- Implement `Pipeline.Seek(target time.Duration)`: transitions to PAUSED, flushes all stage buffers, seeks all inputs to target (nearest keyframe), resumes from PAUSED.
- Emit `StateChanged` events during seek.
- **Deliverable**: Seek to a timestamp mid-file works; output starts from the seeked position.
- **Depends on**: P1.5, P1.7

### P1.9 — CLI: `mediamolder run`
- Implement `cmd/mediamolder` using Cobra or similar.
- `mediamolder run config.json [--metrics-addr=:9090]` — parse JSON, build pipeline, run to completion.
- Real-time progress output to stderr (fps, time elapsed, % complete).
- JSON status output mode (`--json`).
- Exit code 0 on success, non-zero on error with structured error output.
- **Deliverable**: Can run a full transcode from the command line with progress output.
- **Depends on**: P1.3, P1.4, P1.5

### P1.10 — CLI: `mediamolder inspect`
- `mediamolder inspect config.json` — parse and validate JSON config, pretty-print the resolved pipeline graph (nodes, edges, types).
- Report validation errors with context (line number, field path).
- **Deliverable**: Useful for debugging configs without running a pipeline.
- **Depends on**: P0.9, P1.3

### P1.11 — FFmpeg CLI Compatibility Parser
- Implement `mediamolder/compat/ffcli` package, porting logic from `fftools/ffmpeg_json.c` (the `json_command_patches` branch).
- The Go port covers:
  - **CLI→MediaMolder JSON**: parse FFmpeg CLI args (using the argv grouper and option parser logic from `ffmpeg_json_generate()`) and produce a `pipeline.Config` with structured nodes, typed edges, and stream selection. This is the core of `ffcli.Parse()`.
  - **FFmpeg JSON→MediaMolder JSON**: accept a `json_command_patches`-format JSON file (thin CLI-arg wrapper) and translate it to a `pipeline.Config`. Invoked via `mediamolder convert-cmd --ffmpeg-json cmd.json`.
  - **MediaMolder JSON→shell command** (bonus, Phase 1): reverse translation for debugging.
- Parse FFmpeg global options, `-i`, `-c:v`, `-c:a`, `-b:v`, `-vf`, `-af`, `-filter_complex`, `-map`, format options.
- Handle stream specifiers (e.g., `0:v:0`, `0:a:1`) and complex filtergraphs (`-filter_complex "[0:v][1:v]overlay=..."`).
- Seed the test suite with the 8 FFmpeg JSON example files and FATE test cases extracted in P0.3a.
- Return clear errors for unsupported/unrecognized flags.
- **Deliverable**: ~200 initial FFmpeg CLI strings (and the 8 FFmpeg JSON examples) parse correctly to expected MediaMolder JSON output.
- **Depends on**: P0.9, P0.3a

### P1.12 — CLI: `mediamolder convert-cmd`
- `mediamolder convert-cmd "ffmpeg -i in.mp4 -c:v libx264 out.mp4"` — prints equivalent JSON payload to stdout.
- Uses `ffcli.Parse()` from P1.11.
- **Deliverable**: Works for common FFmpeg commands.
- **Depends on**: P1.9, P1.11

### P1.13 — CLI: `mediamolder list-*`
- `mediamolder list-codecs` — query libavcodec and list available codecs.
- `mediamolder list-filters` — query libavfilter and list available filters.
- `mediamolder list-formats` — query libavformat and list available muxers/demuxers.
- **Deliverable**: All three list commands produce human-readable and JSON output.
- **Depends on**: P0.2, P1.9

### P1.14 — Metrics: `Pipeline.GetMetrics()`
- Implement per-node metrics collection: fps, bitrate, frames processed, errors, buffer fill level.
- Implement `Pipeline.GetMetrics()` returning a structured `MetricsSnapshot`.
- Implement `Pipeline.GetGraphSnapshot()` returning the current graph state (nodes, edges, states).
- **Deliverable**: Metrics accessible via Go API during a running pipeline.
- **Depends on**: P1.4, P1.5

### P1.15 — Expanded Test Corpus & Integration Tests
- Expand corpus to ~100 files: multi-stream, multi-format, audio-only, video-only, multi-track.
- Add integration tests for: multi-input overlay, audio+video transcode, split to multiple outputs, seek-then-transcode.
- Add golden-file tests for graph construction.
- Add FFmpeg parity tests: `ffcli.Parse()` → JSON → run → compare output to `ffmpeg` CLI.
- **Deliverable**: Comprehensive integration test suite covering Phase 1 features.
- **Depends on**: P1.1 through P1.14

### P1.16 — Cross-Platform CI Matrix
- Add CI matrix: Linux (Ubuntu LTS) + macOS (latest) + Windows (latest).
- Add FFmpeg version: 8.1 (latest stable patch release).
- Add Go version matrix: current stable + previous stable.
- **Deliverable**: All tests pass on all matrix combinations.
- **Depends on**: P1.15

### P1.17 — Phase 1 Documentation
- Update `README.md`: full CLI usage section (`run`, `inspect`, `convert-cmd`, `list-*`), quickstart examples for multi-input and multi-output configs.
- Write `docs/json-config-reference.md`: describe every field in `schema/v1.0.json` with types, defaults, and examples. Include the annotated config example from spec §9.
- Write `docs/pipeline-state-machine.md`: document the NULL→READY→PAUSED→PLAYING state machine, valid transitions, `ErrInvalidStateTransition`, and Go API usage with code examples.
- Write `docs/clock-and-sync.md`: explain the clock model, live vs. file mode, A/V sync tolerance, seek semantics, and `realtime` config flag.
- Write `docs/event-bus.md`: list all event types, their fields, Go subscription API, and a complete example of consuming events in a goroutine.
- Write `docs/ffmpeg-migration-guide.md`: a cookbook of 20+ common FFmpeg CLI commands with their MediaMolder JSON equivalents, produced using `convert-cmd` output as the basis.
- Write godoc comments for all exported types and functions in `pipeline/`, `graph/`, `runtime/`, and `clock/` packages.
- **Deliverable**: A new contributor can understand the full graph model, state machine, clock, and event bus from docs alone without reading the spec.
- **Depends on**: P1.1 through P1.16

---

## Phase 2 — Observability, Dynamic Reconfiguration, Reliability

**Goal**: Production-grade observability, live graph reconfiguration, and robust error handling.

**Exit criteria**: OpenTelemetry traces and Prometheus metrics exported. Filter parameter hot-reconfiguration works without dropping frames. Error policies (skip, retry, fallback, abort) demonstrated in integration tests. AddOutput at runtime works.

### P2.1 — OpenTelemetry Integration
- Add OpenTelemetry SDK dependency.
- Instrument pipeline with traces: one span per pipeline run, child spans per stage (demux, decode, filter, encode, mux).
- Attach attributes: node ID, codec, format, dimensions, duration.
- Implement structured logging via slog with trace correlation.
- **Deliverable**: Traces visible in Jaeger/OTLP collector when `--metrics-addr` is set.
- **Depends on**: P1.14

### P2.2 — Prometheus Metrics Exporter
- Expose metrics on `--metrics-addr` HTTP endpoint (`/metrics`).
- Metrics: `mediamolder_pipeline_fps`, `mediamolder_pipeline_bitrate_bps`, `mediamolder_node_latency_seconds`, `mediamolder_node_buffer_fill`, `mediamolder_pipeline_errors_total`, `mediamolder_pipeline_frames_total`.
- Labels: pipeline ID, node ID, media type.
- **Deliverable**: Prometheus can scrape metrics from a running pipeline.
- **Depends on**: P1.14, P2.1

### P2.3 — Error Policy Engine
- Implement per-node error policy parsing from JSON config (`error_policy` field).
- Implement `abort` policy: cancel pipeline context immediately.
- Implement `skip` policy: drop current packet/frame, log warning, continue.
- Implement `retry` policy: exponential backoff (base 100ms, max 5s), configurable `max_retries` (default 3). On exhaustion, escalate to fallback.
- Implement `fallback` policy: re-route stream to `fallback_node` if configured; otherwise escalate to `abort`.
- Emit `Error` events on the event bus for every policy invocation.
- **Deliverable**: Integration tests with corrupt inputs demonstrate all four policies.
- **Depends on**: P1.6, P0.10

### P2.4 — Dynamic Filter Reconfiguration
- Implement `Pipeline.Reconfigure(nodeID string, params map[string]any) error`.
- For parameter changes: drain current frame from the affected filter, apply new parameter via libavfilter option API, resume. No frames dropped.
- Emit `ReconfigureComplete` event on success.
- **Deliverable**: Changing drawtext string or volume level on a running pipeline works without interruption.
- **Depends on**: P1.5, P1.6

### P2.5 — Dynamic Node Add/Remove
- Implement `Pipeline.AddOutput(output OutputConfig) error` — add a new output to a running pipeline.
- Implement quiesce-drain-apply flow for structural changes: stop reading into affected subgraph, drain in-flight frames, apply change, resume.
- Return acknowledgement via callback channel when change is live.
- **Deliverable**: Adding a new HLS output to a running RTMP-to-file pipeline works.
- **Depends on**: P2.4, P1.4

### P2.6 — Automatic Node Restart
- On transient errors (where `PipelineError.Transient == true`), automatically restart the affected node's goroutine.
- Respect the node's error policy (retry count, backoff).
- Emit events for each restart attempt.
- **Deliverable**: A decode node that encounters a transient error recovers without pipeline restart.
- **Depends on**: P2.3

### P2.7 — Crash Reports
- On pipeline panic or unrecoverable error, capture: graph snapshot, per-node state, buffer levels, last N events, Go stack traces.
- Write crash report to a JSON file.
- **Deliverable**: Crash reports produced on panic; contain enough info to diagnose.
- **Depends on**: P1.6, P1.14

### P2.8 — Extended Event Bus Types
- Add remaining event types: `BufferingPercent`, `MetricsSnapshot` (periodic), `ClockLost`.
- `MetricsSnapshot` emitted every N seconds (configurable, default: 5s).
- **Deliverable**: All event types from spec §6.7 implemented and tested.
- **Depends on**: P1.6

### P2.9 — Input Validation & Security Hardening
- Implement URL scheme allowlist (default: file, http, https, rtmp, rtsp, srt).
- Implement file path traversal prevention (resolve + base directory check).
- Implement symlink resolution before validation.
- Implement resource limits: max decode dimensions, max stream count, demux probe timeout.
- Implement per-pipeline resource limits: max concurrent pipelines, memory cap, CPU thread limit.
- **Deliverable**: Malicious inputs (path traversal, oversized dimensions, excessive streams) are rejected with clear errors. Integration tests verify each check.
- **Depends on**: P0.10

### P2.10 — Phase 2 Integration Tests
- Error policy tests: configs with corrupt/missing inputs verifying skip, retry, fallback, abort.
- Dynamic reconfiguration tests: parameter change mid-transcode, AddOutput mid-transcode.
- Security tests: path traversal, oversized dimensions, unknown URL schemes.
- Observability tests: verify metrics/traces are emitted correctly.
- **Deliverable**: Full Phase 2 test coverage.
- **Depends on**: P2.1 through P2.9

### P2.11 — Phase 2 Documentation
- Write `docs/error-handling.md`: document the `PipelineError` struct, all four error policies (abort, skip, retry, fallback), exponential backoff parameters, and JSON config syntax. Include worked examples showing each policy in action.
- Write `docs/dynamic-reconfiguration.md`: document `Pipeline.Reconfigure()`, `Pipeline.AddOutput()`, the parameter-change vs. node-add/remove contracts, the quiesce-drain-apply flow, and the `ReconfigureComplete` event.
- Write `docs/observability.md`: document the OpenTelemetry integration (span structure, attributes), Prometheus metrics (all metric names, labels, descriptions), structured logging format, and a sample Grafana dashboard JSON for the exported metrics.
- Write `docs/security.md`: document the URL scheme allowlist, path traversal protection, resource limits, and recommended configurations for multi-tenant deployments. Mirror content of spec §16.
- Update `docs/event-bus.md` with new event types added in P2.8 (`BufferingPercent`, `MetricsSnapshot`, `ClockLost`).
- **Deliverable**: Operators can configure observability, tune error policies, and understand security constraints from docs alone.
- **Depends on**: P2.1 through P2.10

---

## Phase 3 — Hardware Accel, Advanced Filters, Subtitles

**Goal**: Hardware-accelerated encode/decode, full filter parity, bitstream filters, subtitle support.

**Exit criteria**: CUDA, VAAPI, and QSV hardware decode/encode paths tested on CI runners with GPU. Subtitle burn-in and passthrough working. Bitstream filter support (e.g., `h264_mp4toannexb`) available.

### P3.1 — Binding Layer: Hardware Device Contexts
- Implement `av.HWDeviceContext` for CUDA, VAAPI, QSV.
- Implement device enumeration and selection.
- Implement hardware frame pool allocation and transfer (hw→sw, sw→hw).
- **Deliverable**: Can create hardware device contexts and allocate hardware frames in Go tests.
- **Depends on**: P0.3

### P3.2 — Hardware-Accelerated Decoding
- Implement hardware decoder selection: given a codec and device type, find and open a hardware decoder.
- Implement automatic hw→sw frame transfer when downstream nodes require software frames.
- Support H.264 and H.265 hardware decode on CUDA, VAAPI, QSV.
- **Deliverable**: Hardware decode produces same output as software decode (SSIM ≥ 0.99).
- **Depends on**: P3.1, P0.5

### P3.3 — Hardware-Accelerated Encoding
- Implement hardware encoder selection: `h264_nvenc`, `hevc_nvenc`, `h264_vaapi`, `h264_qsv`, etc.
- Implement automatic sw→hw frame upload when upstream provides software frames.
- Pass through hardware frames when both decoder and encoder use the same device (zero-copy path).
- **Deliverable**: Hardware encode produces valid output; throughput significantly exceeds software encode on GPU-equipped CI runners.
- **Depends on**: P3.1, P0.7

### P3.4 — Hardware Filter Support
- Implement hardware-accelerated filters where available (e.g., `scale_cuda`, `scale_vaapi`).
- Implement automatic format conversion insertion when mixing hw/sw filters in a graph.
- **Deliverable**: Scale filter runs on GPU when hardware context is available.
- **Depends on**: P3.1, P1.1

### P3.5 — Subtitle Support
- Implement subtitle stream decoding (text-based: SRT, ASS; bitmap-based: DVB, PGS).
- Implement subtitle burn-in via `subtitles` or `ass` filter.
- Implement subtitle passthrough (copy to output without re-encoding).
- Add `subtitle` edge type support in graph wiring.
- **Deliverable**: Subtitle burn-in and passthrough work for SRT and ASS formats.
- **Depends on**: P1.1, P0.4

### P3.6 — Bitstream Filters
- Implement `av.BitstreamFilter` wrapper (init, send packet, receive packet, close).
- Support common bitstream filters: `h264_mp4toannexb`, `hevc_mp4toannexb`, `aac_adtstoasc`, `extract_extradata`.
- Integrate into pipeline: bitstream filters can be inserted between encoder and muxer via config.
- **Deliverable**: Can remux H.264 from MP4 to MPEGTS with correct annexb conversion.
- **Depends on**: P0.8

### P3.7 — FFmpeg CLI Parser: Hardware & Subtitle Flags
- Extend `ffcli.Parse()` to handle: `-hwaccel`, `-hwaccel_device`, `-hwaccel_output_format`, subtitle selection flags, bitstream filter flags (`-bsf:v`, `-bsf:a`).
- Add ~100 new CLI test cases covering hardware and subtitle scenarios.
- **Deliverable**: `convert-cmd` handles hardware accel and subtitle FFmpeg commands.
- **Depends on**: P1.11, P3.1, P3.5, P3.6

### P3.8 — GPU CI Runners
- Set up CI runners with NVIDIA GPU (for CUDA/NVENC tests).
- Add CI matrix entry for the GPU-equipped runner.
- Hardware tests are skipped gracefully on runners without GPU.
- **Deliverable**: Hardware accel tests run in CI on every merge.
- **Depends on**: P3.2, P3.3, P3.4

### P3.9 — Expanded Test Corpus
- Add to corpus: HDR content (HDR10, HLG), subtitle files (SRT, ASS, PGS), multi-track files with 4+ streams.
- Total corpus: ~200–300 files.
- **Deliverable**: Integration tests cover all Phase 3 features.
- **Depends on**: P3.5, P3.6, P3.8

### P3.10 — Phase 3 Documentation
- Write `docs/hardware-acceleration.md`: setup instructions for CUDA (NVIDIA drivers, CUDA toolkit), VAAPI (Intel/AMD on Linux), and QSV (Intel Media SDK). Document JSON config syntax for selecting hardware devices, zero-copy decode→encode paths, and fallback to software when hardware is unavailable. Include a troubleshooting section.
- Write `docs/subtitles.md`: document subtitle stream selection in JSON config, burn-in vs. passthrough, supported formats (SRT, ASS, DVB, PGS), known limitations, and worked examples.
- Write `docs/bitstream-filters.md`: document all supported bitstream filters, when each is needed (e.g., MP4→MPEGTS remux), JSON config syntax, and examples.
- Update `docs/ffmpeg-migration-guide.md` with hardware accel and subtitle examples (`-hwaccel cuda`, `-vf subtitles=`, `-bsf:v h264_mp4toannexb`).
- Update godoc comments for `av/` package covering `HWDeviceContext`, `BitstreamFilter`, and subtitle types.
- **Deliverable**: A developer can configure hardware acceleration from scratch on each supported platform using docs alone.
- **Depends on**: P3.1 through P3.9

---

## Phase 4 — Production Hardening & Internal Validation

**Goal**: Fuzz-tested, fully validated, production-ready codebase. Repository remains **private** throughout this phase.

**Exit criteria**: Fuzz testing with zero unfixed crashers. JSON Schema generated and validated internally. All performance targets met. Documentation consolidated and reviewed internally. No public releases, sites, or announcements.

### P4.1 — Fuzz Testing
- Add `go test -fuzz` targets for:
  - JSON config parsing (`pipeline.Config` unmarshaling).
  - FFmpeg CLI parsing (`ffcli.Parse()`).
  - Edge definition parsing.
- Run fuzz tests for extended duration (24h+) in CI.
- Triage and fix all crashers found.
- **Deliverable**: Zero unfixed fuzz crashers.
- **Depends on**: P1.11, P0.9

### P4.2 — Property-Based Testing
- Add property-based tests via `pgregory.net/rapid` for:
  - Pipeline graph validation (randomly generated topologies must either pass validation or produce a clear error — no panics/deadlocks).
  - JSON round-trip (marshal → unmarshal → marshal produces identical output).
  - State machine (random sequences of `SetState()` calls never panic, always produce valid transitions or `ErrInvalidStateTransition`).
- **Deliverable**: Property tests pass with 10,000+ iterations.
- **Depends on**: P1.5, P1.3

### P4.3 — Expand Test Corpus to Full Coverage
- Expand to ~500 files drawn from FATE samples.
- Cover all major codec/container/edge-case combinations in spec §13.2.
- Expand FFmpeg CLI parse test suite to ~500+ cases.
- **Deliverable**: Full MMRS test suite as described in spec §13.
- **Depends on**: P3.9

### P4.4 — JSON Schema Generation & Validation
- Generate `schema/v1.0.json` from Go struct definitions.
- Add automated test that validates the schema stays in sync with Go structs.
- Test editor autocompletion (VS Code, JetBrains) with the schema locally.
- **Deliverable**: Schema file generated, validated internally. Not published externally.
- **Depends on**: P0.9

### P4.5 — Schema Migration Tool
- Implement `mediamolder migrate --from=1 --to=2 config.json` (scaffolding for future use).
- For v1.0 release, the tool validates that a config is v1.0-compliant and pretty-prints it.
- **Deliverable**: `mediamolder migrate` command works.
- **Depends on**: P1.9

### P4.6 — Documentation Consolidation (Internal)
- Consolidate and review all `docs/` markdown files written in prior phases for accuracy against code:
  - Getting started / installation guide (from `README.md`).
  - JSON config reference (cross-checked against `schema/v1.0.json`).
  - CLI reference (cross-checked against Cobra `--help` output).
  - Architecture overview (from spec §4).
  - Pipeline state machine, clock/sync, event bus, error handling, dynamic reconfiguration, observability, security, hardware acceleration, subtitles, bitstream filters (all from prior phase docs).
  - FFmpeg migration guide (from `docs/ffmpeg-migration-guide.md`, expanded to 50+ examples).
  - LGPL compliance guide for embedders (from `LICENSING.md`).
  - Benchmark results (from P4.9 output).
- Review all docs for consistency, accuracy, and completeness against implementation.
- Documentation remains in-repo only. Public documentation site deferred to Phase 5.
- **Deliverable**: All docs reviewed, accurate, and ready for publication when the project goes public.
- **Depends on**: P1.17, P2.11, P3.10, P4.5


### P4.8 — Release Packaging Preparation (Internal)
- Verify `go install ./cmd/mediamolder` works from the private repo.
- Prepare build-from-source instructions for all supported platforms (Linux, macOS, Windows).
- Create internal tagged pre-release (`v1.0.0-rc.1`) for validation.
- No pre-compiled binaries or Docker images are distributed (patent license restrictions on compiled codec output).
- Public `go install` from `github.com/MediaMolder/MediaMolder` deferred to Phase 5.
- **Deliverable**: Release packaging validated internally; ready for public release when repo goes public.
- **Depends on**: P4.6

### P4.9 — Performance Validation
- Re-run full benchmark suite; verify all performance targets from spec §15:
  - Throughput overhead < 5% vs. `ffmpeg`.
  - Scheduling latency < 100µs/frame.
  - Memory overhead < 50MB.
  - Startup time < 500ms.
- Document benchmark results in `docs/benchmarks.md`.
- **Deliverable**: All performance targets met and documented internally.
- **Depends on**: P0.12

### P4.10 — Internal v1.0 Milestone
- Final review of all spec requirements.
- Tag `v1.0.0-rc.1` internally.
- Prepare release notes draft.
- All Phase 4 exit criteria met; codebase is release-ready.
- **Deliverable**: Internal milestone reached. Codebase ready for public release when decision is made.
- **Depends on**: P4.1 through P4.9

---

## Phase 5 — Public Release (Future)

**Goal**: Make the project public and release v1.0.

**Pre-requisite**: Decision to go public. Repository visibility changed from private to public.

**Exit criteria**: Public GitHub repo, documentation site live, `go install` works, v1.0 tagged and announced.

### P5.1 — Repository Publication
- Transfer or rename repo to `github.com/MediaMolder/MediaMolder` (or make current repo public).
- Verify `go install github.com/MediaMolder/MediaMolder/cmd/mediamolder@latest` works.
- Publish source-only releases on GitHub (tagged archives with checksums).
- **Deliverable**: Public repo with tagged v1.0.0 release.
- **Depends on**: P4.10

### P5.2 — Documentation Site
- Set up public documentation site (Hugo, MkDocs, or similar).
- Deploy consolidated docs from Phase 4.
- Set up search and versioning on the site.
- Go library API reference (godoc / pkg.go.dev, linked from site).
- **Deliverable**: Comprehensive public documentation site deployed.
- **Depends on**: P4.6, P5.1

### P5.3 — JSON Schema Publication
- Publish `schema/v1.0.json` as a standalone artifact alongside the release.
- **Deliverable**: Schema file publicly available for editor autocompletion and CI validation.
- **Depends on**: P4.4, P5.1

### P5.4 — Announcement
- Publish release notes.
- Announce on relevant channels.
- **Deliverable**: v1.0 publicly announced.
- **Depends on**: P5.1, P5.2, P5.3


