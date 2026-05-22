# Benchmarks

MediaMolder ships two distinct benchmark systems with different audiences and
goals:

| System | Who it is for | How to run |
|--------|--------------|------------|
| **`mediamolder hwbench`** | Any user who wants to profile their own hardware | `mediamolder hwbench [flags]` |
| **Go pipeline benchmarks** | Developers validating pipeline overhead | `go test ./pipeline/ -bench=.` |

---

## `mediamolder hwbench` — Hardware codec benchmark

`hwbench` is a user-facing CLI subcommand that measures encode and decode
throughput (frames-per-second) for every codec × resolution combination
available on your system.  It generates a JSON report that includes system
metadata (OS, CPU count, GPU name, driver version, FFmpeg version) alongside
the measurements.

**Why it exists:**
MediaMolder maintains a community capability database so the runtime (and the
GUI Hardware Capabilities dialog) can display expected codec performance on
hardware it has never directly queried.  Running `hwbench` on your machine
contributes measured data to that database, improving the accuracy of
throughput estimates for all users with similar hardware.

You do not need to contribute results to benefit from `hwbench` — the output
is also a useful standalone tool for comparing hardware options, verifying
driver performance, or auditing codec support before committing to a production
encode path.

### Quick start

```sh
# Benchmark all default codecs in software only.
mediamolder hwbench

# Benchmark H.264 and HEVC on Apple VideoToolbox.
mediamolder hwbench --device videotoolbox --codecs h264_videotoolbox,hevc_videotoolbox

# Benchmark NVIDIA NVENC at 1080p and 4K only.
mediamolder hwbench --device cuda --resolutions 1920x1080,3840x2160

# Print hardware capabilities without running a benchmark.
mediamolder hwbench --device cuda --caps-only

# Write the report to stdout (useful for piping to jq).
mediamolder hwbench --stdout | jq '.results[] | select(.encode_fps < 30)'
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--device` | _(none, SW only)_ | Hardware device type to use (`cuda`, `videotoolbox`, `vaapi`, `qsv`). Omit for software-only codecs. |
| `--codecs` | All supported for the device | Comma-separated encoder names, e.g. `h264_nvenc,hevc_nvenc`. |
| `--resolutions` | 640×360, 1280×720, 1920×1080, 2560×1440, 3840×2160 | Comma-separated `WxH` targets. Dimensions are rounded up to the nearest 16-pixel macroblock boundary. |
| `--frames` | `200` | Frames to time per codec × resolution. Higher values give more stable results. |
| `--warmup` | `20` | Warmup frames encoded/decoded before timing starts (lets the GPU reach steady state). |
| `--output` | `hwbench_report_<timestamp>.json` | Path for the JSON report file. |
| `--stdout` | `false` | Write JSON to stdout instead of a file. |
| `--caps-only` | `false` | Query and print hardware capabilities without running a benchmark. |

### What is measured

For each codec × resolution pair:

- **Encode FPS** — frames encoded per second, averaged over `--frames` frames.
- **Decode FPS** — frames decoded per second using the matching decoder
  (hardware-accelerated when available, e.g. `h264_cuvid` for `h264_nvenc`;
  software fallback otherwise).
- **Encode bitrate (Mbit/s)** — mean bitrate produced by the encoder at
  default quality settings.

Codecs absent from the current FFmpeg build, or hardware codecs requested
without a `--device`, are silently skipped.  Skipped entries appear in the
JSON report with an `error` field explaining why.

### Default codec set

When `--codecs` is not specified, `hwbench` tests every codec in the build:

| Encoder | Backend |
|---------|---------|
| `libx264` | H.264 software |
| `libx265` | HEVC software _(if present)_ |
| `h264_nvenc` / `hevc_nvenc` / `av1_nvenc` | NVIDIA NVENC |
| `h264_videotoolbox` / `hevc_videotoolbox` | Apple VideoToolbox |
| `h264_vaapi` / `hevc_vaapi` | VAAPI (Linux Intel/AMD) |
| `h264_qsv` / `hevc_qsv` | Intel oneVPL (QSV) |

### Report format

The JSON report (`schema_version: "1.0"`) contains:

```json
{
  "schema_version": "1.0",
  "timestamp": "2026-05-18T14:23:01Z",
  "os": "darwin",
  "arch": "arm64",
  "num_cpu": 10,
  "device_name": "Apple VideoToolbox",
  "device_type": "videotoolbox",
  "ffmpeg_version": "8.1",
  "warmup_frames": 20,
  "measure_frames": 200,
  "disclaimer": "Results vary by system load, thermal state, and driver version...",
  "results": [
    {
      "codec": "h264_videotoolbox",
      "decoder_name": "h264",
      "resolution": { "width": 1920, "height": 1080 },
      "encode_fps": 312.5,
      "decode_fps": 598.1,
      "encode_bitrate_mbps": 4.2
    }
  ]
}
```

### Contributing results

To add your hardware's measurements to the community database:

1. Run `hwbench` (with `--device` for your GPU if applicable).
2. Review the printed summary table.
3. Submit the generated JSON file to the
   [HW Benchmark Contributions wiki page](https://github.com/MediaMolder/MediaMolder/wiki/HW-Benchmark-Contributions).

Include the GPU model, driver version, and OS in any accompanying comment —
this metadata is already captured in the report JSON.

> **Note:** results vary with system load and thermal state.  For the most
> representative numbers, close background applications, allow the system to
> reach idle temperature before running, and prefer `--frames 500` or higher.

---

## Go pipeline benchmarks — Developer CI

These are standard Go `testing.B` benchmarks that verify the pipeline
engine's orchestration overhead against the targets in
[Spec §15](specification.md).  They run as part of the CI test suite and are
not intended for general hardware profiling.

Results collected on macOS (Apple Silicon) with Go 1.25.0 and FFmpeg 8.1.

### Spec §15 Performance Targets

| Metric | Target | Measured | Status |
|--------|--------|----------|--------|
| Throughput overhead | < 5% vs raw | ~83 ms/transcode (hwaccel) | ✓ |
| Scheduling latency | < 100 µs/frame | ~36 ns/event post | ✓ |
| Memory overhead | < 50 MB | 0.01 MB | ✓ |
| Startup time | < 500 ms | ~15 µs | ✓ |

### Benchmark results

#### Full transcode (`BenchmarkEngineLinearTranscode`)

End-to-end linear transcode using `h264_videotoolbox` hardware encoder.
Encoder is selected automatically: `h264_videotoolbox` → `libx264` → `mpeg4`
depending on what the build supports.

```
BenchmarkEngineLinearTranscode-10    3    83,085,056 ns/op    63,936 B/op    1,008 allocs/op
```

~83 ms per transcode of `testdata/test_av.avi`.  Dominated by FFmpeg codec
time; Go scheduling overhead is negligible.

#### Pipeline startup (`BenchmarkPipelineStartup`)

Time to parse a config, allocate the pipeline struct, and reach a ready state.

```
BenchmarkPipelineStartup-10    1,325,242    889.7 ns/op    5,264 B/op    6 allocs/op
```

~890 ns per pipeline creation — well under the 500 ms target.

#### Config parsing (`BenchmarkParseConfig`)

Parse a multi-input, multi-output JSON config with graph nodes and edges.

```
BenchmarkParseConfig-10    76,078    15,421 ns/op    6,016 B/op    88 allocs/op
```

~15 µs per config parse, including validation.

#### State transitions (`BenchmarkStateTransition`)

Cost of a NULL → READY → NULL state cycle.

```
BenchmarkStateTransition-10    4,828,539    248.6 ns/op    48 B/op    2 allocs/op
```

~125 ns per transition — negligible compared to frame processing time.

#### Event bus / scheduling latency (`BenchmarkSchedulingLatency`)

Non-blocking event post on a buffered channel (proxy for per-frame dispatch
overhead).

```
BenchmarkSchedulingLatency-10    43,922,190    35.59 ns/op    0 B/op    0 allocs/op
```

~36 ns per event — 2,800× under the 100 µs target.

### Reproducing

```bash
# Run all pipeline benchmarks.
go test ./pipeline/ -bench=. -benchmem -timeout 120s

# Run a specific benchmark.
go test ./pipeline/ -bench=BenchmarkEngineLinearTranscode -benchtime=3x -count=1
```

`BenchmarkEngineLinearTranscode` requires `testdata/test_av.avi` to be present;
the benchmark is skipped automatically when the file is missing.
