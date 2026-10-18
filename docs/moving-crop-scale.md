# `moving_crop_scale` processor

`moving_crop_scale` crops each video frame to a window that follows a list of
keyframes, then scales that window to a fixed output size. A reframing job
uses it where `path_crop` would need a `scale` node after it: the processor
tells the encoder its output size, so the graph does not.

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
| `unmatched` | `hold` | Before the first keyframe and after the last: `hold` (the nearest keyframe), `drop`, or `error`. |
| `aspect` | `free` | `free` interpolates `w` and `h`. `output` interpolates `w` and sets `h` from the output aspect on every frame. A supplied `h` is discarded. If that height is taller than the visible picture, the height clamp is independent and the scale stretches the picture. |
| `align` | `chroma` | `chroma` aligns `x` and `y` down to the chroma grid, as `crop` does. `exact` leaves integer luma positions. `w` and `h` are multiples of the chroma step in both modes. |
| `flags` | `bicubic` | swscale algorithm: `bilinear`, `bicubic`, `lanczos`, `spline`, `area`, `neighbor`. |
| `emit_rect` | `false` | Post metadata with the applied integer rectangle. |

A nil frame returns nil. A non-video frame passes through. `Init` checks types,
bounds, monotonic time, and one convention per keyframe, so a bad job fails
before the first frame is decoded.

## Matching

Keyframes bracket time. They are not consumed, and there is no half-step
tolerance. A frame whose PTS equals a keyframe uses that rectangle. A frame
strictly between two keyframes is interpolated, including the exact midpoint.
With one keyframe, `hold` paints every frame with that rectangle and `drop`
emits only the exact hit.

A frame PTS outside ±2^53 is outside the bracket, so `unmatched` applies.
`AV_NOPTS_VALUE` has no nearest keyframe: the frame is dropped under `hold`
and `drop`, and `error` returns from `Process`. `unmatched: error` returns
from `Process` on the first frame outside the bracket. `Close` returns nil
and does not report unused keyframes.

`t` is checked at `Init` for finite, non-negative, strictly increasing
values. The conversion on the first video frame can still fail: a zero or
negative time base, a tick outside ±2^53, or two times that land on the same
tick. Each of those is a `Process` error. `path_crop` does not read the time
base.

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

## Relationship to `path_crop`

`path_crop` is the trim: one full-height window per frame, frames off the
path dropped, and `Close` fails when a sample matched nothing. The same
pixels come out of `moving_crop_scale` when every frame PTS equals a
keyframe, `y` is 0, `h` and `output.height` both equal the visible height
(so nothing is scaled), the height is even, and `align` is `chroma`.

| `path_crop` | `moving_crop_scale` |
|---|---|
| `width` | `output.width`, and every keyframe's `w` |
| full height | `output.height` and every keyframe's `h` equal the visible height |
| `frames[].cx` | `keyframes[].cx`, with `cy` at half the height |
| one sample per frame | one keyframe per frame, `interpolate: hold` |
| off-path frame dropped | `unmatched: drop` drops only the head and the tail |
| chroma-aligned `x` | `align: chroma` |
| `Close` fails on an unused sample | `Close` returns nil |

A sparse path is a different processor. `path_crop` keeps its half-step
matching. Follow `path_crop` with `scale` when the encoder must open at the
window size. `moving_crop_scale` does not need that node.
