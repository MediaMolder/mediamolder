# Per-Node Performance Monitoring Design

**Status:** Phase 4 implemented (`perf_monitoring` branch)  
**Target branch:** `perf_monitoring`  
**Relates to:** `observability/metrics.go`, `pipeline/metrics.go`, `pipeline/handlers.go`, `runtime/scheduler.go`

---

## 1. Problem Statement

The existing `NodeMetrics` struct records only cumulative totals — frames processed, bytes consumed, errors — since the pipeline started. It can calculate a crude average FPS as `totalFrames / elapsedSeconds`, but this:

- Tells you nothing about **right now**: whether a node is fast, slow, or stuck
- Cannot distinguish *why* a node is slow: is it compute-bound, waiting for input (upstream is the bottleneck), or blocked on a full output buffer (downstream is the bottleneck)?
- Has no stall or backpressure visibility at all

The result: when a transcode runs at 15 fps instead of 60 fps, there is no automated way to know which node is responsible.

### The three node states

At any instant, a node's goroutine is in exactly one of three states:

| State | Meaning | Cause |
|-------|---------|-------|
| **PROCESSING** | Actively computing — decoding, filtering, encoding, muxing | Normal operation |
| **IDLE** | Blocked on `<-ins[i]`, waiting for a frame to arrive | Upstream node is slower, or upstream has ended |
| **STALLED** | Blocked on `outs[i] <- v`, waiting for space in the output buffer | Downstream node is slower (backpressure) |

Measuring the time fraction spent in each state per node tells you exactly where the pipeline bottleneck is:

- A node that is mostly **IDLE** is faster than its upstream — it is starved of input.
- A node that is mostly **STALLED** is faster than its downstream — it is the bottleneck source; the node *downstream* of it is the actual bottleneck.
- A node that is mostly **PROCESSING** is compute-bound — it is the bottleneck itself.

---

## 2. Current Infrastructure

### 2.1 Scheduler and channels (`runtime/scheduler.go`)

The `Scheduler.Run` method allocates one `chan any` per graph edge with a fixed buffer (`BufSize`, default 8). Each node goroutine receives:
- `ins []<-chan any` — one channel per inbound edge
- `outs []chan<- any` — one channel per outbound edge

Backpressure is implicit Go channel blocking. When a downstream node's goroutine falls behind, its inbound channel fills up. The upstream node then blocks on `select { case outs[i] <- v: ... }`. There is currently no accounting of how long or how often this happens.

### 2.2 Existing `NodeMetrics` (`pipeline/metrics.go`)

```go
type NodeMetrics struct {
    NodeID    string
    Frames    atomic.Int64
    Errors    atomic.Int64
    Bytes     atomic.Int64
    StartTime time.Time
    mu        sync.Mutex
}
```

The `Snapshot()` method derives `FPS = float64(Frames) / time.Since(StartTime).Seconds()` — an average-since-start, not a live rate.

### 2.3 Prometheus metrics (`observability/metrics.go`)

`NodeLatency` (histogram) and `NodeBufFill` (gauge) are registered but **never populated** — they are placeholders waiting for this design to be implemented.

---

## 3. Design

### 3.1 New type: `NodePerfTracker`

A `NodePerfTracker` sits alongside `NodeMetrics` for each node and accumulates timing data. It is designed for zero-allocation in the common (non-stall) hot path.

```go
// NodePerfTracker records the time a node spends in each of its three
// operating states: PROCESSING, IDLE, and STALLED.
//
// All methods are safe for concurrent use from a single node goroutine
// (the node goroutine is the only writer; snapshots may be taken from
// any goroutine via Snapshot()).
type NodePerfTracker struct {
    nodeID string

    // Current state bookkeeping (written only by the node goroutine).
    stateStart time.Time
    state      nodePerfState // current state enum

    // Cumulative time accumulators (written by node goroutine, read under mu).
    mu          sync.Mutex
    activeNs    int64 // nanoseconds in PROCESSING state
    idleNs      int64 // nanoseconds in IDLE state
    stalledNs   int64 // nanoseconds in STALLED state

    // Stall event counters.
    stallCount    atomic.Int64
    maxStallNs    atomic.Int64 // largest single stall, nanoseconds

    // Windowed throughput: a fixed-size ring buffer of frame-arrival timestamps.
    // The window is defined as the span from the oldest to newest entry.
    // Size 256 gives ~5–10 s of history at 25–50 fps.
    tsBuf    [256]int64 // Unix nanoseconds of last N frame outputs
    tsBufLen int
    tsBufPos int
}

type nodePerfState uint8

const (
    stateProcessing nodePerfState = iota
    stateIdle
    stateStalled
)
```

#### State transitions

The node goroutine calls these methods to record transitions:

```go
// BeginIdle records the moment the goroutine is about to block waiting for input.
func (t *NodePerfTracker) BeginIdle()

// EndIdle records the moment a frame arrived from the input channel.
// It automatically transitions to PROCESSING state.
func (t *NodePerfTracker) EndIdle()

// BeginStall records the moment the output channel became full.
func (t *NodePerfTracker) BeginStall()

// EndStall records the moment the output channel accepted the frame.
func (t *NodePerfTracker) EndStall()

// RecordFrame records that the node emitted one frame/packet.
// This drives the windowed FPS calculation.
func (t *NodePerfTracker) RecordFrame()

// Snapshot returns a point-in-time read of all performance data.
func (t *NodePerfTracker) Snapshot() NodePerfSnapshot
```

The PROCESSING state is implicit — it is the time between `EndIdle()` and the next `BeginIdle()` or `BeginStall()`.

### 3.2 Windowed FPS via timestamp ring buffer

Rather than an exponentially-weighted moving average (which reacts slowly to sudden changes), `NodePerfTracker` maintains a ring buffer of the last 256 frame-output timestamps. The windowed FPS is computed at snapshot time:

```go
func (t *NodePerfTracker) windowedFPS() float64 {
    if t.tsBufLen < 2 {
        return 0
    }
    // oldest and newest timestamps in the ring
    oldest := t.tsBuf[(t.tsBufPos - t.tsBufLen + 256) % 256]
    newest := t.tsBuf[(t.tsBufPos - 1 + 256) % 256]
    elapsed := float64(newest-oldest) / 1e9
    if elapsed <= 0 {
        return 0
    }
    return float64(t.tsBufLen-1) / elapsed
}
```

With 256 slots at 25 fps this is a ~10-second window; at 240 fps it is ~1 second. The window automatically adapts to the node's actual throughput.

### 3.3 Snapshot type

```go
// NodePerfSnapshot is a point-in-time copy of all performance data for one node.
type NodePerfSnapshot struct {
    NodeID string

    // Windowed throughput (last ~256 frames).
    FPS float64 // frames (or packets) per second

    // Time-distribution fractions (0.0–1.0). Sum ≤ 1.0; gap is startup.
    ActiveFrac  float64 // fraction of total elapsed in PROCESSING
    IdleFrac    float64 // fraction of total elapsed in IDLE (upstream slow)
    StalledFrac float64 // fraction of total elapsed in STALLED (downstream slow)

    // Absolute durations.
    TotalActive  time.Duration
    TotalIdle    time.Duration
    TotalStalled time.Duration

    // Stall event detail.
    StallCount   int64
    MaxStallDuration time.Duration

    // Output queue pressure at last send (instantaneous, 0.0–1.0).
    // A sustained value near 1.0 indicates this node is producing faster
    // than its downstream can consume.
    QueueFillFrac float64

    // Total elapsed wall-clock time since the node started.
    Elapsed time.Duration
}
```

### 3.4 Instrumented send/receive helpers

Rather than modifying every `select` statement individually, two helper functions wrap the channel operations. These are defined at the `pipeline` package level and used by all four handler types.

#### `perfReceive` — instrument idle wait

```go
// perfReceive receives one value from ch, recording the IDLE duration in t.
// Returns (value, false) on success, (nil, true) on ctx cancellation.
func perfReceive(ctx context.Context, ch <-chan any, t *NodePerfTracker) (any, bool) {
    // Optimistic non-blocking check first to avoid time.Now() on the fast path.
    select {
    case v, ok := <-ch:
        if !ok {
            return nil, true
        }
        t.RecordFrame()
        return v, false
    default:
    }
    // Channel empty — begin idle.
    t.BeginIdle()
    select {
    case v, ok := <-ch:
        t.EndIdle()
        if !ok {
            return nil, true
        }
        t.RecordFrame()
        return v, false
    case <-ctx.Done():
        t.EndIdle()
        return nil, true
    }
}
```

#### `perfSend` — instrument stall wait

```go
// perfSend sends v on ch, recording the STALLED duration in t.
// Returns true if ctx was cancelled before the send could complete.
func perfSend(ctx context.Context, ch chan<- any, v any, t *NodePerfTracker) bool {
    // Try non-blocking first — zero cost on the fast path.
    select {
    case ch <- v:
        return false
    default:
    }
    // Channel full — record stall.
    t.BeginStall()
    select {
    case ch <- v:
        t.EndStall()
        return false
    case <-ctx.Done():
        t.EndStall()
        return true
    }
}
```

> **Hot path cost analysis**: In the common case where the channel is neither empty nor full, only one `select` statement with no time calls is executed. `time.Now()` is called only when a stall or idle event actually occurs, which is the exception rather than the rule in a well-balanced pipeline.

### 3.5 Queue fill measurement

At each `perfSend` call, we can also snapshot the current queue fill level of the outbound channel. Because we hold the value `v` that we are about to send, `len(ch) / cap(ch)` gives the fill fraction *before* the send — useful for detecting sustained backpressure even when individual stalls are short:

```go
t.RecordQueueFill(float64(len(ch)) / float64(cap(ch)))
```

`RecordQueueFill` stores the value in the tracker using a decaying average:
```go
// α ≈ 0.05 → smoothed over ~20 observations
t.queueFillEWMA = 0.05*sample + 0.95*t.queueFillEWMA
```

### 3.6 Integration with `graphRunner`

`graphRunner` adds a `NodePerfTracker` per node and makes it available to handlers:

```go
type graphRunner struct {
    // ... existing fields ...
    perf map[string]*NodePerfTracker // keyed by node ID
}
```

Each handler receives its tracker as an additional argument (or via a thin context wrapper — see §3.8 on API ergonomics). The `sendFrame` / `receiveFrame` closures inside each handler are replaced with calls to `perfSend` / `perfReceive`.

**Example: `handleFilter` before:**
```go
for f := range ins[0] {
    if err := fg.PushFrame(f); err != nil { ... }
    for {
        out, _ := av.AllocFrame()
        if err := fg.PullFrame(out); av.IsEAgain(err) { ... }
        select {
        case outs[0] <- out:
        case <-ctx.Done():
            return ctx.Err()
        }
    }
}
```

**After:**
```go
tr := r.perf[node.ID]
for {
    v, done := perfReceive(ctx, ins[0], tr)
    if done { break }
    f := v.(*av.Frame)
    if err := fg.PushFrame(f); err != nil { ... }
    for {
        out, _ := av.AllocFrame()
        if err := fg.PullFrame(out); av.IsEAgain(err) { ... }
        if perfSend(ctx, outs[0], out, tr) {
            return ctx.Err()
        }
    }
}
```

The processing time between `EndIdle` and the next `BeginIdle` or `BeginStall` is implicit — it is the time the goroutine is not blocked on a channel.

### 3.7 Integration with `MetricsRegistry` and Prometheus

`NodePerfSnapshot` is included in `MetricsSnapshot` alongside the existing `NodeMetricsSnapshot`:

```go
type MetricsSnapshot struct {
    State   string
    Elapsed time.Duration
    Nodes   []NodeMetricsSnapshot
    Perf    []NodePerfSnapshot  // NEW — may be nil if tracker not attached
}
```

The `MetricsEmitter` goroutine (already ticking at a configured interval) feeds the snapshots into the Prometheus gauges that are currently unpopulated:

```go
// In MetricsEmitter.tick():
for _, p := range snap.Perf {
    promMetrics.Fps.WithLabelValues(p.NodeID, "all").Set(p.FPS)
    promMetrics.NodeBufFill.WithLabelValues(p.NodeID).Set(p.QueueFillFrac)
    promMetrics.NodeLatency  // populated from stall histogram bucket
}
```

New Prometheus metrics to add:

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `mediamolder_node_active_fraction` | Gauge | `node` | Fraction of wall time in PROCESSING |
| `mediamolder_node_idle_fraction` | Gauge | `node` | Fraction of wall time in IDLE (upstream slow) |
| `mediamolder_node_stalled_fraction` | Gauge | `node` | Fraction of wall time in STALLED (downstream slow) |
| `mediamolder_node_stall_duration_seconds` | Histogram | `node` | Distribution of individual stall event durations |
| `mediamolder_node_stall_count_total` | Counter | `node` | Total number of stall events |
| `mediamolder_node_fps` | Gauge | `node` | Rolling windowed frames per second |
| `mediamolder_node_queue_fill` | Gauge | `node` | Output channel fill fraction (EWMA) |
| `mediamolder_node_threads_configured` | Gauge | `node`, `thread_mode` | Configured libav thread count; label `thread_mode` = `none`/`frame`/`slice`/`auto`/`n_a` |
| `mediamolder_node_threads_busy` | Gauge | `node` | Live tasks in-flight from `execute2`/`execute` callback; -1 if unavailable |
| `mediamolder_node_cpu_cores_estimated` | Gauge | `node` | `threads_configured × active_fraction` (upper-bound) |
| `mediamolder_node_fps_target` | Gauge | `node` | Configured FPS target for this node |
| `mediamolder_node_fps_deficit` | Gauge | `node` | `fps_target − fps_actual`; positive = behind |
| `mediamolder_node_frame_latency_seconds` | Histogram | `node` | Frame processing latency distribution |
| `mediamolder_pipeline_frames_in_flight` | Gauge | — | Total frames buffered across all channels |
| `mediamolder_pipeline_realtime_satisfied` | Gauge | — | 1 if all nodes meeting fps_target, 0 otherwise |
| `mediamolder_node_thread_restarts_total` | Counter | `node` | Graceful restarts for thread reallocation |

### 3.8 API ergonomics

Two design options for how handlers access their tracker:

**Option A: direct parameter**  
`graphRunner.handle` passes the tracker explicitly:
```go
func (r *graphRunner) handleFilter(ctx context.Context, node *graph.Node,
    ins []<-chan any, outs []chan<- any, tr *NodePerfTracker) error
```
Pros: explicit, no reflection. Cons: changes the `runtime.NodeHandler` signature.

**Option B: context value**  
Store the tracker in `ctx` with a package-private key:
```go
ctx = withPerfTracker(ctx, tr)
// Inside handler:
tr := perfTrackerFrom(ctx)  // nil-safe; returns no-op tracker if absent
```
Pros: `runtime.NodeHandler` signature unchanged; tracker is opt-in. Cons: slightly slower context lookup; tracker might be nil in tests.

**Recommendation: Option B** — preserves the `runtime.NodeHandler` interface (which may have other callers), and a no-op tracker returned when absent avoids `nil` checks in handler code.

The no-op tracker is a global `nopTracker` instance where all methods are empty and `Snapshot()` returns zero values, ensuring zero overhead when performance monitoring is disabled.

---

## 4. Handler-by-Handler Instrumentation Points

### 4.1 Source (`handleSource`)

A source node has no input channels — it reads from an AV demuxer/decoder. Timing is:

- **PROCESSING**: `ReadPacket` + `SendPacket` + `ReceiveFrame` calls (I/O + decode)
- **STALLED**: `select { case outs[i] <- frame: ... }` blocked

There is no IDLE state for a source (it is always trying to produce frames). However, `ReadPacket` itself may block on I/O (e.g., RTSP or pipe sources). This should be counted as PROCESSING for now (I/O latency is part of the source's inherent cost), with a future refinement to distinguish `av_read_frame` blocking time.

```
[ReadPacket → Decode] ──STALL?──► outs[0]
                                   outs[1]
```

### 4.2 Filter (`handleFilter`)

```
ins[0] ──IDLE?──► [PushFrame → PullFrame] ──STALL?──► outs[0]
ins[1] ──IDLE?──►
```

The simple 1→1 fast path (`handleSimpleFilter`) and the multi-input path both follow the same pattern.

### 4.3 Encoder (`handleEncoder`)

```
ins[0] ──IDLE?──► [SendFrame → ReceivePacket] ──STALL?──► outs[0]
```

Encoders are often the heaviest compute stage; their PROCESSING fraction is expected to be high.

### 4.4 Sink (`handleSink`)

```
ins[0] ──IDLE?──► [WritePacket]
ins[1] ──IDLE?──►
```

Sinks have no outbound channels, so there is no STALLED state. Sustained IDLE at a sink means the encoders feeding it are slower than the muxer — this is normal. If `WritePacket` itself blocks (e.g., slow NFS, RTMP back-pressure), that time appears as PROCESSING.

---

## 5. Edge Cases and Design Decisions

### 5.1 Fanout nodes (one output to multiple inputs)

When a source or filter fans out to multiple downstream nodes, `perfSend` is called once per outbound channel. The STALLED time accumulates per call:

```go
for _, idx := range outIndices {
    if perfSend(ctx, outs[idx], frame, tr) { return ctx.Err() }
}
```

This is correct: if channel 0 is full but channel 1 is not, the node stalls on channel 0, then immediately sends to channel 1. The total stall time for this frame is the sum of all per-channel stall times.

### 5.2 Multi-input synchronisation (overlay, amix)

Multi-input handlers use a serialised goroutine model (see `handleFilter` multi-input path). The goroutines waiting on individual input channels each get their own IDLE accounting via separate `perfReceive` calls on their respective channels.

### 5.3 Clock resolution

`time.Now()` on Linux/macOS returns nanosecond resolution via `clock_gettime(CLOCK_REALTIME)`. At 60 fps the frame interval is ~16.7 ms — well above the clock resolution. Even at 240 fps (4.2 ms frame interval), measurement jitter is negligible for the intended purpose (identifying bottlenecks at multi-millisecond granularity).

### 5.4 `runLinear` path

The legacy `runLinear` execution path uses inline goroutines rather than the graph runner and does not receive `NodePerfTracker` instances. For this first implementation, `runLinear` is left uninstrumented — it is targeted for retirement (F7 in followups roadmap). A simple note in the snapshot's `Perf` slice will be absent for linear-mode pipelines.

### 5.5 Filter graph internal parallelism

`av.FilterGraph` may internally use libavfilter's `avfilter_graph_config` thread pool. The time spent in `PushFrame`/`PullFrame` includes libavfilter processing time across all its internal filter threads. This is intentional — from the node goroutine's perspective, `PushFrame` + `PullFrame` is atomic work, and its duration is the correct measure of the filter node's throughput contribution.

---

## 5a. CPU Thread Visibility

### Threading models in the pipeline

libav codecs and filters fall into two distinct threading categories, requiring different measurement strategies:

**Category A — libavcodec built-in threading (most software codecs)**  
The codec calls `AVCodecContext.execute2` to dispatch parallel tasks (e.g., encoding/decoding multiple macroblocks simultaneously). This callback is a *public, user-overridable function pointer* on `AVCodecContext`. Examples: libvpx, libopus, MPEG-2, H.263, most hardware wrapper codecs.

**Category B — proprietary internal thread pools (x264, x265)**  
These codecs manage their own thread pools entirely outside of libavcodec's threading machinery. They never call `execute2`. Their internal pools are opaque OS threads not visible to Go or to libavcodec. Examples: libx264, libx265, libsvtav1.

### Approach for Category A: `execute2` / `execute` callback intercept

Both `AVCodecContext` and `AVFilterGraph` expose user-overridable task dispatch callbacks:

```c
// AVCodecContext — called to run `count` parallel codec tasks
int (*execute2)(AVCodecContext *c,
    int (*func)(AVCodecContext *c2, void *arg, int jobnr, int threadnr),
    void *arg2, int *ret, int count);

// AVFilterGraph — called to run `nb_jobs` parallel filter slice tasks
typedef int (avfilter_execute_func)(AVFilterContext *ctx,
    avfilter_action_func *func, void *arg, int *ret, int nb_jobs);
avfilter_execute_func *execute;  // field on AVFilterGraph
```

By replacing the default implementations with thin counting wrappers during `avcodec_open2` / before `avfilter_graph_config`, we get exact `ThreadsBusy` counts with no library modification:

```c
// av/thread_count.c — installed as AVCodecContext.execute2
static int mm_execute2(AVCodecContext *ctx,
                       int (*func)(AVCodecContext*, void*, int, int),
                       void *arg2, int *ret, int count) {
    mm_node_thread_state *s = ctx->opaque_ref; // pointer to our state struct
    atomic_fetch_add(&s->tasks_active, count);
    int r = avcodec_default_execute2(ctx, func, arg2, ret, count);
    atomic_fetch_sub(&s->tasks_active, count);
    return r;
}
```

The `tasks_active` atomic is sampled at snapshot time. Because `execute2` is synchronous (it blocks until all tasks complete), the snapshot captures the task count *while the codec is running*.

An identical pattern applies to `AVFilterGraph.execute`.

This gives `NodePerfSnapshot.ThreadsBusy int` for all Category A codecs and all filter nodes. Zero overhead during IDLE/STALLED states.

### Approach for Category B: x264 C modification

x264 (workspace: `/Users/tom.vaughan/x264`) is the primary codec that needs this treatment. The required change is small: add a read-only accessor to the encoder that reads the thread pool's internal job counter.

In `common/threadpool.c`, x264's `x264_threadpool_t` tracks running jobs internally. A new exported function:

```c
// x264.h — add to public API
/* Returns the number of worker threads currently executing encode jobs.
 * Returns -1 if the encoder uses frame-level threading (not a thread pool). */
int x264_encoder_get_thread_busy_count(x264_t *h);
```

The implementation reads `h->threadpool->i_jobs_running` (or equivalent internal counter) under a brief lock. This is purely additive — no change to existing behaviour, no ABI break.

For **x265**, we do not have the source in this workspace, but x265 exposes per-frame wavefront statistics (`decideWaitTime`, `stallTime`, `avgWPP`) in the per-CTU-row analysis output. These require `x265_param.bWaveFront = 1` and are accessible after encode via the frame's `analysisData`. They provide a proxy for thread utilisation but are not a live busy count. For x265, the estimated utilisation formula (`ThreadsConfigured × ActiveFrac`) remains the practical metric until we have x265 source access.

### Static metrics (all codecs, zero overhead)

Regardless of category, the following are readable via CGO after the codec context is opened:

| libav field | Location | Meaning |
|---|---|---|
| `AVCodecContext.thread_count` | encoder, decoder | Threads configured (as granted by libavcodec) |
| `AVCodecContext.active_thread_type` | encoder, decoder | 0=none, 1=`FF_THREAD_FRAME`, 2=`FF_THREAD_SLICE` |
| `AVFilterGraph.nb_threads` | filter graph | Graph-level thread cap (0 = auto-detected) |

`active_thread_type` is the ground truth: a codec that doesn't support multithreading will report 0 regardless of the configured count.

New `av` package methods:

```go
func (e *EncoderContext) ThreadCount() int       // AVCodecContext.thread_count
func (e *EncoderContext) ActiveThreadType() int  // 0/1/2
func (d *DecoderContext) ThreadCount() int
func (d *DecoderContext) ActiveThreadType() int
func (fg *FilterGraph) ThreadCount() int         // AVFilterGraph.nb_threads
```

### `NodePerfSnapshot` additions

```go
ThreadsConfigured int     // from AVCodecContext.thread_count / AVFilterGraph.nb_threads
ThreadMode        string  // "none", "frame", "slice", "auto", "n/a"
ThreadsBusy       int     // live count from execute2/execute callback; -1 if unavailable
EstimatedCPUCores float64 // ThreadsConfigured × ActiveFrac; upper-bound estimate
```

### Summary

| Metric | Source | Codec scope | Overhead |
|---|---|---|---|
| `ThreadsConfigured` | `AVCodecContext.thread_count` | All | None (static) |
| `ActiveThreadType` | `AVCodecContext.active_thread_type` | All | None (static) |
| `ThreadsBusy` | `execute2`/`execute` callback intercept | Category A + filters | ~20 ns per dispatch |
| `ThreadsBusy` | `x264_encoder_get_thread_busy_count()` | x264 only | ~50 ns per sample |
| `EstimatedCPUCores` | `ThreadsConfigured × ActiveFrac` | All (fallback) | None (derived) |

---

## 5b. Real-Time Mode Requirements

### Goal

Real-time mode maintains throughput ≥ `fps_target` at every node simultaneously. If any node falls behind, the system identifies the bottleneck, determines whether more threads would help, and either reconfigures the node or raises an alert.

The metrics defined in §3–5a cover the *observation* side. Real-time mode requires additional metrics and an *intervention* mechanism.

### Additional metrics needed

#### Per-node: throughput target and deficit

```go
// Added to NodePerfSnapshot:
FPSTarget  float64 // frames/sec budget for this node (from graph output stream config)
FPSDeficit float64 // FPSTarget - FPSActual; positive = falling behind; negative = headroom
```

`FPSTarget` is derived once from the graph's output stream frame rate (e.g., 30 fps) and propagated to all nodes. It is stored in `NodePerfTracker` at construction time.

`FPSDeficit` is the primary signal for the adaptive control loop. A node with `FPSDeficit > 0` is a bottleneck candidate.

#### Per-node: frame processing latency

The time from when a node *receives* its input frame to when it *emits* its output frame(s). Distinct from the active/idle/stalled fractions — a node can be 100% active yet have high latency because its codec buffers multiple frames internally (e.g., B-frame reorder delay, lookahead).

```go
// Added to NodePerfSnapshot:
FrameLatencyMean time.Duration // EWMA of frame latency over the measurement window
FrameLatencyP99  time.Duration // 99th percentile from the ring buffer
```

Implementation: stamp a frame with `time.Now()` on `perfReceive` and record `elapsed` on the corresponding `perfSend`. For encoders with multi-frame output delay, the input frame timestamp is propagated via packet side-data (or an in-process map keyed on PTS).

Prometheus: `mediamolder_node_frame_latency_seconds` (histogram).

#### Pipeline: end-to-end latency and frames in flight

```go
// Added to MetricsSnapshot:
EndToEndLatency time.Duration // sum of FrameLatencyMean across all nodes in the critical path
FramesInFlight  int           // total frames buffered across all pipeline channels (sum of len(ch))
```

`FramesInFlight` is the sum of `len(ch)` for every inter-node channel, read at snapshot time. It bounds memory usage and gives early warning of accumulating buffer buildup.

### What can be adjusted at runtime

This is a hard constraint for the intervention design:

| Setting | Changeable after open? | Mechanism |
|---|---|---|
| `AVCodecContext.thread_count` | **No** | Requires codec close + reopen |
| `AVFilterGraph.nb_threads` | **No** | Requires graph `avfilter_graph_free` + rebuild |
| x264 `i_threads` | **No** | Requires `x264_encoder_close` + `x264_encoder_open` |
| x265 `poolNumThreads` | **No** | Requires `x265_encoder_close` + `x265_encoder_open` |
| x264 QP / bitrate / VBV | **Yes** | `x264_encoder_reconfig()` — no restart |
| x265 QP / bitrate | **Yes** | `x265_encoder_reconfig()` — no restart |
| Encoder preset (libx264 `preset=`) | **No** | Requires restart |
| Source frame rate | **Yes** (lavfi `fps` filter) | `avfilter_graph_send_command()` with the `fps` filter |

**Thread reallocation in practice means graceful node restart**: drain the pipeline channel upstream of the node, call `Close()` on the existing context, reopen with the new thread count, and resume. The restart pauses that node for ~1–3 encode frames worth of time but does not drop data.

### Adaptive control loop

The control loop runs as a separate goroutine within the `pipeline` package, activated by a `PipelineOpts.RealTime = true` flag.

```
┌─────────────────────────────────────────────────────┐
│                 Adaptive Control Loop                │
│                                                      │
│  1. Observe: read MetricsRegistry.Snapshot() every  │
│     500 ms (or every N output frames)               │
│                                                      │
│  2. Decide:                                          │
│     a. Find nodes where FPSDeficit > 0.5 fps         │
│     b. For each: if ActiveFrac > 0.9 AND             │
│           ThreadsBusy ≈ ThreadsConfigured:           │
│           → bottleneck is thread-limited             │
│           → increment ThreadsRequested by 2          │
│        elif ActiveFrac > 0.9 AND                     │
│           ThreadsBusy < ThreadsConfigured:           │
│           → bottleneck is sequential (codec overhead)│
│           → recommend preset change, not threads     │
│        elif StalledFrac > 0.5:                       │
│           → downstream is the real bottleneck        │
│           → do not increase this node's threads      │
│                                                      │
│  3. Actuate:                                         │
│     a. If new thread counts are within budget:       │
│        → trigger graceful restart of affected nodes  │
│     b. If already at max threads:                    │
│        → emit RealTimeViolation event                │
│        → optionally enable frame-drop mode (§5b.3)  │
│                                                      │
│  4. Account: update ThreadBudget; record adjustment  │
│     history for observability                        │
└─────────────────────────────────────────────────────┘
```

#### Thread budget manager

```go
type ThreadBudget struct {
    Total     int            // runtime.NumCPU() at startup, or user-configured cap
    Allocated map[string]int // node ID → currently allocated thread count
    Reserved  int            // threads kept back for Go runtime, OS, audio nodes
}
```

The budget manager is queried before any reallocation decision to prevent overcommit (which causes thrashing, not improvement).

#### Frame-drop mode (last resort)

If a node is already at the thread budget ceiling and still has `FPSDeficit > 1.0 fps`, the control loop can activate frame-drop mode on the upstream source: the source's `perfSend` skips every Nth frame (dropping the AVFrame before enqueueing) to reduce load. This is an explicit last resort, always logged as a warning.

### New Prometheus metrics for real-time mode

| Metric | Type | Labels | Description |
|---|---|---|---|
| `mediamolder_node_fps_target` | Gauge | `node` | Configured FPS target for this node |
| `mediamolder_node_fps_deficit` | Gauge | `node` | `fps_target − fps_actual`; positive = falling behind |
| `mediamolder_node_frame_latency_seconds` | Histogram | `node` | Frame processing latency distribution |
| `mediamolder_pipeline_frames_in_flight` | Gauge | — | Total frames buffered across all channels |
| `mediamolder_pipeline_realtime_satisfied` | Gauge | — | 1 if all nodes meeting fps_target, 0 otherwise |
| `mediamolder_node_thread_restarts_total` | Counter | `node` | Number of graceful restarts for thread reallocation |

---

## 6. Implementation Plan

### Phase 1 — Core tracker, static thread info ✅ DONE
1. ✅ Add `NodePerfTracker` and `NodePerfSnapshot` to `pipeline/` (`pipeline/node_perf.go`).
2. ✅ `*NodePerfTracker` is nil-safe (all methods are no-ops on nil); no separate nop type needed.
3. ✅ Add `perfReceive` and `perfSend` helpers (`pipeline/perf_helpers.go`).
4. ✅ Add `withPerfTracker` / `perfTrackerFrom` context helpers.
5. ✅ Unit tests for all three state transitions, windowed FPS ring buffer, stall detection (27 tests, all pass).
6. ✅ Add `ThreadCount() int` and `ActiveThreadType() int` to `av.EncoderContext` and `av.DecoderContext` (CGO reads of `AVCodecContext.thread_count` / `active_thread_type` after `avcodec_open2`).
7. ✅ Add `ThreadCount() int` to `av.FilterGraph` (reads `AVFilterGraph.nb_threads`).
8. ✅ `NodePerfSnapshot` includes `ThreadsConfigured`, `ThreadMode`, `EstimatedCPUCores`.

### Phase 2 — Handler instrumentation + live thread counting ✅ DONE
9. ✅ Extend `graphRunner` to allocate `NodePerfTracker` per node and inject via context (`pipeline/handlers.go`, `pipeline/engine.go`).
10. ✅ Update `handleSource`, `handleFilter`, `handleEncoder`, `handleSink` to use `perfSend`/`perfReceive` and record frame latency timestamps.
11. ✅ Add C-level `execute2`/`execute` callback wrappers in `av/mm_thread_count.c`; install during `OpenEncoder`, `OpenDecoder`, and `NewFilterGraph` via `mm_install_codec_tracker` / `mm_install_filter_tracker`.
12. ✅ Add `x264_encoder_get_thread_busy_count()` to x264 (`common/threadpool.c`); declared in `x264.h` for future direct use.
13. ✅ Update `MetricsSnapshot` and `MetricsRegistry` to include `[]NodePerfSnapshot`, `RegisterPerfTracker`, and per-node `FrameLatencyMean`.
14. ✅ Integration tests: `TestPipelinePerfMetrics_Populated` verifies `Perf` is non-empty with valid fractions; `TestPipelinePerfMetrics_EncoderThreadInfo` verifies `ThreadsConfigured > 0` for encoder node.

### Phase 3 — Prometheus and API ✅ DONE
15. ✅ Add all new metrics to `observability/metrics.go`.
16. ✅ Wire `MetricsEmitter` to populate all Prometheus gauges/histograms/counters.
17. ✅ Add `mediamolder perf` CLI subcommand: live table of per-node state fractions, FPS, deficit, and thread stats.

### Phase 4 — GUI integration ✅ DONE
18. ✅ Expose `NodePerfSnapshot[]` via the HTTP metrics endpoint JSON response.
    - `MetricsServer.RegisterPerfHandler` adds `/perf` (full `MetricsSnapshot` JSON, CORS-enabled).
    - `MetricsServer.RegisterPerfStreamHandler` adds `/perf/stream` SSE endpoint that pushes
      `[]NodePerfSnapshot` at 2 Hz; empty array sent when pipeline is idle.
19. ✅ Add a per-node performance overlay in the graph editor canvas: coloured activity bars
    (green=processing, yellow=idle, red=stalled) with FPS deficit badge, updating at ~2 Hz.
    - `frontend/src/components/mmnode.tsx`: custom React Flow node with three-segment coloured
      activity bar and colour-coded FPS deficit badge.
    - `frontend/src/lib/usePerfStream.ts`: React hook subscribing to `/perf/stream` SSE.
    - `frontend/src/app.tsx`: React Flow canvas that auto-arranges nodes and preserves
      user-dragged positions between updates.
    - Built with Vite 6 + React 19 + @xyflow/react 12; compiled output in `frontend/dist/`.

### Phase 5 — Adaptive control loop (real-time mode) ✅ DONE (item 24 deferred)
20. ✅ Implement `ThreadBudget` manager in `pipeline/thread_budget.go`.
21. ✅ Implement the adaptive control loop goroutine (`pipeline/realtime_ctrl.go`), activated by `PipelineOpts.Realtime = true`.
22. ✅ Implement graceful encoder restart (drain → close → reopen with new thread count → resume) in `pipeline/handlers_encoder.go`. Filter-graph restart is logged as a violation and deferred (avfilter graph rebuild is more involved; filter graphs are rarely the thread-limited bottleneck).
23. ✅ Implement frame-drop mode in `perfSend` (`pipeline/handlers_source.go`): `ShouldDrop()` sampled at the start of each send.
24. ❌ Integration test deferred: requires a slow-motion CGO pipeline fixture; tracked as a follow-up task.

### Using real-time mode

Real-time mode activates the adaptive control loop. The loop runs every 500 ms, observes per-node performance snapshots, and responds to thread-limited bottlenecks by:
- **Increasing encoder thread count** (graceful restart; x264/x265/aom, etc.) up to the global thread budget.
- **Enabling frame-drop** (1 in 4 frames skipped) once the thread budget is exhausted.
- **Emitting `RealTimeViolation` events** to the event bus when convergence is not possible.

**JSON config** — set `global_options.realtime: true`:
```json
{
  "schema_version": "1.2",
  "global_options": { "realtime": true },
  "nodes": [ … ]
}
```

**CLI flag** — overrides the JSON field without editing the file:
```
mediamolder run --realtime pipeline.json
```

**GUI** — check the **Real-time** checkbox in the toolbar, to the left of the Run button. Toggling the checkbox updates `global_options.realtime` in the in-memory graph; saving the file (Save / Save As…) persists the flag to the `.json` file.

> The `fps_target` per-node field (set in the Inspector under each source or encoder node) defines the target frame rate used to compute `FPSDeficit`. If omitted, the node is excluded from real-time control.


---

## 6a. Phase 6 — Adaptive Encoder Preset Stepping

### Motivation

Phase 5 handles thread-limited bottlenecks (add threads) and frame-rate failure
(drop frames). It does **not** handle the case where every encoder is already at
its CPU budget and the codec is *sequentially* the limit — entropy coding,
rate-distortion search, lookahead. The current loop only emits a
`RealTimeViolation` with the advisory text "consider a faster preset" and
otherwise leaves the operator to fix it manually.

For `ABR_BBB_AVI.json` (four `libx264` encoders + AAC) the typical failure mode
is: every x264 instance is configured `preset=slower`, threads are at the budget
ceiling, `ActiveFrac ≈ 0.97` on every encoder, `FPSDeficit > 1 fps` everywhere.
The right intervention is to drop every video encoder **one preset step faster**
(`slower → slow`) at the next GOP boundary and re-observe. If still behind:
`slow → medium`, and so on, down to `superfast`. If a faster preset overshoots
(deficit goes strongly negative) the loop may step back up.

This phase adds **adaptive preset stepping** as a third intervention, sitting
between "add threads" and "drop frames" in the decision tree.

### Why GOP-boundary switching

Switching preset mid-GOP would force a codec reconfig with no IDR, which is not
supported by libx264, libx265, or SVT-AV1 without a full close+reopen and would
break stream continuity (different reference pictures, different SPS/PPS in some
cases). Switching **at the next I-frame / IDR** lets each rendition start a new
closed GOP under the new preset and remain decodable from that point forward.

The switch is therefore inherently asynchronous: the control loop *requests* a
preset change; the encoder applies it the next time it forces an IDR (which it
does on every multiple of `g` frames, or when the rate-control / scene-cut logic
requests one).

### Per-encoder preset ladders

The ladder is fixed per codec and ordered from highest quality (slowest) to
lowest:

| Codec        | Ladder (slowest → fastest)                                                            | Source of truth |
|--------------|----------------------------------------------------------------------------------------|---|
| `libx264`    | `placebo, veryslow, slower, slow, medium, fast, faster, veryfast, superfast, ultrafast` | `x264.c` `x264_preset_names[]` |
| `libx265`    | `placebo, veryslow, slower, slow, medium, fast, faster, veryfast, superfast, ultrafast` | `x265cli.h` `x265_preset_names[]` |
| `libsvtav1`  | `0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13` (numeric, `0` = slowest)                  | SVT-AV1 `EbSvtAv1Enc.h` `preset` field |

For SVT-AV1 the "preset" is an integer (0–13); for x264/x265 it is a named
token. Internally we normalise both to a `presetIndex` (0 = slowest) so the
control loop is codec-agnostic.

```go
// pipeline/preset_ladder.go
type PresetLadder struct {
    Codec   string   // "libx264", "libx265", "libsvtav1"
    Names   []string // ordered slow → fast; len(Names) == ladder depth
}

// Step returns the name N positions faster (or slower if N < 0).
// Clamps at the ends of the ladder.
func (l PresetLadder) Step(current string, n int) (next string, clamped bool)
```

### What can be adjusted at runtime (extension of §5b table)

Adding a preset row:

| Setting | Changeable after open? | Mechanism |
|---|---|---|
| x264 preset | **No (mid-GOP)** / **Effectively yes at IDR** | `x264_encoder_reconfig` + force IDR, OR close+reopen at next GOP |
| x265 preset | **No (mid-GOP)** / **Effectively yes at IDR** | `x265_encoder_reconfig` + force IDR, OR close+reopen at next GOP |
| SVT-AV1 preset | **Yes** | `svt_av1_enc_set_parameter` with `EbSvtAv1EncConfiguration.enc_mode`, takes effect on next IDR; no encoder restart required |

The mechanism diverges by codec; the pipeline-level API stays uniform.

#### x264

`x264_encoder_reconfig(h, x264_param_t*)` accepts a new param struct. Calling
`x264_param_default_preset(&p, "slow", nil)` against the live params and
re-applying is documented as safe **only for a subset of fields**. Preset
changes touch many internal tables (analysis depth, ME range, subpel refine,
trellis, CABAC tables stay the same). The safest portable path is:

1. At the next forced IDR (`pic_in.i_type = X264_TYPE_IDR`), flush the encoder
   (`x264_encoder_close`).
2. Build new params with `x264_param_default_preset(&p, newPreset, tune)`,
   keeping bitrate / VBV / GOP / threads identical.
3. Open a new encoder (`x264_encoder_open`).
4. Feed the next decoded frame as the first IDR of the new GOP.

This is the same drain → close → reopen path already implemented in
`encoderSession.restartWithThreadCount`; we generalise it to
`restartWithParams(threads, preset)`.

> **No upstream x264 modifications required.** Everything needed is in the
> existing public API.

#### x265

`x265_encoder_reconfig(api, x265_param*)` is the documented runtime-reconfig
path. The x265 docs list which fields are reconfigurable (bitrate, VBV, QP,
keyint, bframes) — **preset is not in that list**. Like x264 we therefore use
the close+reopen path at the next GOP boundary.

> **No upstream x265 modifications required.**

#### SVT-AV1

SVT-AV1 explicitly supports runtime preset changes through
`svt_av1_enc_set_parameter` with the `enc_mode` field. The change is consumed at
the next IDR. We add an `av.EncoderContext.SetPresetIndex(int) error` that
shells out to this call for SVT-AV1 and falls through to the close+reopen path
for x264/x265.

> **No upstream SVT-AV1 modifications required.**

### Encoder modifications (MediaMolder side)

#### `av/` layer

Add to `av/encode.go`:

```go
// PresetCapability describes how this codec accepts runtime preset changes.
type PresetCapability int
const (
    PresetCapNone        PresetCapability = iota // not supported (raw, mjpeg, ...)
    PresetCapRestartIDR                          // close+reopen at next IDR (x264, x265)
    PresetCapHotReconfig                         // svt_av1_enc_set_parameter (SVT-AV1)
)

func (e *EncoderContext) PresetCapability() PresetCapability
func (e *EncoderContext) CurrentPreset() string       // returns "slower", "5", etc.
func (e *EncoderContext) Ladder() PresetLadder        // ordered name list for this codec
func (e *EncoderContext) RequestPresetChange(name string) error // queues for next IDR
```

For `PresetCapHotReconfig` codecs `RequestPresetChange` calls the codec API and
returns nil immediately. For `PresetCapRestartIDR` codecs it records the
pending preset in a `pendingPreset string` field on the context; the pipeline
layer notices the pending value at its next IDR-aligned drain point and
performs the close+reopen.

#### `pipeline/handlers_encoder.go`

Extend the per-frame loop's existing restart hook:

```go
// Pseudocode for the post-send check, generalised from the existing
// thread-count restart path:
if newCount, ok := s.perf.PopRestartRequest(); ok {
    if err := s.restartWithThreadCount(ctx, newCount); err != nil { return err }
}
if newPreset, ok := s.perf.PopPresetRequest(); ok {
    if err := s.applyPresetAtNextIDR(ctx, newPreset); err != nil { return err }
}
```

`applyPresetAtNextIDR` behaviour:

- For `PresetCapHotReconfig`: call `enc.RequestPresetChange(newPreset)` and
  return.
- For `PresetCapRestartIDR`: force the next outgoing frame to be IDR
  (`pic_in.i_type = X264_TYPE_IDR` / x265 equivalent), drain packets through
  that IDR, close, reopen with the new preset, increment
  `s.perf.IncrementPresetSwitches()`.

For ABR jobs the loop should aim to switch **all** renditions at the same input
PTS so the output GOPs stay aligned. The control loop therefore issues a single
"step every video encoder" command, not per-node commands. See §6a.5 below.

### Memory-buffer implications

The close+reopen path holds the encoder offline for one GOP worth of frames
(typically `g=48` at 24 fps = 2 s, but for the ABR job with `g=48` at 24 fps
this is ~2 s; for live 30 fps `g=60` it is 2 s). During this window:

1. The decoder and filter graph keep producing frames.
2. The encoder's *input* channel accumulates frames.
3. The encoder's *output* (packet) side is paused.

The current per-edge channel capacity (default 8 frames in `pipeline/engine.go`)
is far too small for a 2-second pause. Phase 6 therefore requires:

- **Configurable per-edge buffer size**, settable per node or globally. A new
  field `Pipeline.EncoderInputBufferFrames` (default 96 = 4 s @ 24 fps) sizes
  the channel feeding each encoder when `realtime: true`.
- **Backpressure-aware drain**: while the encoder is offline, `perfSend` from
  upstream must **not** block the source indefinitely; if the buffer fills,
  drop the *oldest* frame in the buffer (lossy ring) rather than blocking, and
  bump a `mediamolder_node_preset_switch_frames_dropped_total` counter. (This
  is symmetric with the existing frame-drop mode but applied at the input side
  during a switch.)
- **Pre-switch latency reporting**: before issuing the switch, the controller
  estimates the close+reopen wall-clock cost from `FrameLatencyP99 ×
  packets_pending_in_codec` and posts a `PresetSwitchPlanned` event so the GUI
  can render a "switching preset" indicator on each affected node.

For SVT-AV1 (`PresetCapHotReconfig`) none of this applies — no buffer growth is
needed because the encoder never stops.

### Adaptive control loop changes

Extend the §5b decision tree. The thread-step case is unchanged; the
"sequential bottleneck" case (which currently only emits an advisory) becomes
the trigger for a preset step.

```
3. Decide (revised):
   a. Find nodes where FPSDeficit > rtPresetDeficit (default 0.5 fps) for
      ≥ rtMinCooldownWindows consecutive snapshots.
   b. For each:
      if ActiveFrac > 0.9 AND ThreadsBusy ≈ ThreadsConfigured:
         → thread-limited; try ThreadBudget.Allocate(node, +rtThreadStep)
         → if budget exhausted, fall through to (c)
      elif ActiveFrac > 0.9 AND ThreadsBusy < 0.5 * ThreadsConfigured:
         → sequential bottleneck — go to (c) directly
      elif StalledFrac > 0.5:
         → downstream; skip.

   c. Preset-step phase (NEW):
      Aggregate: among all video encoder nodes, count how many are behind.
      If ≥ N/2 are behind (default: more than half), issue a single
      "step every video encoder one preset faster" command.
      Otherwise step only the individual encoder.
      Apply preset step on each affected node:
          newPreset, clamped = ladder.Step(currentPreset, +1)
          if clamped:
              emit RealTimeViolation{Reason: "preset floor reached"}
              fall through to frame-drop mode
          else:
              tracker.RequestPresetChange(newPreset)
              record event in PresetSwitchLog

   d. Overshoot detection (NEW):
      Track ema_deficit per node. If after a preset step the EMA stays at
      ≤ -rtPresetOvershoot (default -3 fps) for ≥ rtOvershootWindows
      (default 6 = ~3 s) consecutive snapshots, the previous preset was
      probably fine: step back one slower. This prevents the loop from
      collapsing to the fastest preset permanently when load was transient.

   e. Frame-drop only after both (a) and (c) cannot help.
```

New tunables (all expressed at the package level with sensible defaults):

```go
const (
    rtPresetDeficit       = 0.5  // fps; sustained deficit before stepping faster
    rtPresetCooldownWins  = 6    // ~3 s at 500 ms cadence between successive steps
    rtPresetOvershoot     = -3.0 // fps; sustained surplus before stepping slower
    rtOvershootWindows    = 6
    rtPresetGroupQuorum   = 0.5  // fraction of video encoders behind → group-step
)
```

The cool-down is critical: after a preset switch the next 1–2 windows have
artificially distorted latency (close+reopen overhead, codec re-priming),
so the EMA must not act on them.

### Observability — what the controller is doing and why

This phase introduces a first-class **decision log** rather than relying on
ad-hoc events.

#### `RealtimeDecisionLog`

A bounded ring (default 256 entries) of `RealtimeDecision` records, maintained
inside `realtimeController`:

```go
type RealtimeDecision struct {
    Time       time.Time
    Action     string          // "add_threads" | "step_faster" | "step_slower" | "drop_frames" | "noop_cooldown" | "noop_downstream"
    Nodes      []string        // affected node IDs
    Inputs     DecisionInputs  // snapshot fields used
    Outcome    string          // "applied" | "deferred" | "clamped" | "budget_exhausted"
    From, To   string          // "slower" → "slow" (preset transitions only)
    GraphFPS   float64         // current pipeline output frame rate
    GraphFPSTarget float64     // target
}

type DecisionInputs struct {
    FPSDeficit       float64
    ActiveFrac       float64
    StalledFrac      float64
    ThreadsBusy, ThreadsConfigured int
    FramesInFlight   int
}
```

The log is exposed through three surfaces:

1. **`MetricsSnapshot.RealtimeDecisions []RealtimeDecision`** — included in the
   `/perf` JSON. The GUI's per-node performance overlay grows a small "ƒ"
   badge that opens a decision panel when clicked.
2. **`mediamolder perf --decisions`** CLI flag — tails the log to stdout, one
   line per decision, columns: `time | node | action | from→to | deficit | reason`.
3. **Prometheus counters** — one counter per action type:
   `mediamolder_realtime_decisions_total{action="step_faster"}`, etc.

#### Graph-level FPS gauges

The current metrics expose per-node FPS only. Phase 6 adds two graph-level
gauges so the operator sees the *whole-pipeline* picture without picking a
single node:

| Metric | Type | Description |
|---|---|---|
| `mediamolder_pipeline_fps_target` | Gauge | The graph's wall-clock fps target (input source frame rate, or `--target-fps` override) |
| `mediamolder_pipeline_fps_actual` | Gauge | Min of every output sink's measured FPS over the last 1 s window |
| `mediamolder_pipeline_realtime_satisfied` | Gauge | (already exists in §5b) updated to consider preset floor and overshoot |
| `mediamolder_node_preset_switches_total` | Counter | Per-node preset change count, labelled by `from` / `to` |
| `mediamolder_node_preset_current` | Gauge (string-encoded) | Current preset as a numeric index 0=slowest |

In the GUI:

- The toolbar gains a small "Real-time" status pill showing
  `<actual> / <target> fps`, green if `realtime_satisfied = 1`, amber if
  preset floor is reached but no drops yet, red if drops are active.
- Hovering each encoder node in the canvas shows a tooltip with the live
  preset, last switch time, switch count, and "next switch eligible at" time
  (current time + cooldown remaining).
- A toggleable bottom panel ("Real-time Activity") tails the decision log.

### CLI / Core API / GUI control surface

#### CLI

```
mediamolder run --realtime \
  --preset-floor=medium       # never step faster than this preset
  --preset-ceiling=slower      # never step slower than this preset
  --preset-group=true          # step all video encoders together (default)
  --target-fps=24              # explicit graph fps target; overrides source rate
  pipeline.json
```

Sub-commands:

```
mediamolder perf --decisions   # tail decision log
mediamolder preset get <node>  # print current preset
mediamolder preset set <node> <name>   # one-shot override (control-loop pauses
                                       #   stepping for that node until cleared)
mediamolder preset clear <node>        # release the override
```

The `preset set/clear` commands talk to the running pipeline through the
existing metrics HTTP server (the only operator-control channel today; we add
`POST /realtime/preset` and `POST /realtime/preset/clear`).

#### Core API (Go)

```go
// pipeline package additions
type RealtimeOptions struct {
    Enabled       bool
    TargetFPS     float64       // 0 = derive from source
    PresetFloor   string        // codec-relative; "" = ladder fastest
    PresetCeiling string        // codec-relative; "" = configured starting preset
    Group         bool          // group-step all video encoders
}

// On a live Pipeline:
func (p *Pipeline) SetPresetOverride(nodeID, preset string) error
func (p *Pipeline) ClearPresetOverride(nodeID string) error
func (p *Pipeline) RealtimeDecisions(n int) []RealtimeDecision // most recent n
func (p *Pipeline) RealtimeStatus() RealtimeStatus              // graph fps, satisfied flag, per-node preset table
```

`PipelineOpts.Realtime` becomes `PipelineOpts.RealtimeOptions` (back-compat: a
bare `true` value is unmarshalled to `{Enabled: true}` so existing JSON files
keep working).

#### Schema

`schema/v1.2.json` and `schema/v1.3.json` (new): `global_options.realtime` is
either a bool (legacy) or an object matching `RealtimeOptions`. The migration
in `pipeline.Config.Normalize` converts the bool form.

#### GUI

Inspector panel for the graph (the empty-canvas selection) gains a **Real-time**
section:

- **Enable real-time** — same checkbox that already exists in the toolbar.
- **Target FPS** — number input; blank = derive from source.
- **Preset floor** — dropdown populated from the most-restrictive ladder among
  all selected encoders; greys out presets faster than the floor.
- **Preset ceiling** — dropdown, same logic on the slow side.
- **Group video encoders** — checkbox; default checked.

Each encoder node's Inspector gains:

- **Current preset** (read-only when real-time is enabled and the node is
  under control).
- **Manual override** — text input + Apply/Clear buttons that call
  `SetPresetOverride` / `ClearPresetOverride`.

### Phase 6 deliverables

1. `pipeline/preset_ladder.go` — ladder definitions, `Step`, normalisation.
2. `av/encode.go` extensions — `PresetCapability`, `CurrentPreset`,
   `RequestPresetChange`, SVT-AV1 hot-reconfig wrapper.
3. `pipeline/handlers_encoder.go` — `applyPresetAtNextIDR`, generalised restart
   path, force-IDR plumbing for x264/x265.
4. `pipeline/realtime_ctrl.go` — preset-step decision branch, overshoot
   detection, decision-log ring, group-step coordinator.
5. `pipeline/engine.go` — configurable encoder-input channel capacity,
   oldest-drop ring on overflow during a switch.
6. `observability/metrics.go` — new gauges, counters, and Prometheus
   registration.
7. `cmd/mediamolder/cmd_perf.go` — `--decisions` tail mode.
8. `cmd/mediamolder/cmd_preset.go` — `preset get/set/clear`.
9. HTTP endpoints on the existing `MetricsServer` — `/realtime/preset[/clear]`,
   `/realtime/decisions`.
10. Schema updates: `schema/v1.2.json`, `schema/v1.3.json`, `Normalize` migration.
11. Frontend: toolbar status pill, decision panel, per-node tooltip,
    Inspector real-time section.
12. Tests: ladder boundary clamping, group-step quorum, overshoot back-off,
    preset switch during ABR job (4× x264) keeps GOPs aligned, oldest-drop
    behaviour during a 2-s offline window, schema migration.

### Open questions specific to Phase 6

1. **Audio encoders and real-time control.** AAC at 128 kb/s is not CPU-bound.
   Phase 6 only acts on video encoder nodes; audio is filtered out of the
   decision tree by `node.Kind == graph.KindEncoder && streamType == video`.
2. **B-frame look-ahead and "next IDR".** With x264 `bframes=3 b_pyramid=normal`
   the look-ahead can hold 16+ frames. Forcing IDR at the next *input* PTS
   means the look-ahead must flush first; the actual switch lands ~16 frames
   later. The decision log records the input PTS at which the switch was
   requested *and* the PTS of the first frame under the new preset.
3. **Mismatched ladders across renditions.** ABR jobs may mix codecs (libx264
   + libsvtav1). Group-step is then defined by ladder-relative index, not
   ladder name. The coordinator computes a *minimum step size* in normalised
   units (1 step = 1 ladder position) so each codec moves one position even
   though the absolute names differ.
4. **Two-pass encodes.** Two-pass jobs cannot change preset mid-pass without
   invalidating the stats file. Real-time mode is implicitly single-pass; the
   schema validator should warn when `realtime: true` is combined with
   `pass: 2`.
5. **Quality regression visibility.** Stepping faster sacrifices bitrate
   efficiency. The decision log records BD-rate estimates (lookup table, not
   live PSNR) at switch time so the operator can audit quality cost.

---

## 6b. Phase 7 — Real-Time Output Buffering & Readiness Signal

### Motivation

Real-time delivery has two distinct goals:

1. **Steady-state**: each output frame is produced no slower than `1/fps`
   seconds. Phase 5 and Phase 6 address this.
2. **Jitter tolerance at the downstream consumer**: a downstream player or
   muxer that pulls from a MediaMolder output (file growth, named pipe, TCP/UDP
   listener, RTMP push) cannot tolerate even one missed deadline at the start
   of playback. A short pre-roll buffer between the encoder output and the
   consumer absorbs transient slowdowns without underrunning.

Phase 7 adds an explicit **output pre-roll buffer** to every output sink in
real-time mode and a **readiness signal** that the downstream system uses to
know when it is safe to start consuming.

### Design

#### Per-output pre-roll buffer

For every sink node, the pipeline interposes a ring of `AVPacket`-equivalents
between the encoder's drain and the sink's writer goroutine.

```go
// pipeline/output_buffer.go
type OutputPreroll struct {
    nodeID       string
    targetDur    time.Duration   // default 4 s
    targetBytes  int             // optional cap; default 0 = unlimited
    ring         *packetRing     // PTS-ordered ring; drops oldest on overflow
    fillLevel    atomic.Int64    // current buffered duration in ns
    ready        atomic.Bool     // true once fill ≥ targetDur (or stream end)
    onReady      func()          // closure that triggers the readiness signal
}
```

Two parameters drive the buffer, configurable per output or globally:

- `prebuffer_duration_seconds` (default `4.0`) — fill target before signalling
  ready.
- `prebuffer_max_seconds` (default `2 × prebuffer_duration_seconds`) — hard
  cap; older packets are evicted past this point to bound memory.

The duration is computed from packet PTS using the output stream's time base,
not wall-clock. This keeps the semantics correct for variable-bitrate or
variable-framerate streams.

#### State machine

```
[FILLING] ──(fill ≥ target)──> [READY] ──(downstream open)──> [STREAMING]
   │                              │                                │
   │                              └──(reset / EOF)──> [DRAINING] ──┘
   └──(EOF before target)──> [READY_PARTIAL] ──> [STREAMING]
```

Transitions:

- `FILLING → READY` when the buffered duration first reaches `targetDur`.
- `READY → STREAMING` when the downstream consumer attaches (a pull happens, a
  TCP client connects, etc.). Outputs without a back-channel transition
  automatically `~50 ms` after `READY`.
- `* → READY_PARTIAL` if input EOF arrives before the target — the buffer
  flushes whatever it has and signals ready immediately.

The pipeline-level real-time-ready signal is the conjunction:

```
graph.ready = all output sinks ∈ {READY, READY_PARTIAL, STREAMING}
```

#### Interaction with file outputs

For a file output the writer goroutine simply waits to write any data until
`graph.ready`; this guarantees the file starts to grow only when there is
≥ `prebuffer_duration_seconds` worth of encoded data already in hand. A
muxing format that writes a header before the first frame (MP4 `moov`,
Matroska header) still emits the header at writer start; only frame writes are
gated. The buffer therefore exists primarily for streaming sinks (RTMP, named
pipe, TCP/UDP, stdout); for file outputs it serves as a producer-side smoothing
queue.

#### Interaction with the AAC fan-out

ABR_BBB_AVI.json has a single AAC encoder fanned out to four MP4 muxers. The
pre-roll is per-**output**, not per-encoder: the same audio packet enters four
separate output rings (cheap; packets are reference-counted). Each output's
readiness gates only its own writer. `graph.ready` is `AND` across all four.

### Memory implications

At 4 s pre-roll and reference-counted AVPackets the memory cost is dominated by
video. For ABR_BBB_AVI.json @ 24 fps:

| Rendition | Bitrate | 4 s pre-roll |
|---|---|---|
| 1080p / 7 Mb/s | 0.875 MB/s | 3.5 MB |
| 720p / 4 Mb/s | 0.5 MB/s | 2.0 MB |
| 540p / 2 Mb/s | 0.25 MB/s | 1.0 MB |
| 360p / 1 Mb/s | 0.125 MB/s | 0.5 MB |
| AAC × 4 outputs | 4 × 16 kB/s ≈ 64 kB/s | 0.25 MB |
| **Total** |  | **~7.3 MB** |

This is negligible relative to the existing decoded-frame pool. The cap is
nevertheless enforced because pathological inputs (very high bitrate, very long
preset-switch outage) can balloon the ring; oldest-drop is the failure mode and
is reported through a counter.

> **No upstream encoder modifications required.** The buffer lives between
> encoder packet output and sink, on the MediaMolder side.

### Interaction with preset switching (Phase 6)

The pre-roll buffer is *exactly* the buffer that absorbs a Phase 6 preset
switch. When an encoder goes offline for a close+reopen, the pre-roll on the
*downstream* side keeps feeding the consumer; the *upstream* side (between
filter and encoder, see Phase 6 memory section) absorbs incoming frames. The
two buffers together set the maximum tolerable switch outage:

```
max_switch_outage ≈ encoder_input_buffer_duration + output_preroll_drain_margin
```

With defaults (4 s input, 4 s output) up to ~8 s of encoder offline time is
absorbed without a consumer stall — large enough for any reasonable preset
switch on contemporary hardware.

### Readiness signal surfaces

The graph-level readiness is exposed identically on every surface:

| Surface | Mechanism |
|---|---|
| Core API | `Pipeline.Ready() <-chan struct{}` — closes when the graph first becomes ready. `Pipeline.ReadyState() ReadyState` for sampled access. |
| Event bus | `RealTimeReady{When time.Time, PerOutput map[string]ReadyState}` event posted once on each transition into `READY`. |
| CLI | `mediamolder run --realtime` blocks stdin until ready (prints `ready` on its own line on stdout). `--ready-fd=<n>` writes a single byte to the given fd when ready, for shell pipelines. |
| HTTP | `GET /realtime/ready` on the metrics server: returns `200 {ready: true, since: ts, outputs: [...]}` when ready, `425 Too Early` otherwise. `GET /realtime/ready/stream` is an SSE that fires once per state change. |
| Prometheus | `mediamolder_pipeline_ready{}` gauge (0/1), `mediamolder_output_ready{node="..."}` per-sink. |
| GUI | Toolbar status pill (Phase 6) gains a fourth state: blue "buffering 1.8 / 4.0 s" during `FILLING`. When all outputs are ready it goes green and the existing "Ready" indicator on each output node lights up. |

### Configuration

#### JSON

```json
{
  "schema_version": "1.3",
  "global_options": {
    "realtime": {
      "enabled": true,
      "prebuffer_duration_seconds": 4.0,
      "prebuffer_max_seconds": 8.0
    }
  },
  "outputs": [
    {
      "id": "out_1080",
      "url": "/Volumes/SSD/out/1080p.mp4",
      "realtime": {
        "prebuffer_duration_seconds": 6.0
      }
    }
  ]
}
```

Per-output `realtime.prebuffer_*` overrides the global default.

#### CLI

```
mediamolder run --realtime \
  --prebuffer=4s            # global default
  --prebuffer-max=8s        # global cap
  --ready-fd=3              # write byte to fd 3 on ready
  pipeline.json
```

#### Core API

```go
type RealtimeOutputOptions struct {
    PrebufferDuration time.Duration
    PrebufferMax      time.Duration
}

// pipeline.Output gains:
type Output struct {
    ...
    Realtime *RealtimeOutputOptions
}

func (p *Pipeline) Ready() <-chan struct{}
func (p *Pipeline) ReadyState() ReadyState
type ReadyState struct {
    Ready    bool
    Since    time.Time
    Outputs  map[string]OutputReadyState
}
type OutputReadyState struct {
    State        string        // FILLING | READY | READY_PARTIAL | STREAMING | DRAINING
    BufferedDur  time.Duration
    TargetDur    time.Duration
    DroppedCount int64         // packets evicted from this output's ring
}
```

#### GUI

- Toolbar pill (see above).
- Per-output node Inspector adds **Prebuffer (seconds)** and **Max (seconds)**
  inputs, default placeholders showing the inherited global value.
- A small horizontal fill bar inside each output node on the canvas shows
  current `BufferedDur / TargetDur`. The bar disappears once the pipeline is
  `STREAMING` and reappears after a reset.

### Observability for Phase 7

| Metric | Type | Labels | Description |
|---|---|---|---|
| `mediamolder_output_buffer_duration_seconds` | Gauge | `node` | Currently buffered duration per output |
| `mediamolder_output_buffer_target_seconds` | Gauge | `node` | Configured target |
| `mediamolder_output_buffer_state` | Gauge | `node`, `state` | 1 for the active state, 0 otherwise |
| `mediamolder_output_buffer_evictions_total` | Counter | `node`, `reason` | Packets dropped from the ring; `reason ∈ {cap, reset}` |
| `mediamolder_pipeline_ready_seconds` | Histogram | — | Wall-clock seconds between pipeline start and first `READY` |
| `mediamolder_pipeline_ready` | Gauge | — | 0/1; AND of all output readiness |

### Phase 7 deliverables

1. `pipeline/output_buffer.go` — `OutputPreroll`, ring of refcounted
   `AVPacket`-equivalents, PTS-based duration accounting.
2. `pipeline/handlers_sink.go` — sink writer gated on `OutputPreroll.Ready()`;
   per-sink state machine; integration with mux header emission.
3. `pipeline/engine.go` — `Pipeline.Ready()`, `ReadyState()`,
   `RealTimeReady` event, AND-aggregation across outputs.
4. `pipeline.Config` / `pipeline.Output` schema additions; schema v1.3 JSON
   and `Normalize` migration.
5. `cmd/mediamolder/main.go` — `--prebuffer`, `--prebuffer-max`, `--ready-fd`
   flags; stdout `ready\n` line on run.
6. `observability/metrics.go` + `MetricsEmitter` wiring for the new gauges.
7. HTTP: `/realtime/ready` and `/realtime/ready/stream` on `MetricsServer`.
8. Frontend: toolbar pill state, per-output fill bar, Inspector controls,
   ready event surfaced in event log panel.
9. Tests:
   - Buffer fills to target then transitions to `READY` exactly once.
   - EOF before target → `READY_PARTIAL` and full drain.
   - Oldest-drop on max-cap overflow; eviction counter increments.
   - Preset switch (Phase 6 integration): downstream sees no underrun for a
     simulated 3 s encoder outage with 4 s pre-roll.
   - `--ready-fd=3` writes exactly one byte on ready.
10. Documentation: `docs/realtime-output.md` describing the readiness contract
    for downstream consumers; reference from `README.md`.

### Open questions specific to Phase 7

1. **Live mux containers (HLS, DASH, fMP4 chunked).** These muxers segment on
   the *output* side; the pre-roll naturally produces complete segments
   before the first chunk is written. The fMP4 init segment (`ftyp`+`moov`)
   should still be emitted at writer start so downstream readers can probe
   the stream without waiting; gate only `moof` writes on readiness.
2. **UDP / RTP outputs.** UDP has no flow control. The `STREAMING` transition
   for UDP is synthetic (50 ms timer after `READY`), and the pre-roll exists
   purely to smooth producer jitter. RTP sender-side jitter compensation is
   out of scope.
3. **Audio-only outputs.** A 4 s pre-roll of 128 kb/s AAC is 64 kB — fine —
   but `prebuffer_duration_seconds` may be too long for low-latency voice
   use cases. The default for audio-only outputs is 1 s (overridable).
4. **Reset semantics on seek.** If a future "live seek" capability lands, the
   buffer must transition `STREAMING → DRAINING → FILLING` and re-issue a
   single `RealTimeReady` event on completion. This is currently
   forward-looking; for Phase 7 a seek aborts and restarts the pipeline.
5. **Coordinated multi-output readiness.** All four MP4 outputs of an ABR job
   should become ready simultaneously, not staggered. Because the encoder
   topologies differ (different bitrates → different packet rates), one
   output may fill its ring before another. The aggregator therefore reports
   `graph.ready` only when *all* outputs are ready; the slowest output
   defines the start time. A `--ready-mode=any` CLI flag is reserved for
   future use but not implemented in Phase 7.

---

## 7. Worked Example: Identifying a Bottleneck

Suppose a pipeline is `source → scale_filter → libx265_encoder → muxer_sink` running at 18 fps instead of the expected 30 fps. The performance snapshot shows:

| Node | FPS | Target | Deficit | Active % | Idle % | Stalled % | Threads | Busy | Est. cores |
|------|-----|--------|---------|---------|--------|-----------|---------|------|------------|
| `in0` (source) | 30.1 | 30 | −0.1 | 12% | 0% | 88% | n/a | — | — |
| `scale0` (filter) | 30.0 | 30 | 0 | 8% | 10% | 82% | auto (4) | 2 | ~0.3 |
| `enc0` (libx265) | 18.3 | 30 | **+11.7** | 97% | 3% | 0% | 8 | 8 | ~7.8 |
| `out0` (sink) | 18.3 | 30 | +11.7 | 5% | 95% | 0% | n/a | — | — |

Reading the table:
- `enc0` has `FPSDeficit = 11.7`, `ActiveFrac = 0.97`, `ThreadsBusy = 8 ≈ ThreadsConfigured` — thread-limited bottleneck.
- `in0`/`scale0` spend 82–88% stalled: downstream is the constraint, not them.
- `out0` is 95% idle: the muxer is waiting; it is not the problem.

**Control loop decision**: `enc0` is thread-limited with no spare capacity → increment `ThreadsRequested` from 8 to 10 (within budget) → trigger graceful restart of `enc0` → re-observe after 1 s.

**Conclusion if already at thread ceiling**: reduce x265 preset from `slow` → `medium`, or switch to hardware encoding.

---

## 8. Open Questions

1. **Histogram bucket resolution for stall durations**: 1 ms–500 ms is the expected range for media pipelines. Suggest buckets: `{0.001, 0.005, 0.010, 0.025, 0.050, 0.100, 0.250, 0.500}` seconds.

2. **Per-edge vs per-node stall tracking**: The current design tracks stalls per-node (on the sending side). An alternative is to track per-edge (per channel), which would attribute stalls to the specific downstream consumer when a source fans out. This adds complexity (the tracker would need a channel ID) but provides finer data. Leave for Phase 2 extension.

3. **Filter source nodes** (`type: "filter_source"`): These nodes have no `ins[]` channel — they are driven by `handleFilterSource`. The PROCESSING vs STALLED breakdown still applies; IDLE is not relevant. Current plan: handle in Phase 2 alongside the main handlers.

4. **Re-encode after seek**: After a seek, the pipeline flushes buffers; the time between seek completion and first new frame output will appear as a long IDLE burst. This may inflate `IdleFrac` for short clips. Consider resetting the tracker accumulators on seek.

5. **`execute2` callback and thread count cap**: `execute2` is invoked with `count` tasks at a time; the maximum `count` equals the number of parallel codec tasks for that call, not the sustained thread count over a window. A snapshot taken between dispatches will see `ThreadsBusy = 0` even though the codec is active. Mitigation: track a high-water-mark per observation window (`ThreadsBusyMax`) alongside the instantaneous value.

6. **x264 thread-count modification in the workspace repo**: The x264 source in `/Users/tom.vaughan/x264` is the version MediaMolder builds against. Adding `x264_encoder_get_thread_busy_count()` requires understanding the thread model (frame-parallel vs slice-parallel) at build time, since the two models have different internal structures. Confirm which mode is used in production builds before implementing.

7. **Graceful node restart safety**: The restart sequence (drain → close → reopen) pauses the node mid-pipeline. The upstream channel will accumulate frames during the restart. Channel capacity (default 8 frames) must be sufficient for the restart duration, or a larger buffer should be configured for nodes eligible for restart. This needs to be validated for nodes with long frame pipelines (B-frame lookahead can hold 16+ frames).

8. **Thread budget accounting for hardware encoders**: Nodes using NVENC, VideoToolbox, AMF, etc. consume GPU resources, not CPU threads. The `ThreadBudget` manager is CPU-only; hardware-accelerated nodes should be exempt from the CPU thread cap and tracked separately (GPU utilisation requires platform-specific APIs not covered here).

9. **Control loop convergence and oscillation**: Increasing thread count by 2, waiting 500 ms, and re-evaluating creates a potential oscillation if the thread scheduler hasn't stabilised. A cool-down period (minimum 3 observation windows with stable deficit before the next adjustment) should be enforced.
