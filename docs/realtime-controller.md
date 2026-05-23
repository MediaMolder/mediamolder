# Real-Time Controller

Real-time mode is MediaMolder's system for keeping a media processing pipeline
at or above a target frame rate throughout an entire job — whether that job runs
for seconds or days. It is activated with `--realtime` (CLI) or
`global_options.realtime: true` (JSON) and adds three layers of adaptive
control on top of the normal pipeline execution model.

---

## Contents

1. [Why real-time mode exists](#1-why-real-time-mode-exists)
2. [Quick start](#2-quick-start)
3. [Control loop overview](#3-control-loop-overview)
4. [Tier 1 — encoder thread scaling](#4-tier-1--encoder-thread-scaling)
5. [Tier 2 — adaptive preset stepping](#5-tier-2--adaptive-preset-stepping)
6. [Tier 3 — frame-drop (last resort)](#6-tier-3--frame-drop-last-resort)
7. [Output buffer (jitter absorption at sinks)](#7-output-buffer-jitter-absorption-at-sinks)
8. [Configuration reference](#8-configuration-reference)
9. [GUI: Real-Time Controller inspector](#9-gui-real-time-controller-inspector)
10. [`mediamolder watch` CLI](#10-mediamolder-watch-cli)
11. [HTTP API](#11-http-api)
12. [Prometheus metrics](#12-prometheus-metrics)
13. [Tuning guidance](#13-tuning-guidance)
14. [See also](#14-see-also)

---

## 1. Why real-time mode exists

A pipeline running without real-time mode encodes as fast as the CPU allows.
For file-to-file jobs that is fine. For anything where output must pace to a
wall clock — live restreaming, HLS/DASH ingest, broadcast playout — two failure
modes arise:

**Encoder falling behind.** If any encoder node consistently produces fewer
frames per second than the source frame rate, the downstream muxer receives
packets late and the live timeline drifts. A slow preset is the most common
cause; codec thread starvation is the second.

**Downstream jitter.** TCP receive-window stalls, HLS segment-publish hiccups,
SRT recovery bursts, or the brief codec close+reopen window during a preset
switch can all create multi-second gaps during which the muxer cannot write.
Without a buffer, the writer blocks, upstream filters stall, and the encoder
eventually starves.

Real-time mode addresses both with:

| Layer | What it does | When it acts |
|---|---|---|
| **Adaptive control loop** | Observes per-node FPS, adjusts threads and presets, drops frames as last resort | Every 500 ms while playing |
| **Encoder input buffer** | Absorbs short stalls at the encoder's input | Always; default 96 frames ≈ 4 s @ 24 fps |
| **Output buffer (rolling)** | Absorbs downstream jitter at the muxer; paces delivery to PTS wall-clock rate | During STREAMING phase |

---

## 2. Quick start

```sh
# CLI — override any JSON without editing the file
mediamolder run --realtime pipeline.json

# Or set it in the JSON
# global_options.realtime: true

# Watch the controller live
mediamolder watch
```

In the GUI, check the **Real-time** checkbox in the toolbar before pressing
**Run**. The **Inspector → Real-Time Controller** tab opens automatically when
the pipeline starts.

---

## 3. Control loop overview

The adaptive control loop runs as a goroutine in `pipeline/realtime_ctrl.go`,
activated when `PipelineOpts.Realtime = true`. It ticks every ~500 ms.

```
┌── OBSERVE ──────────────────────────────────────┐
│  Read NodePerfSnapshot for every encoder node.   │
│  Key signals: FPSDeficit, ActiveFrac,            │
│  StalledFrac, ThreadsConfigured, ThreadsBusy.    │
└─────────────────────────┬───────────────────────┘
                          │
┌── DECIDE ───────────────▼───────────────────────┐
│  For each node where FPSDeficit > 0:             │
│                                                  │
│  if StalledFrac > 50%:                           │
│    → downstream is the bottleneck; skip          │
│  elif ActiveFrac > 90% AND threads fully used:  │
│    → Tier 1: increase thread count by 2          │
│  elif ActiveFrac > 90% AND threads not helping: │
│    → Tier 2: step preset one notch faster        │
│  elif thread budget & preset ceiling both hit:   │
│    → Tier 3: enable frame-drop on source         │
│                                                  │
│  if node has headroom (deficit < -0.5) for ≥3s: │
│    → step preset one notch slower (quality back) │
└─────────────────────────┬───────────────────────┘
                          │
┌── ACTUATE ──────────────▼───────────────────────┐
│  Apply thread count change (graceful restart).   │
│  Request GOP-boundary preset switch.             │
│  Toggle frame-drop rate on source node.          │
│  Emit RealTimeViolation event if unresolvable.   │
└─────────────────────────────────────────────────┘
```

The loop records every decision in a `RecentDecisions` ring, visible in
the GUI inspector and streamed by the `/realtime/snapshot/stream` SSE
endpoint. Each decision entry carries the node ID, the old and new values,
the reason, and the tick timestamp.

### Status badges

| Badge | Meaning |
|---|---|
| `disabled` | `global_options.realtime` is `false` |
| `observing` | Loop is running; all nodes within target |
| `cooldown(N)` | A change was made; waiting N ticks before the next one |
| `satisfied` | All nodes meeting their `fps_target` |
| `dropping` | Frame-drop mode active on ≥ 1 source |

---

## 4. Tier 1 — encoder thread scaling

When a node's `ActiveFrac > 0.9` and its `ThreadsBusy ≈ ThreadsConfigured`,
the bottleneck is thread-limited: the codec is fully occupying all its threads
but still cannot keep up. The fix is to give it more threads.

**Thread budget.** A global `ThreadBudget` in `pipeline/thread_budget.go`
tracks how many CPU threads are allocated across all encoder nodes.
`Total` defaults to `runtime.NumCPU() − 2` (two threads reserved for the Go
runtime and audio nodes). Hardware-accelerated nodes (NVENC, VideoToolbox,
AMF) are exempt and do not consume the budget.

**Graceful restart.** Because `AVCodecContext.thread_count` cannot be changed
on an open codec, a thread-count change triggers a *graceful restart*:

1. Drain the encoder's input channel until it is empty.
2. Call `Close()` on the existing `EncoderContext`.
3. Reopen with the new thread count via `avcodec_open2`.
4. Resume sending frames.

The restart pauses the encoder for ≈1–3 encode-frame durations. The
upstream encoder input buffer (96 frames by default) absorbs this gap so
no frames are dropped during the restart.

---

## 5. Tier 2 — adaptive preset stepping

When threads are already at the budget ceiling and the codec is still the
bottleneck (entropy coding, rate-distortion search, lookahead), adding
more threads will not help. The right intervention is to lower the encode
complexity by stepping one preset faster.

### Preset ladders

| Codec | Ladder (slowest → fastest) |
|---|---|
| `libx264` | `placebo, veryslow, slower, slow, medium, fast, faster, veryfast, superfast, ultrafast` |
| `libx265` | `placebo, veryslow, slower, slow, medium, fast, faster, veryfast, superfast, ultrafast` |
| `libsvtav1` | `0 (slowest) … 13 (fastest)` |

The ladder is normalised internally to a `presetIndex` so the control loop is
codec-agnostic. `highest_quality_preset` caps the slowest position the
controller is allowed to use; it defaults to the preset configured in the job.

### Quality back-stepping

If a node has been in sustained headroom (`FPSDeficit < −0.5`) for ≥ 3
controller ticks, the loop steps it one notch slower (toward higher quality).
This reclaims encode quality after transient load spikes clear.

### GOP-boundary switching

Preset changes cannot be applied mid-GOP without breaking stream continuity.
For `libx264` and `libx265`, the change is applied at the next IDR boundary
via a close+reopen of the codec (same path as Tier 1 restart). For
`libsvtav1`, `svt_av1_enc_set_parameter` supports hot preset changes that
take effect on the next IDR — no encoder restart required.

---

## 6. Tier 3 — frame-drop (last resort)

When both the thread budget and the preset ceiling are exhausted and
`FPSDeficit > 1 fps`, the control loop enables **frame-drop mode** on the
upstream source: every 4th frame is silently discarded before it enters the
encoder input channel. This reduces load at the cost of output frame rate.

Frame-drop is always surfaced:

- `RealTimeViolation` event emitted on the event bus.
- `mediamolder_pipeline_realtime_satisfied` Prometheus gauge drops to `0`.
- Status badge changes to `dropping` in the GUI inspector and `mediamolder watch`.
- The decision is logged to `realtime_log_path` if configured.

---

## 7. Output buffer (jitter absorption at sinks)

Each muxer/sink node is fronted by an `OutputBuffer`
(`pipeline/output_buffer.go`) when real-time mode is enabled. The buffer
serves two purposes depending on its current phase:

**Fill phase (startup)** — the buffer accumulates encoded packets until the
configured `prebuffer_duration_seconds` of PTS-span is held. All outputs
must reach this threshold before any muxer is allowed to write its first
byte. This ensures every output starts cleanly from the same reference point
and absorbs the initial encoder warm-up transient.

**Rolling phase (streaming)** — once the pipeline is fully running, the
buffer continues as a rolling jitter absorber. N encoder goroutines push
packets in via `Enqueue`; a single consumer calls `TakePaced`, which paces
delivery to the stream's PTS wall-clock rate. `TakePaced` sleeps until each
packet's target wall time (`wallOrigin + (pts − ptsOrigin)`), then passes it
to the muxer. This means the output file or stream always advances at exactly
the source PTS rate, regardless of brief encoder speed variations.

The `AheadNs` metric (encoder lead over real-time) is the primary health
signal for the rolling phase.

### State machine

```
FILLING ──(buffered ≥ target)──► READY ──┐
   │                                      │
   └──(EOS before target)──► READY_PARTIAL┘
                                          │
                              (all outputs ≥ READY)
                                          │
                                          ▼
                                      STREAMING ──(graph shutdown)──► DRAINING
```

| State | Meaning |
|---|---|
| `FILLING` | Accumulating packets. PTS-span accounting; oldest evicted when `prebuffer_max_seconds` exceeded. |
| `READY` | Fill target reached. Per-output ready channel closed; `Pipeline.Ready()` AND-combines all. |
| `READY_PARTIAL` | Upstream EOS'd before target was met (short clips). Treated as ready for aggregation. |
| `STREAMING` | Drainer has emptied the initial fill; rolling jitter-buffer mode active. |
| `DRAINING` | Graph shutting down; remaining packets written or discarded. |

### Combined jitter budget

The encoder input buffer (96 frames ≈ 4 s @ 24 fps) and the output buffer
target (default 4 s) give roughly **8 s** of total downstream jitter
absorption before backpressure propagates upstream.

---

## 8. Configuration reference

### `global_options` fields

| Field | Type | Default | Description |
|---|---|---|---|
| `realtime` | bool | `false` | Enable the adaptive control loop and output buffers. Also settable via `--realtime` CLI flag or the **Real-time** GUI checkbox. |
| `prebuffer_duration_seconds` | float | `4.0` (video) / `1.0` (audio-only) | Target fill duration for each output buffer in PTS-time seconds. `0` disables the buffer for that output. |
| `prebuffer_max_seconds` | float | `2 × prebuffer_duration_seconds` | Hard cap on how much PTS-time may be held. Oldest packets are evicted if this is exceeded. |
| `highest_quality_preset` | string | *(job preset)* | Slowest preset the controller is allowed to use. E.g. `"medium"` allows stepping down to `ultrafast` but no slower than `medium`. |
| `target_fps` | float | `0` (derive from source) | Graph-level FPS target. `0` = auto-derive from the source stream's frame rate. |
| `encoder_input_buffer_frames` | int | `0` (pipeline default) | Per-encoder input channel capacity in frames. `96` (~4 s @ 24 fps) is recommended when using preset stepping to absorb the close+reopen window. |
| `realtime_log_path` | string | *(disabled)* | Path to a per-tick JSONL debug log. Each line is a JSON object with the full `RTControllerSnapshot`, cool-down counters, and any decisions. The file is truncated at pipeline start. Use `jq` to query after the run. |

### Per-output override

An individual output can override the buffer size without changing the global
defaults:

```jsonc
{
  "global_options": {
    "realtime": true,
    "prebuffer_duration_seconds": 4.0
  },
  "outputs": [
    {
      "id": "live_hls",
      "realtime": {
        "prebuffer_duration_seconds": 6.0,   // this output needs a larger buffer
        "prebuffer_max_seconds": 12.0
      }
    }
  ]
}
```

Set `prebuffer_duration_seconds: 0` on a specific output to disable its
buffer entirely while keeping it enabled for others.

### CLI flags

```sh
mediamolder run --realtime \
    --prebuffer=4s \
    --prebuffer-max=8s \
    --ready-fd=3 \
    job.json
```

`--ready-fd=<n>` writes a single `0x01` byte to file descriptor `n` once the
graph is ready (equivalent of `systemd-notify READY=1`). The literal string
`ready\n` is also printed to stdout on the same trigger.

Schema definitions for all `global_options` and per-output `realtime` fields
live in [schema/v1.0.json](../schema/v1.0.json) and
[schema/v1.1.json](../schema/v1.1.json), enforced by
`TestSchemaSyncWithGoStructs`.

---

## 9. GUI: Real-Time Controller inspector

When a pipeline is running in real-time mode the Inspector panel shows a
**Real-Time Controller** tab with three sub-tabs.

### Observed tab

The live view of what the controller sees and does. Updates at ~2 Hz from
the `/realtime/snapshot/stream` SSE endpoint.

**Header row:** `fps / target fps · tick N · elapsed T` with a colour-coded
status badge (see [§3 status badges](#3-control-loop-overview)).

**Performance table** — one row per controlled video encoder:

| Column | Description |
|---|---|
| NODE | Node ID |
| FPS / TARGET | Current windowed FPS and the configured target |
| DEFICIT | `target − fps`; red when positive (falling behind) |
| ACTIVE% | Fraction of time the node is actively encoding |
| STALLED% | Fraction of time blocked on a full output channel |
| IN BUF | Four-segment fill bar for the encoder's input channel |
| OUT BUF | Four-segment fill bar for the encoder's packet output channel |
| PRESET | Current encoder preset |
| CD | Controller cooldown ticks remaining before the next decision |

**Output buffers section** — one row per muxer/sink node:

| Column | Description |
|---|---|
| SINK | Node ID |
| AHEAD | During STREAMING: how far the buffer's leading PTS edge is ahead of the real-time playback position, in seconds and frames. Colour-coded: green (> 0.5 s ahead), yellow (0–0.5 s), red (behind). During fill phase: current buffered PTS span. |
| TARGET | Configured `prebuffer_duration_seconds` |
| FILL | Colour-coded fill bar (green > 60%, yellow 30–60%, red < 30%) |

**Recent decisions** — last 5 entries from the controller's decision log,
showing node ID, action taken, old and new values, and reason.

### Applied tab

Shows the current per-encoder preset and thread-count overrides that have
been applied since the pipeline started, with a manual override control for
each node (useful for testing).

### Settings tab

Exposes the live `global_options.realtime.*` configuration fields with
in-flight update controls.

---

## 10. `mediamolder watch` CLI

Connects to a running pipeline's SSE stream and renders a live ANSI
table in-place.

```sh
mediamolder watch [--url http://host:port]
```

| Flag | Default | Description |
|---|---|---|
| `--url` | `http://127.0.0.1:9090` | Base URL of the running pipeline's metrics server |

The display mirrors the **Observed** tab of the GUI inspector. Press Ctrl-C
to exit.

---

## 11. HTTP API

All endpoints are on the metrics server (default `:9090`). They return
`404` when real-time mode is not active.

| Method | Path | Description |
|---|---|---|
| `GET` | `/realtime/snapshot` | One-shot `RTControllerSnapshot` JSON |
| `GET` | `/realtime/snapshot/stream` | SSE; one `RTControllerSnapshot` event per controller tick (~500 ms) |
| `GET` | `/realtime/ready` | `{"ready": true/false}`; HTTP 425 *Too Early* until all outputs ≥ READY |
| `GET` | `/realtime/ready/stream` | SSE; one event per output-buffer state change |

### `RTControllerSnapshot` fields

| Field | Type | Description |
|---|---|---|
| `Enabled` | bool | `false` when real-time mode is off |
| `Status` | string | Controller status badge (`observing`, `satisfied`, `cooldown`, `dropping`) |
| `FPS` | float64 | Pipeline-level aggregate FPS |
| `FPSTarget` | float64 | Configured target |
| `Tick` | int | Monotonic tick counter since pipeline start |
| `ElapsedNs` | int64 | Nanoseconds since pipeline start |
| `Nodes` | `[]ControllerNodeSnapshot` | Per-encoder node snapshots (see observability doc) |
| `Sinks` | `[]SinkNodeSnapshot` | Per-sink output-buffer snapshots |
| `RecentDecisions` | `[]Decision` | Last 5 controller decisions |

### `SinkNodeSnapshot` fields

| Field | Type | Description |
|---|---|---|
| `NodeID` | string | Sink node identifier |
| `OutputBufferFillFrac` | float64 | Buffer fill fraction \[0, 1\] during fill phase |
| `BufferedNs` | int64 | Currently buffered PTS span in nanoseconds |
| `TargetNs` | int64 | `prebuffer_duration_seconds` in nanoseconds |
| `AheadNs` | int64 | Encoder PTS lead over real-time wall clock (ns); positive = ahead; 0 before STREAMING |

---

## 12. Prometheus metrics

### Output buffer metrics

| Metric | Type | Labels | Description |
|---|---|---|---|
| `mediamolder_output_buffer_duration_seconds` | Gauge | `node` | Currently buffered PTS span |
| `mediamolder_output_buffer_target_seconds` | Gauge | `node` | Configured target duration |
| `mediamolder_output_buffer_state` | Gauge | `node`, `state` | 1 when the buffer is in this state, 0 otherwise |
| `mediamolder_output_buffer_evictions_total` | Counter | `node` | Packets evicted due to `prebuffer_max_seconds` overflow |
| `mediamolder_pipeline_ready` | Gauge | — | 1 once all outputs have reached ≥ READY |

### Controller metrics

| Metric | Type | Labels | Description |
|---|---|---|---|
| `mediamolder_node_fps_target` | Gauge | `node` | Configured FPS target for this node |
| `mediamolder_node_fps_deficit` | Gauge | `node` | `fps_target − fps`; positive = falling behind |
| `mediamolder_pipeline_realtime_satisfied` | Gauge | — | 1 when all nodes are meeting their FPS targets |
| `mediamolder_node_thread_restarts_total` | Counter | `node` | Cumulative Tier-1 graceful restarts |

### Readiness surfaces

| Surface | Description |
|---|---|
| `Pipeline.Ready() <-chan struct{}` | Channel closed once all outputs ≥ READY |
| `Pipeline.ReadyState()` | Per-output state + first-ready timestamp |
| `RealTimeReady` event on the event bus | Fires once when the graph becomes ready |

---

## 13. Tuning guidance

**Live restream (HLS/RTMP/SRT).** The defaults (4 s target / 8 s cap) are
appropriate for most segment durations. Raise `prebuffer_duration_seconds`
toward the HLS segment length if you observe segment-publish gaps.

**ABR ladder (multiple simultaneous encoders).** Set
`encoder_input_buffer_frames: 96` to absorb the close+reopen window when
the controller restarts one encoder while others continue. Set
`highest_quality_preset` to your quality floor — the controller will never
step slower than this, but may step faster under load.

**Audio-only.** The default buffer drops to 1 s automatically. Raise it only
when pairing with a downstream that batches large windows (e.g. HLS with a
10 s segment duration).

**VOD-style file outputs.** Real-time mode is generally unnecessary for
file-to-file jobs. Either omit `--realtime` or set
`prebuffer_duration_seconds: 0` on the specific output to disable that
output's buffer while keeping the control loop active for others.

**Memory accounting.** Buffer caps are in *PTS-time*, not bytes. A
16 Mbps stream at an 8 s cap holds ≈ 16 MB. Reduce `prebuffer_max_seconds`
if bitrate is very high and memory is constrained.

**Eviction warning.** A non-zero `mediamolder_output_buffer_evictions_total`
means the encoder is sustained-faster than the muxer can consume — diagnose
the downstream bottleneck rather than raising the cap further.

**`realtime_log_path` for diagnosis.** Write the per-tick JSONL log and
inspect it after the run:

```sh
# What decisions did the controller make, and when?
jq 'select(.decisions | length > 0) | {tick, decisions}' rt_debug.jsonl

# How did enc_1080's FPS and preset evolve over time?
jq 'select(.nodes) | .nodes[] | select(.NodeID == "enc_1080") | {tick: .tick, fps: .FPS, preset: .CurrentPreset}' rt_debug.jsonl
```

---

## 14. See also

- [docs/architecture/node_perf_monitoring_design.md](architecture/node_perf_monitoring_design.md)
  — full design document for per-node performance monitoring and the adaptive
  control loop, including the `RTControllerSnapshot` type definition, thread
  budget manager, preset stepping mechanics, and implementation plan
- [docs/architecture/observability.md](architecture/observability.md)
  — Prometheus metrics reference, OpenTelemetry tracing, `mediamolder perf`
  CLI, and the full `mediamolder watch` display reference
- [docs/using_mediamolder.md §5.12](using_mediamolder.md#512-real-time-mode)
  — quick reference for real-time mode in the main usage guide
- [pipeline/realtime_ctrl.go](../pipeline/realtime_ctrl.go) — control loop implementation
- [pipeline/output_buffer.go](../pipeline/output_buffer.go) — output buffer implementation
- [pipeline/output_buffer_test.go](../pipeline/output_buffer_test.go) — regression tests
- [pipeline/preset_ladder.go](../pipeline/preset_ladder.go) — codec preset ladder definitions
- [pipeline/thread_budget.go](../pipeline/thread_budget.go) — thread budget manager
