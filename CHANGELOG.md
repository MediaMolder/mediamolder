# Changelog

All notable changes to MediaMolder are documented in this file.
Format follows [Keep a Changelog](https://keepachangelog.com/).

## [Unreleased]

### Added
- **`go_processor` node type** — custom Go per-frame processing in the MediaMolder graph (AI inference, analytics, tracking, metadata emission). Requires `schema_version: "1.1"`.
- `processors` package with `Processor` interface (`Init`/`Process`/`Close`), thread-safe registry, `ProcessorContext`, and `Metadata`/`Detection` types.
- Built-in processors: `null` (passthrough), `frame_counter` (counting with periodic metadata).
- Helper functions in `processors/helpers.go`: `Letterbox`, `ImageToFloat32Tensor`, `DrawDetections`, `FrameToRGBA`, and `FrameToFloat32Tensor`.
- `av.Frame.ToRGBA()` — converts any video frame to `*image.RGBA` via libswscale (supports YUV420P, NV12, RGB24, and all other FFmpeg pixel formats). Also adds `Frame.PixFmt()` accessor.
- `av.Frame.PixFmt()` — returns the frame's pixel format as an `int` (`AVPixelFormat`).
- **Optional `yolo_v8` built-in processor** (behind `-tags with_onnx`): YOLOv8 object detection via ONNX Runtime with CUDA support, greedy NMS, and letterbox-aware coordinate mapping. Pure-Go post-processing (`ParseYOLOv8Output`, `NMS`, `IoU`) in `processors/yolov8.go` compiles without ONNX.
- `docs/yolov8-guide.md` — end-to-end guide for setting up and using the YOLOv8 processor.
- Schema v1.1 (`schema/v1.1.json`) adding `go_processor` to node type enum with conditional `processor` field requirement.
- `mediamolder list-processors` CLI command.
- Comprehensive documentation: `docs/go-processor-nodes.md`, updated `docs/json-config-reference.md` and `README.md`.

### Changed
- Graph builder and runtime support `go_processor` nodes with identical edge/pad semantics as filters.
- Backward-compatible: all existing `schema_version: "1.0"` pipelines work unchanged.
