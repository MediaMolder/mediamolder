# Face Detection Guide

MediaMolder can detect faces in images and video, align each face to a canonical
112×112 crop, and (optionally) compute a 128-dimensional embedding for
recognition/clustering. It is available three ways:

- **CLI** — `mediamolder face-detect <input>` for one-shot analysis of an image or
  video to JSONL / CSV / JSON.
- **Graph node** — the `face_detect` `go_processor`, to detect faces in an image or
  video inside a media graph; emits per-face metadata and can self-write a sidecar.
- **GUI** — the "Face detection" node in the visual editor's palette.

The detect → align → embed pipeline and its models live in the
[`face`](../face) package, behind a small, stable API (`Capable`, `Analyze`,
`AnalyzeImage`). The design and rationale are in
[architecture/face-detection.md](architecture/face-detection.md).

## Models & build

> **Quick check:** `mediamolder face-setup` reports exactly what's missing (build
> support, ONNX Runtime, models) and prints the command to fix each — add `--fetch`
> to download the models. Run it any time face detection won't start.

Face analysis is **gated on the `with_onnx` build tag** and two bundled ONNX models.
The default build links a stub: `face-detect` and the `face_detect` node report that
analysis is unavailable rather than producing wrong results.

Two models are used (both loaded at runtime as **data**, SHA-256-pinned and verified
on load — never linked):

| Role | Model | License |
|---|---|---|
| Detector | YOLOv8-face (`yolov8n-face.onnx`) | AGPL-3.0 |
| Embedder | SFace (`sface.onnx`) | Apache-2.0 |

The detector is AGPL but, being loaded as data behind the `face` API, is swappable for
a permissively-licensed export (e.g. YuNet, MIT) with no code change. MediaMolder ships
**no binaries and no models** — you fetch them:

```bash
# 1. Install the ONNX Runtime. It is then auto-discovered — no env var needed:
brew install onnxruntime                     # macOS;  Linux: your distro's onnxruntime package

# 2. Fetch + SHA-256-verify the face models into the git-ignored testdata/face_models/:
scripts/fetch-face-models.sh
export MEDIAMOLDER_FACE_MODELS="$PWD/testdata/face_models"

# Only if the ONNX Runtime is in a non-standard location (otherwise omit):
# export ONNXRUNTIME_SHARED_LIBRARY_PATH=/path/to/libonnxruntime.dylib
```

> **ONNX Runtime is auto-discovered.** When the library path is not set, MediaMolder
> searches the platform's standard install locations (Homebrew prefixes,
> `/usr/local/lib`, `/usr/lib/*-linux-gnu`, …) for the correctly-named library
> (`libonnxruntime.dylib` on macOS, `libonnxruntime.so` on Linux). So a normal
> `brew install onnxruntime` / distro package needs no configuration; set
> `ONNXRUNTIME_SHARED_LIBRARY_PATH` (or the `ort_lib` param / `--ort-lib` flag / the
> GUI's "ONNX runtime library" field) only for a non-standard install.

> Fetch the models into the git-ignored `testdata/face_models/` (the script's
> default), not an arbitrary path — they are large and the detector is
> copyleft-licensed, so they must never land in a tracked directory.

Build with the tag (ONNX Runtime is `dlopen`ed at runtime, so it is needed only to
*run*, not to build):

```bash
go build -tags with_onnx ./cmd/mediamolder        # CLI + face_detect node
make build-gui-onnx                               # GUI single-binary with the node
```

## CLI: `mediamolder face-detect`

```
mediamolder face-detect [flags] <input.{jpg,png,mp4,…}>
  --format jsonl|csv|json   output format (default jsonl)
  --output -                file, or - for stdout (default)
  --every N                 video: analyse every Nth frame (default 1)
  --max-frames N            cap frames analysed (0 = all)
  --embeddings              include the 128-d embedding per face (default off)
  --conf 0.5                detector confidence threshold (0 = package default)
  --models-dir PATH         directory of models (overrides MEDIAMOLDER_FACE_MODELS)
```

A still image yields one frame; video iterates frames, honouring `--every` and
`--max-frames`. Each detected face is one output record.

```bash
# Faces in a photo, pretty JSON:
mediamolder face-detect --format json portrait.jpg

# Every 10th frame of a clip, with embeddings, to a file:
mediamolder face-detect --every 10 --embeddings --output faces.jsonl clip.mp4

# CSV of boxes + landmarks:
mediamolder face-detect --format csv --output faces.csv group.png
```

### Output record

Each record (Go `face.Record`, mirrored as `FaceRecord` in the GUI) is:

```json
{
  "frame": 0,
  "pts": 0,
  "t": 0.0,
  "bbox": [x, y, w, h],
  "landmarks": [[lx,ly], [rx,ry], [nx,ny], [mlx,mly], [mrx,mry]],
  "score": 0.93,
  "embedding": [ /* 128 floats, only with --embeddings */ ]
}
```

`bbox` is `x, y, w, h` in source pixels; `landmarks` are left eye, right eye, nose,
and the left/right mouth corners. `t` is the frame time in seconds (omitted when the
stream has no usable time base). Embeddings are L2-normalised and **reproducible**
across machines for the same input (the analysis decodes through MediaMolder's
deterministic software path), so they cluster stably.

## Graph node: `face_detect`

Wire a video edge into a `face_detect` node. The frame passes through unchanged;
each face is emitted as `Metadata` — a `Detection` (box + score) plus the richer
`face.Record` (landmarks + optional embedding) under `custom.faces` — and, when
`output_file` is set, **written to a sidecar directly**. Detecting faces into a file
therefore needs nothing more than an input and this one node — an **analysis-only**
graph, no encoder and no muxer:

```json
{
  "schema_version": "1.1",
  "inputs": [
    { "id": "in0", "url": "photo.jpg",
      "streams": [{ "input_index": 0, "type": "video", "track": 0 }] }
  ],
  "graph": {
    "nodes": [
      { "id": "faces", "type": "go_processor", "processor": "face_detect",
        "params": { "every": 1, "conf": 0.5, "embeddings": true,
                    "output_file": "/abs/path/faces.jsonl" } }
    ],
    "edges": [
      { "from": "in0:v:0", "to": "faces:default", "type": "video" }
    ]
  },
  "outputs": []
}
```

Params (all optional): `every` (analyse every Nth frame, default 1), `conf`
(confidence threshold, default 0.5), `embeddings` (compute the 128-d vector, default
false), `models_dir` (override `MEDIAMOLDER_FACE_MODELS`), `output_file` (an
**absolute** path to write detections to) and `output_format` (`jsonl` (default),
`csv`, `timecodes`).

A still image is just a single-frame video stream, so the **same node** handles
images and video — only `every` differs. If you also want the (pass-through) video
out, add an encoder + muxer output and route `faces:default` to it; or, instead of
`output_file`, wire an `events` edge into a
[`metadata_file_writer`](go-processor-nodes.md).

### Example jobs

- [Example 65](../testdata/examples/65_face_detect_image.json) — **image**: point the
  input at a still (`.jpg`/`.png`); detect, align, and embed each face into
  `faces.jsonl`. Analysis-only — an input and one node, no media output.
- [Example 66](../testdata/examples/66_face_detect_video.json) — **video**: run every
  10th frame over a clip (embeddings off for speed), writing `faces.jsonl`.
  Analysis-only.

## GUI

In the visual editor, the **Face detection** node appears in the palette (search
"face", "recognition", "people"). Drop it after a video input, connect the video edge,
and use the Inspector to set frame sampling, confidence, the embeddings toggle, and an
optional models directory. Per-face boxes stream to the run panel as the job executes.
The GUI binary must be built with the node (`make build-gui-onnx`).

## Notes

- **Determinism / clustering.** Embeddings are L2-normalised and reproducible, which is
  what makes cross-file clustering and recognition stable — a host application can consume
  this `face` API to cluster faces across a library.
- **Performance.** Embedding roughly doubles per-face work; leave `--embeddings` off
  when you only need boxes/landmarks (the CLI's `DetectImage` fast path). Use `--every`
  / `every` to sub-sample long videos.

See [architecture/face-detection.md](architecture/face-detection.md) for the full
design, the package API, and the development plan.
