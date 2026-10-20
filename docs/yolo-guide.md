# YOLO object detection

The `yolo` processor runs YOLOv8, YOLO11, and YOLO26 object detection on video frames through [ONNX Runtime](https://onnxruntime.ai/). Detections go out on the event bus. The frame itself passes through unchanged.

`yolo_v8` is a permanent alias of the same processor. Existing graphs that name `yolo_v8` keep the previous letterbox, class-agnostic NMS, and `"unknown"` labels when no labels file is set.

MediaMolder does not ship detection weights. Ultralytics weights are AGPL-3.0 or Enterprise licensed, which conflicts with MediaMolder's LGPL-2.1-or-later licence. Export your own `.onnx` file and pass its path in `model`.

The processor is compiled only with the `with_onnx` build tag. See [Building with ONNX support](#building-with-onnx-support).

## Contents

- [Exporting a model](#exporting-a-model)
- [Output layouts](#output-layouts)
- [Graph configuration](#graph-configuration)
- [Parameters](#parameters)
- [Labels, class id, and NMS](#labels-class-id-and-nms)
- [Letterbox](#letterbox)
- [Empty frames and skipped frames](#empty-frames-and-skipped-frames)
- [Small objects](#small-objects)
- [The yolo_v8 alias](#the-yolo_v8-alias)
- [Reading detections](#reading-detections)
- [CUDA](#cuda)
- [Troubleshooting](#troubleshooting)

## Prerequisites

### ONNX Runtime

`yolo` and `face_detect` share one ONNX Runtime discovery path and one process-wide init.

| Platform | Install |
|----------|---------|
| macOS (arm64) | `brew install onnxruntime` → `/opt/homebrew/lib/libonnxruntime.dylib` |
| macOS (x86_64) | `brew install onnxruntime` → `/usr/local/lib/libonnxruntime.dylib` |
| Ubuntu / Debian | Place `libonnxruntime.so` on the library path, or install a distro package |
| Windows | Extract `onnxruntime.dll` beside the binary |

Lookup order:

1. The `ort_lib` param (absolute path to the shared library).
2. `ONNXRUNTIME_SHARED_LIBRARY_PATH`.
3. Auto-discovery of the usual install locations.

A normal Homebrew or distro install needs no extra configuration.

## Exporting a model

Export with [Ultralytics](https://docs.ultralytics.com/modes/export/). The node reads the tensor shape, so the same processor accepts every layout below. Keep the export **static** (`dynamic=True` is rejected by `yolo`), **square**, **float32**, and a **single file**.

### YOLOv8 and YOLO11

Both families use the raw detection head: `[1, 84, 8400]` for COCO at 640.

```bash
yolo export model=yolov8n.pt format=onnx imgsz=640
yolo export model=yolo11n.pt format=onnx imgsz=640
```

### YOLO26

YOLO26 can export either head. The shape selects the decoder.

End-to-end, NMS already applied in the one-to-one head (`nms=False`):

```bash
yolo export model=yolo26n.pt format=onnx imgsz=640 nms=False
```

End-to-end, NMS inside the graph (`nms=True`). Same row layout. `max_det` defaults to 300 and is an export setting, not a processor param.

```bash
yolo export model=yolo26n.pt format=onnx imgsz=640 nms=True
```

Raw one-to-many head, same layout as YOLOv8 (`nms` left unset):

```bash
yolo export model=yolo26n.pt format=onnx imgsz=640
```

Older Ultralytics releases exported YOLO26 as end-to-end by default. Either file loads. Set `layout` only when you want Init to reject a file whose shape is the other head.

### Single-file ONNX

Init reads the file once and builds the session from those bytes. A model that references a sibling `.onnx.data` fails. The error names `your-model.onnx.data` and asks for a single-file export. Do not split the weights out.

### Dynamic shapes

`yolo` requires a concrete shape. `dynamic=True` fails Init with a message to re-export without it. The `yolo_v8` alias still loads those files: it falls back to `input_size` (default 640), `num_classes` (default 80), and the prediction count `(S/8)² + (S/16)² + (S/32)²`.

### What Init rejects

| Condition | What you see |
|-----------|----------------|
| `task` metadata present and not `detect` | `task "segment"` (or pose, obb, classify) |
| Two outputs (segmentation proto) | input/output count |
| float16 or int8 (`half=True`) | data type is not float32 |
| Any dimension is -1, on `yolo` | re-export without `dynamic=True` |
| Input is not `[1, 3, S, S]` | not square, the wrong rank, or a batch other than 1 |
| End-to-end pose / OBB such as `[1, 300, 56]` | `pose/OBB` |
| `input_size` or `num_classes` set and different from the file | the two numbers |
| Raw head whose parsed names list is shorter than the class count | names versus class count |
| External data | the `.onnx.data` path |

Pose, OBB, segmentation, and classification heads are unsupported. A names list longer than the class count fails to parse and is not an Init error; labels become the decimal class id. A pose export whose `task` and `names` metadata were stripped, and whose single output still has a raw detect shape such as `[1, 56, 8400]`, can still load. Leave Ultralytics metadata in the file.

## Output layouts

| Layout | Shape | Box | NMS |
|--------|--------|-----|-----|
| raw | `[1, 4+nc, N]` | centre, size (`cx, cy, w, h`) in model-input pixels | yes (`yolo` is per-class, the alias is class-agnostic) |
| e2e | `[1, max_det, 6]` | corners | already applied; the node does not run NMS |

`[1, 84, 8400]` is raw COCO at 640. `[1, 9, 2100]` is a 5-class raw head. `[1, 300, 6]`, `[1, 100, 6]`, and `[1, 5, 6]` are end-to-end.

End-to-end rows follow Ultralytics column order only:

`[x1, y1, x2, y2, confidence, class]`

A file from another toolchain that stores those six numbers in a different order will produce wrong boxes, and Init will still accept the shape. `layout` is `auto`, `raw`, or `e2e`. An explicit value must match the shape or Init fails.

## Building with ONNX support

```bash
go build -tags with_onnx ./cmd/mediamolder
go build -tags "ffstatic with_onnx" ./cmd/mediamolder
```

Without the tag, `yolo` and `yolo_v8` are not registered. The pure-Go decoders in `processors/yolov8.go` and `processors/yolo.go` compile in every build.

## Graph configuration

Class names come from the model's `names` metadata when you omit `labels_file`.

```json
{
  "schema_version": "1.1",
  "inputs": [
    {
      "id": "src",
      "url": "input.mp4",
      "streams": [{ "input_index": 0, "type": "video", "track": 0 }]
    }
  ],
  "graph": {
    "nodes": [
      {
        "id": "detect",
        "type": "go_processor",
        "processor": "yolo",
        "params": {
          "model": "/models/yolo11n.onnx"
        }
      }
    ],
    "edges": [
      { "from": "src:v:0", "to": "detect:default", "type": "video" },
      { "from": "detect:default", "to": "out:v", "type": "video" }
    ]
  },
  "outputs": [
    { "id": "out", "url": "output.mp4", "codec_video": "libx264" }
  ]
}
```

Examples: [69_yolo11_basic.json](../testdata/examples/69_yolo11_basic.json), [70_yolo26_basic.json](../testdata/examples/70_yolo26_basic.json). The older [29](../testdata/examples/29_yolov8_basic.json)–[32](../testdata/examples/32_yolov8_metadata_to_file.json) graphs use the `yolo_v8` alias.

## Parameters

| Param | `yolo` default | `yolo_v8` default | Description |
|-------|----------------|-------------------|-------------|
| `model` | required | required | Path to the `.onnx` file |
| `layout` | `auto` | `auto` | `auto`, `raw`, or `e2e`. Must match the tensor |
| `agnostic_nms` | `false` | `true` | Class-agnostic NMS on a raw head. Ignored on an end-to-end head |
| `conf` | `0.5` | `0.5` | Drop scores below this. A score equal to the threshold is kept |
| `iou` | `0.45` | `0.45` | NMS overlap on a raw head. On an end-to-end head the value is ignored, and Init warns on stderr when you set the key |
| `input_size` | from the model | from the model, or 640 on a dynamic alias fallback | When set to a positive number, must equal the model's square side |
| `num_classes` | from the model | from the model, or 80 on a dynamic alias fallback | When set, must equal the raw head's class count. On an end-to-end head it is checked only after `names` parses |
| `input_name`, `output_name` | from the model | from the model, or `images` / `output0` on a dynamic alias fallback | Rename the bound tensors. Extra tensors still fail Init |
| `labels_file` | model `names`, then the decimal class id | file, or `"unknown"` | One class name per line. Wins over model metadata |
| `pad_value` | `114` | ignored (pad stays 0) | Letterbox fill, whole number 0–255, on `yolo` only |
| `resize` | `bilinear` | ignored (nearest) | `bilinear` or `nearest`, on `yolo` only |
| `device` | `cpu` | `cpu` | `cpu` or `cuda`. `cuda:N` is accepted; the index is not read |
| `ort_lib` | discovery | discovery | Shared library path |
| `process_every` | `1` | `1` | Run inference every Nth video frame |

## Labels, class id, and NMS

Every detection JSON object includes `class_id`. `0` is a real class (COCO person). The field is never omitted. Producers that have no class index, `face_detect` and `vidi_analyzer`, write `-1`.

| Labels | `yolo` | `yolo_v8` |
|--------|--------|-----------|
| `labels_file` set | that file; a miss becomes the decimal class id | that file; a miss stays `"unknown"` |
| no file, `names` in the model | the parsed names; a miss becomes the decimal class id | metadata is not read; the label is `"unknown"` |
| no file, names missing or unparseable | the decimal class id | `"unknown"` |

A names-parse failure is not an Init error. The alias never reads `names`, so a graph that omitted `labels_file` keeps the labels it had before.

`yolo` runs per-class NMS on a raw head. Suppression uses `class_id`, so two boxes with the same display name and different ids are kept apart, and a class named `"0"` does not merge with the decimal label `"0"` of a different id. The alias runs class-agnostic NMS, which is what existing graphs produced. Set `agnostic_nms` to pick either behaviour on a raw head.

An end-to-end head is already de-duplicated. Both nodes skip NMS for that layout.

This processor leaves `track_id` at 0.

## Letterbox

`yolo` letterboxes to `S×S` with fill `pad_value` (default 114, the Ultralytics fill) and `resize` (default bilinear), then planar RGB in `[0, 1]`.

The alias uses black bars and nearest-neighbour sampling, the same path as before. `pad_value: 0` on `yolo` still bilinear-samples. To match the alias tensor, set both `pad_value: 0` and `resize: nearest`.

Boxes are mapped back to the original frame with the float letterbox offset. That offset and the integer pad used to place the image can differ by half a pixel on an odd bar. The reverse mapping stays on the float offset so existing boxes do not move.

## Empty frames and skipped frames

| Case | `yolo` | `yolo_v8` |
|------|--------|-----------|
| Non-video frame, or `process_every` skipped it | no metadata | no metadata |
| Inference ran, no boxes | metadata with an empty detection list | no metadata |
| Inference ran, one or more boxes | metadata, each box has `class_id` ≥ 0 | metadata, each box has `class_id` ≥ 0 |

The empty-versus-skip distinction is in-process. `detections` is `omitempty`, so an empty list and a nil list both serialise without a `detections` key. On the wire, **a record present means inference ran**. A missing `detections` key does not mean the frame was skipped.

`yolo` posts one event per processed frame, including empty scenes. `--metadata-out` writes that event as JSONL, and each line includes `node_id`:

```json
{"node_id":"detect","frame_index":0,"pts":0,"metadata":{"detections":[{"label":"person","class_id":0,"confidence":0.92,"bbox":[120,45,380,510]}]}}
{"node_id":"detect","frame_index":1,"pts":1001,"metadata":{}}
```

The second line is a frame that was processed and had no detections. `metadata_file_writer` writes the same records without `node_id`. The alias writes no line for that frame.

CSV output (`output_format: csv`) prints a row for every non-nil metadata value. An empty `yolo` scene still produces a row: frame index, an empty timecode, PTS, and score `0.000`. The `timecodes` format writes a line only when metadata carries a timecode, which detection metadata does not, so that format stays empty. Use JSONL when you need the empty scenes.

## Small objects

For a sports or other small-object model, export a **1-class** detector at a **square** `imgsz` of 1280 or 1440, with `nms=False`:

```bash
yolo export model=best.pt format=onnx imgsz=1280 nms=False
```

The square size is baked into the file. `input_size` checks that size; it does not resize the network. `nms=False` exports the end-to-end head, so the node does not run NMS. Run it on `yolo` (pad 114), with `conf` around 0.15–0.25 and `process_every` 1.

A rectangular export such as `imgsz=[736,1280]` fails Init because the input is not square. Tile the frame into square crops, or re-export a square model. This processor does not assign track ids and does not run ByteTrack or any other tracker.

## The yolo_v8 alias

`yolo_v8` stays registered. There is no deprecation log.

When the file has a concrete shape, the alias uses that shape and those tensor names. `input_size` and `num_classes`, if you set them, must agree with the file. When the shape is dynamic, or ONNX Runtime cannot report a concrete shape, the alias uses the fixed formula above and the tensor names `images` and `output0` unless you override them. An explicit `layout` of `e2e` cannot be confirmed on that fallback, so Init fails; `auto` and `raw` proceed as raw.

`pad_value` and `resize` on the alias are ignored. Labels stay `"unknown"` unless `labels_file` is set. A frame with no detections returns no metadata, matching previous event-bus traffic.

```json
{
  "processor": "yolo_v8",
  "params": {
    "model": "/models/yolov8n.onnx",
    "labels_file": "/models/coco.names"
  }
}
```

## Reading detections

```bash
mediamolder run --metadata-out detections.jsonl job.json
```

Or wrap the node with `metadata_file_writer`:

```json
{
  "id": "detect_and_log",
  "type": "go_processor",
  "processor": "metadata_file_writer",
  "params": {
    "output_file": "detections.jsonl",
    "inner_processor": "yolo",
    "model": "/models/yolo11n.onnx"
  }
}
```

Each detection has:

| Field | JSON | Meaning |
|-------|------|---------|
| `Label` | `label` | Display name |
| `ClassID` | `class_id` | Class index, ≥ 0 for YOLO. `0` is included |
| `Confidence` | `confidence` | Score in `[0, 1]` |
| `BBox` | `bbox` | `[x1, y1, x2, y2]` in original-frame pixels |
| `TrackID` | `track_id` | Omitted. This node leaves it 0 |

## CUDA

Install the CUDA toolkit and the GPU build of ONNX Runtime, then set `device` to `cuda` and point `ort_lib` at that build. Any value that starts with `cuda`, including `cuda:N`, selects that provider. The index is not read, so the session uses the provider's default device.

```json
{
  "processor": "yolo",
  "params": {
    "model": "/models/yolo11n.onnx",
    "device": "cuda",
    "ort_lib": "/usr/local/lib/libonnxruntime.so"
  }
}
```

## Troubleshooting

Error text is prefixed with the registered name, `yolo:` or `yolo_v8:`.

### model file

The `model` path is resolved from the process working directory. Use an absolute path when the working directory is not the one you expect.

### onnxruntime init

The shared library was not found or failed to load. Set `ort_lib` or `ONNXRUNTIME_SHARED_LIBRARY_PATH` to a library built for this CPU.

### CUDA provider

`nvidia-smi` works, the library is the GPU build, and the CUDA version matches that build.

### dynamic=True / external data

Re-export without `dynamic=True`, as one `.onnx` file. A sibling `.onnx.data` is named in the error.

### not square / pose/OBB / task

The file is not a square float32 detection model. Segmentation, pose, and OBB exports fail here. Re-export `task=detect`.

### No boxes

Lower `conf`. Confirm the export is detection and that `num_classes`, when set, matches the file. On the alias, a missing `labels_file` yields the label `"unknown"` with a real `class_id`, which is still a detection.

### Build without the tag

```bash
go build -tags with_onnx ./cmd/mediamolder
```

## See also

- [Go Processor Nodes](go-processor-nodes.md)
- [JSON Config Reference](json-config-reference.md)
- [Ultralytics export](https://docs.ultralytics.com/modes/export/)
- [Ultralytics end-to-end detection](https://docs.ultralytics.com/guides/end2end-detection/)
- [ONNX Runtime](https://onnxruntime.ai/)
