# Observability

MediaMolder integrates OpenTelemetry for distributed tracing and Prometheus
for real-time metrics collection.

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

```
pipeline.run (root span)
├── pipeline.node.source    (per source node)
├── pipeline.node.filter    (per filter node)
├── pipeline.node.encoder   (per encoder node)
└── pipeline.node.sink      (per sink node)
```

### Span Attributes

| Attribute | Description |
|-----------|-------------|
| `pipeline.id` | Pipeline identifier |
| `node.id` | Node identifier |
| `node.kind` | Node type (source, filter, encoder, sink) |
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
configurable interval (default: 5 seconds):

```go
emitter := pipeline.NewMetricsEmitter(
    5*time.Second,
    pipeline.Metrics(),
    pipeline.Events(),
    pipeline.State,
)
emitter.Start()
defer emitter.Stop()
```

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
