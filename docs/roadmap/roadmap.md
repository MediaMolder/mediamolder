# Development Roadmap (Phased)

## Phase 0 (MVP)
Core binding layer + simple input→filter→output pipeline.

**Exit criteria**: Successfully transcode a single-stream video file (YUV or Y4M input → H.264 output) with at least one video filter (scale) applied, driven by a JSON config file. Output passes SSIM ≥ 0.99 against equivalent `ffmpeg` output. Benchmarks show < 5% overhead vs. `ffmpeg` (see spec §15).

## Phase 1
Full declarative graph (JSON primary), CLI, Go control API, state machine, clock/sync.

**Exit criteria**: Multi-input, multi-output pipelines with complex filter graphs (overlay, concat, split) work from JSON. `mediamolder run`, `inspect`, and `convert-cmd` CLI commands operational. Pipeline state machine (NULL→READY→PAUSED→PLAYING) fully implemented with event bus. Clock/sync model working for file-based inputs; A/V sync within ±40ms. `Pipeline.SetState()`, `Pipeline.Seek()`, and `Pipeline.GetMetrics()` Go API methods functional.

## Phase 2
Observability, dynamic reconfiguration, reliability features.

**Exit criteria**: OpenTelemetry traces and Prometheus metrics exported. Filter parameter hot-reconfiguration works without dropping frames. Error policies (skip, retry, fallback, abort) demonstrated in integration tests. AddOutput at runtime works.

## Phase 3
Hardware accel parity, advanced filters, bitstream filters, subtitles.

**Exit criteria**: CUDA, VAAPI, and QSV hardware decode/encode paths tested on CI runners with GPU. Subtitle burn-in and passthrough working. Bitstream filter support (e.g., `h264_mp4toannexb`) available.

## Phase 4
Production hardening, community tools, documentation.

**Exit criteria**: Fuzz testing with zero unfixed crashers. Comprehensive public documentation site. Published JSON Schema files. Package available via `go install`. Stable v1.0 release.
