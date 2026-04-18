# Pipeline Instrumentation & Performance Monitoring — Design & Roadmap

This document is a detailed design and implementation plan for five
improvements to MediaMolder's graph processing. Each section explains the
problem, the design, exact code changes required, dependencies on other
sections, and testing strategy.

The work is ordered by impact and dependency: later sections build on earlier
ones.

---

## Table of Contents

1. [Channel Backpressure Monitoring](#1-channel-backpressure-monitoring)
2. [Per-Node Processing Latency](#2-per-node-processing-latency)
3. [Prometheus Metrics Wiring](#3-prometheus-metrics-wiring)
4. [OpenTelemetry Span Wiring](#4-opentelemetry-span-wiring)
5. [Adaptive Buffer Sizing](#5-adaptive-buffer-sizing)

---

## 1. Channel Backpressure Monitoring — ✅ Implemented

### Problem

The scheduler creates one `chan any` (buffer size 8) per graph edge. When a
downstream node can't keep up, the channel fills up and the upstream node
blocks on send. This is the primary source of pipeline bottlenecks, but
there is currently **no visibility** into channel fill levels.

The Prometheus gauge `mediamolder_node_buffer_fill` and the event type
`BufferOverflow` already exist but are never populated.

### Design

#### Approach: Periodic Sampling (not channel wrapping)

Wrapping every channel send/receive with instrumentation adds overhead on the
hot path. Instead, a **sampler goroutine** polls `len(ch)` / `cap(ch)` at a
configurable interval (default: 500ms). This is lock-free (Go channels
support concurrent `len()`) and adds zero overhead to the data path.

#### New Type: `EdgeStats`

```go
// runtime/edge_stats.go

// EdgeStats tracks per-edge backpressure metrics.
type EdgeStats struct {
    EdgeID   string  // "from_node→to_node:port_type"
    FromNode string
    ToNode   string
    PortType string
    BufSize  int     // channel capacity
    Fill     float64 // current fill ratio (0.0–1.0), updated by sampler
    PeakFill float64 // highest fill ratio observed
    Stalls   int64   // number of samples where fill == 1.0 (channel was full)
}

// EdgeStatsRegistry holds stats for all edges in a running graph.
type EdgeStatsRegistry struct {
    mu    sync.RWMutex
    edges map[string]*EdgeStats
    chans map[string]chan any // raw channels for len() sampling
}
```

#### Sampler Goroutine

```go
// runtime/edge_stats.go

// StartSampler launches a goroutine that polls channel fill levels.
// It stops when ctx is cancelled.
func (r *EdgeStatsRegistry) StartSampler(ctx context.Context, interval time.Duration)
```

The sampler iterates over all registered channels, computes
`float64(len(ch)) / float64(cap(ch))`, and updates `Fill`, `PeakFill`,
and `Stalls` atomically.

#### Integration into Scheduler

```go
// runtime/scheduler.go — changes to Run()

func (s *Scheduler) Run(ctx context.Context, g *graph.Graph, handler NodeHandler) error {
    // ... existing channel creation ...

    // NEW: register channels for backpressure monitoring.
    if s.EdgeStats != nil {
        for e, ch := range edgeCh {
            s.EdgeStats.Register(e, ch)
        }
        s.EdgeStats.StartSampler(ctx, s.SampleInterval)
    }

    // ... rest of existing code unchanged ...
}
```

The `Scheduler` struct gets two new optional fields:

```go
type Scheduler struct {
    BufSize        int
    EdgeStats      *EdgeStatsRegistry // nil = no monitoring (zero overhead)
    SampleInterval time.Duration      // default 500ms
}
```

#### Files to Create/Modify

| File | Action | Description |
|------|--------|-------------|
| `runtime/edge_stats.go` | **Create** | `EdgeStats`, `EdgeStatsRegistry`, sampler goroutine |
| `runtime/edge_stats_test.go` | **Create** | Tests for sampling, stall detection, peak tracking |
| `runtime/scheduler.go` | **Modify** | Add `EdgeStats` field, register channels in `Run()` |
| `pipeline/engine.go` | **Modify** | Create `EdgeStatsRegistry`, pass to `Scheduler`, expose via `Pipeline` |

#### Testing Strategy

- Unit test: Create a channel, fill it partially, verify `Fill` ratio after one sample tick.
- Unit test: Fill channel completely, verify `Stalls` increments.
- Unit test: Verify `PeakFill` tracks the maximum.
- Integration test: Build a graph with a deliberately slow node (sleep in handler), verify its inbound edge shows high fill.

#### Bottleneck Interpretation

Users can identify bottlenecks by looking at edge stats:

| Fill Ratio | Meaning |
|------------|---------|
| < 0.2 | Healthy — downstream is keeping up easily |
| 0.2–0.8 | Normal — some buffering, no concern |
| > 0.8 | **Backpressure** — downstream node is struggling |
| 1.0 (stall) | **Bottleneck** — upstream is blocked waiting for downstream |

The bottleneck node is always `edge.ToNode` on the edge with the highest
fill ratio.

---

## 2. Per-Node Processing Latency — ✅ Implemented

### Problem

There is no way to know how long each node takes to process a single frame.
The Prometheus histogram `mediamolder_node_latency_seconds` exists but is
never observed. Without per-node timing, it's impossible to tell whether a
bottleneck is caused by a slow encoder, a complex filter, or I/O wait.

### Design

#### Approach: Per-Frame Timing Inside Each Handler

Each per-kind handler (`handleSource`, `handleFilter`, `handleEncoder`,
`handleSink`, `handleGoProcessor`) wraps its inner loop body with
`time.Now()` / `RecordLatency()`. This gives precise per-frame latency with
no scheduler involvement. The existing `NodeMetrics` struct gains latency
tracking fields.

#### Changes to `NodeMetrics`

```go
// pipeline/metrics.go

type NodeMetrics struct {
    NodeID    string
    Frames    atomic.Int64
    Errors    atomic.Int64
    Bytes     atomic.Int64
    StartTime time.Time

    // NEW: latency tracking
    latencySum  atomic.Int64 // cumulative nanoseconds
    latencyCount atomic.Int64
    latencyMax   atomic.Int64 // peak nanoseconds

    mu sync.Mutex
}

// RecordLatency records a single frame's processing duration.
func (m *NodeMetrics) RecordLatency(d time.Duration) {
    ns := d.Nanoseconds()
    m.latencySum.Add(ns)
    m.latencyCount.Add(1)
    // CAS loop for max
    for {
        cur := m.latencyMax.Load()
        if ns <= cur || m.latencyMax.CompareAndSwap(cur, ns) {
            break
        }
    }
}
```

Add to `NodeMetricsSnapshot`:

```go
type NodeMetricsSnapshot struct {
    // ... existing fields ...
    AvgLatency time.Duration // average per-frame latency
    MaxLatency time.Duration // peak per-frame latency
}
```

#### Handler Instrumentation Points

Each handler calls `metrics.Node(node.ID).RecordLatency(elapsed)` after
processing one frame/packet. The timing boundary depends on the node kind:

| Kind | What to Time | Where |
|------|-------------|-------|
| Source | One `ReadPacket` + decode cycle | Inner loop of `handleSource` |
| Filter | One `PushFrame` + `PullFrame` cycle | Inner loop of `handleFilter` |
| Encoder | One `SendFrame` + `ReceivePacket` cycle | Inner loop of `handleEncoder` |
| Sink | One `WritePacket` call | Inner loop of `handleSink` |
| GoProcessor | One `Process()` call | Inner loop of `handleGoProcessor` |

Example instrumentation (encoder):

```go
// In handleEncoder, around the per-frame loop body:
start := time.Now()
// ... existing encode logic for one frame ...
r.pipe.Metrics().Node(node.ID).RecordLatency(time.Since(start))
r.pipe.Metrics().Node(node.ID).Frames.Add(1)
```

#### Files to Modify

| File | Action | Description |
|------|--------|-------------|
| `pipeline/metrics.go` | **Modify** | Add `RecordLatency`, `latencySum/Count/Max`, update `Snapshot()` |
| `pipeline/handlers.go` | **Modify** | Add `time.Now()` / `RecordLatency()` calls in each handler's inner loop |
| `pipeline/metrics_test.go` | **Modify** | Test `RecordLatency`, snapshot average/max |

#### Testing Strategy

- Unit test: Call `RecordLatency` with known durations, verify `Snapshot()` returns correct average and max.
- Unit test: Concurrent `RecordLatency` calls from multiple goroutines — verify no races (run with `-race`).

---

## 3. Prometheus Metrics Wiring — ✅ Implemented

### Problem

Eight Prometheus metrics are defined in `observability/metrics.go` but **none**
are ever populated by the pipeline. The internal `MetricsRegistry` collects
frame/error/byte counts but they are never bridged to Prometheus.

### Design

#### Approach: Bridge in MetricsEmitter

The `MetricsEmitter` already runs a periodic ticker and has access to the
`MetricsRegistry`. It currently posts `MetricsSnapshotEvent` to the event bus.
We extend it to also update Prometheus collectors.

This keeps all Prometheus writes in one goroutine on a timer (not on the hot
path), avoiding contention on Prometheus internals.

#### Changes to `MetricsEmitter`

```go
// pipeline/event.go

type MetricsEmitter struct {
    // ... existing fields ...
    prom *observability.Metrics // NEW: nil = no Prometheus export
}

func NewMetricsEmitter(
    interval time.Duration,
    registry *MetricsRegistry,
    events *EventBus,
    getState func() State,
    prom *observability.Metrics,        // NEW parameter (nil-safe)
) *MetricsEmitter
```

In the ticker loop, after posting `MetricsSnapshotEvent`, add:

```go
if m.prom != nil {
    m.prom.PipelineState.WithLabelValues().Set(float64(m.getState()))
    for _, ns := range snap.Nodes {
        labels := []string{ns.NodeID, "video"} // media_type from node kind
        m.prom.FramesTotal.WithLabelValues(labels...).Add(float64(deltaFrames))
        m.prom.BytesTotal.WithLabelValues(labels...).Add(float64(deltaBytes))
        m.prom.ErrorsTotal.WithLabelValues(labels...).Add(float64(deltaErrors))
        m.prom.Fps.WithLabelValues(labels...).Set(ns.FPS)
        if ns.AvgLatency > 0 {
            m.prom.NodeLatency.WithLabelValues(labels...).Observe(ns.AvgLatency.Seconds())
        }
    }
}
```

**Delta tracking:** The emitter needs to remember the previous snapshot to
compute deltas for counters (Prometheus counters are monotonic — you can't
set them, only add). Store `prevSnapshot map[string]NodeMetricsSnapshot` in
the emitter.

#### Backpressure → Prometheus Bridge

If `EdgeStatsRegistry` (from §1) is available, the emitter also updates
`NodeBufFill`:

```go
if m.edgeStats != nil {
    for _, es := range m.edgeStats.Snapshot() {
        m.prom.NodeBufFill.WithLabelValues(es.ToNode).Set(es.Fill)
    }
}
```

#### Files to Modify

| File | Action | Description |
|------|--------|-------------|
| `pipeline/event.go` | **Modify** | Add `prom` field to `MetricsEmitter`, update constructor, add Prometheus bridge in ticker |
| `pipeline/engine.go` | **Modify** | Pass `observability.Metrics` to `NewMetricsEmitter` when available |

#### Dependency

- Requires §2 (per-node latency) for the latency histogram observations.
- Requires §1 (backpressure) for the buffer fill gauge, but this is optional.

#### Testing Strategy

- Unit test: Create a `MetricsEmitter` with a real `observability.Metrics`, record some node metrics, tick once, verify Prometheus collectors have correct values via `prometheus.ToFloat64()`.
- Integration test: Start a pipeline, scrape `/metrics`, verify non-zero `mediamolder_pipeline_frames_total`.

---

## 4. OpenTelemetry Span Wiring — ✅ Implemented

### Problem

`StartPipelineSpan()` and `StartNodeSpan()` exist in `observability/tracing.go`
but are never called from the pipeline. Without spans, tracing backends
(Jaeger, Tempo, etc.) show nothing.

### Design

#### Approach: Spans at Two Levels

**Level 1 — Pipeline span:** Wrap the entire `runGraph()` call in a root span.

```go
// pipeline/engine.go — in runGraph()

ctx, span := obs.StartPipelineSpan(ctx, cfg.Description)
defer func() {
    if err != nil {
        observability.EndSpanError(span, err)
    } else {
        observability.EndSpanOK(span)
    }
}()
```

**Level 2 — Node spans:** Wrap each handler invocation in the scheduler.
Rather than modifying the scheduler (which is runtime-generic), wrap the
handler in `runGraph()` before passing it to the scheduler:

```go
// pipeline/engine.go — in runGraph(), before sched.Run()

wrappedHandler := func(ctx context.Context, node *graph.Node, ins []<-chan any, outs []chan<- any) error {
    ctx, span := observability.StartNodeSpan(ctx, node.ID, node.Kind.String(), "", "")
    err := runner.handle(ctx, node, ins, outs)
    if err != nil {
        observability.EndSpanError(span, err)
    } else {
        observability.EndSpanOK(span)
    }
    return err
}

sched := &runtime.Scheduler{BufSize: 8}
if err := sched.Run(ctx, dag, wrappedHandler); err != nil {
```

This produces a trace like:

```
pipeline.run                        [============================================]
├── pipeline.node.source (src)      [============================================]
├── pipeline.node.filter (scale)      [========================================]
├── pipeline.node.encoder (enc)         [====================================]
└── pipeline.node.sink (out)              [================================]
```

Node spans are children of the pipeline span because `ctx` carries the parent.

#### Structured Log Correlation

Inside each handler, use `observability.Logger(ctx)` for any log messages.
The trace ID and span ID are automatically attached:

```go
observability.Logger(ctx).Info("processing", "node", node.ID, "frames", metrics.Frames.Load())
```

#### Configuration: Opt-In

Tracing should be opt-in. If no `observability.Provider` is configured, the
wrapper is a no-op (Go's OpenTelemetry noop tracer produces zero overhead).
The `Pipeline` struct gets an optional `ObsProvider *observability.Provider`
field, set at construction time.

#### Files to Modify

| File | Action | Description |
|------|--------|-------------|
| `pipeline/engine.go` | **Modify** | Add pipeline span in `runGraph()`, wrap handler with node spans |
| `pipeline/engine.go` | **Modify** | Add `ObsProvider` to `Pipeline` struct and `NewPipeline` |

#### Dependency

- No dependency on §1–§3. Can be implemented independently.

#### Testing Strategy

- Unit test: Use `sdktrace.NewTracerProvider(sdktrace.WithSyncer(tracetest.NewInMemoryExporter()))` to capture spans in memory. Run a graph, verify one pipeline span and N node spans are created.
- Verify span parent-child relationships.

---

## 5. Adaptive Buffer Sizing — ✅ Phase A Implemented (static heuristics)

### Problem

All channels use a fixed buffer size of 8. This is too small for bursty
sources (H.264 B-frame reordering produces bursts) and wastefully large for
simple pass-through edges. Suboptimal sizing causes unnecessary stalls
(buffer too small) or wasted memory (buffer too large).

### Design

#### Approach: Compilation Pass + Historical Stats

This builds on §1 (backpressure monitoring) and the existing graph compilation
framework (§ graph-compilation.md).

**Phase A — Static heuristics (compile-time):**

Add a new compilation pass `computeBufferHints` in `graph/compile.go` that
sets per-edge buffer size hints based on node kinds:

| Upstream Kind | Downstream Kind | Buffer Hint | Rationale |
|--------------|----------------|-------------|-----------|
| Source | Filter | 16 | Demux produces bursts (B-frame reordering) |
| Filter | Filter | 8 | Steady flow |
| Filter | Encoder | 16 | Encoder is typically slower |
| Encoder | Sink | 4 | Packets are small, I/O is fast |
| GoProcessor | any | 8 | Unknown processing time |

The `ExecutionPlan` gets a new field:

```go
type ExecutionPlan struct {
    // ... existing fields ...
    EdgeBufSizes map[*Edge]int // per-edge buffer size recommendations
}
```

**Phase B — Historical feedback (runtime, future):**

After a pipeline run, write edge stats (peak fill, stall count) to a local
cache file keyed by a graph topology hash. On the next run of the same graph,
the compiler reads the cache and adjusts buffer sizes:

- Edges with `PeakFill < 0.1` → halve the buffer (minimum 2)
- Edges with `PeakFill > 0.8` or `Stalls > 0` → double the buffer (maximum 64)

This is a feedback loop:
```
Run 1: default buffers → collect stats → write cache
Run 2: read cache → adjusted buffers → collect stats → update cache
Run 3: converged buffer sizes
```

#### Scheduler Changes

The scheduler currently creates all channels with the same `BufSize`. Change
`Run()` to accept per-edge sizes:

```go
type Scheduler struct {
    BufSize      int            // default if no per-edge hint
    EdgeBufSizes map[*graph.Edge]int // per-edge overrides (from ExecutionPlan)
    // ... existing fields ...
}

// In Run():
for _, e := range g.Edges {
    size := s.BufSize
    if s.EdgeBufSizes != nil {
        if hint, ok := s.EdgeBufSizes[e]; ok {
            size = hint
        }
    }
    edgeCh[e] = make(chan any, size)
}
```

#### Files to Create/Modify

| File | Action | Description |
|------|--------|-------------|
| `graph/compile.go` | **Modify** | Add `computeBufferHints` pass, populate `EdgeBufSizes` |
| `graph/plan.go` | **Modify** | Add `EdgeBufSizes` field to `ExecutionPlan` |
| `runtime/scheduler.go` | **Modify** | Support per-edge buffer sizes |
| `pipeline/engine.go` | **Modify** | Pass `plan.EdgeBufSizes` to scheduler |
| `runtime/edge_stats.go` | **Modify** (Phase B) | Add cache write/read for historical stats |

#### Dependency

- Phase A requires no other sections (pure compile-time).
- Phase B requires §1 (backpressure monitoring) for the stats data.

#### Testing Strategy

- Unit test: Verify `computeBufferHints` assigns expected sizes for each edge kind combination.
- Unit test: Verify scheduler creates channels with per-edge sizes.
- Integration test (Phase B): Run a graph twice, verify second run uses adjusted buffer sizes.

---

## Implementation Order

The sections have the following dependency graph:

```
§1 Backpressure ──────────────┐
       │                      │
       ▼                      ▼
§2 Latency ──→ §3 Prometheus  §5 Adaptive Buffers (Phase B)
                    │
                    ▼
            §4 Tracing (independent, can be done anytime)
            §5 Adaptive Buffers (Phase A, independent)
```

### Recommended Implementation Sequence

| Step | Section | Estimated Scope | Depends On | Status |
|------|---------|-----------------|------------|
| 1 | §1 Backpressure Monitoring | 3 files, ~200 LOC | — | ✅ Done |
| 2 | §2 Per-Node Latency | 2 files, ~80 LOC | — | ✅ Done |
| 3 | §3 Prometheus Wiring | 2 files, ~100 LOC | §1, §2 | ✅ Done |
| 4 | §4 OpenTelemetry Spans | 1 file, ~40 LOC | — | ✅ Done |
| 5 | §5a Adaptive Buffers (static) | 3 files, ~100 LOC | — | ✅ Done |
| 6 | §5b Adaptive Buffers (historical) | 2 files, ~150 LOC | §1, §5a | Not started |

Steps 1, 2, and 4 can be done in parallel since they have no mutual
dependencies. Step 3 should follow 1 and 2. Step 5a can be done at any time.
Step 5b is the final piece that closes the feedback loop.

---

## Verification: How to Confirm Bottlenecks Are Detectable

After implementing §1–§3, a user can identify bottlenecks through three
complementary channels:

### 1. Programmatic (Go API)

```go
pipe, _ := pipeline.NewPipeline(cfg)
pipe.Start(ctx)

// After some processing time:
stats := pipe.EdgeStats().Snapshot()
for _, es := range stats {
    if es.Fill > 0.8 {
        fmt.Printf("BOTTLENECK: %s → %s (fill: %.0f%%, stalls: %d)\n",
            es.FromNode, es.ToNode, es.Fill*100, es.Stalls)
    }
}
```

### 2. Prometheus + Grafana

```promql
# Find the bottleneck node:
topk(1, mediamolder_node_buffer_fill{pipeline="my-pipeline"})

# Correlate with latency:
histogram_quantile(0.99, rate(mediamolder_node_latency_seconds_bucket[1m]))

# Check if the slow node is CPU-bound:
rate(mediamolder_pipeline_frames_total{node="encoder"}[1m])
```

### 3. Event Bus

```go
for ev := range pipe.Events() {
    switch e := ev.(type) {
    case pipeline.MetricsSnapshotEvent:
        for _, n := range e.Snapshot.Nodes {
            if n.MaxLatency > 100*time.Millisecond {
                log.Printf("slow node: %s (max latency: %s)", n.NodeID, n.MaxLatency)
            }
        }
    }
}
```

### 4. OpenTelemetry Traces

View the pipeline in Jaeger/Tempo. Node spans show relative duration — the
longest span is the bottleneck. Drill into it to see codec, media type, and
error events.
