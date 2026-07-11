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
`x265-params`), an optional **Boundary encoder** override, and a **Global
header** toggle. Set the trim window (Start / Duration / End) on the connected
output's **Timing** section — it applies to every stream of that output.

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
                                   copy interior GOPs            (AddStreamFromInput:
                                   re-encode boundary GOPs        extradata = source)
```

For a window `[start, end)`, with `KF(t)` = keyframe at/before `t`:

1. **Head** — the GOP containing `start` is decoded and its frames with
   `pts >= start` are re-encoded; the first is forced to a keyframe.
2. **Interior** — every whole GOP fully inside `[start, end)` is copied
   verbatim (no decode/encode).
3. **Tail** — the GOP containing `end` is decoded and its frames with
   `pts < end` are re-encoded.

The boundary decoder is primed with the **previous GOP** so open-GOP
sources (whose leading frames reference across a GOP boundary) still
decode with their references present; the primed frames are dropped from
the output.

Audio, subtitle and data streams on the same output are stream-copied and
trimmed to the same window at the muxer (packet-accurate; ~1 audio frame).

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
- Open-GOP sources are handled by previous-GOP priming, but the safest
  results come from closed-GOP sources.
- Sample-accurate audio (re-encoding partial audio frames at the edges) is
  not yet implemented; audio is packet-copy-trimmed.
