# Proposal: blur / focus measurement

Status: Proposed — not implemented.
Scope: exposing FFmpeg's existing `blurdetect` filter, a new `blur`
package ported from it, an `av.Frame` adapter, a `blur_detect`
go_processor node, a `mediamolder blur` CLI subcommand for stills, and
the GUI node-catalog entry. Applies to video frames and to single
photographs decoded through the same path.

## 1. Motivation

MediaMolder can already answer "did the picture *change*"
(`scene_change_*`) but not "is the picture *any good*". Blur is the
first and most requested of the no-reference quality measures:

- **Ingest QC** — reject or flag soft-focus takes before they enter an
  edit, without a human scrubbing the rushes.
- **Thumbnail / poster-frame selection** — pick the crispest frame in a
  window instead of the first I-frame, which is frequently a
  motion-blurred pan.
- **Shot triage** — mark runs of frames where focus was lost (focus
  hunting, autofocus pumping, a rack focus that overshot).
- **Downstream gating** — let a `select` expression drop frames whose
  score is below a threshold.

Two pieces exist already, and neither is sufficient alone.

**`av.Frame.Sharpness()`** ([av/frame_image.go:132](../../av/frame_image.go#L132))
— the variance of the Laplacian of the luma plane, computed in C via
`frame_luma_lapvar`. Useful, but not a blur score:

| Property | `Sharpness()` today | Needed |
|---|---|---|
| Range | unbounded, arbitrary units | bounded, comparable |
| Contrast dependence | scales with contrast² | invariant |
| Resolution dependence | scales with pixel count | normalised or explicit |
| Content dependence | a flat sky scores ≈ a defocused face | flat content reported as *indeterminate*, not blurry |
| Noise / grain | film grain and mosquito noise raise the score | debiased |
| Locality | whole-frame only | per-region, so bokeh ≠ defocus |
| Blur kind | none | defocus vs motion blur |

So `Sharpness()` can only rank frames *within one clip at one size*.

**FFmpeg's `blurdetect` filter** (`libavfilter/vf_blurdetect.c`,
LGPL-2.1-or-later, Thilo Borgmann 2021) — implements Marziliano et al.
2002 and **is already compiled into the libavfilter MediaMolder links**.
It is reachable today from any job as an ordinary `filter` node and
publishes `lavfi.blur` frame metadata. Section 3 treats it as the
baseline this proposal must justify itself against; §4 ports it rather
than competing with it.

## 2. Goals

1. A bounded score with documented anchors, comparable across clips,
   resolutions, and cameras.
2. Reference-free (no "original sharp version" available — that is the
   whole point).
3. An interpretable companion number (mean edge width, in pixels) so a
   user can reason about a threshold rather than tune a magic constant.
4. Distinguish *globally soft* from *selectively soft* (shallow depth of
   field, bokeh, a deliberately defocused background).
5. Distinguish *defocus* from *motion blur*.
6. Say "I don't know" on frames with no measurable texture — black,
   fades, sky, fog, a title card — instead of reporting them as blurry
   or, as FFmpeg does, as `NaN`.
7. Cheap enough to run inline on every frame: target < 1 ms at 1080p,
   zero per-frame allocation.
8. Reuse FFmpeg's implementation wherever it is already correct, per the
   project charter. New code only where §3.2 identifies a real gap.

## 3. Baseline: FFmpeg's `blurdetect`

### 3.1 What it does

Per frame, per selected plane (default: plane 0 only):

1. Gaussian pre-blur to suppress noise — `ff_gaussian_blur_8`.
2. Sobel gradients and quantised directions — `ff_sobel_8`.
3. Non-maximum suppression, then hysteresis double-threshold
   (`low` = 15/255, `high` = 30/255) — the Canny edge map, shared with
   `edgedetect` via `libavfilter/edge_common.c`.
4. For every surviving edge pixel, walk along its gradient direction
   (horizontal / vertical / 45up / 45down) outward in both senses until
   the luma stops rising (resp. falling), capped at `radius` = 50. The
   span is that edge's **width**; diagonals scale by 0.7 ≈ √2/2.
5. Widths are averaged per block (`block_width` × `block_height`,
   defaulting to the whole frame). Blocks whose total width is below 2,
   or which contain no edges, are discarded as smooth.
6. Block means are sorted ascending and the **sharpest `block_pct` %**
   (default 80) are averaged. That mean is the result.

Output: `lavfi.blur` frame metadata (float, pixels, higher = blurrier),
a verbose per-frame log line, and a `blur mean:` line over the whole run
at uninit. Buffers are allocated once at `config_input`; the per-frame
path is allocation-free.

### 3.2 Where it falls short

| | `blurdetect` | This proposal |
|---|---|---|
| Method | Marziliano edge width only | edge width, plus re-blur normalisation, plus optional CPBD / lapvar |
| Output range | raw pixels, unbounded, scales with resolution | 0–100 anchored, resolution convention explicit |
| Polarity | higher = blurrier | **same** (§4.4) |
| Locality | blocks exist, pooled to one scalar | per-tile map exposed, spread, `SharpFrac` |
| Flat / black frames | all blocks dropped ⇒ `total_width / blkcnt` is `0/0` | `Confidence` + `indeterminate` class |
| Noise | fixed Gaussian pre-blur, no estimate | Immerkær σ estimate and debias, σ reported |
| Blur kind | directions computed, then discarded | anisotropy + angle ⇒ defocus vs motion blur |
| Pixel formats | 8-bit planar only — no NV12, no p010, no 10/12-bit | 8/10/12/16-bit, NV12, limited vs full range |
| Interlace | none | per-field measurement |
| Aggregation | one mean line to the log | JSONL/CSV, soft-focus runs with timecodes, per-shot join |
| Stills | usable, no ergonomics | dedicated CLI |

Two robustness notes to carry into the port:

- **`NaN` on edge-free frames.** When every block is discarded,
  `blkcnt` is 0 and `calculate_blur` returns `0.0 / 0`. The `NaN` lands
  in `lavfi.blur` *and* accumulates into `blur_total`, poisoning the run
  mean from that frame onward. Black frames, fades to black, and slates
  all hit this. Confirm with a black-frame test, then fix in the port
  and send the fix upstream.
- **`blks` sizing.** The array is sized from luma dimensions
  (`inlink->w / block_width`), but chroma planes iterate with
  `AV_CEIL_RSHIFT`-derived block sizes over subsampled extents. On small
  odd geometries with `planes` ≠ 1 the block count can exceed the
  allocation. The port should size per plane.

Neither affects the default configuration on ordinary content, which is
why the filter has been fine in the field — but an ingest-QC tool runs
over exactly the black frames and slates that trigger the first one.

## 4. Architecture

Four layers, each independently testable:

```
blur/                    pure Go, no cgo, no av dependency
  ├── Analyze(Luma, Options) (Result, error)      ported edge-width core + additions
  ├── FromFrame(*av.Frame, Options)               planar-Y adapter (cgo via av)
  └── FromImage(image.Image, Options)             stills / PNG / JPEG
processors/blur_detect.go   graph node, registered "blur_detect"
cmd/mediamolder blur        CLI for photos and single frames
frontend nodeCatalog        GUI nodes + inspector params
```

`blur` deliberately does **not** import `av`: it takes a plain luma
view, so the unit tests run on synthetic buffers with no decoder, no
cgo, and no test media. `FromFrame` lives in the same package but in a
`_av.go` file that owns the only dependency edge.

### 4.1 Luma extraction

```go
// Luma is a borrowed, non-owning view of a single-channel plane.
type Luma struct {
	Pix       []byte // 8-bit, or little-endian 16-bit when Depth > 8
	Stride    int    // bytes per row
	W, H      int
	Depth     int    // 8, 10, 12, 16
	FullRange bool   // false ⇒ limited/TV range (16..235 at 8-bit)
	Interlace int    // 0 = progressive, 1 = TFF, 2 = BFF
}
```

- Planar YUV (`yuv420p`, `yuv422p`, `yuv444p`, `p010`, …): plane 0
  aliased directly via [`Frame.Plane(0)`](../../av/frame_planes.go#L75) /
  `Linesize(0)` — zero copy, zero conversion.
- Semi-planar (`nv12`/`nv21`): plane 0 is still pure luma. Same path.
  FFmpeg's filter rejects these outright; there is no reason to.
- RGB / packed / hardware frames: fall back to a `GRAY8` conversion
  through the existing swscale wrapper, once, into a reused scratch
  buffer.
- Depth and colour range come from the pixel descriptor. `av` does not
  yet export `comp[0].depth`; add a small `Frame.LumaDepth()` wrapper
  next to the existing `av_pix_fmt_desc_get` uses in
  [av/frame_planes.go](../../av/frame_planes.go).
- Interlaced/field-based frames are measured **per field** and the
  sharper field wins. Measuring a combed frame as a single raster reads
  the comb teeth as high-frequency detail and reports a badly
  motion-blurred field pair as tack sharp.

### 4.2 Normalisation

Luma is mapped to `[0,1]`: `(Y − 16)/219` for limited range, `Y/2^depth`
for full range, then clamped. This makes every downstream quantity
contrast-relative rather than code-value-relative and makes 8-bit and
10-bit sources produce the same score. FFmpeg's fixed `low`/`high`
thresholds in 8-bit code values are precisely what confine it to 8-bit;
normalising first removes the restriction.

Two resolution conventions, selected by `Options.Normalize`:

- `"native"` (default) — measured on the source pixel grid. Answers "is
  this image soft *for its own resolution*". A defocused 4K frame is
  blurry here.
- `"display720"` — area-average down to 720 lines first. Answers "will
  this look soft when displayed". The same defocused 4K frame may pass,
  because downscaling hides the softness.

Both are legitimate and they disagree; the doc must say which is which
rather than pick one silently. Default is `native` because ingest QC is
the primary use case.

## 5. The measures

### 5.1 Primary: edge width, ported from `vf_blurdetect`

*A no-reference perceptual blur metric*, Marziliano et al., ICIP 2002.

The Canny front end (`ff_gaussian_blur_8`, `ff_sobel_8`,
`ff_non_maximum_suppression`, `ff_double_threshold` from
`edge_common.c`) and the `edge_width` walk of §3.1 port directly to Go.
Keep, unchanged in behaviour:

- the four quantised walk directions and the 0.7 diagonal correction;
- the `radius` cap on the walk;
- the "block total width below 2 ⇒ smooth, discard" rule;
- the sharpest-`block_pct` % trimmed mean.

That last one is FFmpeg's answer to the bokeh problem, and it is a good
one: averaging only the sharpest blocks asks "how sharp is this image
where it is *trying* to be sharp". §6 generalises it rather than
replacing it.

Changes: per-plane buffer sizing, normalised thresholds, the `NaN` fix,
and retention of the per-block widths instead of collapsing them
immediately.

Output: `EdgeWidthPx` — mean edge spread in pixels, directly comparable
to `lavfi.blur`. This is the interpretable number. "Edges are spread
over 7.4 pixels" is a claim an operator can check by eye and set a
threshold against; "blurriness 0.61" is not.

### 5.2 Normalisation: re-blur (Crété-Roffet)

*The blur effect: perception and estimation with a new no-reference
perceptual blur metric*, Crété-Roffet et al., SPIE HVEI 2007.

Edge width in pixels is not comparable across resolutions: the same
lens softness measures roughly twice as many pixels at 4K as at 1080p.
The re-blur metric supplies the bounded, resolution-insensitive
companion that turns it into a score.

Intuition: blurring a *sharp* image changes it a lot; blurring an
*already blurred* image barely changes it. So compare the local
variation of the image against the local variation of a deliberately
re-blurred copy. For each direction `d ∈ {h, v}`:

1. `B_d = F * box_d(9)` — a 1×9 (resp. 9×1) box blur.
2. `D_F = |F(i,j) − F(i,j∓1)|` and `D_B = |B_d(i,j) − B_d(i,j∓1)|`,
   the absolute neighbour differences of original and re-blurred.
3. `V = max(0, D_F − D_B)` — variation *lost* to the re-blur.
4. `b_d = (ΣD_F − ΣV) / ΣD_F`.

Then:

- `Blurriness = max(b_h, b_v)` ∈ [0,1]; 0 = sharp, 1 = fully blurred.
- `Anisotropy = |b_h − b_v| / max(b_h, b_v)` ∈ [0,1].

Two separable box filters and two difference passes — cheap enough to
run alongside the edge pass. Global contrast changes cancel out of the
ratio, which is exactly what breaks variance-of-Laplacian.

**Noise debias.** Sensor noise, film grain, and mosquito noise all
inflate `D_F` and therefore read as sharpness. Estimate σ with
Immerkær's fast Laplacian-MAD estimator (*Fast noise variance
estimation*, CVGIP 1996) and subtract the expected noise contribution
from `D_F` before step 3. Report `NoiseSigma` so the user can see when
this correction was large. This also improves on FFmpeg's fixed
Gaussian pre-blur, which over-smooths clean sources (inflating widths)
and under-smooths grainy ones.

### 5.3 Score mapping

```
Score = 100 · clamp((Blurriness − a) / (b − a), 0, 1)
```

`Score` is **blurriness on 0–100, where 0 is crisp** — the same polarity
as `lavfi.blur` and as `scene_change`'s "higher = more of the thing
being detected". `a` and `b` are calibration anchors fixed once against
the σ-ladder corpus (§9.1) and then frozen; expected around `a ≈ 0.25`,
`b ≈ 0.65`. They are constants in the source with the derivation
recorded next to them, not tunable params — a tunable normalisation
would destroy the cross-clip comparability that is the entire point.

### 5.4 Selectable methods

`Options.Method` chooses what drives `Score`; `Result` always carries
every cheap field regardless.

| Value | Cost | Notes |
|---|---|---|
| `edge` | 1× | Default. §5.1 + §5.2 normalisation. `EdgeWidthPx` is parity-tested against `lavfi.blur`. |
| `reblur` | 0.4× | §5.2 alone, skipping the edge pass. Cheapest bounded score. |
| `cpbd` | ~2× | Cumulative Probability of Blur Detection (Narvekar & Karam, TIP 2011): per-edge probability that the blur is above the just-noticeable threshold for that edge's local contrast, accumulated to `P(width ≤ JNB)`. Reuses the §5.1 edge pass. Better perceptual correlation on mixed content. See §11 before shipping. |
| `lapvar` | 0.5× | The existing `Frame.Sharpness()`, contrast-normalised (`Σ∇²F² / Σ(F−μ)²`). Unbounded, same-clip ranking only. Retained for continuity and for cheap "pick the crispest of these N frames" loops. |

## 6. Locality: the tile map

FFmpeg pools blocks to a single scalar and throws the blocks away. Keep
them — a single global number cannot separate these two cases:

- a portrait with a sharp subject and a deliberately blurred background
- a frame where the operator missed focus entirely

Both have a lot of blurred area. So `Analyze` computes the primary
measure on a grid (`Options.TileGrid`, default 8×5) as well as
globally, and derives:

- `Confidence` per tile from its edge count and texture energy. Tiles
  below the floor (flat sky, black bars, a blown highlight) are
  **excluded** — exactly as FFmpeg's "total width < 2" rule does, but
  recorded rather than silently dropped.
- Global verdict from the trimmed mean of the retained tiles (FFmpeg's
  `block_pct` rule, generalised): "how soft is this frame where it is
  trying to be sharp?" This is the number that drives `Class`.
- `SharpFrac` — fraction of retained tiles judged sharp.
- `TileStdDev` — high ⇒ mixed (shallow depth of field), low ⇒ uniform
  (global defocus). This is the distinction the single scalar cannot
  make.

`Options.TileGrid = {1,1}` reduces this to the classical global metric
for users who want exactly that.

Frame-level `Confidence` is the retained-tile fraction. When it falls
below a floor the class is `indeterminate` and the score should be
ignored, not treated as 0 or 100 — the case FFmpeg returns `NaN` for.
Fades, slates, and black frames land here, which is the correct answer.

## 7. Defocus vs motion blur

Motion blur is directional; defocus is (near-)isotropic. `Anisotropy`
and `AngleDeg` from §5.2 — extended to the four Sobel orientations the
edge pass already computes when the two-direction split is ambiguous —
give the shape of the point-spread function cheaply. FFmpeg computes
those directions and discards them.

For video, cross-check against motion. The [`lookahead`](../../lookahead/)
package already computes low-resolution motion vectors for
`scene_change_mc`; when a `blur_detect` node sits in a graph that has
them, or when `blur_detect` is asked to compute its own coarse ME, the
classification becomes:

| Blur | Motion | Class |
|---|---|---|
| low | any | `sharp` |
| moderate | any | `soft` |
| high, isotropic | low | `defocused` |
| high, anisotropic, direction ≈ motion direction | high | `motion_blur` |
| any | any, `Confidence` below floor | `indeterminate` |

The distinction matters editorially: motion blur on a whip pan is
expected and acceptable; the same score from a static locked-off shot
is a defect.

## 8. Public surfaces

### 8.1 Package API

```go
package blur

type Method string // "edge" | "reblur" | "cpbd" | "lapvar"

type Options struct {
	Method     Method
	TileGrid   [2]int  // cols, rows; default {8,5}; {1,1} = global only
	BlockPct   int     // trimmed-mean pooling, FFmpeg's block_pct; default 80
	Radius     int     // edge-walk cap, FFmpeg's radius; default 50
	Low, High  float64 // normalised hysteresis thresholds; default 15/255, 30/255
	Subsample  int     // pixel stride; 0 = auto (cap working set at ~1 MP)
	IgnoreBars bool    // detect and exclude letterbox / pillarbox
	ROI        *image.Rectangle
	Normalize  string  // "native" (default) | "display720"
}

type Tile struct {
	X, Y        int
	Blurriness  float64
	EdgeWidthPx float64
	Confidence  float64
}

type Result struct {
	Score       float64 // 0–100 blurriness; 0 = crisp
	Blurriness  float64 // 0–1, raw re-blur measure
	EdgeWidthPx float64 // mean Marziliano edge spread — comparable to lavfi.blur
	LapVar      float64 // contrast-normalised variance of Laplacian
	Anisotropy  float64 // 0–1; high ⇒ directional blur
	AngleDeg    float64 // dominant blur direction when anisotropic
	NoiseSigma  float64 // estimated noise σ (normalised units)
	Confidence  float64 // 0–1; low ⇒ Score is not meaningful
	SharpFrac   float64 // fraction of retained tiles judged sharp
	TileStdDev  float64 // spread across retained tiles
	Class       string  // sharp | soft | defocused | motion_blur | indeterminate
	Tiles       []Tile
}

func Analyze(y Luma, o Options) (Result, error)
func FromFrame(f *av.Frame, o Options) (Result, error)
func FromImage(img image.Image, o Options) (Result, error)
```

`av.Frame.Sharpness()` is left exactly as it is — same signature, same
value — with a doc-comment pointer to `blur.FromFrame` for the bounded
score. No existing caller changes.

### 8.2 Graph node `blur_detect`

A pass-through analysis processor in the shape of
[processors/frame_info.go](../../processors/frame_info.go), with the
file-output conventions of
[processors/scene_change_mc.go](../../processors/scene_change_mc.go).

Params:

| Param | Type | Default | Meaning |
|---|---|---|---|
| `method` | string | `edge` | §5.4 |
| `tile_grid` | `[2]int` or `"8x5"` | `8x5` | `1x1` for global-only |
| `block_pct` | int | 80 | trimmed-mean pooling (FFmpeg-compatible) |
| `radius` | int | 50 | edge-walk cap (FFmpeg-compatible) |
| `low`, `high` | float | 15/255, 30/255 | hysteresis thresholds (FFmpeg-compatible) |
| `threshold` | float | 60 | `Score` at or above which a frame is reported blurry |
| `min_run` | int | 5 | minimum consecutive blurry frames to emit a soft-focus run |
| `normalize` | string | `native` | §4.2 |
| `subsample` | int | 0 (auto) | pixel stride |
| `ignore_bars` | bool | true | exclude letterbox / pillarbox |
| `roi` | `[4]int` | — | `x,y,w,h` measurement window |
| `log_every` | int | 1 | emit per-frame metadata every N frames |
| `emit_frame_metadata` | bool | false | also stamp `AVFrame` metadata |
| `output_file` | string | — | results path |
| `output_format` | string | `jsonl` | `jsonl` \| `csv` |

The FFmpeg-compatible params keep the filter's names and defaults, so a
user moving from `blurdetect` to `blur_detect` carries their tuning
across unchanged.

Per-frame `Metadata.Custom`: `blur_score`, `blurriness`,
`edge_width_px`, `anisotropy`, `class`, `confidence`, `frame_index`,
`pts`. `Metadata.QualityScore` carries `100 − Score` so the existing
generic quality plumbing shows something sane.

With `emit_frame_metadata`, stamp **`lavfi.blur`** with `EdgeWidthPx` —
the same key and units FFmpeg uses, so `select` / `metadata`
expressions written against the filter keep working unchanged — plus
`mm.blur.score`, `mm.blur.class`, and `mm.blur.confidence` for the
extras, via [av/metadata.go](../../av/metadata.go#L287). That makes it
possible to drop every frame softer than a threshold, or keep only the
crispest frame of each second, without a second pass.

`Close()` writes the aggregate: per-shot statistics (joined to
`scene_change_*` output when present in the same job), and the list of
soft-focus runs longer than `min_run` with their in/out timecodes.
Progress and result lines post to the event bus so the GUI log panel
shows them.

### 8.3 CLI for stills

```
mediamolder blur photo.jpg frame.png shot.tif [--json] [--method cpbd] [--tiles 8x5]
```

Decodes each file (stdlib codecs via `FromImage`, everything else
through the normal demux path) and prints one line per file, or a JSON
array with the full `Result` under `--json`. No graph, no config file —
this is the "is this photo blurry" entry point, and the fastest route to
real-world feedback on the §5.3 anchors.

### 8.4 GUI

A `blur_detect` entry in
[frontend/src/lib/nodeCatalog.ts](../../frontend/src/lib/nodeCatalog.ts)
with typed controls for the params above, and — because the value of
this node is visual — a results strip in the inspector showing the
score sparkline over the job and the tile heat map for the currently
previewed frame.

## 9. Validation

### 9.1 Calibration corpus

A committed set of small still frames (mixed content: faces, text,
foliage, low-contrast, high-grain) each rendered at a ladder of known
Gaussian σ ∈ {0, 0.5, 1, 2, 3, 4, 6, 8} and a ladder of known linear
motion blurs. The ladder is generated by a committed script from the
committed originals, so the corpus is reproducible and small.

### 9.2 Test matrix

| Property | Test |
|---|---|
| **Parity with FFmpeg** | `EdgeWidthPx` matches `blurdetect`'s `lavfi.blur` within tolerance over the corpus at matching params — the port's primary correctness gate |
| Monotonicity | `Score` strictly increases with σ across the whole ladder, for every image in the corpus |
| Contrast invariance | `Y' = αY + β` for α ∈ [0.5, 1.5] shifts `Score` by less than a fixed tolerance |
| Gamma invariance | ±0.3 gamma shift within tolerance |
| Noise robustness | additive Gaussian noise up to σ=8/255 does not drop `Score` below tolerance; `NoiseSigma` tracks the injected value |
| Compression | JPEG q=95…q=40 does not invert the ordering of the σ ladder |
| Directionality | linear motion blur ⇒ `Anisotropy` high, `AngleDeg` within ±10° of truth |
| Locality | synthetic sharp-subject-over-blurred-background composite is **not** flagged blurry; fully blurred version of the same frame is |
| Degenerate | flat / black / pure-noise / single-colour ⇒ `Class = indeterminate`, low `Confidence`, **never `NaN`** (regression against §3.2) |
| Formats | `yuv420p`, `yuv422p`, `yuv444p`, `nv12`, `gray`, `p010` (10-bit), `rgb24` all agree within tolerance on the same source image |
| Range | limited-range and full-range encodes of the same image agree |
| Interlace | a combed frame built from two fields at different focus does not score as sharp |
| Errors | nil frame, zero dimensions, ROI outside frame, `tile_grid` of `0x0`, `block_pct` of 0 |
| Determinism | identical input ⇒ bit-identical `Result` across runs and `Subsample` = 1 |
| Regression | `av.Frame.Sharpness()` values unchanged by this work |

Processor-level: a golden JSONL over a short committed clip, plus the
soft-focus-run aggregation.

Benchmarks: `BenchmarkAnalyze1080p`, `_4K`, per method, asserting the
zero-allocation property via `testing.AllocsPerRun`.

## 10. Performance

- All scratch buffers owned by the analyzer and allocated once, as
  FFmpeg does at `config_input` — zero allocation in steady state.
- The dominant cost is the edge walk: O(`radius`) per surviving edge
  pixel, worst case 50 steps. FFmpeg accepts this; measure it before
  assuming it fits the budget, and consider capping `radius` by
  resolution.
- Auto-subsampling caps the working set at ≈1 MP, so 4K costs about the
  same as 1080p unless the user asks for `subsample: 1`.
- Tiles are computed from the same traversal by accumulating into
  per-tile bins; the grid costs no extra pass.
- Target: < 1 ms per 1080p frame for `edge`, < 0.5 ms for `reblur`, on
  one core. If profiling says otherwise, the inner loops move to C next
  to `frame_luma_lapvar` in [av/frame_cgo.h](../../av/frame_cgo.h) —
  but only after the Go version is correct and the §9.2 matrix is green
  against it.

## 11. Phasing

0. **Expose what exists.** Catalogue `blurdetect` as a filter node with
   its options, and document reading `lavfi.blur` from a `select`
   expression. Zero new backend code, ships immediately, and gives
   phase 1 the reference implementation to test parity against.
1. **Port the core.** `blur` package: `Luma`, the `edge_common.c` +
   `vf_blurdetect.c` port, per-plane sizing, the `NaN` fix, normalised
   thresholds, re-blur, noise debias, tiles, confidence, `FromImage`,
   the calibration corpus, and the §9.2 matrix — with FFmpeg parity as
   the gate. No av, no graph, no cgo. Send the `NaN` and `blks` fixes
   upstream.
2. **Wire it up.** `FromFrame` + `blur_detect` node: pixel-format
   adapters, the `LumaDepth()` helper, `Score`, per-frame metadata,
   JSONL/CSV output, soft-focus runs, `docs/blur-detection.md`,
   `CHANGELOG.md`, `docs/go-processor-nodes.md`.
3. **Surfaces.** `mediamolder blur` CLI, node catalog entry, inspector
   results strip, `docs/gui.md`.
4. **Refinements.** `cpbd` method (subject to §11), motion-vector
   cross-check for `motion_blur` classification, per-shot join with
   `scene_change_*`.

## 12. Open questions

1. **Is phase 0 enough for the near-term need?** If the immediate use
   case is "flag soft takes at ingest", `blurdetect` plus a threshold
   may serve until phase 2 lands. Worth deciding before phase 1 starts —
   it changes the urgency, not the design.
2. **Is the stills CLI in phase 1 or phase 3?** It is the fastest way to
   get real-world feedback on the score, which argues for pulling it
   forward; it is also the surface with the least to do with the graph.
3. ~~Score polarity.~~ **Resolved:** blurriness, higher = blurrier,
   matching `lavfi.blur` and `scene_change`.
4. **Upstream or fork?** The `NaN` fix is small and clearly correct, and
   FFmpeg may well take it. If it lands, phase 1's parity test needs to
   pin which side of that fix it compares against.
