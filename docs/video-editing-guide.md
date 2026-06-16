# Video Editing Guide

MediaMolder can assemble clips into a finished video — cuts, trims,
transitions, layering — entirely inside a job graph, no external NLE
required. This guide explains the timeline tool and the timing model that
trips people up.

- [The timeline tool](#the-timeline-tool)
- [The timing model (read this first)](#the-timing-model-read-this-first)
- [`sequence_editor` (multi-track timeline)](#sequence_editor-multi-track-timeline)
- [Transitions](#transitions)
- [Mixed resolutions and formats](#mixed-resolutions-and-formats)
- [Audio](#audio)
- [Running a timeline job](#running-a-timeline-job)
- [GUI workflow](#gui-workflow)
- [Reference](#reference)

---

## The timeline tool

`sequence_editor` is a MediaMolder-native `go_processor` **FrameSource** node:
it opens its own source files and emits a finished video stream, so it takes
**no inbound A/V edge** — you wire its *output* to an encoder/output.

It is a multi-track, NLE-style timeline. Place clips at explicit times on one
or more tracks; upper tracks win where they overlap. It supports `dissolve`
(a linear cross-fade) **and the full libavfilter `xfade` transition set**
between adjacent clips on a track, and can carry an audio stream that
crossfades across each transition.

Full reference: [go-processor-nodes.md § `sequence_editor`](go-processor-nodes.md#sequence_editor).

---

## The timing model (read this first)

Every clip has two clocks: **source time** (where you are inside the source
file) and **timeline time** (where the clip sits in the finished video). Get
these straight and everything else follows.

For `sequence_editor`, each clip declares:

- `timeline_in` — when the clip starts in the output sequence (seconds).
- `source_in` — the source time that maps to `timeline_in`.
- `source_out` — where to stop reading the source. The clip's length is
  `source_out − source_in` (must be > 0).

A clip plays from `timeline_in` until the next clip's `timeline_in`. When it
carries a transition, its `source_out` must extend **past** that hand-off by
the transition duration, so both clips have material during the overlap:

```
source_out  =  source_in  +  (next.timeline_in − this.timeline_in)  +  transition.duration
```

Example — two 3-second clips with a 0.5 s dissolve at the junction:

```
clip A: timeline_in 0, source_in 0, source_out 3.5   (3.0 s of A + 0.5 s overlap)
clip B: timeline_in 3, source_in 3, source_out 6.5
```

The overlap `[3.0, 3.5]` is the cross-fade window: A fades out while B fades
in. The last clip has no transition, so its `source_out` is just
`source_in + length`.

---

## `sequence_editor` (multi-track timeline)

Declare a fixed output `format` and one or more `tracks`. At every output
frame, the clip on the **highest-index track** that covers that time wins
(upper track replaces lower); uncovered times render black.

```json
{
  "id": "seq",
  "type": "go_processor",
  "processor": "sequence_editor",
  "params": {
    "format": {
      "width": 1920, "height": 1080, "pix_fmt": "yuv420p",
      "frame_rate": 29.97, "time_base": [1, 90000], "length_sec": 30
    },
    "tracks": [
      {
        "id": "V1",
        "type": "video",
        "clips": [
          { "input_id": "intro", "source_in": 0,  "source_out": 10.5, "timeline_in": 0,  "transition": { "type": "dissolve",  "duration": 0.5 } },
          { "input_id": "main",  "source_in": 0,  "source_out": 20.5, "timeline_in": 10, "transition": { "type": "wipeleft",  "duration": 0.5 } },
          { "input_id": "outro", "source_in": 0,  "source_out": 10,   "timeline_in": 20 }
        ]
      }
    ]
  }
}
```

- **Sources** are referenced by `input_id` (or `media_id`) pointing at
  top-level Input nodes; the engine resolves them to URLs before the
  processor runs. You may also give a clip a literal `url`.
- **Fixed format** — every source is scaled and retimed into
  `format.width/height/pix_fmt/frame_rate`. `time_base` is the continuous
  output timebase (e.g. `[1, 90000]`); `length_sec` pins the exact duration.
- **Layering** — put a logo or overlay clip on a higher-index track to
  replace the base track wherever it's active. (Transitions themselves stay
  within a single track — see [Transitions](#transitions).)
- **Debugging** — add `"sequence_log": "/tmp/seq.jsonl"` to get one JSON
  record per output frame: which track/clip won, the exact source time
  fetched, whether the frame was held, etc.

Runnable example:
[`testdata/examples/61_sequence_editor_dissolves.json`](../testdata/examples/61_sequence_editor_dissolves.json).
Full field tables: [go-processor-nodes.md § `sequence_editor`](go-processor-nodes.md#sequence_editor).

---

## Transitions

`sequence_editor` joins clips with libavfilter
[`xfade`](https://ffmpeg.org/ffmpeg-filters.html#xfade) transitions. Set the
type and a `duration` (seconds); the duration is the overlap window between
the outgoing and incoming clip.

Common transition names (any `xfade` transition compiled into your FFmpeg):

```
fade  fadeblack  fadewhite  fadegrays
dissolve   (sequence_editor: linear cross-fade; see note)
wipeleft  wiperight  wipeup  wipedown   wipetl wipetr wipebl wipebr
slideleft slideright slideup slidedown
smoothleft smoothright smoothup smoothdown
circleopen circleclose circlecrop rectcrop
vertopen vertclose horzopen horzclose
distance  radial  zoomin  squeezeh squeezev
hlslice hrslice vuslice vdslice  hblur
```

Notes specific to **`sequence_editor`**:

- **`dissolve` is a *linear* cross-fade** (the `blend` filter), kept for
  backward compatibility — this is **not** xfade's dithered `dissolve`. Every
  other name routes through an `xfade` graph.
- Transitions are **within-track, between two adjacent clips**. They do not
  cross-fade between different track layers (layering is a hard replace).
- Unsupported / misspelled transition names are **rejected when the job
  loads**, not silently turned into a hard cut.

Example using xfade transitions:
[`testdata/examples/62_sequence_editor_wipe.json`](../testdata/examples/62_sequence_editor_wipe.json).

---

## Mixed resolutions and formats

`sequence_editor` normalises everything to its declared `format`, so
mixed-resolution / mixed-fps sources just work.

---

## Audio

`sequence_editor` can carry an audio stream taken from the *same clips* as the
picture, crossfaded across each transition. Audio is opt-in from the job JSON.
See [using-mediamolder.md § Audio and crossfades](using-mediamolder.md#515-multi-track-timelines-with-sequence_editor).

---

## Running a timeline job

CLI:

```bash
mediamolder run my-timeline.json
```

The bundled examples (substitute your own inputs/outputs for the `{{…}}`
placeholders, or run them through the example harness):

- `testdata/examples/61_sequence_editor_dissolves.json` — dissolve timeline
- `testdata/examples/62_sequence_editor_wipe.json` — wipe + slide timeline

---

## GUI workflow

The graphical editor has an Inspector for the `sequence_editor` node
(input-node dropdowns, a clip/transition table editor, and the canvas layout
where input nodes are *not* edge-connected to the sequence node). See
[gui.md § Sequence editor — timeline table editor](gui.md#sequence-editor--timeline-table-editor).

---

## Reference

- [Go Processor Nodes](go-processor-nodes.md) — interface, FrameSource model,
  and full `sequence_editor` field tables.
- [Using MediaMolder (CLI)](using-mediamolder.md) — §5.15 timeline workflow.
- [GUI Reference](gui.md) — building timelines on the canvas.
