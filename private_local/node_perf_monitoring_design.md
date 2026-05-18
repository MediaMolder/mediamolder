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

### Phase 5 — Adaptive control loop (real-time mode)
20. Implement `ThreadBudget` manager in `pipeline/`.
21. Implement the adaptive control loop goroutine, activated by `PipelineOpts.RealTime = true`.
22. Implement graceful node restart (drain → close → reopen with new thread count → resume) for encoder and filter nodes.
23. Implement frame-drop mode in `perfSend` (last resort, logged as warning).
24. Integration test: pipeline running at 0.5× CPU speed with real-time mode enabled converges to target FPS by increasing thread allocation; `mediamolder_pipeline_realtime_satisfied` gauge reaches 1 within the convergence window.

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
