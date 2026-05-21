**MediaMolder: Project Specification**  
**License:** LGPL-2.1 or later (see §14 for details)  
**Language:** Go (top-level orchestration and public APIs)  
**Minimum Go version:** 1.23+  
**Minimum FFmpeg/libav version:** FFmpeg 8.1+ (libavcodec 62.x, libavformat 62.x, libavfilter 11.x, libavutil 60.x)  
**Underlying libraries:** Dynamic linking to libavcodec, libavformat, libavfilter, libavutil, libswscale, libswresample, and any hardware acceleration backends (same as FFmpeg)  
**Repository:** github.com/MediaMolder/mediamolder
**CLI binary:** `mediamolder`

### 1. Project Overview
MediaMolder is a **new, independent open-source media processing engine** written primarily in Go. It reuses the battle-tested libav* C libraries for all heavy lifting (demuxing, decoding, filtering, encoding, muxing, hardware acceleration) but replaces the entire command-line-driven, string-based architecture of FFmpeg with a clean, modern, Go-native design.

The goal is **maximum usability and operational reliability** while preserving 100 % of FFmpeg's functional capabilities. It is **not** a wrapper or fork of the FFmpeg CLI; it is a ground-up redesign of the high-level pipeline layer.

### 2. Primary Objectives
- Eliminate command-line escaping hell and cryptic filtergraph strings.
- Provide a declarative, structured, version-controlled configuration model (**JSON as the primary command payload**, in-memory Go structs).
- Deliver first-class runtime observability, dynamic control, and resilience for long-running jobs.
- Make the engine trivially embeddable as a library in any Go program.
- Achieve developer ergonomics that attract a much larger contributor base than C-based FFmpeg.
- Maintain identical media capabilities (formats, codecs, filters, devices, bitstream filters, etc.) through direct libav* bindings.
- Remain fully LGPL compliant (see §14).
- Provide a function to parse any compliant FFmpeg command-line string, converting to a compliant JSON command payload (see §7).

### 3. Non-Goals
- Pure-Go implementation of codecs/filters (performance and compatibility reasons).
- Replacing any libav* library.

### 4. High-Level Architecture
```
+-------------------+     +---------------------+
|   Public API /    |     |  Observability      |
|   Library Layer   |---->|  & Metrics          |
+-------------------+     +---------------------+
           |
           v
+-------------------+
|   Pipeline Engine |
|   (Go)            |
+-------------------+
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
- **Observability**: Built-in metrics and structured events for monitoring pipeline health.
- **Execution Model**: Goroutine-per-stage concurrency model using Go channels for packet/frame flow. Each pipeline stage runs in its own goroutine group, with configurable channel buffer sizes providing natural back-pressure.

### 5. Technical Stack
- **Language**: Go 1.23+ (modules, generics, structured concurrency via errgroup/context).
- **Build**: cgo enabled; dynamic linking of FFmpeg libs via pkg-config (static linking available but see §14 for LGPL implications).
- **Configuration**: Viper + custom JSON Schema validation (**JSON as primary command payload**).
- **Serialization**: Native Go structs that can be marshaled to/from JSON.
- **Observability**: OpenTelemetry + Prometheus exporter (metrics, traces, logs).
- **Logging**: Zerolog or slog with structured context.
- **Testing**: See §13 for detailed testing strategy.

### 6. Core Components (detailed)

#### 6.1 Binding Layer (`mediamolder/av`)
- Thin, manually maintained Go wrappers for every public libav* API needed.
- Provides:
  - `av.FormatContext`, `av.CodecContext`, `av.FilterGraph`, `av.Frame`, `av.Packet`, etc.
  - **Explicit resource management via `Close()`** as the primary mechanism. All types wrapping C resources implement `io.Closer`. Callers are responsible for calling `Close()` (typically via `defer`). A build-tag-gated leak detector (`-tags=avleakcheck`) logs warnings for unclosed resources in development/test builds.
  - Go error types with rich context (`av.Err` wrapping AVERROR codes).
  - Channel-based streaming APIs (`FrameChan`, `PacketChan`).
  - Hardware device context helpers (CUDA, VAAPI, QSV, etc.).
- All filter parameters are native Go structs (no string escaping).
- **cgo boundary safety**: All C pointer lifetimes are pinned to Go object lifetimes via explicit allocation/free pairs. No C pointers are stored in Go heap objects beyond their owning wrapper. Race-detector-compatible tests exercise concurrent access patterns.

#### 6.2 Pipeline Definition (`mediamolder/pipeline`)
- **Config** struct containing:
  - `inputs[]` — array of `Input` with URL, kind, demuxer options, structured stream selection, timing/seek parameters
  - `graph` — directed acyclic graph of `NodeDef` nodes and `EdgeDef` edges
  - `outputs[]` — array of `Output` with muxer, codec, HLS/DASH options, two-pass, HDR metadata, attachments
  - Global options: thread count, hardware accel, metadata, timestamps, `filter_complex_threads`
- **Node types** (`NodeDef.type`):
  - `"filter"` — any libavfilter filter (scale, drawtext, overlay, …). `filter` names the libavfilter string; `params` carries typed options. Optional `threads` overrides per-node filter thread count; `output_media_type` declares the outbound pad type for cross-media filters (e.g. `showwavespic`).
  - `"filter_source"` — libavfilter source that synthesises frames without a demuxer input (allow-listed: `color`, `testsrc`, `testsrc2`, `smptebars`, `sine`, `anullsrc`, `aevalsrc`, `movie`, `amovie`, etc.). Required when a pipeline has no top-level `inputs[]`.
  - `"filter_sink"` — libavfilter sink that terminates an analyser branch without a muxer output (allow-listed: `nullsink`, `anullsink`).
  - `"go_processor"` — a Go-native frame processor from the `mediamolder/processors` registry (see §18). `processor` names the registered type; `params` is forwarded to `Processor.Init`.
  - `"encoder"`, `"source"`, `"sink"` — internal legacy aliases; prefer the types above in new configs.
- **Input extended fields**:
  - `kind`: `""` / `"file"` (default), `"lavfi"` (libavfilter source via lavfi demuxer), `"raw"` (unframed PCM/video requiring `format` + geometry fields), `"concat"` (libavformat concat demuxer with inline `concat_list[]`).
  - `accurate_seek`, `seek_timestamp`, `thread_queue_size`, `protocol_whitelist[]`, `pattern_type`.
  - `stream_loop` for looped inputs; `itsoffset` for per-input timestamp shift; `read_rate` / `read_rate_initial_burst` / `read_rate_catchup` for live-restream pacing.
  - `map_metadata`, `map_chapters`, `subtitle_charenc` for container-level metadata control.
  - `concat_list[]` — inline playlist (avoids sidecar `.txt` files for concat workflows).
- **Output extended fields**:
  - `kind`: `""` / `"file"` (default), `"tee"` — fan-out to multiple destinations via libavformat tee muxer. Tee outputs carry a `targets[]` array of `TeeTarget` (each with `url`, `format`, `select`, `on_fail`, `use_fifo`, `fifo_options`).
  - `pass` (1 or 2) + `passlogfile` — two-pass video encoding. The runtime assigns a unique pass-log index to each two-pass output so multiple passes in one job do not collide.
  - `loudnorm_pass` (1 or 2) — EBU R128 two-pass loudness normalisation via the `loudnorm` filter.
  - `hls` — typed `HLSOptions` struct (segment duration, playlist type, segment type, fMP4 init filename, HLS flags, etc.) instead of raw `options` map entries.
  - `dash` — typed `DASHOptions` struct (segment duration, window size, extra windows, LDASH, HLS playlist sidecar, DASH flags, etc.).
  - `attachments[]` — files muxed as `AVMEDIA_TYPE_ATTACHMENT` streams (MKV/WebM only; mirrors FFmpeg's `-attach`).
  - `audio_sync` — resync compensation threshold (mirrors `-async N`).
  - `shortest` — stop muxing when the shortest input stream ends.
  - `max_frames_video` / `max_frames_audio` — packet caps per stream type.
  - `fps_mode` — VFR/CFR reconciliation policy.
  - `bsf_video` / `bsf_audio` / `bsf_subtitle` — bitstream filter chains.
  - `codec_tag_video` / `codec_tag_audio` / `codec_tag_subtitle` — FourCC overrides (e.g. `"hvc1"` for Safari HEVC).
  - `encoder_params_video` / `encoder_params_audio` / `encoder_params_subtitle` — codec-specific options (preset, crf, tune, …) attached to implicit encoders; populated by `ffcli.Parse` for CLI round-trips.
- **HDR / colour metadata** (`Output.VideoStream.ColorSpace`, `HDRMetadata`): SMPTE ST 2086 mastering display, CTA-861.3 content light level, and Dolby Vision (`DoViMetadata`) configuration record (`AVDOVIDecoderConfigurationRecord`).
- **Graph UI layout** (`GraphDef.UI`): optional `positions` map keyed by node ID, ignored by the runtime, preserved on round-trip so the visual editor can persist canvas layouts in schema v1.2 configs.
- Edges connect named ports between nodes and carry a media type (`video`, `audio`, `subtitle`, `data`). Validation rejects edges that connect incompatible port types.
- `DisallowUnknownFields` is enforced on all `ParseConfig` calls; schema mismatch surfaces as a clear parse error rather than silent mis-validation.

#### 6.3 Graph Engine (`mediamolder/graph`)
- Builds libavfilter graph from the declarative Go model (loaded from JSON command payload).
- Supports **static** and **dynamic** graphs:
  - Dynamic: add/remove/replace nodes at runtime via the Go API (`Pipeline.Reconfigure()`).
  - Hot-reconfiguration of filter parameters (e.g., change drawtext string live).
- **Dynamic reconfiguration contract**:
  - **Parameter changes** (e.g., drawtext string, volume level): Applied between frames. The engine drains the current frame from the affected filter, applies the new parameter, and resumes. No frames are dropped.
  - **Node add/remove**: Requires a quiesce step. The engine stops reading new packets into the affected subgraph, drains all in-flight frames to the nearest sink or buffer point, applies the structural change, and resumes. Callers receive an acknowledgement callback/channel when the change is live.
  - **Codec changes mid-stream**: Not supported for a given output. To change codecs, remove the output and add a new one (which triggers a new segment/file).

#### 6.4 Runtime Scheduler (`mediamolder/runtime`)
- Goroutine-per-stage model: each pipeline node runs in its own `errgroup` goroutine group.
- Native Go channels for frame/packet flow with configurable buffering (default: 8 frames per channel).
- Automatic back-pressure: a slow consumer blocks its upstream producer via Go channel semantics.
- Dedicated output lanes: each output sink has its own goroutine group to prevent one slow output from stalling others.
- Watchdog timers for stalled stages (configurable timeout, default: 30s).
- Graceful drain on shutdown: context cancellation signals all stages; each stage flushes buffered data before exiting.

#### 6.5 Pipeline State Machine (`mediamolder/pipeline`)

Every pipeline follows a formal state machine with well-defined transitions and invariants:

```
  NULL ──► READY ──► PAUSED ──► PLAYING
   ▲         │         │          │
   └─────────┴─────────┴──────────┘
                (any → NULL)
```

| State | Description |
|-------|-------------|
| `NULL` | Pipeline struct is allocated but no resources are opened. No libav* contexts exist. |
| `READY` | Inputs probed, codecs resolved, filter graph validated, all libav* contexts allocated. No data flows. Equivalent to "armed." |
| `PAUSED` | Graph is fully wired. Data has been read up to the first frame/packet in each stage but sinks are not consuming. Useful for pre-roll and seek-then-inspect workflows. |
| `PLAYING` | Data flows through all stages. Sinks are actively writing output. |

**Transition rules:**
- Transitions must be sequential upward: `NULL → READY → PAUSED → PLAYING`. Skipping states (e.g., `NULL → PLAYING`) is not allowed; the engine transitions through intermediate states automatically if the caller requests a skip.
- Any state can transition directly to `NULL` (teardown). This drains in-flight data (best-effort) and frees all resources.
- `PLAYING → PAUSED` is allowed (suspends data flow without teardown).
- `PAUSED → PLAYING` resumes from exactly where data flow stopped.
- Invalid transitions return a `PipelineError` with code `ErrInvalidStateTransition`.

**Go API:**
```go
p.State() pipeline.State          // returns current state
p.SetState(pipeline.Playing) error // requests a transition
// Convenience methods:
p.Start() error                    // NULL → PLAYING (through READY, PAUSED)
p.Pause() error                    // PLAYING → PAUSED
p.Resume() error                   // PAUSED → PLAYING
p.Close() error                    // any → NULL
```

**Events:** Every state transition emits a `StateChanged` event on the event bus (see §6.7) containing the previous state, new state, and transition duration.

#### 6.6 Clock & Synchronization (`mediamolder/clock`)

MediaMolder provides a pipeline clock system for A/V synchronization, live source timing, and multi-input alignment.

- **Pipeline clock**: Every pipeline has a single reference clock. By default, a monotonic system clock is used. For live inputs (RTMP, RTSP, SRT), the source provides the clock.
- **Clock selection**: When multiple inputs provide clocks, the pipeline selects one as the master (configurable; defaults to the first live source, or the system clock if all inputs are file-based). Other sources are slaved to the master clock.
- **Sink synchronization**: Sink nodes render/write at the correct wall-clock time relative to the pipeline clock. For file outputs, this means frames are muxed with correct PTS/DTS. For live outputs (e.g., RTMP push), the sink paces output to match real-time.
- **A/V sync**: Audio and video sinks sharing an output are synchronized against the pipeline clock. The engine inserts silence or drops audio samples (within a configurable tolerance, default: ±40ms) to maintain lip-sync. Video frames outside the sync window are dropped (late) or held (early).
- **Seek**: `Pipeline.Seek(target time.Duration)` pauses the pipeline, flushes all stage buffers, seeks all inputs to the target timestamp (nearest keyframe), and resumes from `PAUSED`. Callers must explicitly call `Resume()` or `SetState(Playing)` after seek.
- **Live vs. file mode**: The clock system auto-detects live vs. file inputs. File inputs run as fast as possible (no clock pacing) unless `"realtime": true` is set in the config. Live inputs always pace to real-time.

**Go API:**
```go
p.Clock() *clock.Pipeline           // access the pipeline clock
p.Seek(target time.Duration) error   // seek all inputs
```

#### 6.7 Observability & Event Bus
- Built-in metrics: fps, bitrate, latency per node, buffer levels, CPU/GPU usage, errors.
- Metrics exposed via OpenTelemetry SDK and Prometheus exporter.
- **Event bus**: A centralized, typed, async message bus. Pipeline components post structured events; the application subscribes to event types of interest.
  - Event types include: `StateChanged`, `Error`, `EOS` (end of stream), `StreamStart`, `BufferingPercent`, `MetricsSnapshot`, `ReconfigureComplete`, `ClockLost`.
  - **Go API**: `p.Events() <-chan pipeline.Event` returns a read-only channel. Events are non-blocking; slow consumers receive a `BufferOverflow` warning and missed events are counted in metrics.
  - The event bus decouples event producers from consumers, making it straightforward to add new event types without changing the pipeline API.
- **Go API for live control**: `Pipeline.SetState()`, `Pipeline.Pause()`, `Pipeline.Resume()`, `Pipeline.Seek()`, `Pipeline.Reconfigure()`, `Pipeline.AddOutput()`, `Pipeline.GetMetrics()`, `Pipeline.GetGraphSnapshot()`. These methods are the foundation on which a future HTTP or gRPC control plane can be built (see §21).

### 7. FFmpeg Command-Line Compatibility Layer

MediaMolder provides a parser that accepts FFmpeg CLI command strings and converts them to MediaMolder's structured JSON pipeline config.

- **Package**: `mediamolder/compat/ffcli`
- **Function**: `ffcli.Parse(cmdline string) (*pipeline.Config, error)`
- **Scope**: Supports the full set of FFmpeg global options, input/output options, codec selection flags, filter graph strings (simple and complex), stream specifiers, and map directives.
- **Mapping rules**:
  - `-i <url>` → structured `inputs[]` with typed `streams[]` selection
  - `-vf` / `-af` / `-filter_complex` → parsed and decomposed into `graph.nodes[]` and `graph.edges[]` with typed connections
  - `-c:v`, `-c:a`, `-b:v`, etc. → `outputs[].codec_*` and `outputs[].options`
  - `-map` → explicit edge wiring
- **Known limitations**: Device inputs (`-f avfoundation`, `-f v4l2`, etc.) are parsed but runtime behavior depends on OS support. Non-standard or undocumented FFmpeg flags may produce a parse error with context.
- **Round-trip property**: `ffcli.Parse(cmd)` → JSON → `mediamolder run` must produce functionally equivalent output to the original `ffmpeg` command. This property is verified by the integration test suite (see §13).
- **CLI integration**: `mediamolder convert-cmd "ffmpeg -i in.mp4 -c:v libx264 out.mp4"` prints the equivalent JSON payload to stdout.

### 8. JSON Schema Versioning & Migration

- Every JSON command payload includes a top-level `"schema_version"` field (e.g., `"schema_version": "1.2"`).
- **Supported versions**: `"1.0"`, `"1.1"`, `"1.2"`. The parser rejects any other value with a clear error.
- **Parsing rules**:
  - Unknown fields are rejected by default (`DisallowUnknownFields`). An `--allow-unknown-fields` flag enables lenient parsing for forward compatibility during transitions.
  - Missing optional fields receive documented defaults.
- **Schema evolution**:
  - `1.0 → 1.1`: Added per-output `hls` / `dash` typed option structs, `attachments[]`, `pass` / `passlogfile` two-pass video encoding, `loudnorm_pass` audio two-pass.
  - `1.1 → 1.2`: Added `graph.ui` layout metadata (editor canvas positions), `filter_source` / `filter_sink` node types, `go_processor` node type, extended input fields (`stream_loop`, `itsoffset`, `read_rate`, `read_rate_catchup`, `read_rate_initial_burst`, `map_metadata`, `map_chapters`, `subtitle_charenc`), output fields (`audio_sync`, `shortest`, `max_frames_video`, `max_frames_audio`, `fps_mode`, `codec_tag_*`, `encoder_params_*`), HDR/DoVI metadata.
  - Minor versions are additive: older payloads remain valid without modification.
  - Major versions (1.x → 2.0): Breaking changes. A migration tool (`mediamolder migrate`) rewrites payloads automatically.
- **JSON Schema files** are published alongside releases at `mediamolder/schema/v1.0.json`, `mediamolder/schema/v1.1.json`, etc. and are usable for editor autocompletion and CI validation. (`schema/v1.2.json` is pending; the runtime accepts v1.2 configs but the published schema file has not been generated yet.)

### 9. Configuration Example (JSON – Primary Command Payload)
```json
{
  "schema_version": "1.0",
  "inputs": [
    {
      "id": "main",
      "url": "input.mkv",
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
    ],
    "edges": [
      { "from": "main:v:0",     "to": "scale:default",    "type": "video" },
      { "from": "scale:default", "to": "drawtext:default", "type": "video" },
      { "from": "drawtext:default", "to": "hls:v",         "type": "video" },
      { "from": "main:a:0",     "to": "hls:a",            "type": "audio" }
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

### 10. Error Model

Every error in MediaMolder is represented by a structured `PipelineError`:

```go
type PipelineError struct {
    NodeID    string         // which graph node produced the error
    Stage     string         // "demux", "decode", "filter", "encode", "mux"
    Code      int            // AVERROR code (if from libav*) or MediaMolder error code
    Message   string         // human-readable description
    Timestamp time.Time
    Transient bool           // true if the error is likely recoverable
}
```

**Error policies** are configured per-node in the JSON payload or via Go API:

| Policy     | Behavior |
|------------|----------|
| `abort`    | Stop the entire pipeline immediately. Default for mux/encode errors. |
| `skip`     | Drop the current packet/frame and continue. Default for decode errors on non-keyframes. |
| `retry`    | Re-attempt the operation up to `max_retries` times (default: 3) with exponential backoff (base: 100ms, max: 5s). If all retries fail, escalate to the `fallback` policy. |
| `fallback` | Route the stream to an alternative node if one is defined (e.g., a backup encoder). If no fallback node is configured, escalate to `abort`. |

Error policies are set in the JSON config per-node:
```json
{
  "id": "scale",
  "type": "filter",
  "filter": "scale",
  "params": { "width": 1280, "height": 720 },
  "error_policy": {
    "policy": "retry",
    "max_retries": 5,
    "fallback_node": "scale_sw"
  }
}
```

### 11. Reliability Features
- Stage-level isolation (separate goroutine groups with panic recovery).
- Granular error policies per node (see §10).
- Transactional file output (write to `.tmp` then atomic rename).
- Automatic node restart on transient failures.
- Context cancellation propagates cleanly through the entire pipeline.
- Comprehensive crash reports with graph snapshot.

### 12. Library Usage Example
```go
p, err := mediamolder.NewPipeline(ctx, config)
if err != nil { ... }

err = p.Start()
defer p.Close()

// Live control
go controlLoop(p)

// Blocking run until finished
err = p.Wait()
```

**Multi-pipeline concurrency**: Multiple `Pipeline` instances can run concurrently within a single process. Each pipeline owns its own `av.FormatContext`, `av.CodecContext`, and goroutine groups — there is no shared mutable state between pipelines. The binding layer is thread-safe at the libav* level (FFmpeg is initialized once via `sync.Once`). Callers may set per-pipeline resource limits (max threads, max memory) via `PipelineOptions`.

### 13. Testing Strategy (MediaMolder Regression Suite — MMRS)

MediaMolder does **not** need to retest codec/filter/format correctness — that is the responsibility of FFmpeg's own FATE suite and the libav* libraries. MMRS focuses on everything in the Go layer: binding correctness, pipeline wiring, configuration parsing, error handling, and cross-version/cross-platform compatibility.

#### 13.1 Unit Tests
- Every Go package has table-driven unit tests.
- The binding layer uses a mock C interface (build-tagged) for fast, hermetic tests that don't require FFmpeg installed.

#### 13.2 Integration Tests
End-to-end pipeline tests against a curated media corpus:
- **Corpus source**: A curated subset (~200–500 files) drawn from FFmpeg's publicly hosted FATE samples (`fate-suite.ffmpeg.org`), covering major formats/codecs and edge cases. Stored in Git LFS. The full FATE corpus (~5,000+ files) is not needed since we are not retesting libav* internals.
- **Corpus coverage**: H.264/H.265/VP9/AV1 video, AAC/Opus/FLAC audio, MKV/MP4/HLS/DASH containers, subtitle formats (SRT, ASS), multi-track files, HDR content, and edge cases (corrupt headers, truncated files, zero-length streams).
- **Correctness criteria**: Output is validated by re-demuxing and checking stream count/codec/duration. For video quality, SSIM ≥ 0.95 against a reference output. For audio, sample-level comparison with tolerance for codec-inherent drift (≤ 1024 samples). Byte-exact matching is explicitly avoided — codec output varies by libav* version and build flags.
- **FFmpeg parity tests**: For every `ffcli.Parse` test case, the JSON output is run through MediaMolder and compared against the equivalent `ffmpeg` command output using SSIM/sample comparison.

#### 13.3 Pipeline & Graph Regression Tests
- **Golden-file tests**: A suite of JSON configs with expected resolved graph structures. Given the same config, the constructed pipeline graph must match the golden snapshot. Catches regressions in graph construction, edge wiring, and validation logic.
- **Error policy tests**: Configs with intentionally corrupt/missing inputs to verify that skip, retry, fallback, and abort policies behave as specified (see §10).

#### 13.4 Compatibility Layer Tests
- `ffcli.Parse()` is the highest-risk component. A large suite (~500+) of FFmpeg CLI strings with expected JSON output, verified via structural comparison and round-trip execution.
- Covers: simple transcodes, complex filtergraphs, multi-input/multi-output, stream mapping, hardware accel flags, and known edge cases in FFmpeg argument parsing.

#### 13.5 Cross-Platform / Cross-Version CI Matrix
- **Platforms**: Linux (Ubuntu LTS), macOS (latest), Windows (latest).
- **FFmpeg versions**: 8.1 (minimum supported and current stable). CI builds against the latest patch release to catch regressions.
- **Go versions**: Current stable and previous stable release.
- CI runs on GitHub Actions. Unlike FFmpeg's FATE (which relies on a distributed volunteer farm), standard CI runners are sufficient since we are not testing codec internals.

#### 13.6 Property-Based & Fuzz Tests
- **Property-based tests** (via `testing/quick` or `pgregory.net/rapid`): Exercise the pipeline definition layer with randomly generated graph topologies to catch panics, deadlocks, and validation gaps.
- **Fuzz tests**: `go test -fuzz` targets for JSON config parsing and FFmpeg CLI parsing to find crashes on malformed input.

#### 13.7 Benchmark Suite
- Standardized transcoding benchmarks (see §15) run in CI on every merge to `main`.
- Throughput, latency, and memory metrics tracked over time. Regressions > 10% block merges.

### 14. Licensing & LGPL Compliance

MediaMolder's Go code is licensed under **LGPL-2.1-or-later**, matching the libav* libraries it links.

- **Dynamic linking** is the default and recommended build mode. The `mediamolder` binary dynamically links to `libavcodec.so`, `libavformat.so`, etc. via cgo + pkg-config. This satisfies LGPL requirements: end users can replace the shared libraries with their own builds.
- **Static linking**: Supported via `-tags=static` for convenience (e.g., Docker images). Because MediaMolder itself is also LGPL, static linking is permissible — but the complete corresponding source for both MediaMolder and the linked libav* libraries must be provided alongside any distributed binary. Build documentation explains this obligation.
- **Third-party embedders**: Any proprietary application embedding MediaMolder as a library must either dynamically link or comply with LGPL re-linking requirements. This is documented prominently in the README and LICENSING guide.

### 15. Performance Targets

- **Throughput overhead**: < 5% throughput reduction compared to an equivalent `ffmpeg` CLI command for CPU-bound transcoding (measured on a standardized benchmark: 1080p H.264→H.264, `medium` preset, single output).
- **Latency per frame**: Pipeline scheduling overhead (Go layer) < 100µs per frame on average, measured on commodity hardware (4-core, 3GHz).
- **Memory overhead**: Go heap allocation < 50MB above the libav* baseline for a single 1080p pipeline.
- **Startup time**: Pipeline creation (parse JSON + build graph + open codecs) < 500ms for a typical single-input, single-output configuration.
- **Benchmarks** are tracked in CI and regressions > 10% block merges.

### 16. Security Considerations

#### 16.1 Input Validation
- **URLs / input sources**: The `ProtocolWhitelist` field on each `Input` restricts which libavformat protocols that input may dereference (mirrors FFmpeg's `protocol_whitelist` AVOption). Default is libavformat's compiled-in set; set to `["file"]` to forbid all network access for a given input. `movie` / `amovie` filter nodes enforce the same whitelist via a `protocol_whitelist` node param; the runtime rewrites it as a `format_opts` dictionary entry so libavformat honours it inside the filter demuxer. Malformed whitelist entries (empty values, embedded commas) are rejected at config parse time.
- **`movie` / `amovie` filenames**: Rejected at config parse time if the filename contains NUL, CR, or LF bytes, which would truncate the libavfilter args parser or inject a synthetic filter chain.
- **Concat listfile entries**: File paths in `ConcatList` are rejected if they contain a single quote or newline — characters that would break the serialised listfile grammar.
- **Media content**: The libav* libraries handle demuxing/decoding of potentially hostile media. MediaMolder adds resource limits on top:
  - Maximum input file size (configurable, default: none).
  - Maximum decode dimensions (configurable, default: 16384×16384).
  - Maximum stream count per input (configurable, default: 64).
  - Timeout on demux probe (default: 10s) to prevent slowloris-style hangs on network inputs.

#### 16.2 HTTP Server (GUI)
The `mediamolder gui` HTTP server (§18) is intended for **localhost use only** by default (bound to `127.0.0.1`). When bound to a non-loopback address, the following hardening applies:
- **Server-level timeouts**: `ReadHeaderTimeout` (10s), `ReadTimeout` (30s), `WriteTimeout` (60s), `IdleTimeout` (120s) are set on the `http.Server` to prevent slow-loris and connection-leak attacks.
- **Request body limits**: POST bodies are capped — `1 MiB` for `/api/validate` and `/api/run` (`jobConfigBodyLimit`), `16 KiB` for `/api/files/mkdir` (`mkdirBodyLimit`). These constants are in `internal/gui/run.go` and `internal/gui/files.go` respectively; raise them and recompile for pipelines with very large concat lists.
- **File browser path confinement**: `GET /api/files` and `POST /api/files/mkdir` restrict browsing to a set of `defaultRoots` (home directory, cwd, filesystem root on Unix, drive letters on Windows, and mounted volumes). Paths outside the allowed roots are rejected with HTTP 400.
- **No shell usage**: Any OS-level subprocess invocations (e.g. opening a browser) use `exec.Command(binary, args...)` with argument arrays — no shell interpolation, no `sh -c`. MediaMolder does not spawn `ffmpeg` or `ffprobe` as subprocesses; all media operations go through the linked libav\* libraries via cgo.

#### 16.3 cgo Boundary Safety
- All C allocations are paired with explicit frees in `Close()` methods. The leak-detection build tag (`-tags=avleakcheck`) tracks allocations and reports leaks at process exit.
- No C pointers are stored in Go objects that may be moved by the garbage collector. All C pointer access is scoped within cgo call blocks or pinned via `runtime.Pinner`.
- Integration tests run under AddressSanitizer (`CGO_CFLAGS=-fsanitize=address`) in CI.

#### 16.4 Resource Limits
- Configurable maximum concurrent pipelines per process (default: 16).
- Per-pipeline memory cap (enforced via `RLIMIT_AS` on Linux or manual tracking).
- Per-pipeline CPU thread limit (passed through to libav* thread pool).

### 17. CLI Tool (`mediamolder`)
- `mediamolder run [--json] [--metadata-out=PATH] [--set KEY=VALUE ...] config.json` — execute a pipeline. `--json` streams progress as JSON Lines; `--metadata-out` writes processor metadata events to a file or stdout; `--set` performs `{{KEY}}` template substitution in the JSON before parsing (enables re-usable parameterised configs).
- `mediamolder inspect config.json` — validate and pretty-print the resolved pipeline graph.
- `mediamolder convert-cmd "ffmpeg ..."` — parse an FFmpeg CLI string and emit the equivalent JSON payload.
- `mediamolder migrate [--from=N --to=N] config.json` — migrate a config payload between schema versions.
- `mediamolder probe <url>` — probe a media file and print stream metadata as JSON.
- `mediamolder list-codecs`, `list-filters`, `list-formats` — enumerate libav* capabilities.
- `mediamolder list-hw-devices` — probe which hardware acceleration devices (CUDA, VAAPI, QSV, D3D11VA, …) are available on the host. Flags: `--json` (machine-readable array), `--all` (include unavailable devices).
- `mediamolder list-processors` — enumerate registered Go processor types (see §19).
- `mediamolder gui [--host=127.0.0.1] [--port=8080] [--no-open] [--examples=DIR] [--dev]` — serve the browser-based visual editor (see §18).
- `mediamolder version` — print version, FFmpeg configuration, and licence type.
- Real-time progress and JSON status output.

### Related Documents
- [Build & Packaging](../build_and_packaging.md)
- [Development Roadmap](../roadmap/roadmap.md)
- [Contribution & Governance](../../contribution_and_governance.md)
- Possible Future Improvements

---

### 18. Visual Editor (`mediamolder gui`)

The `mediamolder gui` subcommand serves a browser-based visual pipeline editor embedded in the same binary as the CLI — no separate install is required.

#### 18.1 Architecture
- **Backend**: Go HTTP server (`internal/gui`) built on `net/http`. The embedded React/TypeScript frontend is compiled to static assets (`frontend/`) and served from the binary via `embed.FS`. In dev mode (`--dev`) the backend serves only the API; the Vite dev server handles the frontend at `localhost:5173`.
- **Frontend**: React + TypeScript (strict mode) built on [React Flow](https://reactflow.dev) (`@xyflow/react`). Translation between the visual graph and the JSON pipeline config is handled by `frontend/src/lib/jsonAdapter.ts` (`flowToConfig` / `configToFlow`).

#### 18.2 REST API

| Method | Path | Description |
|--------|------|-------------|
| `GET`  | `/api/health` | Liveness check. |
| `GET`  | `/api/nodes` | Palette: full node catalog (filter / encoder / processor metadata). |
| `GET`  | `/api/examples` | List example JSON files from `--examples` directory. |
| `GET`  | `/api/files` | Directory listing for the file picker (path-confined to `defaultRoots`). |
| `POST` | `/api/files/mkdir` | Create a subdirectory (file-save dialog "New folder"). Body capped at `mkdirBodyLimit` (16 KiB). |
| `POST` | `/api/probe` | Probe a URL with FFprobe; returns stream metadata as JSON. |
| `POST` | `/api/convert-cmd` | Parse an FFmpeg CLI string; returns the equivalent pipeline JSON. |
| `GET`  | `/api/encoders/{name}/options` | AVOptions for a named encoder. |
| `GET`  | `/api/filters/{name}/options` | AVOptions (including expression-typed flags) for a named filter. |
| `GET`  | `/api/filters/{name}/eval-expression` | Evaluate a libavutil expression with named variable bindings (powers the live expression preview). |
| `POST` | `/api/validate` | Parse and structurally validate a pipeline config JSON; returns `{ok, inputs, outputs, nodes, edges}`. Body capped at `jobConfigBodyLimit` (1 MiB). |
| `POST` | `/api/run` | Start a pipeline job; returns `{job_id}`. Body capped at `jobConfigBodyLimit`. |
| `POST` | `/api/cancel/{jobId}` | Cancel a running job. |
| `GET`  | `/api/events/{jobId}` | Server-Sent Events stream of job progress (frames, fps, errors, EOS). |

#### 18.3 Job Model
- Each `POST /api/run` spawns a `jobManager` entry. Jobs are identified by a UUID string.
- Progress is streamed over SSE as typed JSON events: `progress` (frame count, fps per stream), `error`, `done`.
- Node badges in the UI show live frame counts and fps; erroring nodes are outlined in red.
- At most one run per job ID; cancellation is context-propagated through the pipeline.

#### 18.4 Filter Expression Evaluation
`GET /api/filters/{name}/eval-expression?expr=…&VAR=N` evaluates the given libavutil expression using a curated per-filter variable table (`filterExprVars` in `internal/gui/filter_eval.go`). Unknown filters fall back to the universal timeline set (`t`, `n`, `pos`, `w`, `h`). The endpoint returns `{ok, value}` on success or `{ok:false, error}` on a parse/eval failure — HTTP 200 in both cases so the frontend can display inline diagnostics without treating eval failures as transport errors.

---

### 19. Go Processor System (`mediamolder/processors`)

Go processor nodes (`type: "go_processor"` in the pipeline config) let application code insert arbitrary Go logic into the frame pipeline — analysis, machine-learning inference, metadata emission — without modifying the core engine.

#### 19.1 Interface

```go
type Processor interface {
    Init(params map[string]any) error
    Process(frame *av.Frame, ctx ProcessorContext) (*av.Frame, *Metadata, error)
    Close() error
}
```

- `Init` receives the node's `params` map from the JSON config; called once before the first frame.
- `Process` receives each decoded frame and returns the (possibly modified) frame, optional metadata, and an error. Returning `nil` for the frame drops it from the pipeline.
- `Close` is called when the pipeline shuts down.

#### 19.2 Registry

Processors are registered by name at init time:

```go
processors.Register("my_processor", func() Processor { return &MyProcessor{} })
```

The runtime resolves `NodeDef.Processor` against the registry at graph-build time; an unknown name fails validation. `mediamolder list-processors` enumerates all registered names.

#### 19.3 Built-in Processors

| Name | Description |
|------|-------------|
| `frame_info` | Emits per-frame metadata (PTS, duration, width, height, pixel format, key-frame flag) as JSON Lines to a configurable output file. Used for frame-extraction and inspection workflows. |
| `scene_change` | Detects scene boundaries by computing inter-frame mean absolute difference. Emits `{scene_change: true, score: N}` metadata on cut frames. Configurable threshold. |
| `frame_counter` | Counts frames passing through; emits count at EOS. Useful for testing and batch-size assertions. |
| `metadata_writer` | Wraps an inner processor and writes its `Metadata` output as JSON Lines to a file or stdout, decoupling metadata I/O from frame processing logic. |
| `null` | No-op pass-through. Used in tests. |
| `yolo_v8` | YOLOv8 object detection via ONNX Runtime (build tag `with_onnx`). Emits bounding-box metadata per frame. Model path and confidence threshold are configurable via `params`. |

#### 19.4 Metadata Events

Processor metadata is surfaced in two ways:
1. **CLI**: `mediamolder run --metadata-out=PATH` writes all `Metadata` events from every processor node as JSON Lines to `PATH` (use `-` for stdout).
2. **GUI**: The SSE job-event stream includes processor metadata events so the frontend can display analysis results live.

Metadata is a typed struct:
```go
type Metadata struct {
    NodeID    string
    PTS       int64
    Timestamp time.Time
    Custom    map[string]any
}
```
