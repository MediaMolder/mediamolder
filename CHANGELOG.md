# Changelog

All notable changes to MediaMolder are documented in this file.
Format follows [Keep a Changelog](https://keepachangelog.com/).

## [Unreleased]

### Added
- **`go_processor` node type** — custom Go per-frame processing in the MediaMolder graph (AI inference, analytics, tracking, metadata emission). Requires `schema_version: "1.1"`.
- `processors` package with `Processor` interface (`Init`/`Process`/`Close`), thread-safe registry, `ProcessorContext`, and `Metadata`/`Detection` types.
- Built-in processors: `null` (passthrough), `frame_counter` (counting with periodic metadata).
- Helper functions in `processors/helpers.go`: `Letterbox`, `ImageToFloat32Tensor`, `DrawDetections`, and stub `FrameToRGBA`/`FrameToFloat32Tensor` (pending `av.Frame` pixel plane accessors).
- Schema v1.1 (`schema/v1.1.json`) adding `go_processor` to node type enum with conditional `processor` field requirement.
- `mediamolder list-processors` CLI command.
- Comprehensive documentation: `docs/go-processor-nodes.md`, updated `docs/json-config-reference.md` and `README.md`.

### Changed
- Graph builder and runtime support `go_processor` nodes with identical edge/pad semantics as filters.
- Backward-compatible: all existing `schema_version: "1.0"` pipelines work unchanged.
