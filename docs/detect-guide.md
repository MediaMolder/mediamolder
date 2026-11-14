# People, ball, and face detection from Go

The [`detect`](../detect) package takes one decoded picture and returns the people, sports balls, and faces in it. It is a Go API for a host program that already has frames in memory. It is not a graph node and has no CLI.

It runs two models:

- A YOLO detect-task model with COCO classes, through the [`yolo`](yolo-guide.md) processor, for people and balls.
- The [`face`](face-detection-guide.md) package's detector, for faces.

MediaMolder ships neither model nor ONNX Runtime. The host supplies them.

## Build

The models run only in a build with the `with_onnx` tag:

```bash
go build -tags with_onnx ./...
```

Without the tag the package still compiles. `Available()` and `Open` return `detect.ErrUnsupported`.

## Use

```go
d, err := detect.Open(detect.Config{
	ObjectModel: "/models/yolo26n.onnx",
	FaceModels:  "/models/face",
})
if err != nil {
	return err
}
defer d.Close()

res, err := d.Detect(img, detect.Want{Persons: true, Balls: true, Faces: true})
if err != nil {
	return err
}
for _, p := range res.Persons {
	fmt.Println(p.X, p.Y, p.W, p.H, p.Score)
}
```

`img` is any `image.Image`. `Detect` calls are serialized inside the detector, so one `Detector` can be shared between goroutines.

## Config

| Field | Meaning |
|-------|---------|
| `ObjectModel` | Path to a YOLO detect-task ONNX file with COCO classes. Empty opens no object detector |
| `FaceModels` | Directory holding `yolov8n-face.onnx` and `sface.onnx`. Empty opens no face detector |
| `ONNXRuntime` | ONNX Runtime shared library path. Empty uses MediaMolder's discovery |
| `ObjectConf` | Score floor for people and balls. `<= 0` means `0.05` |
| `FaceConf` | Score floor for faces. `<= 0` means `0.6` |

At least one of `ObjectModel` and `FaceModels` must be set.

The object floor is low on purpose. A small or blurred ball scores well below a person, so the package returns weak candidates and the caller applies its own threshold. The face floor of `0.6` drops the detector's uncertain boxes.

The object model keeps the `yolo` processor's defaults: layout read from the file, bilinear letterbox, pad value 114. The model must have a fixed square input; see [Exporting a model](yolo-guide.md#exporting-a-model). `ObjectInputSize()` reports that input side, or `0` when no object model is open.

## Open errors

| Error starts with | Cause |
|-------------------|-------|
| `object model:` | The file is missing, is not a detect-task model, or ONNX Runtime did not load |
| `face models:` | A face file is missing or fails its SHA-256 pin, ONNX Runtime did not load, or a different face directory is already loaded |

Both face files must be present and match their pinned SHA-256, although this package never computes embeddings. Fetch them with `scripts/fetch-face-models.sh`.

The face detector is one per process and keeps the first directory that loaded. `Open` with a different `FaceModels` directory than an earlier successful `Open` in the same process returns an error. `Close` releases the object model only; the face detector stays loaded for the life of the process.

## What Detect returns

```go
type Result struct {
	Persons []Box `json:"persons"`
	Balls   []Box `json:"balls"`
	Faces   []Box `json:"faces"`
}

type Box struct {
	X, Y, W, H float64     // pixels, from the image's top-left corner
	Score      float64
	Mouth      *[2]float64 // faces only
}
```

- `Persons` holds COCO class `0` (person). `Balls` holds COCO class `32` (sports ball). Every other class is dropped.
- `Mouth` is the midpoint of the face's two mouth-corner landmarks. It is nil for a person or a ball.
- A box with no width or no height is dropped, and so is an object with a score of zero.
- A kind that was not asked for, or that found nothing, is an empty list, never null. An empty result encodes as `{"persons":[],"balls":[],"faces":[]}`.
- Coordinates are measured from the top-left of the submitted image, including when the image is a sub-image whose bounds do not start at `(0, 0)`.
- The object model runs once per call when either `Persons` or `Balls` is wanted.
- The same image and model files give the same result on every call.

`Detect` returns an error, and no partial result, when:

- the image is nil or has no pixels, or `Want` selects nothing;
- a wanted kind needs a model that is not open. It never substitutes another model and never returns an empty list in place of the error;
- either model fails;
- the detector is closed.

## Tests

The default build tests the package logic with scripted models:

```bash
go test -race ./detect/...
```

The `with_onnx` build adds tests against real models. Each skips when its input is absent:

| Variable | Value |
|----------|-------|
| `MEDIAMOLDER_DETECT_YOLO_MODEL` | A YOLO detect-task ONNX file. Defaults to `/models/yolo26n.onnx` |
| `MEDIAMOLDER_FACE_MODELS` | The face model directory |
| `MEDIAMOLDER_FACE_TEST_IMAGE` | A photo with one clearly visible person and face |
| `ONNXRUNTIME_SHARED_LIBRARY_PATH` | ONNX Runtime, when it is not in a standard location |

```bash
go test -race -tags with_onnx ./detect/... ./processors/... ./face/...
```

## See also

- [YOLO object detection](yolo-guide.md)
- [Face Detection Guide](face-detection-guide.md)
