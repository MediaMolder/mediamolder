# Vidi 2.5 Multimodal Analysis Guide

MediaMolder ships a built-in `vidi_analyzer` processor that connects a running [Vidi 2.5](https://github.com/bytedance/vidi) inference service to any media processing graph. It can caption scenes, answer natural-language questions about video content, ground objects to bounding boxes, and produce structured edit plans — all as structured `Metadata` on the pipeline event bus.

Because Vidi 2.5 is a 9 billion-parameter PyTorch model, it runs as a **separate Python service**. The Go processor is a thin, context-aware HTTP client that batches decoded frames and POSTs them to that service.

## Contents

- [Vidi 2.5 Multimodal Analysis Guide](#vidi-25-multimodal-analysis-guide)
  - [Contents](#contents)
  - [Architecture overview](#architecture-overview)
  - [Setting up the Vidi inference service](#setting-up-the-vidi-inference-service)
    - [Install](#install)
    - [FastAPI wrapper](#fastapi-wrapper)
    - [Start the service](#start-the-service)
  - [Pipeline configuration](#pipeline-configuration)
    - [Minimal example](#minimal-example)
    - [Full parameter reference](#full-parameter-reference)
  - [Tasks](#tasks)
    - [captioning](#captioning)
    - [grounding](#grounding)
    - [qa](#qa)
    - [editing](#editing)
  - [Reading results](#reading-results)
    - [Detections (grounding)](#detections-grounding)
    - [Custom fields (all tasks)](#custom-fields-all-tasks)
    - [Writing results to a file](#writing-results-to-a-file)
  - [Performance tips](#performance-tips)
  - [License note](#license-note)
  - [See also](#see-also)

---

## Architecture overview

```
Media file / stream
       |
  MediaMolder pipeline (Go)
       |
  vidi_analyzer node  ──── HTTP POST /infer ───► Vidi service (Python + PyTorch)
       |                  (batched JPEG frames)       |
       |                                         JSON response
       |                  ◄────────────────────────────
       |
  Metadata on event bus
  (detections, captions, timestamps, edit plans)
       |
  downstream nodes / --metadata-out file
```

The processor passes every video frame through unchanged. Inference results are published as `Metadata` attached to the last frame in each batch; audio frames are always forwarded unmodified.

---

## Setting up the Vidi inference service

### Install

```bash
git clone https://github.com/bytedance/vidi
cd vidi/Vidi1.5_9B
pip install fastapi "uvicorn[standard]"
grep -v '^decord' requirements.txt | pip install -r /dev/stdin
```

> **macOS arm64 note:** The Vidi repo's `requirements.txt` includes `decord`, which has no arm64 wheel. Because MediaMolder sends pre-decoded JPEG frames to the service (it never asks the service to open a video file itself), `decord` is not needed for the MediaMolder integration. The `grep -v` command above installs everything except that line.
>
> Linux / x86\_64 users can run `pip install -r requirements.txt fastapi "uvicorn[standard]"` without the workaround.

### Download model weights

Download the checkpoint from Hugging Face before starting the service. **Run these commands from inside `vidi/Vidi1.5_9B/`** — that is the same directory where you cloned the repo and where `vidi_service.py` lives:

```bash
# Must be run from vidi/Vidi1.5_9B/
pip install huggingface_hub
python3 -c "
from huggingface_hub import snapshot_download
snapshot_download('bytedance-research/Vidi1.5-9B', local_dir='./weights/Vidi1.5-9B')
"
```

This creates `vidi/Vidi1.5_9B/weights/Vidi1.5-9B/` and downloads ~18 GB into it. The start command below uses `VIDI_MODEL_PATH=./weights/Vidi1.5-9B`, which resolves to that path when run from the same directory.

A GPU with at least 20 GB VRAM (e.g. RTX 3090, A100) is recommended for 9B weights at fp16. CPU inference is possible but slow.

### FastAPI wrapper

**Create** `vidi_service.py` in the `vidi/Vidi1.5_9B/` directory (it does not exist in the repo — you must create it):

```python
"""vidi_service.py — FastAPI wrapper for Vidi 1.5-9B.

Place this file in vidi/Vidi1.5_9B/ and set VIDI_MODEL_PATH before starting:

    VIDI_MODEL_PATH=./weights/Vidi1.5-9B python3 -m uvicorn vidi_service:app \\
        --host 0.0.0.0 --port 8000
"""
import base64
import io
import os
import re

import torch
from fastapi import FastAPI
from pydantic import BaseModel
from PIL import Image

from vidi.constants import IMAGE_TOKEN_INDEX, DEFAULT_IMAGE_TOKEN
from vidi.model.builder import load_pretrained_model
from vidi.dataset.img_utils import process_images
from vidi.dataset.txt_utils import tokenizer_image_token, preprocess_chat

MODEL_PATH = os.environ.get("VIDI_MODEL_PATH", "")
if not MODEL_PATH:
    raise RuntimeError(
        "VIDI_MODEL_PATH is not set. "
        "Point it at the directory containing the downloaded Vidi1.5-9B weights."
    )

app = FastAPI()

# Loaded once at startup; stays resident in GPU memory for the lifetime of the process.
model, tokenizer, image_processor, audio_processor = load_pretrained_model(MODEL_PATH)
model.config.mm_splits = 32


class InferenceRequest(BaseModel):
    frames: list[str]        # base64-encoded JPEG frames sent by MediaMolder
    query: str = "describe the scene"
    task: str = "captioning" # captioning | grounding | qa | editing
    duration_s: float = 0.0  # total video duration; required for grounding timestamps


def _grounding_prompt(query: str, duration_s: float) -> str:
    q = query.rstrip(".")
    return (
        f"In this video, you are required to return the start and end timestamps "
        f"(formatted as percentage) that corresponds to query text split by comma. "
        f"Video length is: {duration_s:.2f} and text query is: {q}."
    )


def _parse_timestamps(output: str, duration_s: float) -> list[dict]:
    """Parse 'start-end' percentage pairs from grounding output."""
    results = []
    for start_pct, end_pct in re.findall(r'(\d+\.?\d*)-(\d+\.?\d*)', output):
        results.append({
            "start_s": round(float(start_pct) * duration_s, 3),
            "end_s":   round(float(end_pct)   * duration_s, 3),
        })
    return results


@app.post("/infer")
async def infer(req: InferenceRequest) -> dict:
    images = [
        Image.open(io.BytesIO(base64.b64decode(f))).convert("RGB")
        for f in req.frames
    ]

    # process_images accepts list[PIL.Image], the same type returned by load_video().
    # We bypass load_video (and decord) entirely because MediaMolder pre-decodes frames.
    video = process_images(images, image_processor, model.config)
    video = video.unsqueeze(0).half().cuda()

    question = (
        _grounding_prompt(req.query, req.duration_s)
        if req.task == "grounding" and req.duration_s > 0
        else req.query
    )

    qs = DEFAULT_IMAGE_TOKEN + "\n" + question
    prompt = preprocess_chat([{"from": "human", "value": qs}], tokenizer)
    input_ids = (
        tokenizer_image_token(prompt, tokenizer, IMAGE_TOKEN_INDEX, return_tensors="pt")
        .unsqueeze(0)
        .cuda()
    )

    with torch.inference_mode():
        output_ids = model.generate(
            input_ids,
            images=video,
            audios=None,
            audio_sizes=None,
            do_sample=False,
            max_new_tokens=1024,
            use_cache=True,
            disable_compile=True,
            pad_token_id=tokenizer.pad_token_id,
        )

    output = tokenizer.batch_decode(output_ids, skip_special_tokens=True)[0].strip()

    if req.task == "grounding":
        return {
            "caption":    output,
            "timestamps": _parse_timestamps(output, req.duration_s),
        }
    return {"caption": output}
```

### Start the service

```bash
VIDI_MODEL_PATH=./weights/Vidi1.5-9B python3 -m uvicorn vidi_service:app --host 0.0.0.0 --port 8000
```

> **"command not found" for `uvicorn`?** Use `python3 -m uvicorn` — pip installs scripts into a directory often not on `PATH` (e.g. `~/Library/Python/3.x/bin/` on macOS).

Model loading takes 30–60 seconds on first startup (weights are read from disk into GPU memory). Once you see `Application startup complete`, the service is ready.

---

## Pipeline configuration

### Minimal example

```json
{
  "schema_version": "1.1",
  "inputs":  [{ "id": "src", "url": "input.mp4" }],
  "outputs": [{ "id": "out", "url": "output.mp4" }],
  "nodes": [
    { "id": "dec",  "type": "source",      "input":  "src" },
    {
      "id":        "vidi",
      "type":      "go_processor",
      "processor": "vidi_analyzer",
      "params": {
        "service_url": "http://localhost:8000"
      }
    },
    { "id": "enc",  "type": "sink",        "output": "out" }
  ]
}
```

`schema_version` must be `"1.1"` whenever a `go_processor` node is present.

### Full parameter reference

| Param           | Type    | Default               | Description |
|-----------------|---------|-----------------------|-------------|
| `service_url`   | string  | **(required)**        | Base URL of the Vidi inference service, e.g. `"http://localhost:8000"` |
| `query`         | string  | `"describe the scene"` | Natural-language prompt sent with every batch |
| `task`          | string  | `"captioning"`        | Inference task — see [Tasks](#tasks) |
| `buffer_frames` | int     | `8`                   | Number of decoded frames to accumulate before firing one `/infer` call. Larger batches give Vidi more temporal context but increase latency. |
| `process_every` | int     | `1`                   | Only buffer every Nth video frame; others pass through without being sent to the service. Use this to reduce inference frequency. |
| `jpeg_quality`  | int     | `75`                  | JPEG quality (1–100) used when encoding frames for the HTTP request. Lower values reduce payload size at the cost of image fidelity. |
| `timeout_s`     | float   | `30`                  | Per-request HTTP timeout in seconds. The processor uses `context` cancellation, so pipeline shutdown always interrupts in-flight requests cleanly. |

---

## Tasks

The `task` param controls what the Vidi model produces. The Go processor maps each response shape to `Metadata` as described below.

### captioning

Returns a natural-language description of the video segment.

```json
{ "caption": "A pelican lands on a wooden dock at sunset." }
```

The caption appears in `Metadata.Custom["caption"]`.

### grounding

Localises objects described in `query` to bounding boxes in specific frames.

```json
{
  "boxes": [
    { "frame_index": 3, "label": "pelican", "confidence": 0.91,
      "box_2d": [142, 87, 310, 265] }
  ]
}
```

Boxes map to `Metadata.Detections` (label, confidence, BBox in pixel coordinates). Optionally also returns `"caption"`.

### qa

Answers a natural-language question about the video.

```json
{ "answer": "The pelican lands at approximately 00:04:12." }
```

The answer appears in `Metadata.Custom["answer"]`.

### editing

Returns a structured edit plan describing suggested cuts, trims, or overlays.

```json
{
  "edit_plan": [
    { "action": "trim", "start_s": 0.0,  "end_s": 4.2,  "label": "intro silence" },
    { "action": "cut",  "start_s": 12.1, "end_s": 14.8, "label": "shaky footage" }
  ]
}
```

The plan appears in `Metadata.Custom["edit_plan"]`.

---

## Reading results

### Detections (grounding)

`grounding` results appear in `Metadata.Detections`:

```go
for _, det := range metadata.Detections {
    fmt.Printf("label=%s conf=%.2f box=%v\n", det.Label, det.Confidence, det.BBox)
}
```

`BBox` is `[x1, y1, x2, y2]` in pixel coordinates of the original (pre-processing) frame.

### Custom fields (all tasks)

All other results land in `Metadata.Custom`:

```go
if caption, ok := metadata.Custom["caption"].(string); ok {
    fmt.Println("caption:", caption)
}
if plan, ok := metadata.Custom["edit_plan"].([]any); ok {
    // each element is a map[string]any with "action", "start_s", "end_s", "label"
}
```

If the service call fails for a batch, `Metadata.Custom["vidi_error"]` contains the error string. The graph continues running; no frames are dropped.

### Writing results to a file

Use `--metadata-out` to write all `Metadata` events to a JSONL file:

```bash
mediamolder run job.json --metadata-out results.jsonl
```

Or chain a `metadata_file_writer` node in the graph to interleave metadata writing with other processing. See [go-processor-nodes.md](go-processor-nodes.md#persisting-metadata-to-files) for details.

---

## Performance tips

- **GPU on the inference host** — Vidi 2.5 at 9B parameters requires a GPU for reasonable throughput. The Go side imposes negligible CPU overhead.
- **`buffer_frames`** — a larger buffer gives Vidi more temporal context and amortises HTTP round-trip latency. Start with `8` and increase if captions seem to miss scene context.
- **`process_every`** — pair with a `decimate` filter node to reduce the frame rate before the `vidi_analyzer` node rather than skipping frames inside the processor. This saves the JPEG-encode cost for skipped frames.
- **`jpeg_quality`** — `60`–`75` is usually sufficient. The dominant latency is model inference, not payload size.
- **Dedicated service host** — run the Python service on a separate machine with a GPU and point `service_url` at it. The Go pipeline runs on the encode host with no GPU required.

---

## License note

Vidi 2.5 model weights are released under [CC-BY-NC-4.0](https://creativecommons.org/licenses/by-nc/4.0/). **Non-commercial use only.** The `vidi_analyzer` Go processor itself is LGPL-2.1-or-later.

---

## See also

- [Go Processor Nodes](go-processor-nodes.md) — full processor interface reference
- [YOLOv8 Guide](yolov8-guide.md) — in-process object detection without a separate service
- [JSON Config Reference](json-config-reference.md) — pipeline schema
