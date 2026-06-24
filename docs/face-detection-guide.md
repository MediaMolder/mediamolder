# Face Detection Guide

MediaMolder can detect faces in images and video, align each face to a canonical
112×112 crop, and (optionally) compute a 128-dimensional embedding for
recognition/clustering. It is available three ways:

- **CLI** — `mediamolder face-detect <input>` for one-shot analysis of an image or
  video to JSONL / CSV / JSON.
- **Graph node** — the `face_detect` `go_processor`, to analyse video inside a
  media graph and emit per-face metadata on the event bus.
- **GUI** — the "Face detection" node in the visual editor's palette.

The detect → align → embed pipeline and its models live in the
[`face`](../face) package, behind a small, stable API (`Capable`, `Analyze`,
`AnalyzeImage`). The design and rationale are in
[architecture/face-detection.md](architecture/face-detection.md).

## Models & build

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
scripts/fetch-face-models.sh ./models       # downloads + SHA-256-verifies both models
export MEDIAMOLDER_FACE_MODELS="$PWD/models"
export ONNXRUNTIME_SHARED_LIBRARY_PATH=/path/to/libonnxruntime.so   # ONNX Runtime
```

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

Add a `go_processor` node and wire a video edge into it. The video passes through
unchanged; results are emitted as `Metadata` — a `Detection` per face (box + score)
plus the richer `face.Record` slice under `custom.faces`.

```json
{
  "graph": {
    "nodes": [
      { "id": "faces", "type": "go_processor", "processor": "face_detect",
        "params": { "every": 5, "conf": 0.5, "embeddings": false } }
    ],
    "edges": [
      { "from": "in0:v:0", "to": "faces:v", "type": "video" }
    ]
  }
}
```

Params (all optional): `every` (analyse every Nth frame, default 1), `conf`
(confidence threshold, default the package's 0.5), `embeddings` (compute the 128-d
vector, default false), `models_dir` (override `MEDIAMOLDER_FACE_MODELS`).

To persist results to a sidecar file, wire an `events` edge from `face_detect` into a
[`metadata_file_writer`](go-processor-nodes.md) node, exactly as with the scene
detectors and `whisper_stt` — or wrap it directly (`inner_processor: "face_detect"`),
as the example jobs below do.

### Example jobs

- [Example 65](../testdata/examples/65_face_detect_image.json) — **image** face
  detection: point the input at a still (`.jpg`/`.png`); detects, aligns, and embeds
  each face, writing `faces.jsonl`.
- [Example 66](../testdata/examples/66_face_detect_video.json) — **video** face
  detection: runs every 10th frame over a clip (embeddings off for speed), writing
  `faces.jsonl` and re-encoding the video.

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
