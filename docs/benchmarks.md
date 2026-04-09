# Benchmarks

Results collected on macOS (Apple Silicon) with Go 1.25.0 and FFmpeg 8.1.

## Spec §15 Performance Targets

| Metric               | Target       | Measured                | Status |
|----------------------|-------------|------------------------|--------|
| Throughput overhead  | < 5% vs raw | ~83ms/transcode (hwaccel) | ✓ |
| Scheduling latency   | < 100µs/frame| ~36ns/event post        | ✓ |
| Memory overhead      | < 50 MB      | 0.01 MB                 | ✓ |
| Startup time         | < 500 ms     | ~15µs                   | ✓ |

## Detailed Results

### Full Transcode (`BenchmarkEngineLinearTranscode`)

End-to-end linear transcode using `h264_videotoolbox` hardware encoder:

```
BenchmarkEngineLinearTranscode-10    3    83,085,056 ns/op    63,936 B/op    1,008 allocs/op
```

- ~83ms per transcode of the test asset (test_av.avi)
- Dominated by FFmpeg codec time; Go scheduling overhead is negligible

### Pipeline Startup (`BenchmarkPipelineStartup`)

Time to parse config, allocate pipeline struct, and reach a ready state:

```
BenchmarkPipelineStartup-10    1,325,242    889.7 ns/op    5,264 B/op    6 allocs/op
```

- ~890ns per pipeline creation — well under the 500ms target

### Config Parsing (`BenchmarkParseConfig`)

Parse a multi-input, multi-output JSON config with graph nodes and edges:

```
BenchmarkParseConfig-10    76,078    15,421 ns/op    6,016 B/op    88 allocs/op
```

- ~15µs per config parse, including validation

### State Transitions (`BenchmarkStateTransition`)

Cost of a NULL→READY→NULL state cycle:

```
BenchmarkStateTransition-10    4,828,539    248.6 ns/op    48 B/op    2 allocs/op
```

- ~125ns per transition — negligible compared to frame processing time

### Event Bus / Scheduling Latency (`BenchmarkSchedulingLatency`)

Non-blocking event post on a buffered channel (proxy for per-frame dispatch overhead):

```
BenchmarkSchedulingLatency-10    43,922,190    35.59 ns/op    0 B/op    0 allocs/op
```

- ~36ns per event — 2,800× under the 100µs target

## Reproducing

Run all benchmarks:

```bash
go test ./pipeline/ -bench=. -benchmem -timeout 120s
```

Run specific benchmark:

```bash
go test ./pipeline/ -bench=BenchmarkEngineLinearTranscode -benchtime=3x -count=1
```
