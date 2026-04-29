# Observability

MediaMolder integrates OpenTelemetry for distributed tracing, Prometheus
for real-time metrics collection, and built-in runtime instrumentation for
backpressure monitoring and per-node latency tracking.

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
```

When Prometheus is enabled, the emitter updates all 8 collectors on each tick
using delta tracking for counters (Prometheus counters are monotonic). When
edge stats are provided, the `mediamolder_node_buffer_fill` gauge is populated
with the current fill ratio for each edge's downstream node.

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
