# Face detection — full integration design & development plan

Status: **design + phased implementation**. Owner: face/CLI/GUI.
Related: [`face/`](../../face) package, [scene-detection.md](scene-detection.md),
[yolov8-guide.md](../yolov8-guide.md), [twelvelabs_integration.md](twelvelabs_integration.md).

## Motivation

Commit `feat(face): native face analysis API` landed a `face` package: a single,
build-tag-gated boundary that owns the optional ML dependency and exposes a small,
stable API — `Capable()`, `Analyze(path)`, and the `Face` type (bbox, 5 landmarks,
score, 128-d L2-normalised SFace embedding). The pure-Go core (YOLOv8-face output
parsing, NMS, similarity-transform alignment) compiles and is unit-tested in every
build; the real detect→align→embed pipeline is gated on `with_onnx`.

That package is the engine. It is **not yet reachable by a user**: there is no CLI
command and no pipeline/GUI node. This document specifies the full integration so a
user can detect faces in **images or video**, from the **command line** or the **GUI**.

**Consumer context.** The `face` package is consumed by host applications (for
example, a Digital Asset Manager's "People" pass) that call `face.Capable()` /
`face.Analyze(path)` through a thin seam, mapping `Face` onto their own type and
persisting the embeddings for clustering/recognition across a library. **The `face`
package API is therefore a contract.** Every addition in this plan is purely additive —
`Analyze`, `Capable`, `ErrUnsupported`, and the `Face` field shapes are unchanged, so
existing consumers keep working untouched and gain an optional frame-level entry point
for free.

## The core gap to close first — a frame-level seam

The committed API exposes only `Analyze(path string)`, which **re-opens the file and
decodes a single frame** ([`face/analyze_onnx.go:66`](../../face/analyze_onnx.go)).
That is correct for a still image but cannot:

- analyse a **video** (it only ever sees frame 0), or
- plug into the **pipeline**, where frames arrive already decoded as `*av.Frame`
  (re-decoding by path would be wrong and wasteful).

So step one is a frame-level seam in `face` that every integration layer builds on:

```go
// face.go (pure core, every build): the knobs.
type Options struct {
    ConfThresh float64 // detector confidence; <=0 ⇒ default 0.5
    IoUThresh  float64 // NMS IoU for raw exports; <=0 ⇒ default 0.45
    Embed      bool    // run SFace embedding (false ⇒ detect+align only, faster)
}

// analyze_onnx.go (with_onnx) + analyze_stub.go (!with_onnx): the entry points.
func AnalyzeImage(img image.Image) ([]Face, error)            // detect → align → embed
func DetectImage(img image.Image)  ([]Face, error)            // detect+align only, no embed
func AnalyzeImageOpts(img image.Image, o Options) ([]Face, error)
```

The existing `Analyze(path)` is refactored to `img := decodeRGBA(path); analyzeImage(img, embed=true)`,
so the path- and image-level entry points share one body. `*av.Frame → image.Image`
is already available via `frame.ToRGBA()` ([`av/frame_image.go:33`](../../av/frame_image.go)).
The detect/embed serialisation lock inside the `face` package makes all of these safe
to call concurrently.

### One serialization record, shared by CLI and processor

`Face` has no time dimension, so detections across video frames need a wrapper. It
lives in `face` as the single source of truth (mirrored in the frontend as
`FaceRecord`):

```go
// face.Record is a time-stamped detected face — the JSON unit emitted by both the
// CLI (face-detect) and the face_detect processor.
type Record struct {
    Frame     uint64    `json:"frame"`
    PTS       int64     `json:"pts"`
    Time      float64   `json:"t,omitempty"`     // seconds; 0/omitted when time_base unknown
    BBox      [4]int    `json:"bbox"`            // x, y, w, h (source px)
    Landmarks [5][2]int `json:"landmarks"`       // eyes, nose, mouth corners
    Score     float32   `json:"score"`
    Label     string    `json:"label,omitempty"` // "" until gallery matching (P3)
    Embedding []float32 `json:"embedding,omitempty"` // 128-d, only when requested
}
func (f Face) ToRecord(frame uint64, pts int64, t float64) Record
```

## Layered architecture

```
                 face pkg  (AnalyzeImage / DetectImage / Options / Record)
                          ── single ML boundary, with_onnx-gated ──
                 /                       |                        \
   CLI: face-detect            processor: face_detect             host-app seam
   (cmd_face_detect.go)        (go_processor node, with_onnx)      (Analyze, unchanged)
        |                              |
   stdout: jsonl/csv/json      *Metadata ──► pipeline event bus
                                              |            \
                                       GUI palette     sidecar file (output_file, self-written)
                                       + Inspector
                                       + RunPanel overlay
```

## Layer 1 — CLI: `mediamolder face-detect`

Mirrors the `go-scene-detect` precedent exactly
([`cmd/mediamolder/cmd_go_scene_detect.go`](../../cmd/mediamolder/cmd_go_scene_detect.go)):
open input → decode loop → write structured output.

```
mediamolder face-detect [flags] <input.{jpg,png,mp4,…}>
  --format jsonl|csv|json   output format (default jsonl)
  --output -                file or stdout (default -)
  --every N                 video: analyse every Nth frame (default 1)
  --max-frames N            cap frames analysed (0 = all)
  --embeddings              include 128-d vectors in output (default off → faster)
  --conf 0.5                detector confidence threshold (0 = package default)
  --models-dir PATH         overrides MEDIAMOLDER_FACE_MODELS
```

- New file `cmd/mediamolder/cmd_face_detect.go`; add
  `case "face-detect": return cmdFaceDetect(args[1:])` to the `run()` switch and a
  block to `usage()` in [`main.go`](../../cmd/mediamolder/main.go).
- A still image yields one frame from the loop; video iterates via `av.OpenDecoder`,
  honouring `--every` / `--max-frames`. Each `Face` is wrapped into a `face.Record`
  with frame index, PTS, and PTS×`time_base` seconds (from `StreamInfo.TimeBase`).
- **Always compiled in.** When `!with_onnx` or models are absent, `face.Capable()`
  is false and the command prints an actionable error
  (`requires a build with -tags with_onnx and MEDIAMOLDER_FACE_MODELS set …`). Unlike
  a processor (which simply is not registered when compiled out), a top-level command
  should exist in every build and degrade gracefully.

## Layer 2 — Pipeline processor: `face_detect`

Direct analog of the existing `yolo_v8` ONNX processor
([`processors/yolov8_onnx.go`](../../processors/yolov8_onnx.go)). New file
`processors/face_detect.go`, `//go:build with_onnx`:

```go
type FaceDetect struct { every uint64; conf float64; embed bool }

func (p *FaceDetect) Init(params map[string]any) error  // every, conf, embeddings, models_dir
func (p *FaceDetect) Process(f *av.Frame, ctx ProcessorContext) (*av.Frame, *Metadata, error) {
    if ctx.MediaType != av.MediaTypeVideo || (p.every>1 && ctx.FrameIndex%p.every!=0) {
        return f, nil, nil // pass through, no metadata
    }
    img, _ := f.ToRGBA()
    faces, _ := face.AnalyzeImageOpts(img, face.Options{ConfThresh: p.conf, Embed: p.embed})
    return f, p.metadata(faces, ctx), nil
}
func init() { Register("face_detect", func() Processor { return &FaceDetect{} }) }
```

**Metadata mapping.** `Detection` carries only Label/Confidence/BBox/TrackID
([`processors/processor.go:132`](../../processors/processor.go)) — no landmarks or
embedding. Emit **both**:

- `Metadata.Detections` — one `Detection{Label:"face", BBox:x1y1x2y2, Confidence}` per
  face → works with existing box/overlay consumers for free.
- `Metadata.Custom["faces"]` — `[]face.Record` → carries landmarks + embedding for
  rich consumers and the GUI overlay.

The processor embeds the shared `fileWriteHook` (as `scene_change` does), so an
`output_file` param makes it **self-write** its detections — no `metadata_file_writer`
node required. A complete face-detection job is then just an input wired into one
`face_detect` node with `"outputs": []`: an **analysis-only graph** (the engine
accepts zero outputs when every node is a `go_processor` —
`configHasOnlyGoProcessors`, `job/config.go`). No schema change (`NodeDef.Params` is
free-form `map[string]any`):

```json
{ "schema_version": "1.1",
  "inputs": [{ "id": "in0", "url": "photo.jpg",
    "streams": [{ "input_index": 0, "type": "video", "track": 0 }] }],
  "graph": {
    "nodes": [{ "id": "faces", "type": "go_processor", "processor": "face_detect",
      "params": { "every": 1, "embeddings": true, "output_file": "/abs/faces.jsonl" } }],
    "edges": [{ "from": "in0:v:0", "to": "faces:default", "type": "video" }] },
  "outputs": [] }
```

A still image decodes as a single-frame video stream, so the same node handles images
and video (only `every` differs) — **no image-specific engine path is needed**.
Alternatively, omit `output_file` and wire an `events` edge into a
`metadata_file_writer`, the same path `scene_change_*` and `whisper_stt` use.

## Layer 3 — GUI

The node auto-appears in the palette via `processors.Names()`. Wiring (all additive,
mirrors `scene_change_mc` / `whisper_stt`):

| Where | Change |
|---|---|
| [`internal/gui/api.go`](../../internal/gui/api.go) | `processorDescription` + `processorStreams` → `["video","events"]` for `face_detect` |
| [`internal/gui/curation.go`](../../internal/gui/curation.go) | friendly name "Face detection" + search aliases |
| [`internal/gui/curation_test.go`](../../internal/gui/curation_test.go) | add `face_detect` to the optional set (registered only with `with_onnx`) |
| [`frontend/src/components/Inspector.tsx`](../../frontend/src/components/Inspector.tsx) | `FaceDetectParams`: every / conf / embeddings / models_dir — falls through to the generic editor otherwise |
| [`frontend/src/lib/jobTypes.ts`](../../frontend/src/lib/jobTypes.ts) | `FaceRecord` type mirroring `face.Record` |

The run already streams `metadata` SSE events
([`frontend/src/lib/useJobRun.ts`](../../frontend/src/lib/useJobRun.ts) handles them),
so face boxes reach the RunPanel log with no transport change.

**Preview overlay (P2 stretch / follow-up):** a canvas over the video/image preview
that draws `Custom.faces[].bbox` + landmarks, keyed by frame/PTS. This is the one
genuinely new frontend component; everything above is form-filling and types. It
requires `make build-gui-static` to reflect.

## Build, models, packaging

- **Tag:** reuse `with_onnx` (the same tag `yolo_v8` uses — one ONNX switch for the
  project). The default build keeps the stub: zero ML dependency.
- **GUI build:** `make build-gui-whisper EXTRA_TAGS=with_onnx` already enables it; a
  dedicated `make build-gui-onnx` target is added for the no-whisper case.
- **Models:** `scripts/fetch-face-models.sh` fetches + SHA-pins both models; point
  `MEDIAMOLDER_FACE_MODELS` at that dir (or pass `--models-dir` / the `models_dir`
  param). Models stay out of the repo (`.gitignore`), consistent with the
  **no-binaries** design principle.
- **Licensing:** the YOLOv8-face detector is AGPL-3.0, loaded as runtime **data**, not
  linked — already this package's stated contract; swappable for YuNet (MIT) with no
  code change. SFace is Apache-2.0. Consumers never link the model code.

## Open product decisions (recommendation: ship the core, stage the rest)

1. **Recognition vs detection-only.** Embeddings are already computed but unused
   downstream. Ship **detect+embed** now (embeddings opt-in via `--embeddings` /
   `embeddings`); add **gallery matching** (a dir of named reference faces; set
   `Record.Label` when cosine-sim clears a threshold) and cross-video **clustering**
   into stable person IDs as P3. (A DAM-style consumer does library-level clustering on
   the embeddings, so MediaMolder's job is to emit good embeddings first.)
2. **Annotated output.** Sidecar metadata + live GUI overlay first; **burning boxes
   into an output video** (a second processor mode that draws on the frame instead of
   passing it through, via `DrawDetections` in `processors/helpers.go`) as a follow-up.

## Phasing

- **P1 — engine reachable.** `Options` / `AnalyzeImage` / `DetectImage` /
  `AnalyzeImageOpts` / `Record` seam (+ stub) → `face-detect` CLI → docs. *Shippable
  on its own.*
- **P2 — pipeline + GUI.** `face_detect` processor → GUI palette/Inspector/types →
  `build-gui-onnx`. Optional overlay component.
- **P3 — recognition.** Gallery matching, cross-video clustering (track IDs), and a
  burn-in output mode.

## Testing

- Pure-Go parsers/NMS/alignment already covered in `face/face_test.go` (every build).
- Add a **stub-contract test** (default build): `AnalyzeImage`/`DetectImage`/
  `AnalyzeImageOpts` return `ErrUnsupported`.
- Extend the gated integration test (`with_onnx`) to exercise `AnalyzeImage` /
  `DetectImage` and assert determinism alongside the existing `Analyze` path.
- Processor: a `with_onnx` test asserting per-frame `Metadata` shape (Detections +
  `Custom["faces"]`) and that `every` sub-samples.
- CLI: a small golden-output test on a still image (skipped when not `Capable()`).
- Run the targeted package first, then `go test ./...`; `cd frontend && npm test` +
  `tsc --noEmit` + `eslint` for the GUI changes.

## Documentation

- This page (design); a user-facing `docs/face-detection-guide.md` (build flags,
  models, CLI + node usage) linked from the README node/feature list.
- Update `CHANGELOG.md`, and cross-link from `docs/go-processor-nodes.md` and the GUI
  docs.

## Development checklist

- [ ] `face`: `Options`, `Record`, `AnalyzeImage`, `DetectImage`, `AnalyzeImageOpts`;
      refactor `Analyze`; stub mirrors; unit + integration tests.
- [ ] CLI: `cmd_face_detect.go`; wire `run()` switch + `usage()`.
- [ ] Processor: `processors/face_detect.go` (`with_onnx`) + register + test.
- [ ] GUI backend: `api.go` (description + streams), `curation.go`, `curation_test.go`.
- [ ] GUI frontend: `jobTypes.ts` `FaceRecord`, `Inspector.tsx` `FaceDetectParams`.
- [ ] Build: `Makefile` `build-gui-onnx` target.
- [ ] Docs: user guide, README link, CHANGELOG, cross-refs.
- [ ] `go test ./...` green (default); `-tags with_onnx` package tests green with models.
</content>
</invoke>
