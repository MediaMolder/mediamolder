# Changelog

All notable changes to MediaMolder are documented in this file.
Format follows [Keep a Changelog](https://keepachangelog.com/).

## [Unreleased]

### Added
- **`go_processor` node type** — custom Go per-frame processing in the MediaMolder graph (AI inference, analytics, tracking, metadata emission). Requires `schema_version: "1.1"`.
- `processors` package with `Processor` interface (`Init`/`Process`/`Close`), thread-safe registry, `ProcessorContext`, and `Metadata`/`Detection` types.
- Built-in processors: `null` (passthrough), `frame_counter` (counting with periodic metadata), `frame_info` (frame dimensions/format/PTS diagnostics), `scene_change` (scene detection using the same MAFD + diff-of-MAFD algorithm as FFmpeg's `scdet` filter — zero-copy Y plane access for YUV formats, swscale GRAY8 fallback for RGB).
- `av.Frame.Clone()` — reference-counted frame clone via `av_frame_clone()`.
- `av.FrameSceneScore(a, b)` — computes luma MAFD between two frames (0–100 scale). For YUV planar formats, reads the Y plane directly with zero allocation; falls back to swscale GRAY8 conversion for RGB/packed formats.
- Helper functions in `processors/helpers.go`: `Letterbox`, `ImageToFloat32Tensor`, `DrawDetections`, `FrameToRGBA`, and `FrameToFloat32Tensor`.
- `av.Frame.ToRGBA()` — converts any video frame to `*image.RGBA` via libswscale (supports YUV420P, NV12, RGB24, and all other FFmpeg pixel formats). Also adds `Frame.PixFmt()` accessor.
- `av.Frame.PixFmt()` — returns the frame's pixel format as an `int` (`AVPixelFormat`).
- **Optional `yolo_v8` built-in processor** (behind `-tags with_onnx`): YOLOv8 object detection via ONNX Runtime with CUDA support, greedy NMS, and letterbox-aware coordinate mapping. Pure-Go post-processing (`ParseYOLOv8Output`, `NMS`, `IoU`) in `processors/yolov8.go` compiles without ONNX.
- `docs/yolov8-guide.md` — end-to-end guide for setting up and using the YOLOv8 processor.
- **`metadata_file_writer` built-in processor** — decorator that wraps any processor and writes emitted metadata to a JSON Lines file. Configurable entirely in JSON with `output_file` and `inner_processor` params.
- **`--metadata-out` CLI flag** on `mediamolder run` — writes all `ProcessorMetadata` events as JSON Lines to a file (or stdout with `-`).
- JSON tags on `pipeline.ProcessorMetadata` for clean serialisation (`node_id`, `frame_index`, `pts`, `metadata`).
- Schema v1.1 (`schema/v1.1.json`) adding `go_processor` to node type enum with conditional `processor` field requirement.
- `mediamolder list-processors` CLI command.
- Comprehensive documentation: `docs/go-processor-nodes.md`, updated `docs/json-config-reference.md` and `README.md`.

### Changed
- Graph builder and runtime support `go_processor` nodes with identical edge/pad semantics as filters.
- Backward-compatible: all existing `schema_version: "1.0"` pipelines work unchanged.
