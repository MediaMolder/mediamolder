# `path_crop` processor

`path_crop` crops each video frame to a full-height window whose left edge
follows a path of per-frame samples, and drops frames that are not on the
path. It is the reframing step of a 16:9 → 9:16 (or any narrower aspect)
pipeline: an analysis stage decides where the subject is in every frame and
writes the window centre per presentation timestamp; this processor applies
that path exactly, frame by frame.

It does not scale, smooth, rebase timestamps, strip side data, or touch
audio. The rest of the graph does those things.

## Node

```json
{
  "id": "crop",
  "type": "go_processor",
  "processor": "path_crop",
  "params": {
    "width": 608,
    "frames": [
      {"pts": 18399380, "cx": 1285.2289979757086},
      {"pts": 18402383, "cx": 1285.8241402692095}
    ]
  }
}
```

| Param | Required | Meaning |
|---|---|---|
| `width` | yes | Window width in luma pixels. Even, at least 2, and a multiple of the frame format's chroma step. A JSON number, a Go int, or a decimal string. |
| `frames` | yes | One object per path sample, PTS strictly increasing. |
| `frames[].pts` | yes | Source PTS of that frame: an integer in −2^53 … 2^53. |
| `frames[].cx` | yes | Horizontal centre of the window in luma pixels: a finite number. |

`cx` is used exactly as given; the processor does not re-round. A writer may
round `cx` to hundredths to keep jobs small: that moves the left edge only
when the unrounded edge lies within 0.005 of a half-integer, and then by one
chroma step (2 luma pixels for 4:2:0).

`Init` fails if `width` is missing, odd, or below 2; if `frames` is missing,
empty, or not an array of `{pts, cx}` objects; if a `pts` is fractional,
outside ±2^53, or a string; if a `cx` is a string, NaN or infinite; or if
PTS values are not strictly increasing. A legacy form that packs `pts` and
`cx` into two comma-separated strings is not accepted and fails with
`missing frames`.

## Preconditions

The processor matches integer PTS values. It cannot see the time base of
the frames it receives (`ProcessorContext` carries none), so the job must
guarantee:

- `copy_ts` is true, so decoded frames keep source PTS.
- Every path PTS is in the time base of the frames that reach the
  processor — normally the source stream's time base, e.g. 1/90000.
- No node before the processor changes PTS or the time base, unless the
  path was computed in that node's output time base. `fps`, `setpts`,
  `framerate` and `minterpolate` change them, and so do `bwdif` and
  `yadif` in every mode (they halve the time base and double PTS,
  `libavfilter/yadif_common.c`). `scale`, `setsar` and `format` are fine.
- Frames are in system memory. A hardware frame is an error.
- The incoming frame's `crop_*` fields are zero. Frames out of a filter
  graph satisfy this.

If a precondition is broken, no frame matches. `Close` reports that as an
error, so the job does not produce an empty file in silence.

## What one frame does

1. A nil frame returns nil; that is not an error.
2. A non-video frame is returned unchanged.
3. The frame's PTS is matched to one unused path sample: an exact PTS
   wins, otherwise the nearest unused sample within the half-frame limit
   (half the smallest step between consecutive path PTS; 1 when the path
   has one sample or that step is below 2). A tie goes to the earlier
   sample. A sample is used at most once. Because every step is at least
   twice the limit, only the samples either side of the PTS can be in
   range, found by binary search.
4. No match drops the frame. That is how a keyframe seek before the in
   point, or a trim that runs past the out point, stays out of the file.
5. A match returns a new frame; the runtime releases the original.

The crop:

1. The left edge is `cx − width / 2`.
2. Round to the nearest integer; a fraction of exactly one half goes to
   the lower integer.
3. Clamp to `[0, frameWidth − width]`.
4. Align down to the format's chroma step, `x &^ ((1 << log2_chroma_w) − 1)`,
   as FFmpeg's `crop` filter does (`libavfilter/vf_crop.c`). For 4:2:0 and
   4:2:2 this clears the low bit; for 4:4:4 there is no step.
5. A frame narrower than `width`, or a `width` that is not a multiple of
   the chroma step, is an error, not a drop.
6. The window is cut with `av.Frame.CropColumns(x, width)`: a new
   reference of the source frame gets `crop_left`/`crop_right` and goes
   through `av_frame_apply_cropping(AV_FRAME_CROP_UNALIGNED)`, FFmpeg's own
   plane math (`(x >> shift) * comp.step`), so 8-bit, high-bit-depth,
   semi-planar (`nv12`, `p010`) and packed formats are all cut correctly.
   The flag keeps the left edge exact; without it FFmpeg moves it to keep
   data pointers aligned.
7. `CropColumns` then copies the window into a frame with its own aligned
   buffers (`av_frame_copy`), so swscale sees aligned rows and
   `Frame.Plane()` slices stay inside their buffer.
8. The output keeps the source frame's properties — PTS, duration, side
   data (`av_frame_copy_props`). Side data is not cleared; put a
   `sidedata` filter after the processor if the output must not carry it.

The processor returns no metadata.

`Close` returns an error if any path sample was never used, naming the
count and the first unused PTS. That catches a broken precondition and a
source that ends before the out point. `Close` also runs after a failed or
cancelled run; the runtime collects its error with the others.

A hold is two samples with the same `cx`: they produce the same x. The
processor never invents a glide between samples.

## Output size

The processor changes the frame width. MediaMolder opens the encoder at the
size of the last filter before it, and a `go_processor` with inputs cannot
report its output size (`FrameSourceInfo` applies only to processors with
no inbound edges). Follow the processor with a `scale` to `width` × frame
height; on correct frames it changes nothing.

## Runtime note

The `go_processor` runtime used to forward a processor's returned frame
only when the processor also returned metadata; otherwise it sent the
original frame on and leaked the new one, so a cropping processor's output
never reached the encoder and the following `scale` squashed the full
frame. Fixed alongside this processor
(`TestGoProcessor_ForwardsReplacementFrameWithoutMetadata`).

## Alternatives considered

FFmpeg's `crop` filter recalculates `x` and `y` per frame and accepts them
as runtime commands, so a job could drive it with `sendcmd`, one command
per frame, and use `trim` for the in and out points. That needs no new
processor and gives the encoder a real filter output size. It was not
chosen: `sendcmd` matches time intervals in seconds, not exact PTS, so a
frame near an interval edge can take the neighbouring x; and off-path
frames are not dropped, so `trim` would have to reproduce the in and out
points exactly.

## Tests

- `processors/path_crop_test.go`: registration, the integer left-edge rule,
  a lossless left-edge test on painted frames (held `cx` keeps its x; side
  data passes through), PTS matching and tie-breaking, `Close` reporting
  unused samples, every `Init` failure.
- `av/frame_crop_test.go`: `CropColumns` plane offsets on `yuv420p`,
  `yuv420p10le`, `nv12` and `rgb24`, side-data copy, range checks.
- `job/path_crop_e2e_test.go`: a `copy_ts` job with input `ss`/`to`,
  `format`, `path_crop`, a window `scale`, `setpts` and `sidedata` over a
  lavfi source encoded with `ffv1` writes exactly one frame per path
  sample at `width` × source height, with the expected left edges.

## Example graph

```json
{
  "schema_version": "1.0",
  "copy_ts": true,
  "inputs": [{"id": "src", "url": "source.mp4",
    "options": {"ss": "204.437", "to": "212.510"},
    "streams": [{"input_index": 0, "type": "video", "track": 0}]}],
  "graph": {
    "nodes": [
      {"id": "fmt", "type": "filter", "filter": "format", "params": {"pix_fmts": "yuv420p"}},
      {"id": "crop", "type": "go_processor", "processor": "path_crop",
       "params": {"width": 608, "frames": [{"pts": 18399380, "cx": 1285.23}]}},
      {"id": "window", "type": "filter", "filter": "scale", "params": {"w": 608, "h": 1080}},
      {"id": "rebase", "type": "filter", "filter": "setpts", "params": {"expr": "PTS-STARTPTS"}}
    ],
    "edges": [
      {"from": "src:v:0", "to": "fmt", "type": "video"},
      {"from": "fmt", "to": "crop", "type": "video"},
      {"from": "crop", "to": "window", "type": "video"},
      {"from": "window", "to": "rebase", "type": "video"},
      {"from": "rebase", "to": "out:v", "type": "video"}
    ]
  },
  "outputs": [{"id": "out", "url": "vertical.mp4", "codec_video": "libx264"}]
}
```
