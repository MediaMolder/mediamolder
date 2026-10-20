# `moving_crop_scale` processor

`moving_crop_scale` crops each video frame to a window that follows a list of
keyframes, then scales that window to a fixed output size. The processor
tells the encoder its output size, so the graph does not need a following
`scale`. `match: sample` is the trim: each keyframe is used once, frames
that fall between samples are dropped, and `Close` fails if a keyframe
matched no frame.

The window is placed with sub-pixel accuracy. A slow pan generates the
in-between pixels instead of stepping a whole pixel at a time. The window
may sit partly or fully outside the source. Missing pixels are digital
black, so a 16:9 picture can become a 9:16 frame with black bars above and
below the picture. Zoom runs from 1/16 (the window is sixteen times the
output) to 64 (the window is one sixty-fourth of the output).

## Node

```json
{
  "id": "reframe",
  "type": "go_processor",
  "processor": "moving_crop_scale",
  "params": {
    "output": {"width": 1080, "height": 1920},
    "keyframes": [
      {"pts": 0, "x": 420, "y": 0, "w": 608, "h": 1080},
      {"pts": 90000, "cx": 960, "cy": 540, "w": 540, "h": 960}
    ],
    "interpolate": "linear",
    "unmatched": "hold",
    "aspect": "free",
    "flags": "bicubic"
  }
}
```

| Param | Default | Meaning |
|---|---|---|
| `output.width`, `output.height` | required | Output size in luma pixels. Even, at least 2. The encoder opens at this size. |
| `keyframes` | required | One rectangle per time. At least one. Time is strictly increasing. |
| `keyframes[].pts` | one of `pts` or `t` | Source PTS in the time base of the frames that reach the node. An integer in ±2^53. |
| `keyframes[].t` | one of `pts` or `t` | Time in seconds. Converted to PTS on the first video frame with `av_rescale_q` (nearest, halfway away from zero). A list uses `pts` or `t`, not both. |
| `keyframes[].x`, `y` | one of | Top-left corner, luma pixels. May be fractional. Both are required together. |
| `keyframes[].cx`, `cy` | one of | Centre. `x = cx − w/2`, `y = cy − h/2`. One keyframe uses one convention. |
| `keyframes[].w`, `h` | one of | Window size, luma pixels. May be fractional. `h` may be omitted when `aspect` is `output`. Each side is at least 1 pixel, at least `output/64`, and at most `output×16`. |
| `keyframes[].zoom` | one of | Window relative to the output: `w = output.width / zoom`, `h = output.height / zoom`. `zoom = 1` is a window the size of the output. Inclusive range is 1/16 to 64. Every keyframe uses `zoom`, or none do. |
| `interpolate` | `linear` | Between keyframes: `hold` (the earlier rectangle), `linear`, or `smooth` (Catmull-Rom). |
| `unmatched` | `hold` | Before the first keyframe and after the last, and any frame `match: sample` does not take: `hold` (the nearest keyframe), `drop`, or `error`. |
| `match` | `bracket` | `bracket` places every frame inside the first and last keyframe, including the midpoint. `sample` uses each keyframe at most once: the exact PTS, or the nearest unused keyframe strictly less than half the smallest gap away (one tick when there is a single keyframe). A frame exactly halfway matches neither. `sample` requires `interpolate: hold`. `Close` then fails if a keyframe was never used. |
| `aspect` | `free` | `free` interpolates `w` and `h`. `output` interpolates `w` and sets `h` from the output aspect on every frame, including an exact keyframe. A supplied `h` is discarded. A derived height taller than the picture extends past the frame, and the gap is digital black. |
| `align` | `chroma` | `chroma` or `exact`. Accepted so an older job still opens. Both values sample at sub-pixel positions. The choice does not snap the window. |
| `flags` | `bicubic` | Resample kernel: `bilinear`, `bicubic` (Catmull-Rom), `lanczos` (3 lobes), `spline` (cubic B-spline), `area` (box), `neighbor` (nearest). |
| `emit_rect` | `false` | Post metadata with the window rounded half away from zero, after the size clamp. A negative origin is kept. |

A nil frame returns nil. A non-video frame passes through. `Init` checks types,
bounds, monotonic time, and one convention per keyframe, so a bad job fails
before the first frame is decoded.

## Matching

With `match: bracket` (the default), keyframes bracket time. They are not
consumed, and there is no half-step tolerance. A frame whose PTS equals a
keyframe uses that rectangle. A frame strictly between two keyframes is
interpolated, including the exact midpoint. With one keyframe, `hold` paints
every frame with that rectangle and `drop` emits only the exact hit.
`Close` returns nil.

A frame PTS outside ±2^53 is outside the bracket, so `unmatched` applies.
`AV_NOPTS_VALUE` has no nearest keyframe: the frame is dropped under `hold`
and `drop`, and `error` returns from `Process`. `unmatched: error` returns
from `Process` on the first frame outside the bracket. `match: sample` is
below.

`t` is checked at `Init` for finite, non-negative, strictly increasing
values. The conversion on the first video frame can still fail: a zero or
negative time base, a tick outside ±2^53, or two times that land on the same
tick. Each of those is a `Process` error.

## Interpolation

`linear` blends `x`, `y`, `w`, and `h`. A list of `zoom` keyframes blends
`log(w)` and `log(h)`, so the midpoint of zoom 1 and zoom 2 is √2, not 1.5.
On a 100×100 output that midpoint window is about 70.71 pixels on a side.
`emit_rect` rounds it half away from zero to 71.

`smooth` is Catmull-Rom through the keyframes. The phantom samples at the
ends duplicate the first and last keyframe, so two keyframes still meet the
linear midpoint. The spline can overshoot. A keyframe outside the zoom
range fails at `Init`. At `Process`, a positive size outside
`[output/64, output×16]` is clamped into that range. A non-positive size is
a `Process` error (`crop side collapsed to 0`). Zoom is splined in log
size, so that size stays positive.

Under `aspect: output`, only `w` affects the result. `h` is recomputed from
the output aspect after interpolation, on every frame, and a centre is
converted from that `h` at `Init`. The size clamp runs after that
recompute. A derived height that does not fit hangs off the picture. The
gap is black. The picture is not stretched.

## Sampling the window

For each frame, in order:

1. Under `aspect: output`, set `h = w * output.height / output.width`.
2. Reject a non-finite or non-positive side (`crop side collapsed to 0`).
   Clamp each positive side into `[output/64, output×16]`. The origin is
   not clamped. `Init` also rejects a keyframe side below 1 pixel, which is
   stricter than 64× on a small output: an output of 32 pixels would allow
   a window of 0.5 at the ratio limit, and that keyframe still fails
   `Init`. A spline overshoot at `Process` may be clamped below 1 pixel.
3. Paint the output.

A sample whose centre lies outside the source stays digital black. Index 0
is the centre of the first pixel, so the picture covers index
`[-0.5, width − 0.5)`. The centre of output pixel `i` is

```
sx = x + (i + 0.5) * w / output.width - 0.5
```

and the same for `y`. An integer origin and a 1:1 window copy source pixel
`x+i` for an interpolating kernel. Each plane uses that formula on its own
grid: a chroma coordinate is the luma coordinate divided by the chroma
subsampling. An even integer origin at 1:1, fully inside the picture,
copies chroma. An odd origin on 4:2:0 is filtered even at 1:1, so chroma
does not jump when a pan crosses an odd luma pixel. A chroma sample is
still written when any luma pixel it covers has its centre inside the
picture, so the edge of a black bar is not picture luma with neutral chroma.

Digital black follows the frame's colour range. At 8 bits and above,
limited and unspecified YUV use luma `16 << (depth-8)` and chroma
`128 << (depth-8)`. Full range (`AVCOL_RANGE_JPEG`) uses luma 0 and the
same chroma. RGB is 0. Alpha is opaque. Unspecified range is limited.

Taps the kernel needs from outside the source are digital black. They stay
in the weight sum. The edge is not repeated, and those taps are not
dropped. Bicubic and Lanczos have negative weights, so a flat field next
to the border is not flat: a black tap through a negative lobe raises the
edge. Interior pixels of a flat field stay flat. Unused bits above a
component that owns the low bits of a 16-bit sample are written as zero.

When the window is larger than the output on an axis, `bilinear`,
`bicubic`, `lanczos`, and `spline` stretch by `window / output` on that
axis, so the footprint covers the source pixels under each output pixel.
A 1:1 window keeps the native support. `neighbor` stays nearest. `area`
already averages the rectangle the output pixel covers.

| `flags` | Kernel |
|---|---|
| `neighbor` | Nearest sample, `floor(sx+0.5)`. An index that falls outside while the centre is inside is clamped into the picture. |
| `bilinear` | Tent, support 1. |
| `bicubic` | Catmull-Rom, cubic `a = -0.5`. An integer coordinate copies that pixel, including at the frame edge, because the off-integer taps have weight 0. |
| `lanczos` | Lanczos with 3 lobes, six taps. Interpolating. |
| `spline` | Cubic B-spline, support 2. Not interpolating: an integer coordinate mixes the neighbours (centre weight 4/6, each side 1/6). |
| `area` | Average of the source rectangle the output pixel covers. At 1:1 that rectangle is one pixel. The part of the rectangle outside the picture counts as black. A centre outside the picture leaves the pixel black even when the rectangle overlaps it. |

`CropRect` is used only when `x`, `y`, `w`, and `h` are all within 1e-6 of
integers, the window is fully inside the visible picture, the origin sits
on the chroma grid, `w` and `h` equal the output size, and the kernel
reproduces that pixel. `spline` always resamples, because a cubic B-spline
blurs an integer coordinate. Every other window, including an integer
scale, goes through the resampler. A zoom that passes through an integer
size stays on one kernel, so that frame does not change filter.
`ScaleReinits` stays 0, and the Run panel shows a swscale count only when
that count is non-zero.

A pending crop is folded before sampling, so the window is in visible
pixels. Colour properties are copied onto the destination before the
resample, and the frame's sample aspect ratio passes through. Square pixels
belong on the encoder (`sar` / `dar`) or on a following `setsar`. Hardware
frames, interlaced frames, and floating-point frames are errors. Paletted,
bitstream, and bayer frames are errors too. A deinterlacer goes before the
node.

## `emit_rect`

```json
{"pts": 1000, "x": 420, "y": 0, "w": 608, "h": 1080, "interpolated": false}
```

`pts` is the frame PTS. `x`, `y`, `w`, and `h` are the window after the
size clamp, rounded half away from zero. A negative origin is kept.
`interpolated` is true only for a frame blended between two keyframes by
`linear` or `smooth`. An exact hit, a held keyframe, and `interpolate: hold`
are false.

## Seconds instead of PTS

```json
"keyframes": [
  {"t": 0, "cx": 960, "cy": 540, "zoom": 1},
  {"t": 1.5, "cx": 1100, "cy": 540, "zoom": 2}
]
```

`ProcessorContext.TimeBase` is the input edge time base, filled by the
go_processor handler. Hosts that already know the time base can keep using
`pts` and ignore `t`.

## Sample matching

`match: sample` with `unmatched: drop` is a path of windows. A frame takes
the unused keyframe at its PTS, or the nearest unused keyframe strictly
less than half the smallest gap away. One keyframe allows one tick. A frame
exactly halfway between two keyframes matches neither, so a keyframe on
every second frame is not taken by the frame before it. Each keyframe is
used at most once. Anything else is dropped, including a frame with no PTS.

```json
{
  "output": {"width": 608, "height": 1080},
  "match": "sample",
  "interpolate": "hold",
  "unmatched": "drop",
  "keyframes": [
    {"pts": 0, "cx": 960, "cy": 540, "w": 608, "h": 1080},
    {"pts": 3003, "cx": 1000, "cy": 540, "w": 608, "h": 1080}
  ]
}
```

`Close` returns an error naming the first unused keyframe PTS. That catches
a missing `copy_ts`, a filter that changed PTS or the time base, or a
source that ends before the last keyframe. `unmatched: hold` still emits
the nearest keyframe for a frame that matched nothing, and that emission
does not count as using the keyframe. `interpolate` must be `hold`: sample
matching does not glide between keyframes.

`match: bracket` does not consume keyframes. A frame between two keyframes
is interpolated, including the exact midpoint, and `Close` returns nil.
