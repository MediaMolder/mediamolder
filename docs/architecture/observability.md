# Observability

MediaMolder integrates OpenTelemetry for distributed tracing, Prometheus
for real-time metrics collection, and built-in runtime instrumentation for
channel backpressure monitoring, per-node latency tracking, and full
per-node performance profiling via `NodePerfTracker`.

## Runtime Instrumentation

### Channel Backpressure Monitoring

Every inter-node channel is registered with an `EdgeStatsRegistry`. A sampler
goroutine periodically polls `len(ch)/cap(ch)` (default interval: 500ms) to
track fill ratios without adding overhead to the data path.

```go
pipe, _ := pipeline.NewPipeline(cfg)
pipe.SetState(pipeline.StatePlaying)

// After some processing:
for _, es := range pipe.EdgeStats().Snapshot() {
    fmt.Printf("%s → %s: fill=%.0f%% peak=%.0f%% stalls=%d\n",
        es.FromNode, es.ToNode, es.Fill*100, es.PeakFill*100, es.Stalls)
}
```

| Fill Ratio | Meaning |
|------------|---------|
| < 0.2 | Healthy — downstream is keeping up |
| 0.2–0.8 | Normal buffering |
| > 0.8 | **Backpressure** — downstream is struggling |
| 1.0 (stall) | **Bottleneck** — upstream is blocked |

The bottleneck node is `ToNode` on the edge with the highest fill ratio.

### Per-Node Processing Latency

Every handler (source, filter, encoder, sink, Go processor) records per-frame
latency using lock-free atomics. Latency is available in metrics snapshots:

```go
snap := pipe.Metrics().Snapshot()
for _, ns := range snap.Nodes {
    fmt.Printf("%s: avg=%s max=%s fps=%.1f\n",
        ns.NodeID, ns.AvgLatency, ns.MaxLatency, ns.FPS)
}
```

### Per-Node Performance Monitoring (NodePerfTracker)

`NodePerfTracker` (`pipeline/node_perf.go`) tracks each pipeline node through
three mutually exclusive states:

| State | Meaning |
|-------|---------|
| **Processing** | The node received a frame and is actively working on it |
| **Idle** | The node is waiting for its input channel to deliver the next frame |
| **Stalled** | The node finished processing a frame but is blocked trying to send it to a full output channel |

The tracker computes windowed fractions for each state, a rolling FPS, stall
counts and durations, per-frame processing latency (measured from channel
receive to channel send), and an EWMA queue fill fraction sampled at each
send.

#### Channel helpers: `perfReceive` and `perfSend`

Every handler wraps its hot-path channel operations with the two helpers:

```go
frame, ok := perfReceive(ctx, tracker, inputCh)  // accounts for idle time
if !ok {
    return
}
// ... process frame ...
perfSend(ctx, tracker, outputCh, frame)           // accounts for stall time
```

`perfReceive` calls `tracker.BeginIdle()` before blocking on the channel and
`tracker.EndIdle()` (which transitions to Processing) when a frame arrives.
`perfSend` calls `tracker.BeginStall()` before blocking on the output channel
and `tracker.EndStall()` after the send succeeds.  Both helpers record frame
latency and queue fill at the send site.  Nil trackers are a no-op so callers
need no guard.

#### Snapshot fields

| Field | Type | Description |
|-------|------|-------------|
| `FPS` | float64 | Windowed output rate (frames or packets per second) |
| `FPSTarget` | float64 | Expected output rate (0 = no target known) |
| `FPSDeficit` | float64 | `FPSTarget − FPS`; positive means falling behind |
| `ActiveFrac` | float64 | Fraction of elapsed time spent processing |
| `IdleFrac` | float64 | Fraction of elapsed time idle (waiting for input) |
| `StalledFrac` | float64 | Fraction of elapsed time stalled on a full output channel |
| `StallCount` | int64 | Cumulative stall events since pipeline start |
| `MaxStallDuration` | time.Duration | Longest single stall |
| `QueueFillFrac` | float64 | EWMA of output channel fill ratio \[0, 1\] at each send |
| `ThreadsConfigured` | int | libavcodec decode threads configured (0 for non-decoders) |
| `ThreadMode` | string | libavcodec thread mode (`frame` / `slice` / `auto`) |
| `ThreadsBusy` | int | Live threads in-flight; −1 for HW decoders without thread pools |
| `EstimatedCPUCores` | float64 | `ThreadsBusy × ActiveFrac` |
| `FrameLatencyMean` | time.Duration | Mean per-frame latency (receive→send) |

#### Thread visibility

`av.FrameDecoder` exposes three new methods so `NodePerfTracker` can forward
libavcodec's internal thread pool state:

```go
type FrameDecoder interface {
    // ...
    ThreadCount() int           // threads configured in AVCodecContext
    ActiveThreadType() int      // FF_THREAD_FRAME, FF_THREAD_SLICE, or 0
    ThreadsBusy() int           // live in-flight threads; -1 if not available
}
```

Hardware decoders (`HWDecoderContext`, `VTDecoderContext`) that do not use
libavcodec thread pools return `ThreadsBusy() = -1`.

#### Accessing snapshots

Snapshots are aggregated by `MetricsEmitter` and are available in two ways:

```go
// Poll on demand.
snap := pipe.GetMetrics()   // returns pipeline.MetricsSnapshot
for _, p := range snap.Perf {
    fmt.Printf("%s: fps=%.1f active=%.0f%% stalls=%d\n",
        p.NodeID, p.FPS, p.ActiveFrac*100, p.StallCount)
}

// Register a callback (used by MetricsServer and the perf CLI).
emitter.RegisterPerfHandler(func() snap.MetricsSnapshot {
    return pipe.GetMetrics()
})
```

The shared types (`NodePerfSnapshot`, `MetricsSnapshot`, `NodeMetricsSnapshot`)
live in the `pipeline/snap` package so both `pipeline` and `observability` can
import them without a circular dependency.  The `pipeline` package re-exports
them as type aliases for backward compatibility.

## OpenTelemetry Tracing

### Configuration

```go
provider, err := observability.Init(ctx, observability.Config{
    ServiceName:  "mediamolder",
    OTLPEndpoint: "localhost:4318", // OTLP HTTP endpoint
})
defer provider.Shutdown(ctx)
```

If `OTLPEndpoint` is empty, a noop provider is used (no traces exported).

### Span Structure

The pipeline creates spans at two levels when an `observability.Provider` is
configured:

1. **Pipeline span** — wraps the entire `runGraph()` execution. Created via
   `obs.StartPipelineSpan(ctx, description)`. Ended with `EndSpanOK` or
   `EndSpanError` based on the pipeline's exit status.

2. **Node spans** — each handler goroutine is wrapped with
   `observability.StartNodeSpan(ctx, nodeID, kind, codec, mediaType)`. Node
   spans are children of the pipeline span because the context carries the
   parent.

```
pipeline.run                        [============================================]
├── pipeline.node.source (src)      [============================================]
├── pipeline.node.filter (scale)      [========================================]
├── pipeline.node.encoder (enc)         [====================================]
└── pipeline.node.sink (out)              [================================]
```

Tracing is opt-in. Call `pipeline.SetObsProvider(provider)` before starting.
If no provider is set, the noop tracer is used (zero overhead).

### Span Attributes

| Attribute | Description |
|-----------|-------------|
| `pipeline.id` | Pipeline identifier |
| `node.id` | Node identifier |
| `node.kind` | Node type (source, filter, encoder, sink, copy, go_processor) |
| `node.codec` | Codec name (e.g., h264, aac) |
| `node.media_type` | Media type (video, audio) |

### Structured Logging

The `Logger(ctx)` function returns a `slog.Logger` with trace correlation:

```go
logger := observability.Logger(ctx)
logger.Info("processing frame", "pts", frame.PTS())
// Output includes trace_id and span_id fields
```

## Prometheus Metrics

### Metric Names and Labels

All metrics use the `pipeline` constant label for identification.

#### Pipeline-level metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `mediamolder_pipeline_fps` | Gauge | node, media_type | Current processing frame rate |
| `mediamolder_pipeline_bitrate_bps` | Gauge | node, media_type | Current bitrate in bits/sec |
| `mediamolder_node_latency_seconds` | Histogram | node, media_type | Per-frame processing latency |
| `mediamolder_node_buffer_fill` | Gauge | node | Buffer fill level (0.0–1.0) |
| `mediamolder_pipeline_errors_total` | Counter | node, media_type | Total errors per node |
| `mediamolder_pipeline_frames_total` | Counter | node, media_type | Total frames processed |
| `mediamolder_pipeline_bytes_total` | Counter | node, media_type | Total bytes processed |
| `mediamolder_pipeline_state` | Gauge | — | Current state (0=NULL, 1=READY, 2=PAUSED, 3=PLAYING) |
| `mediamolder_pipeline_frames_in_flight` | Gauge | — | Frames currently in-flight across all nodes |
| `mediamolder_pipeline_realtime_satisfied` | Gauge | — | 1 when all nodes are meeting their FPS targets |

#### Per-node performance metrics

These metrics all carry a `node_id` label.

| Metric | Type | Description |
|--------|------|-------------|
| `mediamolder_node_active_fraction`        | Gauge     | Fraction of elapsed time spent actively processing |
| `mediamolder_node_idle_fraction`          | Gauge     | Fraction of elapsed time idle (waiting for input) |
| `mediamolder_node_stalled_fraction`       | Gauge     | Fraction of elapsed time stalled on a full output channel |
| `mediamolder_node_stall_duration_seconds` | Histogram | Distribution of individual stall event durations |
| `mediamolder_node_stall_count_total`      | Counter   | Cumulative stall events per node |
| `mediamolder_node_fps`                    | Gauge     | Windowed output rate (fps or pkt/s) |
| `mediamolder_node_fps_target`             | Gauge     | Expected output rate (0 if no target is known) |
| `mediamolder_node_fps_deficit`            | Gauge     | `fps_target − fps`; positive means falling behind |
| `mediamolder_node_queue_fill`             | Gauge     | EWMA of output channel fill ratio \[0, 1\] at each send |
| `mediamolder_node_threads_configured`     | Gauge     | libavcodec decode threads configured (label `mode`: `frame`/`slice`/`auto`) |
| `mediamolder_node_threads_busy`           | Gauge     | Live threads currently in-flight (−1 for HW decoders) |
| `mediamolder_node_cpu_cores_estimated`    | Gauge     | Estimated CPU cores consumed (`threads_busy × active_fraction`) |
| `mediamolder_node_thread_restarts_total`  | Counter   | Cumulative libavcodec thread restart events |
| `mediamolder_node_frame_latency_seconds`  | Histogram | Per-frame processing latency (receive→send) |

### HTTP Endpoints

```go
metrics := observability.NewMetrics("my-pipeline")
server := observability.NewMetricsServer(":9090", metrics.Registry())
addr, err := server.Start()
defer server.Shutdown(ctx)
```

| Endpoint | Description |
|----------|-------------|
| `/metrics` | Prometheus scrape endpoint |
| `/health` | Health check (returns 200 OK) |
| `/perf` | JSON snapshot of `[]NodePerfSnapshot` on demand |
| `/perf/stream` | Server-Sent Events stream of `[]NodePerfSnapshot` at 2 Hz |

The `/perf` and `/perf/stream` endpoints become active once a snapshot
callback is registered on the emitter:

```go
// Wired by the pipeline engine after building the graph.
emitter.RegisterPerfHandler(func() snap.MetricsSnapshot {
    return engine.GetMetrics()
})
emitter.RegisterPerfStreamHandler(func() snap.MetricsSnapshot {
    return engine.GetMetrics()
})
```

The SSE event name on `/perf/stream` is `perf`; each event carries the full
`[]NodePerfSnapshot` JSON array.  Clients that miss events can reconnect; the
server resends the latest snapshot immediately on reconnect.

### Periodic Metrics Snapshots

The `MetricsEmitter` posts `MetricsSnapshotEvent` to the event bus at a
configurable interval (default: 5 seconds). It also bridges internal metrics
to Prometheus collectors and backpressure stats when configured:

```go
emitter := pipeline.NewMetricsEmitter(
    5*time.Second,
    pipeline.Metrics(),
    pipeline.Events(),
    pipeline.State,
    pipeline.WithPrometheus(promMetrics),   // optional: populate Prometheus collectors
    pipeline.WithEdgeStats(edgeStatsReg),   // optional: bridge backpressure to Prometheus
)
emitter.Start()
defer emitter.Stop()

// Register callbacks so the MetricsServer can serve /perf and /perf/stream.
emitter.RegisterPerfHandler(func() snap.MetricsSnapshot {
    return engine.GetMetrics()
})
emitter.RegisterPerfStreamHandler(func() snap.MetricsSnapshot {
    return engine.GetMetrics()
})
```

When Prometheus is enabled, the emitter updates all collectors on each tick
using delta tracking for counters (Prometheus counters are monotonic). When
edge stats are provided, the `mediamolder_node_buffer_fill` gauge is populated
with the current fill ratio for each edge's downstream node.  Per-node
performance metrics are populated from `NodePerfSnapshot` fields whenever a
snapshot callback is registered.

## `mediamolder perf` CLI

A terminal-based live performance monitor polls the pipeline's `/perf`
endpoint and renders a colour-coded table updated at a configurable interval:

```
mediamolder perf [--url http://host:port/perf] [--interval duration]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--url` | `http://localhost:9090/perf` | URL of the running pipeline's `/perf` endpoint |
| `--interval` | `1s` | How often to refresh the display |

Table columns: **NODE**, **FPS**, **TARGET**, **DEFICIT**, **ACTIVE%**,
**IDLE%**, **STALL%**, **THREADS**, **BUSY**, **LATENCY**.  Rows are
colour-coded: green when meeting the target, amber when deficit ≤ 1 fps,
red when deficit > 1 fps.  Press Ctrl-C to exit.

## `mediamolder watch` CLI (real-time controller)

When a pipeline is running with `global_options.realtime` enabled, the
controller's full per-tick state is available via two HTTP endpoints:

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/realtime/snapshot` | One-shot `RTControllerSnapshot` JSON; `404` when realtime is off |
| `GET` | `/realtime/snapshot/stream` | SSE stream — one event per ~500 ms controller tick |

The `mediamolder watch` subcommand connects to the SSE stream and renders a
live ANSI table in-place:

```
mediamolder watch [--url http://host:port]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--url` | `http://127.0.0.1:9090` | Base URL of the running pipeline's metrics server |

The display has three sections:

- **Header line** — tick count, elapsed time, `fps/target`, status badge
  (grey *disabled* / blue *observing* / amber *cooldown(N)* / green
  *satisfied* / red *dropping*).
- **PERFORMANCE / APPLIED table** — one row per controlled video encoder
  with columns: `fps`, `deficit`, `active%`, `stalled%`, `in buf`
  (four-block encoder frame-input queue bar), `out buf` (encoder
  packet-output queue bar), `preset`, `cd` (cooldown remaining).
- **SINKS** — one row per muxer/sink node with an `out buf` fill bar.
- **RECENT DECISIONS** — last 5 entries from the controller decision log.

No external dependencies; output is plain ANSI escape codes for broad
terminal compatibility.  Press Ctrl-C to exit.

## Sample Grafana Dashboard

```json
{
  "panels": [
    {
      "title": "Pipeline FPS",
      "type": "timeseries",
      "targets": [
        { "expr": "mediamolder_pipeline_fps{pipeline=\"$pipeline\"}" }
      ]
    },
    {
      "title": "Frame Latency (p99)",
      "type": "timeseries",
      "targets": [
        { "expr": "histogram_quantile(0.99, rate(mediamolder_node_latency_seconds_bucket{pipeline=\"$pipeline\"}[1m]))" }
      ]
    },
    {
      "title": "Error Rate",
      "type": "timeseries",
      "targets": [
        { "expr": "rate(mediamolder_pipeline_errors_total{pipeline=\"$pipeline\"}[1m])" }
      ]
    },
    {
      "title": "Buffer Fill",
      "type": "gauge",
      "targets": [
        { "expr": "mediamolder_node_buffer_fill{pipeline=\"$pipeline\"}" }
      ]
    },
    {
      "title": "Pipeline State",
      "type": "stat",
      "targets": [
        { "expr": "mediamolder_pipeline_state{pipeline=\"$pipeline\"}" }
      ]
    }
  ]
}
```
