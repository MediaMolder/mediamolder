# Real-time output buffering (§6b Phase 7)

When MediaMolder is run with `--realtime`, each output sink is fronted by
a **pre-roll buffer** that holds encoded packets *before* the muxer is
allowed to write its first byte. This page documents the state machine,
configuration knobs, observability surfaces, and tuning guidance for the
Phase 7 implementation that lives in
[pipeline/output_buffer.go](../pipeline/output_buffer.go).

## Why pre-roll the muxer?

Downstream jitter — TCP receive-window stalls, HLS segment-publish
hiccups, SRT recovery bursts, or a Phase 6 encoder preset close+reopen —
can take several seconds to clear. Without a buffer those seconds turn
into a muxer underrun: the writer blocks, upstream filters stall, and
the live timeline drifts behind walltime.

By holding ~4 s of packets in front of the muxer, MediaMolder can
*continue* to write at native pace through a transient outage. Combined
with Phase 6's encoder-input buffer (default 96 frames ≈ 4 s @ 24 fps),
the graph absorbs roughly **8 s** of total downstream jitter before
upstream backpressure manifests.

## State machine

Each `OutputPreroll` advances through:

```
FILLING ──(buffered ≥ target)──► READY ────► STREAMING ────► DRAINING
   │                                ▲
   └──(upstream EOS before target)──┴── READY_PARTIAL
```

- **FILLING** — accumulate packets, PTS-based duration accounting,
  evict oldest when `max_seconds` exceeded.
- **READY** — fill target reached. The per-output readiness channel is
  closed; the graph-level `Pipeline.Ready()` aggregator AND-combines all
  outputs.
- **READY_PARTIAL** — upstream EOSed before the target was met (short
  clips, etc.). Treated as ready for aggregation.
- **STREAMING** — once *every* output is ≥ READY, the drainer empties
  each buffer through the muxer's normal write path and switches to
  pass-through.
- **DRAINING** — graph shutting down; remaining buffered packets are
  written or discarded depending on the close path.

## Configuration

### Job JSON

```jsonc
{
  "global_options": {
    "realtime": true,
    "prebuffer_duration_seconds": 4.0,   // default 4.0 (video) / 1.0 (audio-only)
    "prebuffer_max_seconds":       8.0   // default 2 × prebuffer_duration_seconds
  },
  "outputs": [
    {
      "id": "live_hls",
      "realtime": {                       // optional per-output override
        "prebuffer_duration_seconds": 6.0,
        "prebuffer_max_seconds":       12.0
      }
    }
  ]
}
```

A `prebuffer_duration_seconds` of `0` disables pre-roll for that output
entirely. Schema definitions live in
[schema/v1.0.json](../schema/v1.0.json) and
[schema/v1.1.json](../schema/v1.1.json) and are enforced by
`TestSchemaSyncWithGoStructs`.

### CLI

```sh
mediamolder run --realtime \
    --prebuffer=4s \
    --prebuffer-max=8s \
    --ready-fd=3 \
    job.json
```

`--ready-fd=<n>` writes a single `0x01` byte to file descriptor `n` once
the graph is ready (Phase-7-friendly equivalent of `systemd-notify --ready`).
With `--realtime` the literal string `ready\n` is also printed to
stdout on the same trigger.

## Observability

| Surface                                          | What it reports                              |
| ------------------------------------------------ | -------------------------------------------- |
| `Pipeline.Ready() <-chan struct{}`               | Closed once all outputs ≥ READY              |
| `Pipeline.ReadyState() pipeline.ReadyState`      | Per-output state + first-ready timestamp     |
| `RealTimeReady` event on the event bus           | Fires once when the graph becomes ready      |
| `GET /realtime/ready`                            | JSON; HTTP 425 *Too Early* until ready       |
| `GET /realtime/ready/stream`                     | SSE; one event per state change              |
| `mediamolder_output_buffer_duration_seconds`     | Gauge labelled by node                       |
| `mediamolder_output_buffer_target_seconds`       | Gauge labelled by node                       |
| `mediamolder_output_buffer_state{node,state=…}`  | 1/0 per state                                |
| `mediamolder_output_buffer_evictions_total`      | Counter, `reason="overflow"`                 |
| `mediamolder_pipeline_ready`                     | Gauge, 1 once all outputs ready              |
| `MetricsSnapshot.Realtime.Outputs[]`             | Per-output snapshot for GUI consumers        |

## Tuning guidance

- **Live restream (HLS/RTMP/SRT).** Defaults (4 s / 8 s cap) are
  appropriate for most segment durations. Bump `prebuffer_duration_seconds`
  toward your HLS segment length if you observe segment-publish
  hiccups.
- **Audio-only.** The default automatically drops to 1 s; raise it only
  when paired with a downstream that batches large windows.
- **VOD-style file outputs.** You generally don't need pre-roll. Either
  omit `--realtime` or set `prebuffer_duration_seconds: 0` on the
  specific output.
- **Memory accounting.** The cap is in *PTS-time*, not bytes, so a high
  bitrate stream uses correspondingly more memory at the same cap.
  `prebuffer_max_seconds` defaults to `2 × prebuffer_duration_seconds`;
  shrink it if your encoder bitrate is very high.
- **Eviction warning.** A non-zero
  `mediamolder_output_buffer_evictions_total` indicates the upstream
  producer is sustained-faster than the downstream consumer — diagnose
  the producer, don't just raise the cap.

## See also

- [docs/architecture/node_perf_monitoring_design.md](architecture/node_perf_monitoring_design.md)
  — §6b Phase 7 design anchors
- [pipeline/output_buffer.go](../pipeline/output_buffer.go) — implementation
- [pipeline/output_buffer_test.go](../pipeline/output_buffer_test.go) — regression tests
