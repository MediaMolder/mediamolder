# Smart-cut trimming (`smartcopy`)

`smartcopy` trims a clip **frame-accurately** while re-encoding only the
GOP(s) the cut points land in. Every whole GOP between the cuts is
stream-copied byte-for-byte, so the interior is never re-degraded.

## Why

Two existing options each fall short for trimming:

| Option | Speed | Quality | Accuracy |
| --- | --- | --- | --- |
| `codec_video: "copy"` | fast | lossless | **keyframe-only** (cannot cut mid-GOP) |
| `codec_video: "libx264"` (re-encode) | slow | lossy (whole clip) | frame-accurate |

`smartcopy` gives frame accuracy at both edges **and** keeps the interior
lossless, by re-encoding only the boundary GOPs.

## Usage

Set `codec_video: "smartcopy"` on an output and give it a trim window via
`options.ss` / `options.t` / `options.to` (the same FFmpeg time grammar as
everywhere else):

```json
{
  "inputs": [{ "id": "in0", "url": "source.mp4",
    "streams": [{ "input_index": 0, "type": "video", "track": 0 },
                { "input_index": 0, "type": "audio", "track": 0 }] }],
  "graph": { "nodes": [], "edges": [
    { "from": "in0:v:0", "to": "out0:v", "type": "video" },
    { "from": "in0:a:0", "to": "out0:a", "type": "audio" }
  ]},
  "outputs": [{
    "id": "out0", "url": "clip.mp4",
    "codec_video": "smartcopy", "codec_audio": "copy",
    "options": { "ss": "12.500", "to": "48.200" }
  }]
}
```

### In the GUI

Drag **Copy → Smart copy (video)** from the palette between an input's video
pin and an output. The node's properties panel exposes the full boundary
encoder (rate control, preset, tune, profile/level, raw `x264-params` /
`x265-params`) and an optional **Boundary encoder** override. Set the trim
window (Start / Duration / End) on the connected output's **Timing** section
— it applies to every stream of that output. (Older graphs may still carry a
`smartcopy_global_header` param; it is ignored, since parameter-set placement
is now derived from the source — see below.)

For audio, drag **Copy → Smart copy (audio, PCM)** between an input's audio pin
and an output. Its panel has no tunable parameters (PCM boundary slicing has no
encoder); it just confirms the PCM-only, sample-accurate, lossless behaviour and
points to the output's **Timing** section for the window.

### In JSON

The **target is identical to the source** in codec, resolution, frame
rate, pixel format, sample aspect ratio, profile/level and bit rate — you
opt into that by choosing `smartcopy`. No scaling, fps change, or codec
change is permitted on a smartcopy stream.

Optional boundary-encoder quality knobs may be supplied via
`encoder_params_video` (they only affect the 1–2 re-encoded GOPs):

```json
"encoder_params_video": { "crf": "16", "preset": "slow" }
```

Structural parameters (encoder, profile/level, resolution, pixfmt, SAR,
frame rate, timebase) are always taken from the source and cannot be
overridden — changing them would break decode compatibility with the
copied interior.

## How it works

The graph splits the work along the existing source/copy boundary:

```
 source (demux)  --raw packets-->  smartcopy node  --packets-->  sink
                                   copy interior GOPs            (AddStreamFromInput +
                                   re-encode boundary runs        the node's stream plan)
```

Packets are grouped into GOPs in decode order (a keyframe and everything up
to the next keyframe). A GOP's **presentation span** is the range of PTS it
carries: with open GOPs that starts *before* the keyframe, because the
leading B-pictures that display before the keyframe are coded after it. For
a window `[start, end)`:

1. A GOP whose span ends before `start`, or starts at/after `end`, is
   dropped (the last one before the window is kept as decoder priming).
2. A GOP whose keyframe is at/after `start` and whose span ends before `end`
   is **copied** verbatim — no decode, no encode.
3. Any other GOP overlaps a cut and joins the pending **boundary run**.

A run is re-encoded in one encoder session when the next copied GOP (or
the end of the window) arrives: the priming GOP, the run and — when a
copied GOP follows — that GOP's leading pictures are decoded, the frames
inside the window and not yet emitted are re-encoded (the first forced to
an IDR), and the copied GOP then starts at its keyframe. Leading pictures
of a copied GOP are re-encoded rather than copied because they reference
pictures that are no longer in the output; trailing pictures never do.
Window membership is decided by presentation time, so a cut that lands
among the leading pictures of a keyframe keeps exactly the frames inside
the window.

The re-encoded packets have to decode next to the copied ones, which the
node arranges per codec (the *stream plan*, shared with the sink):

- **Framing.** Encoders emit Annex B; an ISOBMFF source (avcC/hvcC) is
  length-prefixed, so re-encoded packets are re-framed to the source's NAL
  framing. Their DTS are derived from display order with the source's
  reorder depth, so they slot into the copied DTS timeline.
- **H.264 with libx264.** The boundary encoder uses a free `sps-id`; its
  SPS/PPS are appended to the output's avcC next to the source's. Nothing
  travels in-band, and copied packets stay byte-identical — except after an
  open-GOP head splice, where copied slice *headers* are renumbered until
  the next IDR (see below; slice data is untouched). The boundary encoder's
  B-frame depth and reference count are kept within the source's.
- **HEVC, and H.264 with other encoders.** Parameter-set ids cannot be
  chosen, so the boundary encoder repeats its VPS/SPS/PPS before each IDR
  and the source's parameter sets are re-inserted in front of every copied
  keyframe. MP4/MOV outputs get the in-band sample entry (`hev1` /
  `avc3`).
- **Open-GOP splices.** The first copied keyframe after a re-encoded head
  is not an IDR. For HEVC its CRA slices become BLA, the standard splicing
  tool: a new coded video sequence with its leading pictures discarded.
  H.264 has no equivalent, so the copied slices' `pic_order_cnt_lsb` and
  `frame_num` are shifted by a constant (fixed-width fields rewritten in
  place; slice data untouched) until the next IDR, so the decoder's output
  order continues from the re-encoded run. Sources with
  `pic_order_cnt_type 2` get a B-frame-free boundary run instead; type 1 is
  not renumbered.

## Audio (`codec_audio: "smartcopy"`)

Smart copy also applies to **audio**, sample-accurately. Because every audio
packet is independently decodable, there are no GOPs: interior packets (fully
inside the window) are copied verbatim and only the packet(s) straddling each
edge are sliced to the exact sample.

**PCM only.** PCM is byte-sliceable at any sample with no decode, no encoder
priming, and no container edit-list surgery, so the result is lossless, the
interior is byte-identical, and the cut is exact:

```json
{
  "codec_audio": "smartcopy",
  "options": { "ss": "12.5", "to": "48.2" }
}
```

Compressed audio (AAC/FLAC/Opus/…) is **rejected** with a clear error: seamless
boundary re-encode would require per-codec priming / gapless handling (per-stream
pre-skip for Opus/Vorbis, edit lists for AAC). For those, use
`codec_audio: <encoder>` — a re-encoded audio stream with an `ss`/`t`/`to`
window is trimmed sample-accurately via an auto-inserted `atrim` filter (see
[json-config-reference.md](json-config-reference.md)) — or `codec_audio: "copy"`
for a packet-accurate (~21 ms) lossless copy.

## Limitations

- Video parameters are identical source→target by design.
- For open-GOP H.264 sources the copied slice *headers* after the head
  cut are renumbered (slice data is byte-identical); when the head cut
  lands within a few frames of a non-IDR keyframe, the copied slices'
  memory-management operations may address pictures that were before the
  cut, which libavcodec reports as `mmco: unref short failure` while
  decoding correctly.
- `hev1` / `avc3` in-band parameter sets and avcC records carrying several
  SPS/PPS are standard, but playback in AVFoundation has not been verified.
- A source that is not a regular file cannot be probed for its reorder
  depth; re-encoded packet DTS are then derived from the run alone and
  clamped at the joins.
- Sample-accurate audio (re-encoding partial audio frames at the edges) is
  not yet implemented; audio is packet-copy-trimmed.
