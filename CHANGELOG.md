# Changelog

All notable changes to MediaMolder are documented in this file.
Format follows [Keep a Changelog](https://keepachangelog.com/).

## [Unreleased]

### Added
- **GUI: edge attribute popover with full technical metadata** — edges no longer carry an inline text label (which cluttered dense graphs). Hover (or click to pin) any connection to open a popover at the midpoint listing every known attribute for the stream and which node established each value. The probe + inference pipeline now surfaces a much richer set of properties: codec profile/level, bit_rate, bit_depth, color_space, color_range, color_primaries, color_transfer, sample aspect ratio, field order, r_frame_rate, codec FourCC tag, and start time, in addition to the existing width/height/pix_fmt/frame_rate/sample_rate/sample_fmt/channels/channel_layout/duration. Image files are reported as a single video stream with the image's geometry and pixel format. Implemented in `frontend/src/components/MMEdge.tsx`, `frontend/src/lib/streamAttrs.ts`, `av/demux.go`, `av/probe_names.go` (new helpers `ColorSpaceName`, `ColorRangeName`, `ColorPrimariesName`, `ColorTransferName`, `ProfileName`, `FieldOrderName`, `CodecTagString`), and `internal/gui/probe.go`.
- **GUI: `Get properties` button on Input nodes** — clicking it in the Inspector calls a new `POST /api/probe` backend endpoint that opens the source URL with `avformat_open_input` + `avformat_find_stream_info` and returns per-stream technical metadata (codec, width/height, pix_fmt, frame rate, sample rate, sample format, channels, channel layout, duration). The probed values are stored on the Input node (editor-only — never serialised) and seed the edge attribute inference, so every downstream connection in the graph gets accurate technical attributes automatically. Implemented in `internal/gui/probe.go`, `av/probe_names.go` (helpers `PixFmtName`, `SampleFmtName`, `CodecName`, `DefaultChannelLayoutName`), and `frontend/src/components/Inspector.tsx`.
- **Visual editor (`mediamolder gui` subcommand)** — browser-based pipeline editor served from the same single binary as the CLI. Drag-and-drop palette populated from `/api/nodes` (every libavfilter, codec, demuxer/muxer, and registered Go processor in the binary), stream-typed handles and edges, dagre auto-layout, JSON import/export, and a typed inspector for every node kind.
- **Live run + progress streaming** — Run/Stop buttons execute the current graph via `POST /api/run`; per-job state, metrics, errors, and logs stream back over Server-Sent Events (`GET /api/events/{jobId}`). Live frame counts, FPS, and error highlights overlay each node on the canvas.
- **GUI HTTP API**: `/api/health`, `/api/nodes`, `/api/examples`, `/api/validate`, `/api/run`, `/api/cancel/{jobId}`, `/api/events/{jobId}`.
- **Schema v1.2** adds optional `graph.ui.positions` for editor-side node-position persistence. Runtime ignores the block; older v1.0 / v1.1 jobs load unchanged.
- `internal/gui` package: embedded production frontend via `//go:embed`, job manager with bounded history replay (64 events) and finished-job retention (16 runs).
- `frontend/` workspace: Vite 6 + React 19 + TypeScript strict + @xyflow/react v12 + dagre + Zustand.
- Makefile targets `frontend-install`, `frontend-dev`, `frontend-build`, `gui`, `gui-dev`, `build-gui`.
- `docs/gui.md` — full GUI user + developer guide.
- CI: `gui` job builds the frontend and the embedded GUI binary on every push.
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
- **GUI: stream-type legend moved to the bottom centre** and laid out horizontally so it no longer overlays the bottom-right minimap.
- **GUI: toolbar Help button is now labelled `Help`** instead of `?` (the `?` keyboard shortcut still opens the dialog).
- Graph builder and runtime support `go_processor` nodes with identical edge/pad semantics as filters.
- Backward-compatible: all existing `schema_version: "1.0"` pipelines work unchanged.
