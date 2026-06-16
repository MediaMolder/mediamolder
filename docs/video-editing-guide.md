# Video Editing Guide

MediaMolder assembles clips into a finished video — cuts, trims, transitions,
layering, and audio crossfades — entirely inside a job graph. No external editor
required.

One node does all of it: **`sequence_editor`**. You describe a timeline (which
clip plays when), wire its output to an encoder, and run the job. This guide
starts with a complete working example and then adds one capability at a time.

- [Your first timeline](#your-first-timeline)
- [How timing works](#how-timing-works)
- [Transitions](#transitions)
- [Audio](#audio)
- [Layering with multiple tracks](#layering-with-multiple-tracks)
- [Mixed resolutions and formats](#mixed-resolutions-and-formats)
- [Running the job](#running-the-job)
- [Reference](#reference)

---

## Your first timeline

Here is a complete job that plays two clips back-to-back (a hard cut) and renders
a 13-second 1080p video. Save it as `my-timeline.json`, edit the paths, and run
`mediamolder run my-timeline.json`.

```json
{
  "schema_version": "1.1",
  "inputs": [
    { "id": "clip_a", "url": "/path/to/a.mp4" },
    { "id": "clip_b", "url": "/path/to/b.mp4" }
  ],
  "graph": {
    "nodes": [
      {
        "id": "seq",
        "type": "go_processor",
        "processor": "sequence_editor",
        "params": {
          "format": { "width": 1920, "height": 1080, "pix_fmt": "yuv420p", "frame_rate": 30, "length_sec": 13 },
          "tracks": [
            { "id": "V1", "type": "video", "clips": [
              { "input_id": "clip_a", "source_in": 0, "source_out": 7, "timeline_in": 0 },
              { "input_id": "clip_b", "source_in": 0, "source_out": 6, "timeline_in": 7 }
            ]}
          ]
        }
      },
      { "id": "enc", "type": "encoder", "params": { "codec": "libx264" } }
    ],
    "edges": [
      { "from": "seq", "to": "enc",    "type": "video" },
      { "from": "enc", "to": "out0:v", "type": "video" }
    ]
  },
  "outputs": [ { "id": "out0", "url": "/path/to/output.mp4" } ]
}
```

The three things to understand:

- **`sequence_editor` is a FrameSource** — it opens its own source files and
  emits a finished video stream, so it takes **no input edge**. You declare the
  source files as `inputs` and reference them from clips by `input_id`; you wire
  the node's *output* to the encoder.
- **`format` is the project setting.** Every clip is scaled and retimed into this
  resolution, frame rate, and pixel format, so mixed sources combine seamlessly.
  `length_sec` pins the exact output duration.
- **Each clip is a placement**: a slice of a source (`source_in` → `source_out`)
  dropped onto the timeline at `timeline_in`.

That's the whole pattern. Everything below adds to this one job.

---

## How timing works

Every clip has two clocks. Keep them straight and the rest follows:

- **source time** — where you are *inside the source file* (`source_in`,
  `source_out`).
- **timeline time** — where the clip sits *in the finished video* (`timeline_in`).

A clip starts at its `timeline_in` and plays until the next clip's `timeline_in`.
So for a plain cut, a clip's source length just fills the gap until the next one:

```
source_out  =  source_in  +  (next.timeline_in − this.timeline_in)
```

In the first example, `clip_a` runs `[0, 7)` on the timeline and `clip_b` runs
`[7, 13)` — they abut, no overlap. The last clip has no successor, so its
`source_out` is simply `source_in + how_long_you_want_it`.

Transitions change this one rule (next section): the outgoing clip must supply a
little *extra* source so both clips have footage during the overlap.

---

## Transitions

To cross-fade or wipe between two clips, add a `transition` to the **outgoing**
clip and extend its `source_out` so the two clips overlap by the transition
duration:

```
source_out  =  source_in  +  (next.timeline_in − this.timeline_in)  +  transition.duration
```

Two 6.5-second clips joined by a 0.5 s dissolve:

```json
"clips": [
  { "input_id": "clip_a", "source_in": 0, "source_out": 7,   "timeline_in": 0,
    "transition": { "type": "dissolve", "duration": 0.5 } },
  { "input_id": "clip_b", "source_in": 0, "source_out": 6.5, "timeline_in": 6.5 }
]
```

The window `[6.5, 7.0]` is the cross-fade: `clip_a` fades out while `clip_b`
fades in. The transition's `duration` is that overlap, in seconds.

Available transition types — `dissolve` (a linear cross-fade) plus the full
`xfade` set, composited by MediaMolder's native Go engine:

```
fade  fadeblack  fadewhite  fadegrays
wipeleft  wiperight  wipeup  wipedown   wipetl wipetr wipebl wipebr
slideleft slideright slideup slidedown
smoothleft smoothright smoothup smoothdown
circleopen circleclose circlecrop rectcrop
vertopen vertclose horzopen horzclose
distance  radial  zoomin  squeezeh squeezev
hlslice hrslice vuslice vdslice  hblur
```

A misspelled or unsupported transition name is **rejected when the job loads**,
not silently turned into a hard cut. Runnable example:
[`testdata/examples/62_sequence_editor_wipe.json`](../testdata/examples/62_sequence_editor_wipe.json).

---

## Audio

`sequence_editor` can mix an audio track from the **same clips** as the picture,
crossfading the sound across each transition automatically. It's opt-in and takes
two additions to the job:

1. Add `sample_rate` (and optionally `channels`, default 2) to `format`:

   ```json
   "format": { "width": 1920, "height": 1080, "frame_rate": 30,
               "sample_rate": 48000, "channels": 2, "length_sec": 13 }
   ```

2. Wire the node's **audio** output to an audio encoder (the node now has both a
   `video` and an `audio` output):

   ```json
   "nodes": [
     { "id": "seq",  "type": "go_processor", "processor": "sequence_editor", "params": { … } },
     { "id": "venc", "type": "encoder", "params": { "codec": "libx264" } },
     { "id": "aenc", "type": "encoder", "params": { "codec": "aac", "b": "192000" } }
   ],
   "edges": [
     { "from": "seq",  "to": "venc",   "type": "video" },
     { "from": "seq",  "to": "aenc",   "type": "audio" },
     { "from": "venc", "to": "out0:v", "type": "video" },
     { "from": "aenc", "to": "out0:a", "type": "audio" }
   ]
   ```

Each clip's video `transition` now also crossfades the audio across the same
window. To tune it, add a `transition.audio` object: `curve` (e.g. `qsin` for an
equal-power fade), `duration` (defaults to the video transition's), or
`off` to hard-cut the sound while the picture still transitions. A clip whose
source has no audio contributes silence. Runnable example:
[`testdata/examples/63_sequence_editor_audio.json`](../testdata/examples/63_sequence_editor_audio.json).

---

## Layering with multiple tracks

Tracks stack. At every output frame, the clip on the **highest-numbered track**
that covers that time wins (an upper track replaces the lower one wherever it's
present); uncovered times render black. Use this for logos, lower-thirds,
picture-in-picture cutaways, or multi-cam selects.

```json
"tracks": [
  { "id": "V1", "type": "video", "clips": [ … the base timeline … ] },
  { "id": "V2", "type": "video", "clips": [
    { "input_id": "logo", "source_in": 0, "source_out": 5, "timeline_in": 2 }
  ]}
]
```

Here the `logo` clip replaces the base track for the 5 seconds starting at
timeline 2 s. Transitions stay **within a single track** (between adjacent clips);
moving between track layers is a hard replace, not a cross-fade.

---

## Mixed resolutions and formats

You don't have to pre-normalise anything. `sequence_editor` scales and retimes
every source into the `format` you declared, so a 4K 24 fps clip and a 1080p
30 fps clip drop onto the same timeline and combine cleanly.

---

## Running the job

**CLI:**

```bash
mediamolder run my-timeline.json
```

The bundled examples render against any input you point them at (substitute the
`{{input}}` / `{{output}}` placeholders):

- `testdata/examples/61_sequence_editor_dissolves.json` — dissolve timeline
- `testdata/examples/62_sequence_editor_wipe.json` — wipe + slide timeline
- `testdata/examples/63_sequence_editor_audio.json` — audio with crossfades

**GUI:** select the `sequence_editor` node to edit the output format and audio
inline, and open the spreadsheet-style **Edit Timeline…** dialog to add clips and
transitions in a table. Input nodes appear on the canvas but are *not*
edge-connected to the sequence node (the processor opens them itself). See
[gui.md § Sequence editor — timeline table editor](gui.md#sequence-editor--timeline-table-editor).

---

## Reference

- [Go Processor Nodes § `sequence_editor`](go-processor-nodes.md#sequence_editor)
  — the full field tables and the FrameSource model.
- [Using MediaMolder § 5.15](using-mediamolder.md#515-multi-track-timelines-with-sequence_editor)
  — the same workflow from the CLI/JSON reference.
- [Video Transitions](architecture/transitions.md) — how the native Go transition
  and audio-crossfade engines work, and how to add your own.
