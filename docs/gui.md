# MediaMolder GUI

The `mediamolder gui` subcommand serves a browser-based visual editor for
building, validating, and running MediaMolder JSON pipelines. It is bundled
into the same single binary as the CLI — no separate install or web server is
required.

## Quick start

```sh
# Build the binary with the embedded production frontend.
make build-gui

# Launch the editor (opens http://127.0.0.1:8080 by default).
./mediamolder gui
```

Useful flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--host`     | `127.0.0.1` | Interface to bind. Use `0.0.0.0` to expose on the LAN. |
| `--port`     | `8080`      | TCP port. |
| `--no-open`  | `false`     | Do not auto-open a browser tab. |
| `--examples` | `testdata/examples` | Directory whose `*.json` files are listed in the **Example** dropdown. Set to `""` to disable. |
| `--dev`      | `false`     | Skip the embedded frontend; expects you to run `npm run dev` separately. |

## Your first pipeline (5-minute walkthrough)

If you have never used the editor before, the canvas opens with an
**onboarding card** that summarises the steps below. Click the
**Help** toolbar button (or press <kbd>?</kbd>) at any time to open the
in-app help dialog.

1. **Pick a starting point.**
   * The fastest path is the **Example ▾** dropdown in the toolbar — choose
     any entry to load a working pipeline you can edit.
   * Otherwise click **New** to start from a blank canvas.
2. **Add a Source.** From the **Sources** category in the left palette, drag
   *Input file* onto the canvas. Click the new node, then in the Inspector
   on the right click **Browse…** next to **URL** to pick a media file from
   your local filesystem.
3. **Add processing nodes.** From the palette categories:
   * **Filters** — libavfilter operations (scaling, colour, denoise, audio
     dynamics, …) grouped by intent. Hover any entry for a tooltip with the
     full description; type in the search box to narrow the list.
   * **Encoders** — codec implementations grouped by stream type (Video /
     Audio / Subtitle).
   * **Processors** — Go-side custom blocks (frame extraction, scene
     detection, transcript writers, …).
4. **Add a Sink.** Drag *Output file* from **Sinks**, click **Browse…** and
   pick **Save** mode in the dialog to choose a destination path and
   filename. Set the **Format** field (e.g. `mp4`) and the encoder names if
   you have not already added explicit encoder nodes.
5. **Connect the nodes.** Each node exposes one handle per stream type on
   each side. Drag from a source handle to a target handle of the **same
   colour**. Mismatched stream types are rejected.
6. **Run.** Click **Run** in the toolbar. The Run panel opens; node badges
   show live `Frames` / `FPS`, and a node that errors is outlined in red.
   Click **Stop** to cancel.
7. **Save / Export.** **Export** downloads the current graph as a
   MediaMolder JSON job. **Import** loads any job JSON (including the
   bundled examples and files written by the CLI).

### Tips

* **Auto layout** rearranges the nodes using a left-to-right Dagre layout
  whenever the graph gets messy.
* `Backspace` / `Delete` removes the selected node. The shortcut is ignored
  while you are typing in a form.
* Press <kbd>?</kbd> (or click the **Help** button) for the in-app help,
  <kbd>Esc</kbd> to dismiss any open dialog.
* The bottom-centre **Stream types** legend shows the colour code used for
  edges and handles. The bottom-right minimap stays clear of it.
* Edges are unlabelled by default. Hover (or click) any edge to open a
  popover listing every technical property MediaMolder can infer for that
  stream — width×height, pix_fmt, frame_rate, color_space, color_range,
  bit_depth, codec profile/level, bit_rate, sample_rate, channel_layout,
  sample_fmt, etc. Click to pin the popover open. Attributes that no
  upstream node has set are intentionally omitted — MediaMolder never
  guesses. See [Edge attributes](#edge-attributes) below.

## File browser

The Browse… buttons next to **Input → URL** and **Output → URL** open a
modal file picker. It does *not* upload files anywhere — it just helps you
type a correct local path.

* The left sidebar lists shortcuts for your home directory, the directory
  the binary was launched from, and the filesystem root.
* The pathbar at the top lets you type a path directly and press
  <kbd>Enter</kbd> or click **Go**, or use the **↑** button to ascend.
* In **Open** mode, double-click a file to select it. The dialog filters by
  common media extensions; toggle this off by clearing the URL field
  before browsing.
* In **Save** mode, navigate to the destination directory, edit the
  **Filename** field at the bottom, and click **Save**. The dialog does
  *not* create the file — that happens when the pipeline runs.
* Hidden files (starting with `.`) are not shown.

## Filter categories

Rather than a flat alphabetical list of ~360 filters, the palette organises
filters by intent:

| Bucket | Examples |
|--------|----------|
| Scaling & geometry | `scale`, `crop`, `pad`, `rotate`, `transpose` |
| Color & exposure | `eq`, `curves`, `colorbalance`, `lut3d` |
| Denoise & deinterlace | `hqdn3d`, `nlmeans`, `yadif`, `bwdif` |
| Sharpen & blur | `unsharp`, `gblur`, `boxblur` |
| Text & overlays | `drawtext`, `drawbox`, `overlay` |
| Timing & framerate | `fps`, `setpts`, `framerate`, `tinterlace` |
| Format conversion | `format`, `setdar`, `setsar`, `pixfmt`-related |
| Metadata & inspection | `metadata`, `signalstats`, `cropdetect` |
| Hardware acceleration | `*_qsv`, `*_cuda`, `*_vaapi`, `*_videotoolbox` |
| Subtitles | `subtitles`, `ass` |
| Audio: format & routing | `aresample`, `aformat`, `pan`, `amerge` |
| Audio: dynamics & loudness | `loudnorm`, `acompressor`, `alimiter` |
| Audio: EQ & effects | `equalizer`, `bass`, `treble`, `aecho` |
| Audio: visualisation | `showwaves`, `showspectrum` |
| Other | anything that does not match the heuristics above |

Each entry shows a friendly label first (e.g. *Scale — set the input video
size*) with the canonical libavfilter name underneath. Hover for the full
description. Use the search box to narrow across all categories — matches
expand the relevant subcategories automatically.

## Anatomy

```
┌──────────────────────────────────────────────────────────────┐
│ Toolbar  [Example ▾] [Auto layout] [New] [Import] [Export]   │
│          [Run] / [Stop] [Show log]                           │
├────────────┬─────────────────────────────────────┬───────────┤
│            │                                     │           │
│  Palette   │            Canvas                   │ Inspector │
│  (search   │   (React Flow with stream-typed     │  (form    │
│   filters, │    handles + edges)                 │   for the │
│   codecs,  │                                     │  selected │
│   processors)                                    │   node)   │
│            │                                     │           │
└────────────┴─────────────────────────────────────┴───────────┘
                                       ┌──────────────┐
                                       │  Run panel   │
                                       │  (status,    │
                                       │   per-node   │
                                       │   metrics,   │
                                       │   error log) │
                                       └──────────────┘
```

### Palette

Populated at runtime from `GET /api/nodes`, which lists every libavfilter,
libavcodec encoder, demuxer/muxer, and registered Go processor available in
the binary you are running. Drag any entry onto the canvas to spawn a
configured node.

### Canvas

* Each node exposes one source and one target handle per stream type
  (video / audio / subtitle / data). Handles only accept connections of the
  same type — incompatible drags are rejected.
* Edges are colour-coded by stream type.
* Hover or click any edge to open a popover with every technical property
  the editor can infer for the stream (see [Edge attributes](#edge-attributes)).
* Node positions are persisted into the saved JSON under `graph.ui.positions`
  (schema v1.2) so reopening a job preserves the layout. The runtime ignores
  this block — it is metadata for the editor only.
* `Backspace` / `Delete` removes the selected node (input fields are not
  hijacked).

### Edge attributes

Edges carry no inline label. Hover an edge — or click to pin it — to
open a popover at the midpoint listing every known technical property
of the stream travelling along it. Typical content for a probed input:

```
Video:
  size            1920×1080
  pix_fmt         yuv420p
  frame_rate      29.97 fps
  bit_depth       8 bit
  color_space     bt709
  color_range     tv
  color_primaries bt709
  color_transfer  bt709
  codec           h264
  profile         High@L4.0
  bit_rate        8.50 Mbps

Audio:
  sample_rate     48000 Hz
  channels        2 (stereo)
  sample_fmt      fltp
  bit_depth       32 bit
  codec           aac
  profile         LC
  bit_rate        192 kbps
```

The values are inferred at edit time from upstream node parameters by
walking the graph backwards from the edge:

1. Look at the immediate source node. If its `params` set a known
   attribute (`pix_fmt`, `width`, `height`, `frame_rate`, `sample_rate`,
   `channel_layout`, `sample_fmt`, `bit_rate`, …) record it.
2. Apply filter-specific shortcuts: `scale` / `scale_*` contribute
   `width` and `height`; `format` contributes `pix_fmt`; `fps` /
   `framerate` contribute `frame_rate`; `aresample` / `asetrate` /
   `aformat` contribute `sample_rate` / `sample_fmt` / `channel_layout`;
   `encoder` nodes contribute `codec` and `bit_rate`; `Output` nodes
   contribute their `codec_video` / `codec_audio` / `codec_subtitle`.
3. For any attribute not yet known, repeat the lookup on the source's
   own incoming edges (same stream type). The closest node that
   establishes a value wins, so transparent passthroughs like
   `setpts` or `drawtext` correctly propagate the upstream value.
4. Attributes that no node has set are omitted — the popover never
   guesses. An edge with no upstream constraints renders the path
   only and shows nothing on hover.

The popover labels each value with the node that established it
(`pix_fmt: yuv420p · format0`).

The inference code lives in
[`frontend/src/lib/streamAttrs.ts`](../frontend/src/lib/streamAttrs.ts);
add a new entry to `attrsFromGraphNode` to teach the editor about a new
filter or processor that constrains a property.

#### Seeding from the source file ("Get properties")

When the inputs to a graph are unknown, downstream attribute inference can
only show what the JSON explicitly declares — usually nothing for a
freshly-dropped Input node. To bootstrap the chain, click an Input node
and press **Get properties** in the Inspector. The editor calls
`POST /api/probe` with the input URL; the backend opens the file with
`avformat_open_input` + `avformat_find_stream_info` and returns one entry
per stream with all available technical metadata: codec (and FourCC tag),
profile/level, bit_rate, bit_depth, width/height, pix_fmt, frame_rate
(and r_frame_rate), sample aspect ratio, field order, color_space,
color_range, color_primaries, color_transfer, sample_rate, sample_fmt,
channels, channel_layout, duration, and start time. The probed values
are attached to the Input node (editor-only — never written back into the
JSON) and become the seed for the upstream walk, so every connection
downstream of that input gets accurate edge popovers automatically.
Image files (jpg/png/...) are reported as a single video stream with the
image's geometry and pixel format.

The probed metadata is invalidated when the URL changes; click
**Get properties** again after editing the path.

### Inspector

The right-hand panel shows a typed form for the selected node. Codec, filter,
and processor parameters surface as editable fields; arbitrary key/value pairs
can be added for less common options.

### Run panel

Click **Run** to execute the current graph. The frontend POSTs the job to
`/api/run`, then opens an `EventSource` against `/api/events/{jobId}` to
receive a stream of typed events:

| Event      | Payload                                                                 |
|------------|-------------------------------------------------------------------------|
| `state`    | `{from, to}` — pipeline state transitions (Ready → Playing → ...).     |
| `metrics`  | `{State, Elapsed, Nodes:[{NodeID, Frames, FPS, Errors, ...}]}` snapshot.|
| `error`    | `{node_id, stage, error}` — per-node failures.                          |
| `log`      | `{message}` — informational entries (e.g. EOS).                         |
| `metadata` | `pipeline.ProcessorMetadata` events from custom processors.             |
| `done`     | `{status: "succeeded"\|"failed"\|"canceled", error}` — terminal event.  |

Live data is merged back into each node on the canvas: frame counts and FPS
appear as badges, and any node that has logged an error is outlined in red.
**Stop** cancels the underlying `context.Context` so the run unwinds cleanly.

## HTTP API

All endpoints are unauthenticated and intended for `localhost` use. Bind
explicitly to `127.0.0.1` (the default) if untrusted users share the host.

| Method | Path                          | Purpose                                               |
|--------|-------------------------------|-------------------------------------------------------|
| `GET`  | `/api/health`                 | Liveness probe.                                       |
| `GET`  | `/api/nodes`                  | Catalogue of available filters/codecs/processors.     |
| `GET`  | `/api/examples`               | List of bundled example job JSONs.                    |
| `GET`  | `/examples/{file}`            | Static serve of the examples directory.               |
| `POST` | `/api/validate`               | Parse + structurally validate a posted JobConfig.     |
| `POST` | `/api/run`                    | Start a run; returns `{job_id}`.                      |
| `POST` | `/api/cancel/{jobId}`         | Cancel an in-flight run.                              |
| `GET`  | `/api/events/{jobId}`         | Server-Sent Events stream for the run.                |
| `GET`  | `/api/files`                  | List a directory (`?path=&filter=ext1,ext2&dirs_only=`). |
| `POST` | `/api/probe`                  | Probe an input URL with libavformat. Body `{url, options?}`; response `{url, streams: [{index, type, codec, codec_tag, profile, level, bit_rate, bit_depth, bits_per_coded_sample, bits_per_raw_sample, width, height, pix_fmt, frame_rate, r_frame_rate, sar, field_order, color_space, color_range, color_primaries, color_transfer, sample_rate, sample_fmt, channels, channel_layout, duration_sec, start_sec, time_base_num, time_base_den}]}`. Used by the Inspector's **Get properties** button. |

### Why SSE rather than WebSockets?

Progress streaming is one-way (server → client), so SSE is sufficient and
considerably simpler:

* `EventSource` is built into every modern browser; no client library needed.
* No additional Go module dependency.
* Auto-reconnect and event framing are handled by the protocol.

The job manager keeps the most recent 64 events per run in memory so a client
that connects mid-run still sees prior `error`/`state` events.

## Development workflow

```sh
# Terminal 1: backend in dev mode (skips the embedded frontend).
make gui-dev

# Terminal 2: Vite dev server with hot reload + /api proxy.
make frontend-dev
```

Open <http://127.0.0.1:5173>. Edits to `frontend/src/**` reload instantly;
edits to Go code require restarting the backend.

To produce a single shippable binary with the production frontend embedded:

```sh
make build-gui
```

## Schema impact

The GUI persists node positions under `graph.ui.positions` keyed by node ID
(schema v1.2). Older `schema_version: "1.0"` and `"1.1"` jobs load and run
unchanged; the editor will add the `ui` block on save. Pipelines authored
without the GUI never need to include it.

## Security considerations

* The GUI server has no authentication. Treat it as a developer tool and bind
  it to a trusted interface.
* `/api/run` accepts any JobConfig the local pipeline package can parse,
  including file paths the binary has access to. Do not expose the port on
  untrusted networks.
* The job manager retains the 16 most recent finished runs (events + metrics)
  in memory; older runs are garbage-collected.

## Troubleshooting

| Symptom | Likely cause / fix |
|---------|--------------------|
| **Blank canvas, palette empty.** | The frontend could not reach `/api/nodes`. Check the terminal where `mediamolder gui` is running for an error and confirm the page URL points at the same host/port. |
| **"Too many redirects" loading the page.** | You are hitting an old build. Rebuild with `make build-gui` (the embedded SPA fallback no longer rewrites `/index.html`). |
| **Browse… dialog shows "permission denied".** | The directory is not readable by the user running `mediamolder`. Either `chmod` it or pick a different location. |
| **Connections rejected when wiring nodes.** | Handles are stream-typed. A video output cannot connect to an audio input — see the bottom-centre legend for the colour code. |
| **`Run` button does nothing.** | The pipeline failed validation. Check the toolbar for a red error banner; common causes are missing URLs, dangling outputs, or unknown filter/codec names. |
| **No live FPS in node badges.** | The pipeline is not in `Playing` state. Confirm the Run panel shows progressing frame counts; otherwise check the `error` events in the panel. |
| **Filter not in the palette.** | The binary was built without that filter (e.g. a stripped FFmpeg). Rebuild FFmpeg with the missing component enabled. |
| **`mediamolder` binary date didn't change after `go build ./...`.** | `go build ./...` is a compile check only. Use `make build-gui` or `go build -o mediamolder ./cmd/mediamolder` to actually overwrite the binary. |
