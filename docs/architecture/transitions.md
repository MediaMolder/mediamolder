# Video Transitions

MediaMolder composites timeline transitions (wipes, slides, fades, circles, …)
with a **native Go transition engine** rather than libavfilter's `xfade` filter.
This page describes that codebase: where it lives, the per-pixel model, how the
`sequence_editor` drives it, and how to add a new transition.

## Why Go-native

The `sequence_editor` originally fed the two clips of a non-dissolve transition
into a libavfilter `xfade` graph. When the two pre-converted frames were pushed
into that graph via `buffersrc`, `xfade` produced a chroma shift on its *second*
(incoming) input — a green cast on the incoming clip — that did not occur when
the identical filter ran from the `ffmpeg` CLI. The corruption followed the
input **position**, not the content, and was invariant to colorimetry tags,
graph reuse, and graph topology. Rather than keep fighting the push-fed `xfade`,
transitions are now computed directly in Go from the two converted frames. This
also makes transitions **hackable**: a contributor can add a custom transition
in pure Go without touching cgo or libavfilter.

The per-pixel formulas are ported from FFmpeg's `libavfilter/vf_xfade.c`, so the
output matches `xfade` for the implemented set.

## The model

A transition is a function of three things:

- **a** — the outgoing clip's frame (xfade's "from").
- **b** — the incoming clip's frame (xfade's "to").
- **progress** — a scalar in `[0, 1]`.

`progress` uses **xfade's convention** so the C formulas port verbatim:

```
progress == 1.0  → output is fully a   (start of the transition window)
progress == 0.0  → output is fully b   (end of the transition window)
```

Note this is the *reverse* of the `sequence_editor`'s timeline progress (which is
`0` at the window start); the caller converts with `1 - prog` (see below).

Both `a` and `b` are already converted to the sequence pixel format (8-bit planar
YUV, limited range) by the per-source converters, so the engine operates on clean,
matching frames. Transitions work **per plane** at each plane's own resolution, so
the chroma planes of a subsampled format (e.g. yuv420p) are handled at half size
and stay aligned with luma. `mix(a, b, m) = a*m + b*(1-m)` — also from `vf_xfade.c`.

## Components

### `av` — frame pixel access

The engine needs to read and write raw plane bytes, which `av.Frame` now exposes
(`av/frame_planes.go`):

| Method                              | Purpose                                                        |
| ----------------------------------- | ------------------------------------------------------------- |
| `Frame.NumPlanes()`                 | plane count for the pixel format (3 for yuv420p)              |
| `Frame.Linesize(i)`                 | byte stride of plane `i` (includes padding)                  |
| `Frame.PlaneWidth(i)` / `PlaneHeight(i)` | sample dimensions of plane `i` (chroma is subsampled)   |
| `Frame.Plane(i)`                    | plane `i` bytes as a `[]byte` aliasing the C buffer (read/write) |
| `av.NewVideoFrame(w, h, pixFmt)`    | allocate a writable output frame with fresh plane buffers    |
| `Frame.CopyPropsFrom(src)`          | copy colorimetry/SAR/side-data (not pixels) onto a frame     |

The slice returned by `Plane(i)` aliases the frame's buffer and is only valid
until the frame is closed — do not retain it.

### `transition` — the engine

The `transition` package (module path `…/transition`) holds the framework and the
ported transitions:

- `transition.go` — the `RenderFunc` type, the name→func **registry**
  (`Register`/`Lookup`/`Names`), the `renderPointwise` helper, the `pixelFunc`
  abstraction, and math helpers (`mix`, `smoothstep`, `fract`, `clip8`,
  `blackLevel`/`whiteLevel`).
- `builtin.go` — the transitions, registered from `init()`.

```go
// 1 → a, 0 → b. out is freshly allocated; fill every plane.
type RenderFunc func(out, a, b *av.Frame, progress float64)
```

Most transitions are **pointwise** — each output sample depends only on the two
co-located input samples plus position and progress — so they are written as a
`pixelFunc` and registered with `renderPointwise`, which walks every plane/row/col:

```go
type pixelFunc func(a, b float64, x, y, w, h, plane int, progress float64) float64
```

### `sequence_editor` — the driver

In the transition window the editor fetches and converts both clips, then
(`processors/sequence_editor.go`, `renderTransition`):

1. looks up the transition by name (`transition.Lookup`);
2. allocates the output frame with `av.NewVideoFrame` and copies a's props;
3. calls the `RenderFunc` with `1 - transProg` (timeline → xfade progress);
4. closes the two inputs and returns the output (the caller stamps the PTS).

A supported transition name with no engine entry **falls back to a linear fade**
(logged once per name) so the timeline still renders. The linear `"dissolve"`
remains a separate path (the libavfilter `blend` filter, which composites cleanly).

## Adding a transition

Pointwise (the common case) — add to `builtin.go`'s `init()`:

```go
registerPointwise("myfade", func(a, b float64, x, y, w, h, plane int, p float64) float64 {
    // p == 1 → a, p == 0 → b
    return mix(a, b, p)
})
```

Non-pointwise (neighbourhood/fetch access, like the slides) — register a
`RenderFunc` directly and walk the planes yourself (see `slideHorizontal`).

Either way, also add the name to `processors.seqSupportedTransitions` so the
`sequence_editor` accepts it at `Init`.

## Coverage

The engine ports the **entire** xfade transition set the `sequence_editor`
accepts (`processors.seqSupportedTransitions`), so it never falls back:

> `fade`, `fadeblack`, `fadewhite`, `fadegrays`,
> `wipeleft`, `wiperight`, `wipeup`, `wipedown`,
> `wipetl`, `wipetr`, `wipebl`, `wipebr`,
> `slideleft`, `slideright`, `slideup`, `slidedown`,
> `smoothleft`, `smoothright`, `smoothup`, `smoothdown`,
> `circleopen`, `circleclose`, `circlecrop`, `rectcrop`,
> `vertopen`, `vertclose`, `horzopen`, `horzclose`,
> `radial`, `distance`, `zoomin`, `squeezeh`, `squeezev`,
> `hlslice`, `hrslice`, `vuslice`, `vdslice`, `hblur`.

`builtin.go` holds the pointwise core; `builtin_more.go` holds the crop/corner/bar
transitions plus those needing neighbourhood or resample access (`distance` reads
all planes per pixel, `hblur` is a running box blur, `squeezeh/v` and `zoomin`
resample). The fallback-to-`fade` path in `sequence_editor` remains only as a
defensive guard. (The dithered `dissolve` and the few xfade transitions outside
`seqSupportedTransitions` — `pixelize`, `diagtl…`, `hwind…`, `cover`/`reveal`,
`fadefast/slow` — are deliberately not exposed.)

## Audio crossfades

The `sequence_editor` mixes an **audio** stream alongside the video, derived from
the *same clips* as the picture, and crossfades it across each transition window —
**auto-coupled** to the clip's video transition by default. Video-only jobs are
unchanged.

**Opting in (job JSON).** Audio is off until two things are present:

1. a positive `sample_rate` in `params.format` (plus optional `channels`,
   default 2) — this makes the node emit a second, audio output stream;
2. an `audio` edge from the node to an audio encoder (the node now has both a
   `video` and an `audio` output port).

```json
"format": { "width": 1920, "height": 1080, "frame_rate": 30,
            "sample_rate": 48000, "channels": 2, "length_sec": 130 },
…
"edges": [
  { "from": "seq",  "to": "venc", "type": "video" },
  { "from": "seq",  "to": "aenc", "type": "audio" }
]
```

The full step-by-step (encoder nodes, output wiring, `transition.audio`
override) is in
[using-mediamolder.md § 5.16 → Audio and crossfades](../using-mediamolder.md#audio-and-crossfades).
The rest of this section is the *design*.

### `acrossfade` — the engine

The `acrossfade` package is the audio analogue of `transition`: a name→curve
registry (`Register`/`Lookup`/`Names`) plus a `Mix` helper. A curve is a fade-in
gain `g(x)` for `x ∈ [0,1]`; the outgoing clip is faded with `g(1-x)`.

```go
type CurveFunc func(x float64) float64        // 0 → silent, 1 → unity
func Mix(curve string, out, a, b *av.Frame, p0, p1 float64)
```

`Mix` blends two planar-float (`fltp`) frames sample-by-sample, ramping the
fraction linearly from `p0` (first sample) to `p1` (last) so a crossfade is smooth
within each step rather than stepped per video frame. The curves are ported from
FFmpeg's `af_afade.c`: `tri` (linear, the default), `qsin` (equal-power —
`g_in² + g_out² = 1`, no mid-point dip), `hsin`, `esin`, `qua`, `cub`, `squ`,
`par`, `exp`, `log`. The set is exposed to the GUI at `/api/audio-transitions`.

### Audio path in `sequence_editor`

`sequence_audio.go` adds the audio half:

- An **`audioReader`** per source URL decodes that file's audio with its *own*
  demuxer + decoder (independent of the video `clipReader`, so the two never
  contend for one demuxer's read position), resamples it to the sequence's `fltp`
  working format via `av.Resampler`, and buffers it in a per-channel sample FIFO.
  A file with no audio track serves silence.
- On the same per-frame timeline as the video, `renderAudioStep` produces the
  step's audio: a single covering clip yields that clip's audio; a transition
  window crossfades the outgoing/incoming clips with `acrossfade.Mix`; a gap
  yields silence. Sample counts are tracked against a rounded per-step target so
  the audio total stays exactly aligned to the video frame count.

### Multi-stream output

A `FrameSource` historically emitted one stream. The editor now implements
`processors.MultiStreamSource` (`OutputStreams` + `RunStreams`); the runner routes
each emitted frame to the downstream edges whose port type matches that stream's
media type (video → the video encoder, audio → the audio encoder). The audio
encoder reads the sequence's sample rate/channels via `resolveEdgeStreamInfo`, and
the audio-encoder adapter conforms the `fltp` mix to the codec's required sample
format and frame size.

### Coupling and overrides

A clip's `transition` drives both the video transition and the audio crossfade
over the **same window**. Override per clip via `transition.audio`:

```json
"transition": { "type": "wipeleft", "duration": 1.0,
                "audio": { "curve": "qsin", "duration": 0.3, "off": false } }
```

- `curve` — crossfade curve (empty → the default `tri`).
- `duration` — narrows the fade to the tail of the window (a faster fade that
  still lands on the cut); blank couples it to the video duration.
- `off` — hard-cut the audio while the video still transitions.

### Adding a curve

Add to `acrossfade/builtin.go`'s `init()` — that is all (the GUI picker and Init
validation read `Names()` automatically):

```go
Register("mycurve", func(x float64) float64 { /* g(0)=0, g(1)=1, monotonic */ return x })
```

## Performance

Each transition frame is a per-pixel pass over the output (≈ 3.1 M samples for
1080p yuv420p) in Go. Transition windows are short, so this is inexpensive
relative to decode/encode, but the pointwise path is not vectorized — a hot
transition can be specialized with a tight per-plane loop (as the slides are)
instead of the `pixelFunc` closure.

Audio adds a second decode + resample pass per source plus a per-sample mix in
the crossfade window — negligible next to video decode/encode.
