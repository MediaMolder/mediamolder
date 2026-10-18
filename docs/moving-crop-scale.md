# `moving_crop_scale` processor

`moving_crop_scale` crops each video frame to a window that follows a list of
keyframes, then scales that window to a fixed output size. The processor
tells the encoder its output size, so the graph does not need a following
`scale`. `match: sample` is the trim: each keyframe is used once, frames
that fall between samples are dropped, and `Close` fails if a keyframe
matched no frame.

The window moves in whole luma pixels, stepped down to the chroma grid unless
`align` is `exact`. On 4:2:0 that step is 2 luma pixels. Sub-pixel panning is
out of scope.

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
    "align": "chroma",
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
| `keyframes[].w`, `h` | one of | Window size, luma pixels. May be fractional. `h` may be omitted when `aspect` is `output`. |
| `keyframes[].zoom` | one of | Window relative to the output: `w = output.width / zoom`, `h = output.height / zoom`. `zoom = 1` is a window the size of the output. `zoom > 0`. Every keyframe uses `zoom`, or none do. |
| `interpolate` | `linear` | Between keyframes: `hold` (the earlier rectangle), `linear`, or `smooth` (Catmull-Rom). |
| `unmatched` | `hold` | Before the first keyframe and after the last, and any frame `match: sample` does not take: `hold` (the nearest keyframe), `drop`, or `error`. |
| `match` | `bracket` | `bracket` places every frame inside the first and last keyframe, including the midpoint. `sample` uses each keyframe at most once: the exact PTS, or the nearest unused keyframe strictly less than half the smallest gap away (one tick when there is a single keyframe). A frame exactly halfway matches neither. `sample` requires `interpolate: hold`. `Close` then fails if a keyframe was never used. |
| `aspect` | `free` | `free` interpolates `w` and `h`. `output` interpolates `w` and sets `h` from the output aspect on every frame. A supplied `h` is discarded. If that height is taller than the visible picture, the height clamp is independent and the scale stretches the picture. |
| `align` | `chroma` | `chroma` aligns `x` and `y` down to the chroma grid, as `crop` does. `exact` leaves integer luma positions. `w` and `h` are multiples of the chroma step in both modes. |
| `flags` | `bicubic` | swscale algorithm: `bilinear`, `bicubic`, `lanczos`, `spline`, `area`, `neighbor`. |
| `emit_rect` | `false` | Post metadata with the applied integer rectangle. |

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
On a 100×100 output that midpoint is a 70×70 window.

`smooth` is Catmull-Rom through the keyframes. The phantom samples at the
ends duplicate the first and last keyframe, so two keyframes still meet the
linear midpoint. The spline can overshoot through zero; a negative size is
raised to 0 and then rejected, because a side that quantises to 0 is a
`Process` error. Zoom is splined in log size, so that size stays positive.

Under `aspect: output`, only `w` affects the result. `h` is recomputed from
the output aspect after interpolation, and a centre is converted from that
`h` at `Init`. Quantising then clamps each side to the visible picture on
its own. A derived height that does not fit becomes a wider window than the
output, and scaling that window stretches the picture.

## Quantising the window

For each frame, in order:

1. Clamp `w` and `h` to the visible picture (`VisibleWidth` / `VisibleHeight`,
   after any pending `crop_*` fields). A negative size is raised to 0 first.
2. Round `w` and `h` to the nearest even integer (halfway away from zero) and
   align them down to the chroma step. Clamp again: an odd visible width of
   1919 rounds to 1920, which is wider than the picture, and the second clamp
   brings it back to 1918 on 4:2:0.
3. Clamp `x` and `y` to the range that keeps the window inside the picture.
4. Round `x` and `y` to the nearest integer, with ties toward the lower
   integer. `align: chroma` then clears the low chroma bits. `align: exact`
   leaves the integer. Aligning down does not push the window past the picture.
5. A side that quantises to 0 is a `Process` error.

Hardware frames and interlaced frames are errors. A deinterlacer goes before
the node.

When the quantised window already has the output size, the frame is cropped
with `CropRect` and swscale is not called. Otherwise the window is a
`CropWindow` (its own reference of the source buffers, pending crop folded
first) passed to one `sws_scale_frame`. The five colour fields are copied onto
the destination before the scale, because an unspecified range is treated as
limited and would convert a full-range picture. The frame's sample aspect
ratio passes through. Square pixels belong on the encoder (`sar` / `dar`) or
on a following `setsar`.

`flags` and an automatic thread count are the only swscale options. The graph
inside libswscale is rebuilt when the window size or the colour properties
change. A continuous zoom rebuilds it every frame. Node metrics record that
count as `SwsReinits`, and the Run panel shows it beside the node name when
it is non-zero.

## `emit_rect`

```json
{"pts": 1000, "x": 420, "y": 0, "w": 608, "h": 1080, "interpolated": false}
```

`pts` is the frame PTS. `interpolated` is true only for a frame blended
between two keyframes by `linear` or `smooth`. An exact hit, a held
keyframe, and `interpolate: hold` are false.

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
