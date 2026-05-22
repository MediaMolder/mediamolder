# PySceneDetect → Go Port & MediaMolder Integration Plan

Source: https://github.com/Breakthrough/PySceneDetect  
License: BSD 3-Clause — https://github.com/Breakthrough/PySceneDetect/blob/main/LICENSE  
Copyright (C) 2014 Brandon Castellano. All rights reserved.

---

## Background

MediaMolder already has a `scene_change` processor (`processors/scene_change.go`) that
implements the FFmpeg `scdet` algorithm (MAFD on the luma channel, min(mafd, |mafd-prev|)).
This covers fast cuts only, with no fade-in/out detection, no adaptive rolling-average mode,
no perceptual hash mode, and no histogram mode.

PySceneDetect v0.7 provides five distinct detection algorithms, per-frame statistics
persistence, a pluggable multi-detector `SceneManager`, and a well-tested public API.
Porting it to Go gives MediaMolder parity with the best open-source scene detection library,
integrated directly into the media graph with zero subprocess overhead and full access to the
decoded AVFrame stream.

---

## Target folder layout

All ported files live under `go_scene_detect/`. Each file carries the verbatim PySceneDetect
copyright notice from the corresponding Python source file, plus a reference to the BSD
3-Clause license URL above. 

```
go_scene_detect/
  LICENSE                          BSD 3-Clause text (verbatim from upstream)
  README.md                        Go port notes, upstream reference, build instructions
  doc.go                           Package-level godoc
  frame_timecode.go                FrameTimecode type (PTS + frame rate)
  scene_detector.go                SceneDetector interface + FlashFilter
  common.go                        SceneList, CutList, CropRegion, TimecodeLike, Interpolation
  scene_manager.go                 SceneManager (orchestrates detectors, manages frame buffer)
  stats_manager.go                 StatsManager (per-frame metric map, CSV/JSONL export)
  detectors/
    content_detector.go            ContentDetector  (HSV delta-weighted + optional Canny edges)
    adaptive_detector.go           AdaptiveDetector (rolling-window ratio, extends Content)
    threshold_detector.go          ThresholdDetector (fade in/out, FLOOR/CEILING mode)
    hash_detector.go               HashDetector     (perceptual DCT hash, Hamming distance)
    histogram_detector.go          HistogramDetector (YUV Y-channel histogram correlation)
  internal/
    colorspace.go                  BGR→HSV, BGR→YUV via libswscale CGO (or pure-Go fallback)
    canny.go                       Canny edge detection — port of cv2.Canny logic using CGO
    dct.go                         2-D DCT (pure Go; matches cv2.dct output for HashDetector)
    downscale.go                   Frame downscaling via libswscale (replaces cv2.resize)
    mean_pixel_distance.go         Mean absolute pixel distance (pure Go, replaces numpy.abs)
  processors/
    scene_change_content.go        processors.Processor wrapper → ContentDetector
    scene_change_adaptive.go       processors.Processor wrapper → AdaptiveDetector
    scene_change_threshold.go      processors.Processor wrapper → ThresholdDetector
    scene_change_hash.go           processors.Processor wrapper → HashDetector
    scene_change_histogram.go      processors.Processor wrapper → HistogramDetector
  cmd/
    cmd_go_scene_detect.go         `mediamolder go-scene-detect` CLI subcommand
  testdata/
    (reference frames and expected output for regression tests)
```

---

## Copyright header template

Every ported `.go` file must start with the copyright notice from its Python counterpart.
Example for `content_detector.go` (matches `scenedetect/detectors/content_detector.py`):

```go
//
//            PySceneDetect: Python-Based Video Scene Detector
//  -------------------------------------------------------------------
//     [  Site:   https://scenedetect.com                           ]
//     [  Docs:   https://scenedetect.com/docs/                     ]
//     [  Github: https://github.com/Breakthrough/PySceneDetect/    ]
//
// Copyright (C) 2018 Brandon Castellano <http://www.bcastell.com>.
// PySceneDetect is licensed under the BSD 3-Clause License; see the
// included LICENSE file, or visit one of the above pages for details.
// License URL: https://github.com/Breakthrough/PySceneDetect/blob/main/LICENSE
//
//
```

Files with no direct Python counterpart (e.g. `processors/scene_change_content.go`,
`cmd/cmd_go_scene_detect.go`) carry only the MediaMolder copyright.

---

## Detector algorithms — port notes

### 1. ContentDetector (`detect-content`)

**Python source:** `scenedetect/detectors/content_detector.py`  
Copyright (C) 2018 Brandon Castellano

**Algorithm:**
1. Convert BGR frame to HSV via `cv2.cvtColor(frame, cv2.COLOR_BGR2HSV)`.
2. Split into hue (H), saturation (S), luma/value (L) planes.
3. Optionally compute Canny edges on the luma plane and dilate with a kernel.
4. For each adjacent pair of frames compute weighted mean pixel distance across
   the four components: `delta_hue`, `delta_sat`, `delta_lum`, `delta_edges`.
5. Divide by sum of absolute weights → `content_val`.
6. If `content_val >= threshold` → candidate cut.
7. Pass candidate through `FlashFilter` (merges or drops cuts closer together
   than `min_scene_len`).

**Default weights:** `(delta_hue=1, delta_sat=1, delta_lum=1, delta_edges=0)`.  
**Luma-only mode:** `(0, 0, 1, 0)`.  
**Default threshold:** 27.0.

**Go port dependencies:**
- BGR→HSV: `internal/colorspace.go` using `libswscale` (`sws_scale` with
  `AV_PIX_FMT_BGR24` → `AV_PIX_FMT_HSV` if available, else manual conversion).
  Fallback: pure-Go per-pixel BGR→HSV using the well-known formula.
- Canny: `internal/canny.go` — port of `cv2.Canny` + `cv2.dilate`. Canny itself
  is a Gaussian-smoothed Sobel gradient + double-threshold hysteresis. This is
  ~200 lines of pure Go using the same sigma/median auto-threshold approach as the
  Python. The dilation pass is a simple morphological max-pool.
- `_mean_pixel_distance`: trivial pure-Go sum-of-absolute-differences / num_pixels.
- `_estimated_kernel_size`: `4 + round(sqrt(w*h)/192)`, odd-padded — direct port.
- `FlashFilter`: pure Go; MERGE mode collapses adjacent cuts, SUPPRESS mode drops
  short scenes.

**Relationship to existing `SceneChange` processor:**  
The existing processor computes `min(mafd, |mafd-prev_mafd|)` on luma only. That is
the scdet algorithm, not the PySceneDetect algorithm. They are complementary:
- `scdet` / `SceneChange`: very fast (luma only, no color, no edges), good for
  hard cuts, same as FFmpeg's built-in filter.
- `ContentDetector`: more accurate (HSV + optional edges), matches PySceneDetect
  reference outputs, slower.

Both will be kept. The new `processors/scene_change_content.go` wraps `ContentDetector`
and is registered as `"scene_change_content"`, distinct from the existing `"scene_change"`.

---

### 2. AdaptiveDetector (`detect-adaptive`)

**Python source:** `scenedetect/detectors/adaptive_detector.py`  
Copyright (C) 2021 Brandon Castellano

**Algorithm (two-pass):**
1. Run `ContentDetector.process_frame()` to get per-frame `content_val` scores.
2. Buffer the last `2*window_width + 1` frames.
3. For the centre frame of the buffer, compute
   `adaptive_ratio = content_val[centre] / mean(content_val[others])`.
4. If `adaptive_ratio >= adaptive_threshold` AND `content_val >= min_content_val`
   AND minimum scene length elapsed → emit cut at the centre frame.

This two-pass design handles fast camera pans where content changes every frame:
the ratio stays low, suppressing false positives, while genuine cuts spike the ratio.

**Default:** `adaptive_threshold=3.0`, `window_width=2`, `min_content_val=15.0`.

**Go port:** Extends `ContentDetector` (struct embedding). Ring buffer of
`(FrameTimecode, float64)` pairs, same min-length check as parent.

---

### 3. ThresholdDetector (`detect-threshold`)

**Python source:** `scenedetect/detectors/threshold_detector.py`  
Copyright (C) 2018 Brandon Castellano

**Algorithm (fade detection):**
1. Compute `frame_avg = mean(all pixels in frame)` (mean of R, G, B channels).
2. Track transitions between "above threshold" (fade-in) and "below threshold"
   (fade-out) states.
3. A cut is emitted when transitioning from fade-out back to fade-in, with optional
   `fade_bias` to skew the cut timestamp toward the fade-out or fade-in end.
4. `Method.FLOOR`: fade-out when avg < threshold (default — detects black frames).
   `Method.CEILING`: fade-out when avg > threshold (detects white flash).

**Default:** `threshold=12`, `Method.FLOOR`, `fade_bias=0.0`.

**Go port:** Pure Go. No color-space conversion needed (mean over all channels).

---

### 4. HashDetector (`detect-hash`)

**Python source:** `scenedetect/detectors/hash_detector.py`  
Copyright (C) 2022 Brandon Castellano

**Algorithm (perceptual hashing):**
1. Convert frame to grayscale.
2. Resize to `(size * lowpass) × (size * lowpass)` — default 32×32 with lowpass=2.
3. Normalise pixel values to [0, 1].
4. Compute 2-D DCT of the resized image.
5. Keep only top-left `size × size` block of DCT coefficients (low frequencies).
6. Binarise using the median of the low-frequency block.
7. Compare to previous frame's hash using Hamming distance / (size²).
8. If normalised Hamming distance ≥ threshold → cut. Default threshold = 0.395.

**Go port:**
- Grayscale conversion: pull luma plane from YUV frame, or `internal/colorspace.go`.
- Resize: `internal/downscale.go` using `libswscale`.
- 2-D DCT: `internal/dct.go` — standard row-column separable 1-D DCT. The input is
  float32 (matching `cv2.dct` which operates on float32). This is ~60 lines of pure Go.
- Hamming distance: `bits.OnesCount64` on XOR of uint64 words.

---

### 5. HistogramDetector (`detect-hist`)

**Python source:** `scenedetect/detectors/histogram_detector.py`  
Copyright (C) 2024 Brandon Castellano

**Algorithm:**
1. Convert frame to YUV, extract Y channel.
2. Build 256-bin histogram of Y, normalise so bins sum to 1.
3. Compare adjacent-frame histograms using correlation metric:
   `cv2.compareHist(h1, h2, cv2.HISTCMP_CORREL)` (Pearson correlation coefficient).
4. If `correlation ≤ (1 - threshold)` → cut. Default threshold = 0.05.

**Go port:** Pure Go. Y-channel extraction from YUV frame (or luma plane of AVFrame),
histogram construction, and Pearson correlation are all trivial math with no external
dependencies.

---

### 6. TransNetV2 (deferred)

**Python source:** `scenedetect/detectors/transnet_v2.py`  
Requires TensorFlow / ONNX inference.

This detector is out of scope for the initial port. It requires neural network inference
that would pull in a large ML runtime dependency. Deferred to a future phase once
MediaMolder has a general inference node (ONNX, TFLite, or Torch via CGO/subprocess).

---

## Core types

### FrameTimecode

```go
// Equivalent to Python's FrameTimecode.
type FrameTimecode struct {
    FrameNum  int64   // zero-based frame index
    FrameRate float64 // frames per second
}

func (t FrameTimecode) Seconds() float64
func (t FrameTimecode) Timecode() string           // HH:MM:SS.mmm
func (t FrameTimecode) Sub(other FrameTimecode) FrameTimecode
func (t FrameTimecode) Less(other FrameTimecode) bool
```

Internally MediaMolder uses PTS + AVRational timebase. `FrameTimecode` is a thin
convenience wrapper; the SceneManager receives frames tagged with timecodes from
the host pipeline context.

### SceneDetector interface

```go
type SceneDetector interface {
    // ProcessFrame receives the decoded frame and its timecode. Returns timecodes
    // where cuts were detected (may be empty, or behind current frame for
    // detectors with a lookahead buffer like AdaptiveDetector).
    ProcessFrame(timecode FrameTimecode, frame *av.Frame) ([]FrameTimecode, error)

    // PostProcess is called after the last frame. Returns any final cuts.
    PostProcess(timecode FrameTimecode) ([]FrameTimecode, error)

    // GetMetrics returns stat keys this detector produces.
    GetMetrics() []string

    // EventBufferLength returns how many frames behind the current frame a cut
    // may be reported. 0 for most detectors; window_width for AdaptiveDetector.
    EventBufferLength() int
}
```

### SceneManager

```go
type SceneManager struct {
    statsManager *StatsManager  // optional
    detectors    []SceneDetector
    cutList      []FrameTimecode
    // downscale, crop, interpolation settings...
}

func (sm *SceneManager) AddDetector(d SceneDetector)
func (sm *SceneManager) DetectScenes(ctx context.Context, frames <-chan FrameImg) (int, error)
func (sm *SceneManager) GetSceneList(startInScene bool) []Scene
func (sm *SceneManager) GetCutList() []FrameTimecode
```

`FrameImg` is a pair of `(FrameTimecode, *av.Frame)` delivered by the pipeline's
source handler. `DetectScenes` consumes the channel in a loop (no background goroutine
needed since MediaMolder's pipeline model already handles concurrency via handler goroutines).

### StatsManager

```go
type StatsManager struct {
    metrics map[int64]map[string]float64  // frame_num → key → value
    keys    map[string]struct{}
}

func (s *StatsManager) SetMetrics(timecode FrameTimecode, m map[string]float64)
func (s *StatsManager) GetMetrics(timecode FrameTimecode, keys []string) []float64
func (s *StatsManager) SaveToCSV(path string) error
func (s *StatsManager) SaveToJSONL(path string) error  // MediaMolder extension
```

`SaveToJSONL` extends the Python CSV API to match MediaMolder's existing
`scene_changes.jsonl` output convention.

---

## Image processing dependencies

| Python (OpenCV)                          | Go replacement                                                |
|------------------------------------------|---------------------------------------------------------------|
| `cv2.cvtColor(f, COLOR_BGR2HSV)`         | `internal/colorspace.BGRToHSV()` via libswscale CGO          |
| `cv2.cvtColor(f, COLOR_BGR2YUV)`         | `internal/colorspace.BGRToYUV()` via libswscale CGO          |
| `cv2.cvtColor(f, COLOR_BGR2GRAY)`        | Extract luma plane from AVFrame (zero-copy if YUV source)     |
| `cv2.Canny(lum, low, high)`              | `internal/canny.Canny()` — pure Go Canny implementation       |
| `cv2.dilate(edges, kernel)`              | `internal/canny.Dilate()` — morphological dilation, pure Go   |
| `cv2.dct(img)`                           | `internal/dct.DCT2D()` — pure Go 2-D DCT                     |
| `cv2.resize(img, size, INTER_AREA)`      | `internal/downscale.Resize()` via libswscale `sws_scale()`   |
| `cv2.calcHist([y], [0], None, [256]...)`  | `internal/histogram.Calc()` — pure Go bucket accumulation    |
| `cv2.compareHist(h1, h2, HISTCMP_CORREL)`| `internal/histogram.Correlation()` — Pearson, pure Go        |
| `numpy.abs`, `numpy.sum`, `numpy.mean`   | Pure Go loops over `[]uint8` / `[]float32`                   |
| `numpy.median`                           | `sort.Float64s` + midpoint, pure Go                          |

The libswscale CGO is already present in MediaMolder's `av/` package. No new build
dependencies are required.

---

## Integration with MediaMolder processors

Each detector becomes a `processors.Processor` registered by name. JSON config example:

```json
{
  "id": "scene_detect",
  "type": "processor",
  "processor": "scene_change_content",
  "params": {
    "threshold": 27.0,
    "luma_only": false,
    "min_scene_len": "0.6s",
    "filter_mode": "merge"
  }
}
```

Params map directly to the corresponding Python `__init__` arguments.

| Processor name              | Algorithm         | Key params                                         |
|-----------------------------|-------------------|----------------------------------------------------|
| `scene_change`              | MAFD / scdet      | `threshold`, `pts_threshold` (existing)            |
| `scene_change_content`      | ContentDetector   | `threshold`, `luma_only`, `weights`, `kernel_size`, `min_scene_len`, `filter_mode` |
| `scene_change_adaptive`     | AdaptiveDetector  | `adaptive_threshold`, `window_width`, `min_content_val`, `luma_only`, `min_scene_len` |
| `scene_change_threshold`    | ThresholdDetector | `threshold`, `method` (`floor`/`ceiling`), `fade_bias`, `add_final_scene`, `min_scene_len` |
| `scene_change_hash`         | HashDetector      | `threshold`, `size`, `lowpass`, `min_scene_len`    |
| `scene_change_histogram`    | HistogramDetector | `threshold`, `bins`, `min_scene_len`               |

Metadata emitted on each detected cut (all processors):

```json
{
  "scene_change": true,
  "detector": "content",
  "frame_index": 1234,
  "timecode": "00:00:41.133",
  "pts": 3703200,
  "score": 42.7,
  "content_val": 42.7
}
```

Detector-specific fields (e.g. `adaptive_ratio`, `hash_dist`, `hist_diff`,
`fade_type`) are added where applicable.

---

## CLI subcommand: `mediamolder go-scene-detect`

The command name `go-scene-detect` explicitly attributes the feature to PySceneDetect in
every invocation. The help text (`mediamolder go-scene-detect --help`) must include:

```
go-scene-detect uses algorithms ported directly from PySceneDetect by Brandon Castellano.
See https://github.com/Breakthrough/PySceneDetect and https://scenedetect.com for details.
```

New subcommand added to `cmd/mediamolder/main.go`:

```sh
mediamolder go-scene-detect [flags] <input.[mp4|mkv|...]>
```

| Flag               | Default   | Description |
|--------------------|-----------|-------------|
| `--detector`       | `content` | `content`, `adaptive`, `threshold`, `hash`, `histogram` |
| `--threshold`      | detector default | Override detector threshold |
| `--luma-only`      | `false`   | ContentDetector/Adaptive: use luma channel only |
| `--min-scene-len`  | `0.6s`    | Minimum scene length (e.g. `0.6s`, `15`, `00:00:00.600`) |
| `--output`         | stdout    | Write scene list to file (JSONL or CSV) |
| `--format`         | `jsonl`   | Output format: `jsonl`, `csv`, `timecodes` |
| `--stats`          | _(none)_  | Write per-frame statistics to this file (CSV) |
| `--start`          | `0`       | Start time (timecode or seconds) |
| `--end`            | _(end)_   | End time |
| `--downscale`      | `auto`    | Downscale factor (1 = disabled, 0 = auto) |

The command opens the input via libavformat/libavcodec (MediaMolder's existing demux/decode
stack), converts decoded frames to the required pixel format via libswscale, and feeds
them to a `SceneManager` instance. The output is the same `scene_changes.jsonl` format
already produced by the `scene_change` processor.

---

## Implementation phases

### Phase 1 — Core types and interface (2–3 days)
1. Create `go_scene_detect/` directory; add `LICENSE`, `README.md`, copyright header template.
2. Implement `FrameTimecode` with tests.
3. Define `SceneDetector` interface.
4. Implement `FlashFilter` (MERGE + SUPPRESS modes).
5. Implement `StatsManager` with `SetMetrics`, `GetMetrics`, `SaveToCSV`, `SaveToJSONL`.
6. Implement `common.go` (`Scene`, `SceneList`, `CutList`, `TimecodeLike`, `CropRegion`).
7. Unit tests for all core types.

### Phase 2 — Image math primitives (~2 days)
8. `internal/mean_pixel_distance.go` — pure Go, unit-tested against Python reference outputs.
9. `internal/colorspace.go` — BGR→HSV and BGR→YUV via libswscale CGO; unit tests comparing
   to manually computed reference values.
10. `internal/dct.go` — 2-D DCT; validate against known DCT outputs.
11. `internal/canny.go` — Canny + dilate; visual regression test with reference frames.
12. `internal/downscale.go` — libswscale resize; test aspect-ratio preservation.
13. `internal/histogram.go` — Y-histogram + Pearson correlation; unit tests.

### Phase 3 — ContentDetector (~2 days)
14. Port `ContentDetector` with all component weights, kernel-size estimation, edge detection.
15. Register as `processors.Processor` under `"scene_change_content"`.
16. Regression test: run against `testdata/` reference video and assert scene list matches
    the Python reference output (allow ±1 frame tolerance for downscale rounding).

### Phase 4 — AdaptiveDetector (~1 day)
17. Port `AdaptiveDetector` extending `ContentDetector` with ring-buffer rolling ratio.
18. Register as `"scene_change_adaptive"`.
19. Regression test: assert output matches Python reference for a high-motion clip.

### Phase 5 — ThresholdDetector (~1 day)
20. Port `ThresholdDetector` (FLOOR + CEILING, fade_bias, add_final_scene).
21. Register as `"scene_change_threshold"`.
22. Test: video with fade-to-black transitions; assert fade-in/out timecodes are detected.

### Phase 6 — HashDetector + HistogramDetector (~2 days)
23. Port `HashDetector` (grayscale → resize → DCT → binarise → Hamming).
24. Port `HistogramDetector` (YUV Y-histogram → correlation).
25. Register as `"scene_change_hash"` and `"scene_change_histogram"`.
26. Regression tests against Python reference outputs.

### Phase 7 — SceneManager (~2 days)
27. Implement `SceneManager` with `AddDetector`, `DetectScenes` (channel-based), frame
    buffer for lookahead, downscale/crop support.
28. Wire `SceneManager.GetSceneList()` to return `[]Scene` (start/end `FrameTimecode` pairs).
29. Add `mediamolder go-scene-detect` CLI subcommand (`cmd/mediamolder/cmd_go_scene_detect.go`).
30. Integration test: run `go-scene-detect` on a reference clip, assert output JSONL matches
    expected scene boundaries.

### Phase 8 — GUI integration (~1 day)
31. Add curation entries for the five new processor names in `internal/gui/curation.go`.
32. Add `scene_change_content` and `scene_change_adaptive` to the "Common" curation list.
33. Update Inspector to show a pre-populated threshold slider and detector dropdown for
    all `scene_change_*` processors.
34. Scene cut markers: emit `SceneChange` events on the event bus (already done by the
    existing processor); the GUI receives these via the SSE stream and can render frame
    markers on the run-panel timeline (future GUI item).

### Phase 9 — Documentation and cleanup
35. `docs/scene-detection.md`: document all five detectors, parameter reference, CLI usage,
    comparison table (speed vs accuracy), and the relationship to the existing `scdet`-based
    `scene_change` processor. The page must open with a clear attribution:
    > Scene detection algorithms in MediaMolder are ported directly from
    > **[PySceneDetect](https://github.com/Breakthrough/PySceneDetect)** by Brandon Castellano,
    > licensed under the BSD 3-Clause License.
36. Update `docs/go-processor-nodes.md` with new processor names.
37. Update `CHANGELOG.md`.
38. `go test ./go_scene_detect/...` and `go test ./processors/...` must pass.
39. Run `gofmt -s` and `goimports` across all new files.

---

## Open questions / risks

1. **libswscale BGR→HSV availability**: libswscale may not expose
   `AV_PIX_FMT_HSV` directly in all FFmpeg builds. Fallback: pure-Go per-pixel
   conversion (max + min + standard HSV formula). Need to benchmark and pick the
   faster path. The luma plane (Y) can always be obtained zero-copy from a YUV AVFrame.

2. **Canny edge detection accuracy vs OpenCV**: The Python ContentDetector's
   edge map uses `cv2.Canny` with per-frame adaptive low/high thresholds
   (`low = (1-sigma)*median`, `high = (1+sigma)*median`, sigma=1/3). The Go
   implementation must match this exactly or scene scores will diverge from the
   Python reference. Validate with per-frame score comparison before finalising.

3. **DCT float precision**: `cv2.dct` operates on float32. The Go DCT must use
   `float32` internally and produce values within float32 rounding of the reference.
   Test with the exact 32×32 matrix from a known reference frame.

4. **Downscale factor differences**: PySceneDetect auto-downscales to 256 px
   minimum width using `frame_width / effective_width`. libswscale uses integer
   target dimensions. The formula rounds differently between Python and Go for
   some resolutions — document the difference and define acceptable tolerance
   (±1 frame on scene boundaries).

5. **`min_scene_len` in seconds vs frames**: The Python API accepts either frames
   (int), seconds (float), or a timecode string. The Go `TimecodeLike` type must
   handle all three. Parse at `Init()` time using the stream's frame rate.

6. **Existing `scene_change` processor**: Do not modify or remove it. It is faster
   (luma MAFD only, no color conversion) and matches the FFmpeg scdet output exactly.
   Users who need PySceneDetect-compatible results use the new `scene_change_content`
   or `scene_change_adaptive` processors.

7. **TransNetV2**: Deferred. If MediaMolder gains a general ONNX inference node,
   TransNetV2 can be added as an optional detector in a later phase.
