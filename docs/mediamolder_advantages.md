**MediaMolder** is a modern, open-source media processing engine. 

It is a ground-up redesign of the interface and orchestration layers of FFmpeg. The
goal is to match FFmpeg's functional requirements, while also meeting the 
non-functional requirements of modern applications, including usability, observability,
maintainability, extensibility, portability and security.

MediaMolder is built on the same proven libav\* libraries — libavcodec, libavformat,
libavfilter, x264, x265, etc. that power FFmpeg. It replaces the FFmpeg command-line 
interface with a declarative JSON graph model, a browser-based visual editor, and 
rigorous pre-flight validation, giving you 100% of FFmpeg's codec and filter power
without the CLI pain.

It is **not** a wrapper around the `ffmpeg` binary. The entire orchestration
layer — graph construction, scheduling, error handling, hardware wiring,
metadata propagation — is rewritten from scratch in Go, with direct zero-copy
bindings to the same libav\* libraries the FFmpeg CLI itself uses.

---

## Why MediaMolder?

### Visual editor

FFmpeg runs media processing graphs, but until now you have had to visualize 
those graphs in your head. Now you can import your FFmpeg command-line into 
MediaMolder, view, edit, validate, and run your graph with clear performance 
metrics. The MediaMolder Graphical User Interface (GUI) is a fluid, 
drag-and-drop graph editor that runs in your web browser. The GUI is launched
from the same mediamolder binary executable as the CLI (`mediamolder gui`). 

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


### Safe by default

**MediaMolder validates your pipeline before the first frame is touched.**

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



### Hardware acceleration — any platform, properly

FFmpeg's hardware support works. MediaMolder makes it *safe and understandable*.

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

### Observable at every level

MediaMolder was designed for long-running and production jobs where "check
after it finishes" is not an option.

- **Per-node performance tracking** (`NodePerfTracker`) records each node's
  active, idle, and stalled fractions, windowed FPS vs. target, stall count
  and duration, per-frame processing latency, and — for decoder nodes —
  the libavcodec thread pool fill (`threads_busy`). The bottleneck node and
  its constraint are always visible.
- **Prometheus metrics** for every node and pipeline: 20+ gauges, counters,
  and histograms covering frames, errors, bitrate, frame latency, FPS,
  queue fill, CPU core estimates, and thread visibility.
- **`/perf` and `/perf/stream`** HTTP endpoints expose the per-node snapshot
  as JSON on demand or as a 2 Hz Server-Sent Events stream for dashboards.
- **`mediamolder perf`** renders a live colour-coded terminal table — green
  when nodes meet their FPS target, amber/red when they fall behind — with
  no extra tooling required.
- **OpenTelemetry** span wiring: every pipeline run and every handler goroutine
  emits a child span so your existing distributed trace shows exactly where
  decode/filter/encode time goes.

### Extensible in pure Go

Custom processing logic — object detection, AI filters, scene detection,
subtitle generation, business-specific metadata — slots into any graph as a
first-class node, written as an ordinary Go struct that implements the
`processors.Processor` interface. No C, no rebuilds, no filtergraph string
hacks. The engine schedules, monitors, and error-handles custom nodes
identically to built-in ones.

### Production-grade infrastructure

- **Declarative, version-controlled pipelines.** JSON files are diffable,
  database-storable, reliably generated programmatically, and fully schema-
  validated (v1.0/v1.1). The graph layout (node positions) round-trips through 
  the GUI without polluting the runtime config.
- **Full timing control.** `-ss`/`-t`/`-to` at input *and* output scope, a
  faithful Go port of FFmpeg's demuxer trim logic, `av_parse_time` string
  parsing, and per-encoder time-base control.
- **Pipeline state machine** with live pause/resume, graceful cancellation via
  `context.Context`, per-node error policies, and a structured event bus.
  Suitable for live streams and unattended overnight jobs alike.
- **Trivially embeddable.** The CLI and GUI are thin consumers of a clean Go
  API. Drop the engine into any service or CI/CD pipeline with a single import.

### Drop-in FFmpeg migration

`mediamolder convert-cmd` turns any FFmpeg command line into a validated JSON
config in one step: rate-control flags, per-stream maps, stream-copy nodes,
tee/HLS/DASH muxers, bitstream filters, hardware devices, cover-art and
attachment handling, `-map_metadata`/`-map_chapters`, two-pass encoding, and
more — all converted with high fidelity and covered by round-trip regression
tests. The generated graph runs immediately; the Inspector shows every option
the conversion inferred so you can review and adjust.

---

**In short:** MediaMolder gives you 100% of FFmpeg's modern media capabilities —
every codec, filter, hardware backend, and container format — with a pipeline
model that validates before it runs, shows you what's happening while it runs,
and tells you exactly what went wrong when it doesn't.
