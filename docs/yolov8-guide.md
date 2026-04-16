# YOLOv8 Object Detection Guide

MediaMolder ships an optional `yolo_v8` built-in processor that runs [YOLOv8](https://docs.ultralytics.com/) inference on every video frame via [ONNX Runtime](https://onnxruntime.ai/). Detections are emitted on the pipeline event bus so downstream code can log them, trigger alerts, draw overlays, or store results.

This guide covers installation, model setup, JSON configuration, and troubleshooting.

## Contents

- [YOLOv8 Object Detection Guide](#yolov8-object-detection-guide)
	- [Contents](#contents)
	- [Prerequisites](#prerequisites)
		- [ONNX Runtime shared library](#onnx-runtime-shared-library)
		- [YOLOv8 ONNX model](#yolov8-onnx-model)
		- [Labels file](#labels-file)
	- [Building with ONNX support](#building-with-onnx-support)
	- [Pipeline configuration](#pipeline-configuration)
		- [Minimal example](#minimal-example)
		- [Full parameter reference](#full-parameter-reference)
	- [How it works](#how-it-works)
	- [Reading detections](#reading-detections)
	- [CUDA / GPU acceleration](#cuda--gpu-acceleration)
	- [Custom models](#custom-models)
		- [Training a custom model](#training-a-custom-model)
		- [Non-standard input sizes](#non-standard-input-sizes)
		- [Non-YOLOv8 models](#non-yolov8-models)
	- [Troubleshooting](#troubleshooting)
		- ["yolo\_v8: model file: no such file or directory"](#yolo_v8-model-file-no-such-file-or-directory)
		- ["yolo\_v8: onnxruntime init: ..."](#yolo_v8-onnxruntime-init-)
		- ["yolo\_v8: CUDA provider: ..."](#yolo_v8-cuda-provider-)
		- ["yolo\_v8: preprocess: ..."](#yolo_v8-preprocess-)
		- [No detections on any frame](#no-detections-on-any-frame)
		- [Build fails without `with_onnx` tag](#build-fails-without-with_onnx-tag)
	- [See also](#see-also)

---

## Prerequisites

### ONNX Runtime shared library

The `yolo_v8` processor uses [onnxruntime\_go](https://github.com/yalue/onnxruntime_go) which requires the ONNX Runtime C shared library at runtime.

| Platform | Install |
|----------|---------|
| macOS (arm64) | `brew install onnxruntime` → lib at `/opt/homebrew/lib/libonnxruntime.dylib` |
| macOS (x86\_64) | `brew install onnxruntime` → lib at `/usr/local/lib/libonnxruntime.dylib` |
| Ubuntu / Debian | Download from [ONNX Runtime releases](https://github.com/microsoft/onnxruntime/releases) and place `libonnxruntime.so` in `/usr/local/lib` (or any path on `LD_LIBRARY_PATH`) |
| Windows | Download the Windows zip from releases, extract `onnxruntime.dll` alongside the binary |

Tell MediaMolder where to find it using **one** of:

1. The `ort_lib` processor param (absolute path to the `.so` / `.dylib` / `.dll`).
2. The `ONNXRUNTIME_SHARED_LIBRARY_PATH` environment variable.

If neither is set, the Go runtime's default library search path is used.

### YOLOv8 ONNX model

Export a YOLOv8 model to ONNX format using [Ultralytics](https://docs.ultralytics.com/modes/export/):

```bash
pip install ultralytics
yolo export model=yolov8n.pt format=onnx opset=17 simplify=True
```

This produces `yolov8n.onnx`. Available sizes:

| Model | Params | COCO mAP | Notes |
|-------|--------|----------|-------|
| yolov8n | 3.2M | 37.3 | Fastest — good for real-time on CPU |
| yolov8s | 11.2M | 44.9 | Good balance |
| yolov8m | 25.9M | 50.2 | Mid-range |
| yolov8l | 43.7M | 52.9 | Higher accuracy |
| yolov8x | 68.2M | 53.9 | Best accuracy, slowest |

All sizes use the same output format. The default `input_size` is 640; if you export with a different `imgsz`, pass the matching `input_size` param.

### Labels file

Create a text file with one class name per line (`coco.names` for the default 80-class models):

```
person
bicycle
car
motorcycle
...
```

The standard COCO names file is widely available — search for "coco.names" or generate it:

```bash
python3 -c "
from ultralytics import YOLO
m = YOLO('yolov8n.pt')
with open('coco.names', 'w') as f:
    for name in m.names.values():
        f.write(name + '\n')
"
```

If you omit `labels_file`, detections will use numeric class IDs (e.g. `"class_0"`) instead of human-readable names.

---

## Building with ONNX support

The `yolo_v8` processor is behind a build tag so it doesn't add a cgo dependency on systems that don't need it:

```bash
# Dynamic linking (pkg-config finds FFmpeg + onnxruntime):
go build -tags with_onnx ./cmd/mediamolder

# Static FFmpeg + dynamic onnxruntime:
go build -tags "ffstatic with_onnx" ./cmd/mediamolder
```

Without the `with_onnx` tag, the processor is not registered and `processors.Get("yolo_v8")` returns `nil`. The pure-Go post-processing code (`ParseYOLOv8Output`, `NMS`, `IoU`) in `processors/yolov8.go` always compiles.

---

## Pipeline configuration

### Minimal example

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
        "processor": "yolo_v8",
        "params": {
          "model": "/models/yolov8n.onnx"
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

With defaults, this runs YOLOv8 at 640×640 with 80 COCO classes, confidence 0.5, NMS IoU 0.45, on CPU.

### Full parameter reference

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| `model` | string | **(required)** | Absolute path to the `.onnx` model file |
| `conf` | float | `0.5` | Minimum confidence threshold — detections below this are discarded |
| `iou` | float | `0.45` | IoU threshold for NMS — higher keeps more overlapping boxes |
| `input_size` | int | `640` | Square input dimension the model was exported with |
| `num_classes` | int | `80` | Number of classes the model detects |
| `labels_file` | string | — | Path to newline-separated class label file |
| `input_name` | string | `"images"` | ONNX model input tensor name (rarely needs changing) |
| `output_name` | string | `"output0"` | ONNX model output tensor name |
| `ort_lib` | string | env | Path to the onnxruntime shared library |
| `device` | string | `"cpu"` | `"cpu"` or `"cuda"` / `"cuda:N"` for GPU inference |
| `process_every` | int | `1` | Run inference every N-th video frame. Non-processed frames pass through unchanged with no metadata. |

---

## How it works

The processor runs a five-stage pipeline on each video frame:

```
┌───────────┐     ┌────────────┐     ┌──────────┐     ┌───────┐     ┌─────────┐
│ Letterbox  │────▶│ Float32    │────▶│ ONNX     │────▶│ Parse │────▶│ NMS     │
│ (resize +  │     │ tensor     │     │ Runtime  │     │ output│     │ (remove │
│  pad)      │     │ [1,3,H,W]  │     │ .Run()   │     │       │     │ dupes)  │
└───────────┘     └────────────┘     └──────────┘     └───────┘     └─────────┘
```

1. **Letterbox** — The frame is resized to `input_size × input_size` preserving aspect ratio, with black padding bars. This matches what Ultralytics does during training.

2. **Tensor conversion** — The letterboxed image is written into a pre-allocated `[1, 3, H, W]` float32 tensor in NCHW layout (channel-first, RGB, values normalised to `[0, 1]`).

3. **ONNX inference** — `session.Run()` executes the model. Input and output tensors are pre-allocated during `Init()`, so there's zero per-frame allocation on the Go side.

4. **Parse output** — YOLOv8 outputs a transposed tensor of shape `[1, 4+num_classes, num_predictions]`. The parser iterates over predictions, finds the best class per box, and filters by confidence. Bounding boxes are **reverse-mapped** from letterboxed coordinates back to original frame pixel coordinates.

5. **NMS** — Greedy non-maximum suppression removes overlapping detections of the same class, keeping the highest-confidence one.

Detections (if any) are attached as `*Metadata` and emitted on the event bus. The frame itself passes through unchanged.

When `process_every` is set to N (> 1), inference is skipped on frames where `FrameIndex % N != 0`. Those frames still pass through to the output — only the (expensive) inference step is bypassed. This is useful for reducing GPU/CPU load on high-frame-rate streams where detecting on every frame is unnecessary:

```json
{
  "id": "detect",
  "type": "go_processor",
  "processor": "yolo_v8",
  "params": {
    "model": "/models/yolov8n.onnx",
    "process_every": 5
  }
}
```

At 30 fps with `"process_every": 5`, inference runs 6 times per second instead of 30.

---

## Reading detections

Register an event bus listener to receive detections:

```go
bus.Subscribe(func(evt pipeline.Event) {
    if md, ok := evt.(processors.ProcessorMetadataEvent); ok {
        if md.Metadata == nil || len(md.Metadata.Detections) == 0 {
            return
        }
        for _, d := range md.Metadata.Detections {
            fmt.Printf("frame %d: %s (%.1f%%) at [%.0f, %.0f, %.0f, %.0f]\n",
                md.FrameIndex, d.Label, d.Confidence*100,
                d.BBox[0], d.BBox[1], d.BBox[2], d.BBox[3])
        }
    }
})
```

Each `Detection` has:

| Field | Type | Description |
|-------|------|-------------|
| `Label` | string | Class name (from labels file) or `"class_N"` |
| `Confidence` | float64 | Detection confidence in `[0, 1]` |
| `BBox` | [4]float64 | `[x1, y1, x2, y2]` in original frame pixel coordinates |

---

## CUDA / GPU acceleration

To use an NVIDIA GPU for inference:

1. Install [CUDA Toolkit](https://developer.nvidia.com/cuda-downloads) (11.x or 12.x) and cuDNN.
2. Install the ONNX Runtime **GPU** package (the CPU-only build won't have CUDA providers):
   - Download `onnxruntime-linux-x64-gpu-*.tgz` from releases.
3. Set `"device": "cuda"` (uses GPU 0) or `"device": "cuda:1"` for a specific GPU.

```json
{
  "id": "detect",
  "type": "go_processor",
  "processor": "yolo_v8",
  "params": {
    "model": "/models/yolov8n.onnx",
    "device": "cuda",
    "ort_lib": "/usr/local/lib/libonnxruntime.so"
  }
}
```

CUDA inference is typically 5-20× faster than CPU for YOLOv8, depending on model size and GPU.

---

## Custom models

The `yolo_v8` processor works with any model that uses the standard YOLOv8 output format — including custom-trained models.

### Training a custom model

```bash
yolo train model=yolov8n.pt data=my_dataset.yaml epochs=100
yolo export model=runs/detect/train/weights/best.pt format=onnx opset=17 simplify=True
```

Then configure the processor with your model's class count:

```json
{
  "model": "/models/my_custom.onnx",
  "num_classes": 5,
  "labels_file": "/models/my_classes.names"
}
```

### Non-standard input sizes

If you exported with a different image size:

```bash
yolo export model=yolov8n.pt format=onnx imgsz=320
```

Set `"input_size": 320` in the params. The prediction count adjusts automatically based on `input_size`.

### Non-YOLOv8 models

The built-in processor only handles the YOLOv8 output layout (`[1, 4+C, N]` transposed). For other architectures (YOLOv5, SSD, EfficientDet), write a custom processor using the same `onnxruntime_go` library and register it with `processors.Register()`. See [Go Processor Nodes](go-processor-nodes.md) for how to implement a custom processor.

---

## Troubleshooting

### "yolo_v8: model file: no such file or directory"

The `model` path must be absolute or relative to the working directory of the mediamolder process.

### "yolo_v8: onnxruntime init: ..."

The ONNX Runtime shared library wasn't found. Check:
- `ort_lib` param points to the correct `.so` / `.dylib` file.
- Or `ONNXRUNTIME_SHARED_LIBRARY_PATH` is set.
- The library version matches your platform (arm64 vs x86_64).

### "yolo_v8: CUDA provider: ..."

CUDA-related error. Verify:
- CUDA toolkit is installed and `nvidia-smi` works.
- You installed the **GPU** variant of ONNX Runtime (not CPU-only).
- cuDNN is installed and on the library path.
- The CUDA version matches what ONNX Runtime was built against.

### "yolo_v8: preprocess: ..."

Frame-to-tensor conversion failed. This usually means the video frame had unexpected dimensions (0×0) or the pixel format couldn't be converted. Check that the input source is a valid video stream.

### No detections on any frame

- Lower `conf` (e.g. `0.1`) to see if detections exist at low confidence.
- Verify `num_classes` matches the model (80 for COCO, your custom count for fine-tuned models).
- Check that `input_size` matches the export size.
- Try the model with Ultralytics CLI first: `yolo predict model=yolov8n.onnx source=frame.jpg` to confirm it works.

### Build fails without `with_onnx` tag

If you see linker errors about onnxruntime symbols, you forgot the build tag:

```bash
go build -tags with_onnx ./cmd/mediamolder
```

Without the tag, none of the ONNX code compiles. The pure-Go post-processing (`ParseYOLOv8Output`, `NMS`, `IoU`) always compiles regardless of tags.

---

## See also

- [Go Processor Nodes](go-processor-nodes.md) — full go_processor reference
- [JSON Config Reference](json-config-reference.md) — pipeline schema documentation
- [Ultralytics Docs](https://docs.ultralytics.com/) — YOLOv8 training and export
- [ONNX Runtime](https://onnxruntime.ai/) — inference runtime documentation
