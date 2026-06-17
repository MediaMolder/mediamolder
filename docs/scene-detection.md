# Scene Detection

> Scene detection algorithms in MediaMolder are ported directly from
> **[PySceneDetect](https://github.com/Breakthrough/PySceneDetect)** by Brandon Castellano,
> licensed under the BSD 3-Clause License.
> See <https://scenedetect.com> for the upstream project.

MediaMolder ships seven scene-change detectors. The original `scene_change` processor uses
the same algorithm as FFmpeg's built-in `scdet` filter (fast, luma-only). The five
PySceneDetect processors are full ports of PySceneDetect v0.7's algorithms and accept the
same parameters as their Python counterparts. The seventh, `scene_change_mc`, is a
motion-compensated detector that additionally finds **dissolves and fades with
frame-accurate boundaries** — the others detect hard cuts only.

All processors run as [`go_processor`](go-processor-nodes.md) nodes directly inside the
media graph — no subprocess, no Python runtime, no inter-process overhead.

---

## Contents

- [Detector comparison](#detector-comparison)
- [The `scene_change` processor (scdet)](#the-scene_change-processor-scdet)
- [go-scene-detect processors](#go-scene-detect-processors)
  - [`scene_change_content`](#scene_change_content)
  - [`scene_change_adaptive`](#scene_change_adaptive)
  - [`scene_change_threshold`](#scene_change_threshold)
  - [`scene_change_hash`](#scene_change_hash)
  - [`scene_change_histogram`](#scene_change_histogram)
- [The motion-compensated detector (`scene_change_mc`)](#the-motion-compensated-detector-scene_change_mc)
- [Common parameters](#common-parameters)
- [Persisting events](#persisting-events)
- [CLI: `mediamolder go-scene-detect`](#cli-mediamolder-go-scene-detect)
- [Output format](#output-format)
- [Using detectors in the GUI](#using-detectors-in-the-gui)
- [Choosing a detector](#choosing-a-detector)

---

## Detector comparison

| Processor | Algorithm | Speed | Accuracy | Best for |
|-----------|-----------|-------|----------|----------|
| `scene_change` | MAFD / scdet (luma only) | ★★★★★ | ★★★ | Fast hard-cut detection; matches FFmpeg `scdet` output exactly |
| `scene_change_content` | Weighted HSV delta + optional Canny edges | ★★★★ | ★★★★★ | General-purpose; best accuracy on colour content |
| `scene_change_adaptive` | Rolling-window ratio over content scores | ★★★★ | ★★★★★ | High-motion clips, action sequences, fast pans |
| `scene_change_threshold` | Mean brightness threshold (fade detection) | ★★★★★ | ★★★ | Fades to/from black or white |
| `scene_change_hash` | Perceptual DCT hash, Hamming distance | ★★★★ | ★★★★ | Near-duplicate detection; robust to minor colour grading changes |
| `scene_change_histogram` | Y-channel histogram correlation | ★★★★★ | ★★★ | Fast secondary check; low memory, one histogram per frame |
| `scene_change_mc` | Motion-compensated lookahead (x264-style lowres inter/intra cost) | ★★ | ★★★★★ | **Dissolves and fades** with frame-accurate start/end bounds, plus hard cuts |

---

## The `scene_change` processor (scdet)

The original MediaMolder scene-change processor uses the same algorithm as FFmpeg's
[`scdet`](https://ffmpeg.org/ffmpeg-filters.html#scdet) filter:

- Computes the **Mean Absolute Frame Difference** on the luma (Y) plane.
- Score: `min(mafd, |mafd − prev_mafd|)` — suppresses gradual pans while catching hard cuts.
- Reads the Y plane directly from YUV frames (zero copy, zero conversion).

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| `threshold` | float64 | 10 | 0–100; min scdet score to trigger |
| `pts_threshold` | int64 | 0 | Min PTS gap to flag (0 = disabled) |

```json
{
  "id": "detect",
  "type": "go_processor",
  "processor": "scene_change",
  "params": { "threshold": 10 }
}
```

---

## go-scene-detect processors

All five processors below are Go ports of the algorithms in
[PySceneDetect v0.7](https://github.com/Breakthrough/PySceneDetect) by Brandon Castellano.
Copyright (C) 2014–2024 Brandon Castellano. Licensed under the BSD 3-Clause License.

### `scene_change_content`

**Algorithm** — `scenedetect/detectors/content_detector.py`

1. Convert each decoded frame from BGR to HSV.
2. Split into hue (H), saturation (S), luma/value (L) planes.
3. Optionally compute Canny edges on the luma plane and dilate with a kernel.
4. Compute a weighted mean pixel distance across the four components
   (`delta_hue`, `delta_sat`, `delta_lum`, `delta_edges`) divided by the sum of
   absolute weights → `content_val`.
5. If `content_val ≥ threshold` → candidate cut.
6. Pass through the flash filter (merge or suppress adjacent cuts).

Default weights: `(hue=1, sat=1, lum=1, edges=0)`. Luma-only mode sets `(0, 0, 1, 0)`.

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| `threshold` | float64 | 27.0 | Weighted HSV delta threshold. Lower = more sensitive. |
| `min_scene_len` | int / string | 15 | Minimum scene length: frames (`15`), seconds (`"0.6s"`), or timecode (`"00:00:00.600"`) |
| `luma_only` | bool | false | Use luma-only weights `(0, 0, 1, 0)` |
| `filter_mode` | string | `"merge"` | `"merge"` collapses adjacent cuts; `"suppress"` drops short scenes |
| `kernel_size` | int | 0 | Canny dilation kernel edge length; 0 = auto-compute from frame size |
| `frame_rate` | float64 | 25.0 | Stream frame rate (used when `min_scene_len` is in time units) |

```json
{
  "id": "detect",
  "type": "go_processor",
  "processor": "scene_change_content",
  "params": {
    "threshold": 27.0,
    "min_scene_len": "0.6s",
    "luma_only": false,
    "filter_mode": "merge"
  }
}
```

Event record on each detected cut:

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

---

### `scene_change_adaptive`

**Algorithm** — `scenedetect/detectors/adaptive_detector.py`

Two-pass wrapper around `scene_change_content`:

1. Compute `content_val` for each frame (same as `scene_change_content`).
2. Buffer the last `2 × window_width + 1` frames.
3. For the centre frame of the buffer, compute
   `adaptive_ratio = content_val[centre] / mean(content_val[others])`.
4. If `adaptive_ratio ≥ adaptive_threshold` AND `content_val ≥ min_content_val` AND
   minimum scene length has elapsed → emit cut at the centre frame.

Because the ratio is normalised by the rolling mean, gradual pans (where every frame
shows a similar delta) do not trigger cuts. Genuine scene boundaries spike the ratio.

The detector confirms a cut `window_width` frames after the centre frame of the
rolling window. MediaMolder compensates for that lookahead in the graph runtime:
metadata timestamps, segment cut signals, and encoder IDR requests are applied
to the centre/cut frame, not the later confirmation frame.

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| `threshold` | float64 | 3.0 | Adaptive ratio threshold. Lower = more sensitive. |
| `min_scene_len` | int / string | 15 | Minimum scene length |
| `window_width` | int | 2 | Half-width of the rolling window (full window = `2w+1` frames) |
| `min_content_val` | float64 | 15.0 | Minimum raw content score required for a cut |
| `luma_only` | bool | false | Use luma-only weights for the underlying content score |
| `frame_rate` | float64 | 25.0 | Stream frame rate |

```json
{
  "id": "detect",
  "type": "go_processor",
  "processor": "scene_change_adaptive",
  "params": {
    "threshold": 3.0,
    "min_scene_len": "0.6s",
    "window_width": 2,
    "min_content_val": 15.0
  }
}
```

Event record on each detected cut:

```json
{
  "scene_change": true,
  "detector": "adaptive",
  "frame_index": 1234,
  "timecode": "00:00:41.133",
  "pts": 3703200,
  "score": 12.5,
  "adaptive_ratio": 12.5,
  "content_val": 42.7
}
```

---

### `scene_change_threshold`

**Algorithm** — `scenedetect/detectors/threshold_detector.py`

Detects fades to/from black (or white) by tracking the mean brightness of each frame:

1. Compute `frame_avg = mean(all pixels in frame)` (mean of B, G, R channels).
2. Track transitions between "above threshold" (fade-in) and "below threshold"
   (fade-out) states.
3. A cut is emitted when transitioning back from fade-out to fade-in.
4. `fade_bias` skews the cut timestamp toward the fade-out end (−1.0) or fade-in
   end (+1.0); 0.0 = midpoint.
5. `method: "ceiling"` inverts the logic for fades to/from white.

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| `threshold` | float64 | 12.0 | Average pixel brightness threshold (0–255). Default detects fades to black. |
| `min_scene_len` | int / string | 15 | Minimum scene length |
| `method` | string | `"floor"` | `"floor"` = fade to black (`avg < threshold`); `"ceiling"` = fade to white (`avg > threshold`) |
| `fade_bias` | float64 | 0.0 | Cut position: −1.0 = fade-out start, +1.0 = fade-in start, 0.0 = midpoint |
| `add_final_scene` | bool | false | Always emit a cut at the last frame |
| `frame_rate` | float64 | 25.0 | Stream frame rate |

```json
{
  "id": "detect",
  "type": "go_processor",
  "processor": "scene_change_threshold",
  "params": {
    "threshold": 12.0,
    "method": "floor",
    "fade_bias": 0.0
  }
}
```

Event record on each detected cut:

```json
{
  "scene_change": true,
  "detector": "threshold",
  "frame_index": 1234,
  "timecode": "00:00:41.133",
  "pts": 3703200,
  "score": 8.3,
  "frame_avg": 8.3,
  "fade_type": "fade_out"
}
```

---

### `scene_change_hash`

**Algorithm** — `scenedetect/detectors/hash_detector.py`

Perceptual hashing via DCT (similar to pHash):

1. Convert frame to grayscale.
2. Resize to `(size × lowpass) × (size × lowpass)` — default 32 × 32.
3. Normalise pixel values to [0, 1].
4. Compute 2-D DCT of the resized image.
5. Keep only the top-left `size × size` block of DCT coefficients (low frequencies).
6. Binarise using the median of the low-frequency block.
7. Compare to previous frame's hash using Hamming distance / `size²`.
8. If normalised Hamming distance ≥ `threshold` → cut.

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| `threshold` | float64 | 0.395 | Normalised Hamming distance threshold (0–1). |
| `min_scene_len` | int / string | 15 | Minimum scene length |
| `size` | int | 16 | DCT low-frequency block edge length. Larger = more bits, slower. |
| `lowpass` | int | 2 | Resize multiplier. Input to DCT is `(size × lowpass)²` pixels. |
| `frame_rate` | float64 | 25.0 | Stream frame rate |

```json
{
  "id": "detect",
  "type": "go_processor",
  "processor": "scene_change_hash",
  "params": {
    "threshold": 0.395,
    "size": 16,
    "lowpass": 2
  }
}
```

Event record on each detected cut:

```json
{
  "scene_change": true,
  "detector": "hash",
  "frame_index": 1234,
  "timecode": "00:00:41.133",
  "pts": 3703200,
  "score": 0.42,
  "hash_dist": 0.42
}
```

---

### `scene_change_histogram`

**Algorithm** — `scenedetect/detectors/histogram_detector.py`

Compares adjacent-frame luma histograms using the Pearson correlation coefficient:

1. Convert frame to YUV; extract Y (luma) channel.
2. Build a normalised `bins`-bin histogram of Y (bins sum to 1.0).
3. Compare to the previous frame's histogram: `corr = Pearson(h1, h2)`.
4. If `corr ≤ (1 − threshold)` → cut.

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| `threshold` | float64 | 0.05 | Histogram divergence threshold (1 − Pearson correlation). |
| `min_scene_len` | int / string | 15 | Minimum scene length |
| `bins` | int | 256 | Number of histogram bins (must divide 256 evenly for best accuracy) |
| `frame_rate` | float64 | 25.0 | Stream frame rate |

```json
{
  "id": "detect",
  "type": "go_processor",
  "processor": "scene_change_histogram",
  "params": {
    "threshold": 0.05,
    "bins": 256
  }
}
```

Event record on each detected cut:

```json
{
  "scene_change": true,
  "detector": "histogram",
  "frame_index": 1234,
  "timecode": "00:00:41.133",
  "pts": 3703200,
  "score": 0.08,
  "hist_diff": 0.95
}
```

---

## The motion-compensated detector (`scene_change_mc`)

Motion compensation is one element of inter-frame prediction, a technique
used by modern video encoders. Blocks of a current frame can be represented
(predicted) by a block of the same size in another frame, offset by some 
motion vector. This is the most efficient way to code video. A less efficient 
way is to use intra-frame prediction, which takes pixels from adjacent blocks 
above and to the left of the current block, copying them at some angle (down
and to the right) into the current block to form the prediction. The "cost"
of either method of prediction can be represented by the bits required to
encode the residual error (the block represented by subtracting the predicted
block from the block of source pixels we are trying to encode). When 2 frames
are similar, inter-prediction has a low cost. When 2 frames are not similar,
the inter / intra prediction cost goes up.

`scene_change_mc` is the only detector that finds **gradual transitions —
cross-dissolves and fades — with frame-accurate boundaries**, in addition to
hard cuts. The PySceneDetect detectors react to per-frame content deltas and
fire on cuts. Other types of scene transitions, such as a dissolve, are more 
gradual, occurring over many frames with no single large delta, so they miss it. 
`scene_change_mc` instead asks a motion-aware question:
*how well can frame *j* be predicted from an earlier frame?*

It runs an x264-style **lookahead** on a quarter-resolution copy of each frame:
for several reference distances *k* it motion-compensates frame *j* from frame
*j−k* and forms the **inter/intra cost ratio** (how much better real
prediction does than coding the frame fresh). Across a dissolve from scene A
to scene B, prediction from pure-A into the blend fails progressively, so the
ratio rises into a saturated **plateau** whose flanks pin the transition's
start and end. The detector is batch-only: it accumulates the whole input,
runs a cheap coarse pass, then refines candidate regions with extra reference
distances, forward/reverse narrowing, and complementary signals (per-frame AC
texture energy and Y/U/V channel-mean steps) to drive the measured bounds
toward frame accuracy. See the [design notes](#how-it-works) below.

### Output

Hard cuts emit a single frame; dissolves and fades emit a span. In JSONL,
`frame_index` is the last fully-unblended frame **before** the transition and
`dissolve_frames` is the inclusive length to the first fully-unblended frame
**after** it (so a true *D*-frame blend reports `D + 2`). Both endpoints are
clean frames suitable as encoder anchors.

### Parameters

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| `coarse_prediction_distance` | int \| []int | `5` | Reference distance(s) used (with lag 1) in the cheap first pass over the whole video. A multi-scale set like `[30, 90]` localizes both short and long dissolves. Alias: `coarse_prediction_distances`. |
| `refined_prediction_distances` | []int | built-in menu | Menu of distances the refinement stage draws from near an estimated dissolve duration (e.g. `[5,15,30,45,60,75,90,105,120]`). |
| `threshold` | float64 | `0.50` | Hard-cut score-surface peak threshold. |
| `dissolve_min_len` | int | `2` | Minimum measured blend length to call a dissolve. |
| `dissolve_max_len` | int | `0` (= L/2) | Max transition width for dissolve/fade classification; raise for long dissolves. |
| `agg_window` | int | `5` | Score-surface aggregation window (larger aids slow dissolves). |
| `prediction_failure_threshold` | float64 | `0.985` | Inter/intra ratio level (0–1, near 1.0) treated as full prediction failure — the plateau saturation level. Per-distance overrides via `prediction_failure_thresholds`. |
| `fade_threshold` / `fade_white_threshold` | float64 | `0.10` / `0.90` | Normalised luma below/above which a frame is dark/bright (fade detection); `>1.0` disables white fades. |
| `fade_min_len` / `fade_max_len` | int | `3` / `120` | Min frames for a valid fade / max before treating as programme black/white. |
| `min_scene_len` | int/float64/string | `15` | Min frames or duration (`"0.5s"`) between detections. |
| `fullres_refine` | bool | `false` | Re-measure short/mid dissolve ends at full resolution via a windowed re-decode of `source_url` (sharper than the half-res lookahead on short blends). |
| `source_url` | string | — | Input path the detector re-opens when `fullres_refine` is on. |
| `cost_matrix_csv` | string | — | Debug: write the full augmented cost matrix (per-frame intra/luma/chroma/energy + per-distance inter/ratio, forward and reverse) to a CSV. |
| `frame_rate` | float64 | `25.0` | Stream frame rate, for timecode generation. |
| `output_file` / `output_format` | string | — / `"jsonl"` | Write results (`jsonl`, `csv`, `timecodes`). |

```json
{
  "id": "detect",
  "type": "go_processor",
  "processor": "scene_change_mc",
  "params": {
    "coarse_prediction_distance": [30, 90],
    "refined_prediction_distances": [1, 2, 3, 5, 15, 30, 45, 60, 75, 90, 105, 120],
    "dissolve_max_len": 150,
    "output_file": "scenes.jsonl"
  }
}
```

### How it works

The detector is a Go port of the x264 lowres lookahead (half-res SATD motion
estimation and intra cost) plus a dissolve-detection pipeline built on top:
multi-scale coarse plateau detection → region seeding → reference-distance
ladder refinement → ramp-foot cross-distance consensus → forward/reverse edge
narrowing → AC-energy and channel-mean witnesses. Because it ports x264's
mature ME/intra logic, the cost engine is reliable; the novelty is the
staged measurement architecture that turns those costs into frame-accurate
transition bounds. (The `lookahead/` package is the engine; the detector
itself is `processors/scene_change_mc.go`.)

---

## Common parameters

All five go-scene-detect processors share these parameters:

| Param | Type | Accepted forms | Description |
|-------|------|---------------|-------------|
| `min_scene_len` | int / float64 / string | `15`, `"0.6s"`, `"00:00:00.600"` | Minimum distance between cuts. Frames (int), seconds with `s` suffix, or `HH:MM:SS.mmm` timecode. |
| `frame_rate` | float64 | `25.0`, `29.97`, `23.976` | Stream frame rate used to interpret time-based `min_scene_len` values. The `mediamolder run` pipeline sets this automatically from the demuxed stream. |
| `output_file` | string | absolute path | Write cut events to this file. Format controlled by `output_format`. Leave unset to publish to the event bus only. |
| `output_format` | string | `"jsonl"` (default), `"csv"`, `"timecodes"` | Format written to `output_file`. `jsonl`: one JSON record per cut. `csv`: header row + one row per cut (Frame Index, Timecode, PTS, Score). `timecodes`: comma-separated cut timecodes, flushed at stream end. |

---

## Persisting events

Each detector emits an **event** on the pipeline event bus whenever it detects a cut.
Events carry the frame index, PTS, timecode, and a detector-specific score (see the
"Event record" blocks above). They are visible in the SSE stream at `/api/run/events`
and in the GUI **Observations** panel. Two mechanisms write events to disk:

### `output_file` param

Add `"output_file"` directly to the detector's `params`. Events are appended in
the format chosen by `"output_format"` (default `"jsonl"`).

| `output_format` | Writes |
|-----------------|--------|
| `"jsonl"` (default) | One JSON record per cut — same as the event bus record shown above |
| `"csv"` | Header row + one row per cut: `Frame Index,Timecode,PTS,Score` |
| `"timecodes"` | Comma-separated cut timecodes written in a single line at stream end |

```json
{
  "id": "detect",
  "type": "go_processor",
  "processor": "scene_change_content",
  "params": {
    "output_file": "/tmp/cuts.jsonl",
    "threshold": 27.0
  }
}
```

CSV example:

```json
{
  "params": {
    "output_file": "/tmp/cuts.csv",
    "output_format": "csv",
    "threshold": 27.0
  }
}
```

Timecodes example (e.g. for FFmpeg chapter markers):

```json
{
  "params": {
    "output_file": "/tmp/cuts.txt",
    "output_format": "timecodes",
    "threshold": 27.0
  }
}
```

### `events` edge to `metadata_file_writer`

For graphs where you want to keep the detector node's params clean, or where you need
to fan out the same events to multiple sinks, add a `metadata_file_writer` node and
wire an **`events`** edge from the detector to it. The engine opens the file and routes
every cut event the detector emits into it without any video data passing through the
wire.

```json
{
  "nodes": [
    {
      "id": "detect",
      "type": "go_processor",
      "processor": "scene_change_content",
      "params": { "threshold": 27.0 }
    },
    {
      "id": "cut_log",
      "type": "go_processor",
      "processor": "metadata_file_writer",
      "params": { "output_file": "/tmp/cuts.jsonl" }
    }
  ],
  "edges": [
    { "from": "in0:v:0",       "to": "detect:default", "type": "video"  },
    { "from": "detect:default", "to": "out0:v",         "type": "video"  },
    { "from": "detect",         "to": "cut_log",        "type": "events" }
  ]
}
```

The `events` edge carries no video data and is independent of video routing.
`metadata_file_writer` here acts as a pure sink — it exposes no video handles and
requires only `output_file`. The `output_format` param is also accepted and applies
the same `jsonl` / `csv` / `timecodes` logic.

> **Note:** The CLI `--output` flag (see below) writes a _scene list_ — one record
> per complete scene with start/end frame and timecode — which is a different format
> from the per-cut event records written by `output_file` or `events` edges in a
> pipeline.

---

## CLI: `mediamolder go-scene-detect`

The `go-scene-detect` subcommand runs scene detection on a media file outside a full
pipeline, writing the detected scene list to stdout or a file.

```
mediamolder go-scene-detect [flags] <input>
```

**Scope — which detectors the CLI exposes.** The subcommand offers only the
**five PySceneDetect *streaming* detectors** (`content`, `adaptive`,
`threshold`, `hash`, `histogram`). These consume the video one frame at a time
and emit a flat **scene list** — contiguous scenes split at cut points — which
the [output formats](#output-format) below all encode. The two non-streaming
detectors are intentionally **not** CLI flags and run only inside the media
graph (or the GUI):

- **`scene_change` (scdet)** — a `go_processor`; wire it into a pipeline as
  shown in [The `scene_change` processor](#the-scene_change-processor-scdet).
- **`scene_change_mc`** — its result is **transition *intervals*** (dissolve
  start→end, fade-in/out bounds), which a flat cut-point scene list cannot
  represent, and it is **batch + multi-pass** (staged refinement, optional
  full-resolution re-decode of the source). Run it as a `go_processor` and
  collect results via its own `output_file` or an `events` edge — see
  [the motion-compensated detector](#the-motion-compensated-detector-scene_change_mc)
  and [Persisting events](#persisting-events).

| Flag | Default | Description |
|------|---------|-------------|
| `--detector` | `content` | `content`, `adaptive`, `threshold`, `hash`, `histogram` (PySceneDetect streaming detectors only — see Scope above) |
| `--threshold` | detector default | Override detector threshold (0 = use detector default) |
| `--luma-only` | false | `content` / `adaptive`: use luma channel only |
| `--min-scene-len` | `0.6s` | Minimum scene length (frames, seconds, or timecode) |
| `--output` | `-` (stdout) | Write scene list to file (`-` = stdout) |
| `--format` | `jsonl` | Output format: `jsonl`, `csv`, `timecodes` |
| `--stats` | _(none)_ | Write per-frame detector statistics to a CSV file |
| `--downscale` | `0` (auto) | Downscale factor: `0` = auto-compute from frame width, `1` = disabled, `N` = N× |

### Examples

```sh
# Detect scenes with the default content detector.
mediamolder go-scene-detect input.mp4

# Use the adaptive detector with a custom threshold; write CSV.
mediamolder go-scene-detect --detector=adaptive --threshold=2.5 \
  --format=csv --output=scenes.csv input.mp4

# Fade/black detection; write only the cut timecodes.
mediamolder go-scene-detect --detector=threshold --threshold=15 \
  --format=timecodes input.mp4

# Write scene list to a JSONL file (default format is jsonl).
mediamolder go-scene-detect --output=scenes.jsonl input.mp4

# Write scene list as CSV.
mediamolder go-scene-detect --format=csv --output=scenes.csv input.mp4

# Perceptual hash detector; write JSONL scene list and per-frame stats.
mediamolder go-scene-detect --detector=hash --output=scenes.jsonl --stats=stats.csv input.mp4

# Disable auto-downscale (process at full resolution).
mediamolder go-scene-detect --downscale=1 input.mp4
```

---

## Output format

These three formats describe the **CLI scene list** (the streaming detectors'
cut-based output). The pipeline-only `scene_change_mc` writes its own `jsonl` /
`csv` / `timecodes` files that additionally carry the transition *type* and span
— see [its Output section](#output) — so a `scene_change_mc` JSONL record is not
the same shape as the records shown here.

### JSONL (default)

One JSON object per scene, one per line:

```json
{"scene":1,"start_frame":0,"start_timecode":"00:00:00.000","end_frame":149,"end_timecode":"00:00:05.960"}
{"scene":2,"start_frame":149,"start_timecode":"00:00:05.960","end_frame":312,"end_timecode":"00:00:12.480"}
```

### CSV

Matches [PySceneDetect's CSV output format](https://scenedetect.com/projects/PySceneDetect/en/latest/cli/output-formats/):

```
Scene Number,Start Frame,Start Timecode,End Frame,End Timecode
1,0,00:00:00.000,149,00:00:05.960
2,149,00:00:05.960,312,00:00:12.480
```

### Timecodes

Cut points only, comma-separated, for use with FFmpeg `-ss` flags or chapter markers:

```
00:00:05.960,00:00:12.480,00:00:24.160
```

---

## Using detectors in the GUI

All seven detectors are available as drag-and-drop nodes in the GUI palette under
**Processors**. `scene_change_content` and `scene_change_adaptive` appear in the
default *Common* view; the others require *All* mode or a text search (`hash`,
`histogram`, `threshold`, `scdet`, or — for `scene_change_mc` — `mc`, `dissolve`,
or `fade`).

For full GUI instructions — including how to set `output_file` from the Inspector,
how to wire an `events` edge to a `metadata_file_writer` sink node, and a quick-
reference detector table — see
[GUI guide → Scene detection processors](gui.md#scene-detection-processors).

---

## Choosing a detector

| Scenario | Recommended detector |
|----------|---------------------|
| Fast hard-cut detection; must match FFmpeg `scdet` | `scene_change` |
| General-purpose; highest accuracy | `scene_change_content` |
| Action/sports/high-motion clips; suppresses false positives from fast pans | `scene_change_adaptive` |
| Fade to black / fade to white transitions only | `scene_change_threshold` |
| Robust to minor colour grading or compression artefact changes | `scene_change_hash` |
| Fast secondary detector; low memory usage | `scene_change_histogram` |
| **Cross-dissolves / fades with frame-accurate start+end bounds** (plus hard cuts) | `scene_change_mc` |
| Need to match PySceneDetect Python output exactly | `scene_change_content` or `scene_change_adaptive` |

For most narrative video content, `scene_change_content` (threshold 27) or
`scene_change_adaptive` (threshold 3) will produce the best results. When processing
speed is the only constraint, `scene_change` (scdet) is the fastest option. When you
need **gradual transitions** — dissolves and fades — located with frame-accurate
boundaries (e.g. to set clean encoder cut points or chapter marks), reach for
`scene_change_mc`; it is the slowest and is pipeline/GUI-only (not a
`go-scene-detect` CLI flag — see [Scope](#cli-mediamolder-go-scene-detect)).

`scene_change_content` and `scene_change_adaptive` require a BGR→HSV conversion per
frame (via libswscale). `scene_change_threshold` and `scene_change_histogram` operate
directly on raw channel data and are noticeably faster. `scene_change_hash` adds a
DCT step but operates on a heavily downscaled image (default 32×32 pixels).
