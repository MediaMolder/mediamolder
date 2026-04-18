# Graph Compilation

This document explains the **graph compilation** phase — the analysis step that
sits between building a validated graph and actually running it. 

---

## What Problem Does This Solve?

When you write a MediaMolder JSON config, you're describing a **processing
graph**: media flows from inputs through filters and encoders to outputs.
Before your media starts flowing, MediaMolder needs to do three things:

1. **Build** — Read your config and make sure the graph makes sense (no typos
   in node names, no impossible loops, etc.). 
2. **Compile** *(new)* — Analyze the valid graph to catch potential mistakes
   and prepare information that helps the runtime (buffer sizes, stage layout).
3. **Execute** — Actually run the pipeline, flowing media through the graph.

The compile step is like a spell-checker for your graph. The graph might be
technically valid (all the connections exist, no loops), but it could still have
issues: maybe you connected a filter that doesn't lead anywhere, or you
declared an input you never used. The compiler catches these issues and warns
you *before* your pipeline starts processing, saving you from wasting time on
a misconfigured job.

---

## Where It Fits in the Pipeline

```
JSON config
    │
    ▼
ParseConfig()          Parse and validate the JSON
    │
    ▼
graph.Build()          Validate structure, detect cycles, topological sort
    │
    ▼
graph.Compile()   ◄── NEW: analyze graph, group stages, detect issues, size buffers
    │
    ▼
Open AV resources      Open files, create decoders/encoders/filters
    │
    ▼
Scheduler.Run()        Execute: one goroutine per node, channels per edge
```

The compile step **never modifies** the graph. It only reads the graph and
produces an `ExecutionPlan` with analysis results and buffer size
recommendations. If the graph is valid, the pipeline always proceeds —
compile warnings are informational, not blocking.

---

## What the Compiler Does

### Pass 1: Stage Grouping

The compiler figures out which processing steps can happen at
the same time and which steps have to wait for others.

Each node is assigned a *topological depth*:

- **Depth 0:** Nodes with no inputs (sources). These can start immediately.
- **Depth N:** Nodes whose deepest input is at depth N−1. They must wait for
  their inputs to produce data before they can start.

Nodes at the same depth form a **stage**. Within a stage, nodes are independent
— they don't depend on each other and can run concurrently.

**Example:**

```
Config:
  bg ──→ overlay ──→ split ──→ scale_hd ──→ hd
  fg ──┘                └────→ scale_sd ──→ sd

Stages:
  Stage 0: [bg, fg]              ← both sources, no dependencies
  Stage 1: [overlay]             ← waits for bg and fg
  Stage 2: [split]               ← waits for overlay
  Stage 3: [scale_hd, scale_sd]  ← both wait for split, independent of each other
  Stage 4: [hd, sd]              ← both wait for their respective scale nodes
```

Currently, MediaMolder already runs one goroutine per node, so every node is
inherently concurrent. The stage information is useful for:

- **Debugging:** Understanding which nodes are at the same "level" helps
  reason about where bottlenecks might occur.
- **Visualization:** Tools can display the graph as a layered diagram.
- **Future schedulers:** A batch scheduler could process stage-by-stage instead
  of launching all goroutines at once.

### Pass 2: Dead-Branch Detection

The compiler checks if every processing step actually
contributes to an output. If you have a filter connected to a source but not to
any output, its work will be thrown away. The compiler warns you about this.

The compiler walks *backward* from all sink nodes
(outputs), following edges in reverse. Any node that isn't reached during this
walk is a **dead branch** — it has no path to any output.

**Example:**

```
Config:
  src ──→ scale ──→ out
   └────→ orphan              ← connected to src but not to any output

Warning: node "orphan" is not connected to any output and its results will be discarded
```

This often indicates a config mistake — you probably meant to connect that
filter to an output. Dead nodes still run (the scheduler doesn't know they're
dead), so this warning also helps you avoid wasting CPU time.

### Pass 3: Disconnected-Source Detection

If you declare an input but never connect it to anything,
the compiler tells you. This usually means you forgot an edge in your config.

Any source node (created from your `inputs` array) with
zero outbound edges triggers a warning.

**Example:**

```
Config:
  inputs: [src, unused]
  src ──→ out

Warning: source "unused" has no outbound edges and will not contribute to any output
```

### Pass 4: Buffer Size Hints

The compiler assigns per-edge channel buffer sizes based on the kinds of the
upstream and downstream nodes. Different node kinds have different processing
characteristics, so a single fixed buffer size is suboptimal.

| Upstream Kind | Downstream Kind | Buffer Size | Rationale |
|--------------|----------------|-------------|-----------|
| Source | any | 16 | Demuxers produce bursts (B-frame reordering) |
| any | Encoder | 16 | Encoders are typically the slowest stage |
| Encoder | Sink | 4 | Packets are small, muxer I/O is fast |
| any | any (default) | 8 | Steady flow between similar-speed nodes |

The hints are stored in `ExecutionPlan.EdgeBufSizes` and used by the scheduler
when creating inter-node channels. This replaces the previous uniform buffer
size (8 for all edges).

---

## The ExecutionPlan

The `Compile()` function returns an `ExecutionPlan` struct:

```go
type ExecutionPlan struct {
    Graph        *Graph         // the validated graph this plan was compiled from
    Stages       []Stage        // nodes grouped by topological depth
    Warnings     []Warning      // non-fatal issues detected during compilation
    EdgeBufSizes map[*Edge]int  // per-edge channel buffer size recommendations
}

type Stage struct {
    Depth int       // zero-based topological depth
    Nodes []*Node   // nodes at this depth, sorted alphabetically
}

type Warning struct {
    NodeID  string       // which node the warning is about
    Code    WarningCode  // machine-readable category (e.g., "dead_node")
    Message string       // human-readable description
}
```

### Warning Codes

| Code | Meaning |
|------|---------|
| `dead_node` | Node output is never consumed by any sink. It runs but its results are discarded. |
| `disconnected_source` | Source node has no outbound edges. It will be started but produce no useful output. |

---

## How It Works in Practice

When your pipeline starts, compilation happens automatically. You don't need to
call `Compile()` yourself — the pipeline engine calls it internally. If there
are warnings, they're emitted as pipeline events (visible in your event
listener or logs):

```
graph compilation warning [dead_node]: node "orphan" is not connected to any output and its results will be discarded
```

The pipeline **does not stop** on compilation warnings. They are informational.
The only way compilation can fail is if you pass it a nil or empty graph (which
would mean `Build()` already failed earlier).

---

## Future Compilation Passes

The compilation framework is designed to be extended. Planned future passes:

### Format Propagation (planned)

Annotate edges with resolved pixel formats, sample rates, and channel layouts
based on upstream node configuration. This would catch format mismatches
*before* the pipeline starts — for example, feeding a YUV420P stream into a
filter that only accepts RGB24.

### Node Fusion (planned)

Identify sequences of simple filters that can be merged into a single FFmpeg
filtergraph string. For example, `scale` → `format` → `setpts` could become a
single complex filter node, reducing the overhead of inter-node channel
communication.

### Resource Estimation (planned)

Estimate memory and CPU requirements per stage based on node types and
parameters. This could be used for admission control (rejecting pipelines that
would exceed available resources) or for scheduling decisions in multi-pipeline
environments.

### Adaptive Buffer Feedback Loop (planned)

Phase B of adaptive buffer sizing: after a pipeline run, write edge stats
(peak fill, stall count) to a local cache file keyed by a graph topology hash.
On the next run of the same graph, the compiler reads the cache and adjusts
buffer sizes — halving buffers with `PeakFill < 0.1` and doubling buffers with
`PeakFill > 0.8` or stalls. This closes the feedback loop so buffer sizes
converge over multiple runs.

---

## Code Layout

| File | Purpose |
|------|---------|
| [graph/plan.go](../graph/plan.go) | Type definitions: `ExecutionPlan`, `Stage`, `Warning`, `WarningCode`, `EdgeBufSizes` |
| [graph/compile.go](../graph/compile.go) | `Compile()` function and all analysis passes (stages, dead branches, disconnected sources, buffer hints) |
| [graph/compile_test.go](../graph/compile_test.go) | Tests for stage grouping, dead branches, disconnected sources, buffer hints, determinism |
| [pipeline/engine.go](../pipeline/engine.go) | Integration point: `Compile()` is called in `runGraph()` between `Build()` and resource allocation |
| [runtime/edge_stats.go](../runtime/edge_stats.go) | `EdgeStatsRegistry` and backpressure sampler (wired in scheduler and engine) |
| [runtime/scheduler.go](../runtime/scheduler.go) | `Scheduler.Run()` creates per-edge channels using `EdgeBufSizes` from compilation |
| [pipeline/metrics.go](../pipeline/metrics.go) | `NodeMetrics.RecordLatency()` and latency fields in `NodeMetricsSnapshot` |
