# Using MediaMolder

This guide covers every feature available in MediaMolder, from installing the binary and running jobs at the command line to building and running complex graphs visually in the browser-based GUI editor.

---

## Contents

1. [Installation](#1-installation)
2. [Core concepts](#2-core-concepts)
3. [Command-line interface](#3-command-line-interface)
   - [run](#31-run)
   - [inspect](#32-inspect)
   - [validate](#33-validate)
   - [convert-cmd](#34-convert-cmd)
   - [list-codecs / list-filters / list-formats / list-hw-devices](#35-list-commands)
   - [version](#36-version)
   - [gui](#37-gui)
   - [perf](#38-perf)
   - [hwbench](#39-hwbench)
   - [py-scene-detect](#310-py-scene-detect)
4. [Graph JSON reference](#4-graph-json-reference)
   - [Top-level structure](#41-top-level-structure)
   - [inputs](#42-inputs)
   - [graph.nodes](#43-graphnodes)
   - [graph.edges](#44-graphedges)
   - [outputs](#45-outputs)
   - [global_options](#46-global_options)
5. [Common CLI workflows](#5-common-cli-workflows)
   - [Simple transcode](#51-simple-transcode)
   - [Stream copy (remux)](#52-stream-copy-remux)
   - [Filter chains](#53-filter-chains)
   - [Multi-input and multi-output](#54-multi-input-and-multi-output)
   - [Hardware-accelerated encoding](#55-hardware-accelerated-encoding)
   - [HLS and DASH streaming outputs](#56-hls-and-dash-streaming-outputs)
   - [Tee (fan-out) outputs](#57-tee-fan-out-outputs)
   - [Subtitles](#58-subtitles)
   - [Bitstream filters](#59-bitstream-filters)
   - [Live / device inputs](#510-live--device-inputs)
   - [Scene detection in a pipeline](#511-scene-detection-in-a-pipeline)
6. [Validation](#6-validation)
7. [Graphical user interface](#7-graphical-user-interface)
   - [Launching the GUI](#71-launching-the-gui)
   - [Canvas](#72-canvas)
   - [Palette (left sidebar)](#73-palette-left-sidebar)
   - [Inspector (right sidebar)](#74-inspector-right-sidebar)
   - [Toolbar reference](#75-toolbar-reference)
   - [Run panel](#76-run-panel)
   - [Validate panel](#77-validate-panel)
   - [File browser](#78-file-browser)
   - [Asset manager](#79-asset-manager)
   - [Hardware dialog](#710-hardware-dialog)
   - [Import / Export FFmpeg commands](#711-import--export-ffmpeg-commands)
   - [Keyboard shortcuts](#712-keyboard-shortcuts)
8. [Tips and troubleshooting](#8-tips-and-troubleshooting)

---

## 1. Installation

See [installation guide](installation.md).

---

## 2. Core concepts

MediaMolder accepts a single JSON file containing the media graph and all runtime options and parameters. It replaces the FFmpeg's command-line string with a **structured JSON graph file**. A graph has three sections:

| Section | Purpose |
|---|---|
| `inputs` | Declare every source file, device, or URL |
| `graph` | The Directed Acyclic Graph (DAG) — defines how the nodes are connected with typed edges |
| `outputs` | Every output file or stream destination |

Each input is assigned a short **id** (`"src"`, `"bg"`, etc.) and each stream inside it is mapped by index and type. Edges connect input streams through graph nodes to output sinks using the notation `"<id>:<type>:<track>"`.

MediaMolder reads the JSON, builds an in-memory processing graph, validates it, opens every input and output, and runs the frame loop using the libav* C libraries directly — without shelling out to the `ffmpeg` binary.

For more details, see the [concepts guide](concepts-and-graph-basics.md).

---

## 3. Command-line interface

```
Usage: mediamolder <command> [args]
```

Run `mediamolder help` at any time for a summary of all commands.

---

### 3.1 `run`

Execute a graph.

```sh
mediamolder run <config.json>
```

Signals `SIGINT` / `SIGTERM` (Ctrl-C) cancel the run cleanly, flushing any in-progress frames.

| Flag | Description |
|---|---|
| `--realtime` | Enable adaptive real-time mode — see [§5.12](#512-real-time-mode) |
| `--json` | Output progress snapshots as JSON Lines on stderr instead of the default human-readable line |
| `--metadata-out <path>` | Write processor metadata events (e.g. scene-change JSON) to a file; use `-` for stdout |
| `--set KEY=VALUE` | Substitute `{{KEY}}` in the JSON config before parsing; may be repeated |

---

### 3.2 `inspect`

Pretty-print the **resolved** graph config as indented JSON, then exit. No media I/O occurs.

```sh
mediamolder inspect transcode.json
```

`inspect` runs the full config parser and normalisation pass (`NormalizeConfig`), so it shows the graph *after* implicit encoder nodes have been synthesised and shorthand fields lowered. This is the definitive view of what `run` will actually execute.

---

### 3.3 `validate`

Statically analyse a graph config and report issues — no encoding happens.

```sh
mediamolder validate [--no-probe] [--json] [--strict] <config.json>
```

| Flag | Effect |
|---|---|
| *(no flags)* | Phase A static checks + Phase B probe-assisted checks. Human-readable output. |
| `--no-probe` | Skip Phase B; run Phase A static analysis only (no file I/O, sub-millisecond). |
| `--json` | Emit the full `ValidationReport` as JSON for programmatic consumption. |
| `--strict` | Treat `WARNING`-level issues as errors (exit code 1). |

**Exit codes:**

| Code | Meaning |
|---|---|
| `0` | No issues (or INFO-only). |
| `1` | One or more `ERROR`-level issues found (or `WARNING` with `--strict`). |
| `2` | `WARNING`-level issues found (without `--strict`). |

Validation runs in two phases:

- **Phase A — Static analysis.** Uses only config fields and compile-time codec/container metadata. Detects missing required fields, incompatible codec↔container pairs, filter arity errors, and security constraint violations. Runs in < 1 ms; suitable for CI.
- **Phase B — Probe-assisted analysis.** Opens each input with `avformat_find_stream_info` to read the actual pixel format, interlacing, sample rate, channel layout, color primaries, etc. Detects runtime issues such as missing deinterlace filters, HDR→SDR color gamut mismatches, VFR-to-CFR conversion needs, and audio resampling requirements.

Each issue has:
- **Severity** — `ERROR`, `WARNING`, or `INFO`
- **Code** — machine-readable key (e.g. `VIDEO_INTERLACED_NO_DEINTERLACE`)
- **Location** — node ID, edge index, or input ID
- **Message** — plain-English explanation
- **Suggestion** — how to fix it (often includes the exact filter name and parameters)
- **Fix** — for supported issues the CLI (and GUI) can apply a one-click repair

---

### 3.4 `convert-cmd`

Convert an FFmpeg command line to a MediaMolder JSON config and print it to stdout.

```sh
mediamolder convert-cmd "ffmpeg -i input.mp4 -vf scale=1280:720 -c:v libx264 -c:a aac output.mp4"
```

The output can be piped directly to a file:

```sh
mediamolder convert-cmd "ffmpeg -i in.mp4 -c:v libx265 out.mkv" > job.json
mediamolder run job.json
```

Both single-string and multi-argument forms are accepted:

```sh
# Single quoted string
mediamolder convert-cmd "ffmpeg -i in.mp4 -c:v libx264 out.mp4"

# Multi-argument (useful from scripts)
mediamolder convert-cmd ffmpeg -i in.mp4 -c:v libx264 out.mp4
```

For a complete mapping table of FFmpeg options to JSON fields, see [ffmpeg-migration-guide.md](ffmpeg-migration-guide.md).

---

### 3.5 `export`

Export a MediaMolder graph (JSON) to the equivalent FFmpeg command line (the inverse of `convert-cmd`). The command is written to stdout; normalisation warnings and any unsupported-feature notes go to stderr.

```sh
mediamolder export [--from-graph] <config.json>
```

| Flag | Default | Description |
|---|---|---|
| `--from-graph` | `false` | Normalise through `pipeline.NormalizeConfig` first, then render via `ExportGraph`; produces a more accurate result for graphs that use implicit encoders or shorthand fields |

**Examples:**

```sh
# Quick export — direct config-to-command translation
mediamolder export job.json

# Graph-normalised export — resolves implicit encoders and shorthand fields
mediamolder export --from-graph job.json

# Copy the command to the clipboard (macOS)
mediamolder export job.json | pbcopy
```

Not every MediaMolder feature has a direct FFmpeg CLI equivalent (per-node error policies, named assets, etc.). Such features are reported as notes on stderr and omitted from the command. The canonical, lossless representation of a pipeline is always the JSON file.

---

### 3.6 List commands

These commands query the libav* libraries at runtime and return JSON arrays.

```sh
mediamolder list-codecs          # all encoders and decoders
mediamolder list-filters         # all available filters
mediamolder list-formats         # all muxers and demuxers
mediamolder list-hw-devices      # hardware acceleration device types
```

All output is JSON. Pipe through `jq` for filtering:

```sh
# Find all HEVC encoders
mediamolder list-codecs | jq '[.[] | select(.name | contains("hevc") or contains("h265"))]'

# Find GPU-capable encoders
mediamolder list-codecs | jq '[.[] | select(.name | test("nvenc|vaapi|qsv|amf|vt"))]'
```

---

### 3.7 `version`

Print the versions of all linked libav* libraries.

```sh
mediamolder version
```

Example output:
```
libavcodec  62.3.100
libavformat 62.1.101
libavfilter 11.3.100
libavutil   60.3.100
libswresample 5.3.100
libswscale   8.3.100
```

---

### 3.8 `gui`

Launch the browser-based visual job editor.

```sh
mediamolder gui [--port <port>] [--static <path>]
```

| Flag | Default | Description |
|---|---|---|
| `--port` | `7042` | HTTP port to listen on |
| `--static` | auto-detected | Path to the built frontend (`frontend/dist/`) |

The server starts, prints the URL, and opens the browser automatically on macOS, Windows, and Linux (via `xdg-open`). Press **Ctrl-C** to stop.

```
MediaMolder GUI
  Listening on http://localhost:7042
  Press Ctrl-C to stop.
```

The GUI communicates with the local Go process over HTTP. JSON config files saved through the GUI are fully compatible with `mediamolder run`.

### 3.9 `perf`

Display a live terminal table of per-node performance data for a running
pipeline.  Polls the pipeline's `/perf` JSON endpoint and redraws the
display in place at the configured interval.

```sh
mediamolder perf [--url <url>] [--interval <duration>]
```

| Flag | Default | Description |
|---|---|---|
| `--url` | `http://localhost:9090/perf` | URL of the running pipeline's `/perf` endpoint |
| `--interval` | `1s` | How often to refresh the display |

The table columns are **NODE**, **FPS**, **TARGET**, **DEFICIT**, **ACTIVE%**,
**IDLE%**, **STALL%**, **THREADS**, **BUSY**, and **LATENCY**.  Rows are
colour-coded green/amber/red relative to the FPS target.  Press **Ctrl-C**
to exit.

The `/perf` endpoint is served by the `MetricsServer` when a pipeline is
running.  Start the server with `--metrics-addr :9090` (or configure it in
code via `observability.NewMetricsServer`).

### 3.10 `hwbench`

Benchmark hardware and software codec encode/decode throughput and optionally
contribute the results to the MediaMolder community capability database.

```sh
mediamolder hwbench [--device <type>] [--codecs <list>] [--resolutions <list>]
                    [--frames N] [--warmup N] [--output <path>] [--stdout]
                    [--caps-only]
```

| Flag | Default | Description |
|---|---|---|
| `--device` | _(none, SW only)_ | Hardware device type (`cuda`, `videotoolbox`, `vaapi`, `qsv`) |
| `--codecs` | All supported | Comma-separated encoder names, e.g. `h264_nvenc,hevc_nvenc` |
| `--resolutions` | 360p → 4K | Comma-separated `WxH` targets, e.g. `1920x1080,3840x2160` |
| `--frames` | `200` | Frames to time per codec × resolution |
| `--warmup` | `20` | Warmup frames before timing (lets GPUs reach steady state) |
| `--output` | `hwbench_report_<ts>.json` | Path for the JSON report |
| `--stdout` | `false` | Write JSON to stdout instead of a file |
| `--caps-only` | `false` | Print hardware capabilities without benchmarking |

See [docs/benchmarks.md](benchmarks.md) for full documentation, the JSON
report schema, and contribution instructions.

### 3.10 `py-scene-detect`

Analyse a single media file for scene boundaries without encoding anything.
Decodes every frame, runs one of five detectors ported from
[PySceneDetect](https://github.com/Breakthrough/PySceneDetect) by Brandon Castellano
(BSD-3-Clause), and writes the scene list to stdout or a file.

The same five detectors are also available as `go_processor` nodes inside a
pipeline graph (see [§5.11](#511-scene-detection-in-a-pipeline)), letting you
detect scenes *during* a transcode in a single pass with no extra decode step.
Use this subcommand when you need the scene list before building or running a
graph.

```sh
mediamolder py-scene-detect [flags] <input>
```

| Flag | Default | Description |
|---|---|---|
| `--detector` | `content` | Algorithm: `content`, `adaptive`, `threshold`, `hash`, `histogram` |
| `--threshold` | _(detector default)_ | Override detector threshold (0 = use detector default) |
| `--luma-only` | `false` | `content` / `adaptive`: use luma channel only |
| `--min-scene-len` | `0.6s` | Minimum scene length — frames (`15`), seconds (`0.6s`), or timecode (`00:00:00.600`) |
| `--output` | `-` (stdout) | Write scene list to a file; `-` = stdout |
| `--format` | `jsonl` | Output format: `jsonl`, `csv`, or `timecodes` |
| `--stats` | _(none)_ | Write per-frame detector statistics to a CSV file |
| `--downscale` | `0` (auto) | Downscale factor: `0` = auto (based on frame width), `1` = disabled, `N` = N× |

**Quick examples:**

```sh
# Content detector (default) — JSONL to stdout.
mediamolder py-scene-detect input.mp4

# Adaptive detector with custom threshold; write CSV.
mediamolder py-scene-detect --detector=adaptive --threshold=2.5 \
  --format=csv --output=scenes.csv input.mp4

# Fade-to-black detection; print cut timecodes only.
mediamolder py-scene-detect --detector=threshold --threshold=15 \
  --format=timecodes input.mp4

# Perceptual hash detector; also export per-frame stats.
mediamolder py-scene-detect --detector=hash --stats=frame_stats.csv input.mp4
```

**Output formats:**

| Format | Description |
|---|---|
| `jsonl` | One JSON object per scene, one per line: `{"scene":1,"start_frame":0,"start_timecode":"00:00:00.000","end_frame":149,…}` |
| `csv` | Scene table compatible with PySceneDetect's CSV output |
| `timecodes` | Comma-separated cut timecodes for use with FFmpeg `-ss` or chapter markers |

For algorithm descriptions, parameter references, and a speed-vs-accuracy
comparison table see [docs/scene-detection.md](scene-detection.md).

---

## 4. Graph JSON reference

### 4.1 Top-level structure

```json
{
  "schema_version": "1.0",
  "inputs":  [ ... ],
  "graph":   { "nodes": [...], "edges": [...] },
  "outputs": [ ... ],
  "global_options": { ... }
}
```

| Field | Type | Required | Description |
|---|---|---|---|
| `schema_version` | string | yes | Must be `"1.0"` |
| `inputs` | array | yes | Input sources |
| `graph` | object | yes | Processing graph |
| `outputs` | array | yes | Output sinks |
| `global_options` | object | no | Graph-wide settings |

---

### 4.2 `inputs`

```json
{
  "id": "src",
  "url": "input.mp4",
  "streams": [
    { "input_index": 0, "type": "video", "track": 0 },
    { "input_index": 0, "type": "audio", "track": 0 }
  ]
}
```

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string | yes | Unique identifier, referenced in edge `from` endpoints |
| `url` | string | yes | File path, RTSP/RTMP/HTTP URL, or device specifier |
| `streams` | array | yes | Stream selections |
| `format` | string | no | Force demuxer (e.g. `"v4l2"`, `"avfoundation"`) |
| `options` | object | no | Demuxer AVOptions (e.g. `{"rtsp_transport": "tcp"}`) |
| `hw_device` | string | no | Named hardware device context to use for decoding |
| `hwaccel` | string | no | Hardware acceleration backend (`"cuda"`, `"vaapi"`, `"qsv"`, `"videotoolbox"`) |
| `hwaccel_device` | string | no | Override which HW device context to use (`-hwaccel_device`) |
| `hwaccel_output_format` | string | no | Pixel format to request from the HW decoder (e.g. `"cuda"`, `"vaapi"`) |
| `subtitle_charenc` | string | no | Character encoding for text subtitle streams (e.g. `"UTF-8"`, `"latin1"`) |
| `duration_us` | int | no | Limit how many microseconds to read from this input |
| `loop` | bool | no | Loop the input indefinitely (or until the graph run ends) |
| `ts_offset_us` | int | no | Shift all timestamps in this input by this many microseconds |

#### StreamSelect

| Field | Type | Required | Description |
|---|---|---|---|
| `input_index` | int | yes | Zero-based index into the container's stream list |
| `type` | string | yes | `"video"`, `"audio"`, `"subtitle"`, `"data"` |
| `track` | int | yes | Track number assigned within this type (0-based) — referenced in edge `from` as `:a:0`, `:a:1`, etc. |

---

### 4.3 `graph.nodes`

Each node is either a **filter** or an **encoder** node.

```json
{
  "id": "scale",
  "type": "filter",
  "filter": "scale",
  "params": { "w": "1280", "h": "720" }
}
```

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string | yes | Unique node identifier |
| `type` | string | yes | `"filter"` or `"encoder"` |
| `filter` | string | if filter | libavfilter filter name (e.g. `"scale"`, `"overlay"`, `"yadif"`) |
| `params` | object | no | AVOption parameters as key-value strings |
| `device` | string | no | Hardware device context name to associate with this node |
| `auto_map_hw` | bool | no | Promote sw filter name to hw equivalent and insert upload/download nodes at device boundaries |
| `error_policy` | object | no | Per-node error handling (see below) |

**Encoder node** — explicitly selects a codec and parameters for one stream:

```json
{
  "id": "enc_video",
  "type": "encoder",
  "params": {
    "codec": "libx264",
    "preset": "fast",
    "crf": "23"
  }
}
```

#### ErrorPolicy

| Field | Type | Description |
|---|---|---|
| `policy` | string | `"abort"` (default), `"skip"`, `"retry"`, `"fallback"` |
| `max_retries` | int | Maximum retry attempts (default: 3) |
| `fallback_node` | string | Node ID to route frames to on failure |

---

### 4.4 `graph.edges`

Edges are directed connections between stream producers and consumers.

```json
{ "from": "src:v:0",  "to": "scale",    "type": "video" },
{ "from": "scale",    "to": "out:v",    "type": "video" },
{ "from": "src:a:0",  "to": "out:a",    "type": "audio" }
```

| Field | Type | Required | Description |
|---|---|---|---|
| `from` | string | yes | Source endpoint |
| `to` | string | yes | Destination endpoint |
| `type` | string | yes | `"video"`, `"audio"`, `"subtitle"`, `"data"` |

#### Endpoint notation

| Format | Meaning |
|---|---|
| `"inputID:v:0"` | Video track 0 from input `inputID` |
| `"inputID:a:1"` | Audio track 1 from input `inputID` |
| `"inputID:s:0"` | Subtitle track 0 from input `inputID` |
| `"nodeID"` | Default (first) port on a filter/encoder node |
| `"nodeID:default"` | Explicit default port |
| `"nodeID:overlay"` | Named port on a filter (e.g. the `overlay` filter's second video input) |
| `"nodeID:out0"` | Named output port on a split filter |
| `"outputID:v"` | Video input sink of output `outputID` |
| `"outputID:a"` | Audio input sink of output `outputID` |
| `"outputID:s"` | Subtitle input sink |

---

### 4.5 `outputs`

```json
{
  "id": "out",
  "url": "output.mp4",
  "codec_video": "libx264",
  "codec_audio": "aac"
}
```

| Field | Type | Description |
|---|---|---|
| `id` | string | Unique output identifier, referenced in edge `to` endpoints |
| `url` | string | Output file path or streaming URL |
| `format` | string | Force muxer (e.g. `"matroska"`, `"mpegts"`, `"hls"`) |
| `codec_video` | string | Video encoder (`"libx264"`, `"libx265"`, `"h264_nvenc"`, `"copy"`, …) |
| `codec_audio` | string | Audio encoder (`"aac"`, `"libopus"`, `"copy"`, …) |
| `codec_subtitle` | string | Subtitle codec (`"srt"`, `"ass"`, `"copy"`) |
| `codec_tag_video` | string | Override codec tag (e.g. `"hvc1"` for HEVC in MP4) |
| `codec_tag_audio` | string | Override audio codec tag |
| `bsf_video` | string | Video bitstream filter chain (e.g. `"h264_mp4toannexb"`) |
| `bsf_audio` | string | Audio bitstream filter chain |
| `bsf_subtitle` | string | Subtitle bitstream filter |
| `options` | object | Muxer AVOptions |
| `metadata` | object | Container-level metadata tags (`{"title": "...", "comment": "..."}`) |
| `chapters` | array | Chapter table (each: `{start, end, title, metadata}`, times in seconds) |
| `streams` | array | Per-stream overrides (disposition, per-stream metadata, encoder) |
| `cover_art` | string | Path to an image to embed as cover art |
| `hls` | object | HLS options (see below) |
| `dash` | object | DASH options (see below) |
| `kind` | string | Output mode: `""` (file), `"tee"` (fan-out) |
| `targets` | array | Tee output targets (when `kind` is `"tee"`) |

#### HLS options (`output.hls`)

| Field | Type | Description |
|---|---|---|
| `time` | float | Segment duration in seconds (default: 2) |
| `init_time` | float | Duration of the first segment |
| `list_size` | int | Number of segments to keep in the playlist (0 = keep all) |
| `start_number` | int | Starting sequence number |
| `playlist_type` | string | `"vod"` or `"event"` |
| `segment_type` | string | `"mpegts"` or `"fmp4"` |
| `segment_filename` | string | Pattern for segment file names |
| `fmp4_init_filename` | string | Init segment filename (fMP4 only) |
| `master_pl_name` | string | Master playlist filename |
| `var_stream_map` | string | Variant stream grouping for ABR |
| `flags` | array | Checkbox flags (`"hls_time_delta_in_m3u8"`, `"program_date_time"`, …) |

#### DASH options (`output.dash`)

| Field | Type | Description |
|---|---|---|
| `seg_duration` | float | Segment duration in seconds |
| `frag_duration` | float | Fragment duration |
| `window_size` | int | Manifest window size in segments |
| `extra_window_size` | int | Extra segments to keep after the manifest window |
| `use_template` | bool | Use `$Number$`/`$Time$` template URIs |
| `use_timeline` | bool | Use `SegmentTimeline` in manifest |
| `adaptation_sets` | string | Explicit adaptation set mappings |
| `streaming` | bool | Progressive fragment writes |
| `ldash` | bool | Low-latency DASH |
| `hls_playlist` | bool | Also write an HLS `.m3u8` (CMAF dual-pack) |
| `single_file` | bool | Single-file output (`SegmentBase`) |
| `flags` | array | Checkbox flags |

---

### 4.6 `global_options`

| Field | Type | Description |
|---|---|---|
| `threads` | int | Maximum worker threads (0 = auto) |
| `hw_accel` | string | Hardware acceleration backend: `"cuda"`, `"vaapi"`, `"qsv"`, `"videotoolbox"` |
| `hw_device` | string | Device selector: GPU index (`"0"`), `/dev/dri/renderD128` for VAAPI, etc. |
| `realtime` | bool | Enable adaptive real-time mode: the control loop dynamically increases encoder thread counts and (as a last resort) drops frames to keep every node at or above its `fps_target`. Set via `--realtime` CLI flag or the **Real-time** checkbox in the GUI toolbar — see [§5.12](#512-real-time-mode) |

---

## 5. Common CLI workflows

### 5.1 Simple transcode

```json
{
  "schema_version": "1.0",
  "inputs": [
    {
      "id": "src",
      "url": "input.mp4",
      "streams": [
        { "input_index": 0, "type": "video", "track": 0 },
        { "input_index": 0, "type": "audio", "track": 0 }
      ]
    }
  ],
  "graph": {
    "nodes": [],
    "edges": [
      { "from": "src:v:0", "to": "out:v", "type": "video" },
      { "from": "src:a:0", "to": "out:a", "type": "audio" }
    ]
  },
  "outputs": [
    {
      "id": "out",
      "url": "output.mp4",
      "codec_video": "libx264",
      "codec_audio": "aac"
    }
  ]
}
```

Equivalent FFmpeg command:
```sh
ffmpeg -i input.mp4 -c:v libx264 -c:a aac output.mp4
```

---

### 5.2 Stream copy (remux)

Set `codec_video` and `codec_audio` to `"copy"` to pass streams through without re-encoding:

```json
"codec_video": "copy",
"codec_audio": "copy"
```

To remux MP4 → MKV with no quality loss:

```json
{
  "id": "out",
  "url": "output.mkv",
  "codec_video": "copy",
  "codec_audio": "copy"
}
```

---

### 5.3 Filter chains

Add filter nodes and wire edges through them. Filters are connected in series by chaining edges:

```json
"nodes": [
  { "id": "scale", "type": "filter", "filter": "scale", "params": { "w": "1280", "h": "720" } },
  { "id": "fps",   "type": "filter", "filter": "fps",   "params": { "fps": "30" } }
],
"edges": [
  { "from": "src:v:0", "to": "scale",   "type": "video" },
  { "from": "scale",   "to": "fps",     "type": "video" },
  { "from": "fps",     "to": "out:v",   "type": "video" }
]
```

**Common video filters:**

| Filter | Purpose | Key params |
|---|---|---|
| `scale` | Resize | `w`, `h` — use `"-1"` to preserve aspect ratio |
| `fps` | Set frame rate | `fps` |
| `crop` | Crop region | `w`, `h`, `x`, `y` |
| `pad` | Add padding | `w`, `h`, `x`, `y`, `color` |
| `yadif` | Deinterlace | `mode` (`0`=frame, `1`=field) |
| `overlay` | Composite two videos | `x`, `y` |
| `split` | Duplicate a stream | `outputs` (number of outputs) |
| `drawtext` | Render text | `text`, `fontsize`, `fontcolor`, `x`, `y` |
| `subtitles` | Burn in SRT subtitles | `filename` |
| `ass` | Burn in ASS subtitles | `filename` |
| `zscale` + `tonemap` | HDR → SDR tone-mapping | `t=linear`, `npl=100` / `tonemap=hable` |

**Common audio filters:**

| Filter | Purpose | Key params |
|---|---|---|
| `volume` | Adjust volume | `volume` (e.g. `"2.0"`) |
| `loudnorm` | EBU R128 loudness normalization | `I`, `LRA`, `TP` |
| `aformat` | Force sample format / layout | `sample_fmts`, `channel_layouts`, `sample_rates` |
| `aresample` | Resample | `sample_rate` |
| `amerge` | Merge multiple audio streams | `inputs` |
| `pan` | Channel remapping | `layout`, `c0`, `c1`, … |

---

### 5.4 Multi-input and multi-output

**Overlay two videos:**

```json
{
  "inputs": [
    { "id": "bg", "url": "background.mp4", "streams": [{"input_index": 0, "type": "video", "track": 0}] },
    { "id": "fg", "url": "logo.png",        "streams": [{"input_index": 0, "type": "video", "track": 0}] }
  ],
  "graph": {
    "nodes": [
      { "id": "ov", "type": "filter", "filter": "overlay", "params": {"x": "10", "y": "10"} }
    ],
    "edges": [
      { "from": "bg:v:0",      "to": "ov:default", "type": "video" },
      { "from": "fg:v:0",      "to": "ov:overlay",  "type": "video" },
      { "from": "ov:default",  "to": "out:v",       "type": "video" }
    ]
  },
  "outputs": [{ "id": "out", "url": "composited.mp4", "codec_video": "libx264" }]
}
```

**Split and encode at two resolutions:**

```json
"nodes": [
  { "id": "sp", "type": "filter", "filter": "split" },
  { "id": "hd", "type": "filter", "filter": "scale", "params": {"w": "1920", "h": "1080"} },
  { "id": "sd", "type": "filter", "filter": "scale", "params": {"w": "640",  "h": "480"} }
],
"edges": [
  { "from": "src:v:0",     "to": "sp:default",  "type": "video" },
  { "from": "sp:out0",     "to": "hd",          "type": "video" },
  { "from": "sp:out1",     "to": "sd",          "type": "video" },
  { "from": "hd",          "to": "out_hd:v",    "type": "video" },
  { "from": "sd",          "to": "out_sd:v",    "type": "video" }
]
```

---

### 5.5 Hardware-accelerated encoding

Set `global_options.hw_accel` to enable a hardware backend. When set, MediaMolder opens a device context, uses hardware decoders where available, and routes frames through hardware filters automatically.

```json
{
  "global_options": {
    "hw_accel": "cuda",
    "hw_device": "0"
  },
  "outputs": [
    { "id": "out", "url": "output.mp4", "codec_video": "h264_nvenc", "codec_audio": "aac" }
  ]
}
```

**Supported backends and encoders:**

| Backend | `hw_accel` value | Example encoder | Device selector |
|---|---|---|---|
| NVIDIA CUDA/NVENC | `"cuda"` | `h264_nvenc`, `hevc_nvenc`, `av1_nvenc` | GPU index: `"0"` |
| Intel/AMD (Linux) | `"vaapi"` | `h264_vaapi`, `hevc_vaapi`, `vp9_vaapi` | `/dev/dri/renderD128` |
| Intel Quick Sync | `"qsv"` | `h264_qsv`, `hevc_qsv`, `av1_qsv` | `/dev/dri/renderD128` |
| Apple VideoToolbox | `"videotoolbox"` | `h264_videotoolbox`, `hevc_videotoolbox` | `""` (auto) |

When both decoder and encoder use the same hardware device, frames stay in GPU memory with no host↔device copies.

**Hardware filter mapping** — when `hw_accel` is set and `auto_map_hw: true` on a filter node, software filter names are automatically promoted:

| SW filter | CUDA → | VAAPI → | QSV → |
|---|---|---|---|
| `scale` | `scale_cuda` | `scale_vaapi` | `scale_qsv` |
| `yadif` | `yadif_cuda` | — | — |
| `transpose` | `transpose_cuda` | `transpose_vaapi` | — |
| `overlay` | `overlay_cuda` | `overlay_vaapi` | `overlay_qsv` |

List available hardware device types on the current machine:

```sh
mediamolder list-hw-devices
```

---

### 5.6 HLS and DASH streaming outputs

Set `format` to `"hls"` or `"dash"` and include the corresponding options object:

**HLS VOD:**
```json
{
  "id": "out",
  "url": "stream/index.m3u8",
  "format": "hls",
  "codec_video": "libx264",
  "codec_audio": "aac",
  "hls": {
    "time": 4,
    "list_size": 0,
    "playlist_type": "vod",
    "segment_filename": "stream/seg%03d.ts"
  }
}
```

**DASH live:**
```json
{
  "id": "out",
  "url": "dash/manifest.mpd",
  "format": "dash",
  "codec_video": "libx264",
  "codec_audio": "aac",
  "dash": {
    "seg_duration": 4,
    "streaming": true,
    "use_template": true,
    "use_timeline": true
  }
}
```

---

### 5.7 Tee (fan-out) outputs

Record to disk while simultaneously streaming:

```json
{
  "id": "out",
  "url": "",
  "kind": "tee",
  "targets": [
    { "url": "archive.mp4",                          "format": "mp4" },
    { "url": "rtmp://live.example.com/live/stream",  "format": "flv" }
  ]
}
```

Optional per-target fields:

| Field | Description |
|---|---|
| `select` | Stream selector (e.g. `"v:0,a:0"`) |
| `bsfs` | Bitstream filter chain for this target |
| `onfail` | `"abort"` or `"ignore"` |
| `use_fifo` | Buffer target writes through a FIFO (useful for network targets) |
| `fifo_options` | Semicolon-separated `key=value` options for the FIFO muxer (e.g. `"queue_size=1024;recover_any_error=1"`) |
| `options` | Extra muxer AVOptions for this target |

---

### 5.8 Subtitles

**Select and pass through subtitle stream:**

```json
{
  "id": "src",
  "url": "movie.mkv",
  "streams": [
    { "input_index": 0, "type": "video",    "track": 0 },
    { "input_index": 0, "type": "audio",    "track": 0 },
    { "input_index": 0, "type": "subtitle", "track": 0 }
  ]
}
```

Wire with:
```json
{ "from": "src:s:0", "to": "out:s", "type": "subtitle" }
```

**Burn in SRT subtitles (hardsub):**

```json
{ "id": "subs", "type": "filter", "filter": "subtitles", "params": { "filename": "subs.srt" } }
```

**Burn in ASS/SSA with styling:**

```json
{ "id": "subs", "type": "filter", "filter": "ass", "params": { "filename": "subs.ass" } }
```

For non-UTF-8 subtitle files, set `subtitle_charenc` on the input:

```json
{ "subtitle_charenc": "latin1" }
```

---

### 5.9 Bitstream filters

Apply a bitstream filter to a stream inside the muxer:

```json
{
  "id": "out",
  "url": "output.ts",
  "codec_video": "copy",
  "bsf_video": "h264_mp4toannexb"
}
```

Chain multiple BSFs with a comma:

```json
"bsf_video": "h264_mp4toannexb,dump_extra"
```

HEVC in MP4 requires the `hvc1` codec tag:

```json
{
  "codec_video": "libx265",
  "codec_tag_video": "hvc1"
}
```

---

### 5.10 Live / device inputs

Use a format matching the capture API and set `url` to the device specifier:

**macOS (avfoundation):**
```json
{
  "id": "cam",
  "url": "0",
  "format": "avfoundation",
  "streams": [{ "input_index": 0, "type": "video", "track": 0 }]
}
```

**Linux (V4L2):**
```json
{
  "id": "cam",
  "url": "/dev/video0",
  "format": "v4l2",
  "options": { "framerate": "30", "video_size": "1280x720" }
}
```

**Windows (DirectShow):**
```json
{
  "id": "cam",
  "url": "Integrated Camera",
  "format": "dshow",
  "streams": [{ "input_index": 0, "type": "video", "track": 0 }]
}
```

For live streaming to RTMP/SRT where the output must pace to wall clock, combine device input with `global_options.realtime: true` — see [§5.12](#512-real-time-mode) below.

---

### 5.11 Scene detection in a pipeline

Scene detection can be embedded directly in a transcoding graph using `go_processor`
nodes. Five detectors are available; all are ported from
[PySceneDetect](https://github.com/Breakthrough/PySceneDetect) by Brandon Castellano
(BSD-3-Clause). For offline use on a single file see [§3.10](#310-py-scene-detect).

| Processor | Threshold default | Best for |
|---|---|---|
| `scene_change_content` | 27.0 | General-purpose; highest accuracy |
| `scene_change_adaptive` | 3.0 | Action/sports; suppresses false positives on fast pans |
| `scene_change_threshold` | 12.0 | Fade to/from black or white |
| `scene_change_hash` | 0.395 | Robust to colour-grading and compression artefacts |
| `scene_change_histogram` | 0.05 | Fast coarse filter; low memory |

#### Wiring rules for scene detector nodes

A scene detector is a **passthrough** node: it receives decoded video frames,
inspects them, and forwards them unchanged to the next node. This means it must
be wired **in-line** on a video path — it cannot sit at the end of the graph
with nothing downstream.

```
[video source] ──video──▶ [scene detector] ──video──▶ [consumer]
```

**Valid video sources** (left side of the detector):

| Source | Example edge `from` |
|---|---|
| Input node, single-stream file | `"in0:v:0"` |
| `split` / `vsplit` filter output | `"vsplit:0"`, `"vsplit:1"`, … |
| Any filter that produces video | `"scale_720"`, `"deinterlace"`, … |

**Valid video consumers** (right side of the detector):

| Consumer | Notes |
|---|---|
| Encoder | Most common; the detected frames are encoded as-is |
| Video filter | Apply further processing after detection |
| Another `go_processor` | Chain multiple detectors or processors |
| Output (copy) | Only when the output uses stream-copy (no re-encode) |

**Multi-rendition / ABR ladders**

When the video is split to multiple resolutions, wire the detector on exactly
one of the splitter outputs (typically the highest resolution, so the detector
sees the most detail). The other outputs go straight to their scale filters.

```
                   ┌─[0]──▶ scene_detector ──▶ enc_1080
split (outputs=4) ─┤─[1]──▶ scale_720 ──▶ enc_720
                   │─[2]──▶ scale_540 ──▶ enc_540
                   └─[3]──▶ scale_360 ──▶ enc_360
```

JSON edges for this pattern:

```json
{ "from": "vsplit",   "to": "scene_detector", "type": "video" },
{ "from": "scene_detector", "to": "enc_1080", "type": "video" },
{ "from": "vsplit:1", "to": "scale_720",      "type": "video" },
{ "from": "vsplit:2", "to": "scale_540",      "type": "video" },
{ "from": "vsplit:3", "to": "scale_360",      "type": "video" }
```

`"from": "vsplit"` (no index suffix) selects output 0, the same as `"vsplit:0"`.

**Common mistakes**

| Mistake | Symptom | Fix |
|---|---|---|
| Wiring to `in0:v:1` when the file has only one video stream | `STREAM_INDEX_OUT_OF_RANGE` | Use `in0:v:0`; remove the `track: 1` stream entry from the input |
| Connecting the detector's input but not its output | `dead_node` warning; no scene data | Wire `scene_detector → encoder` (or next node) |
| Setting `split outputs=4` but wiring 5 outputs | `Invalid argument` from libavfilter | Match the `outputs` param to the number of outgoing edges |
| Wiring an `events` edge to carry video | Nothing encoded | `events` edges carry metadata only; use a `video` edge for the frame path |

#### Edge type reference

MediaMolder has several edge types that look similar but carry very different
kinds of data:

| Edge type | What it carries | libav\* library involvement |
|---|---|---|
| `video`, `audio`, `subtitle` | Decoded media frames | Demuxed by libavformat, filtered by libavfilter, encoded/muxed by libavcodec/libavformat |
| `data` | `AVMEDIA_TYPE_DATA` streams — raw timed-data tracks such as SCTE-35 splice markers, closed-caption side data, or timecode tracks | A real media stream; demuxed and muxed by libavformat alongside audio/video |
| `metadata` | Container-level metadata and chapter tables copied between inputs and outputs via `metadata_reader` / `metadata_writer` nodes | Resolved at pipeline build time via libavformat metadata APIs; no frames flow at runtime |
| `events` | Structured event objects emitted by a `go_processor` via its `Process()` return value (scene boundaries, object detections, frame diagnostics, …) | **None** — handled entirely by the Go runtime event bus; never touches any libav\* library |

The `events` edge type is specifically for routing Go-processor event
output to a `metadata_file_writer` sink. An `events` edge does not
carry video frames; it is a pure routing annotation that tells the
engine "write events from node A to the `.jsonl` file of node B".

#### Writing scene events to a file via graph wiring

Connect a scene detector's **events** handle to a `metadata_file_writer`
node in the GUI by dragging from the pink handle on the right of the
detector node to the pink handle on the left of the writer node. The
writer node only needs an `output_file` param; no `inner_processor` is
required.

JSON equivalent:

```json
{
  "schema_version": "1.1",
  "inputs": [
    { "id": "src", "path": "input.mp4" }
  ],
  "graph": {
    "nodes": [
      {
        "id": "detect",
        "type": "go_processor",
        "processor": "scene_change_content",
        "params": { "threshold": 27.0, "min_scene_len": "0.6s" }
      },
      {
        "id": "enc",
        "type": "encoder",
        "codec": "libx264",
        "params": { "preset": "fast", "crf": 23 }
      },
      {
        "id": "log",
        "type": "go_processor",
        "processor": "metadata_file_writer",
        "params": { "output_file": "cuts.jsonl" }
      }
    ],
    "edges": [
      { "from": "src:v:0",      "to": "detect",   "type": "video" },
      { "from": "detect",       "to": "enc",       "type": "video" },
      { "from": "enc",          "to": "out:v",     "type": "video" },
      { "from": "detect",       "to": "log",       "type": "events" }
    ]
  },
  "outputs": [
    { "id": "out", "url": "output.mp4", "codec_video": "libx264" }
  ]
}
```

The `events` edge wires the detector's metadata stream to the file
writer without interrupting the video processing path. Multiple event
sinks can be wired from the same detector — each receives a copy.

**Legacy wrapper mode** (still supported for JSON-authored pipelines):

```json
{
  "id": "detect_and_log",
  "type": "go_processor",
  "processor": "metadata_file_writer",
  "params": {
    "output_file": "cuts.jsonl",
    "inner_processor": "scene_change_content",
    "threshold": 27.0
  }
}
```

In wrapper mode the `metadata_file_writer` wraps the inner processor
directly; no `events` edge is needed. This mode is not exposed in the
GUI Inspector — use graph wiring instead.

**CLI output** (alternative to graph wiring):

```sh
mediamolder run --metadata-out cuts.jsonl pipeline.json
```

Each detected scene boundary is emitted as a metadata event:

```json
{"node":"detect","pts":3703200,"custom":{"scene_change":true,"detector":"content","frame_index":1234,"timecode":"00:00:41.133","score":42.7}}
```

**Using the adaptive detector for action content:**

```json
{
  "id": "detect",
  "type": "go_processor",
  "processor": "scene_change_adaptive",
  "params": {
    "threshold": 3.0,
    "min_scene_len": "0.6s",
    "window_width": 2,
    "min_content_val": 15.0
  }
}
```

**Stats export** — pass `stats_path` to write per-frame detector scores to a CSV
file (same format as `py-scene-detect --stats`):

```json
{
  "id": "detect",
  "type": "go_processor",
  "processor": "scene_change_content",
  "params": {
    "threshold": 27.0,
    "stats_path": "frame_stats.csv"
  }
}
```

For full parameter references and algorithm descriptions see
[docs/scene-detection.md](scene-detection.md).

---

### 5.12 Real-time mode

Real-time mode activates an adaptive control loop that runs every 500 ms while the pipeline is playing. It observes per-node performance and attempts to keep every node at or above its configured `fps_target`.

**Enabling real-time mode:**

```sh
# CLI flag (overrides the JSON without editing it)
mediamolder run --realtime pipeline.json

# JSON config
{
  "schema_version": "1.2",
  "global_options": { "realtime": true },
  "nodes": [ … ]
}
```

In the GUI, check the **Real-time** checkbox in the toolbar before pressing **Run**. Toggling the checkbox updates `global_options.realtime` in the in-memory graph; saving the file persists the flag to the `.json` file.

**What the control loop does:**

| Condition | Action |
|---|---|
| Node behind `fps_target` + `ActiveFrac > 90%` + threads fully occupied | Increase codec thread count by 2 (graceful restart) |
| Thread budget exhausted + `FPSDeficit > 1 fps` | Enable frame-drop mode on the upstream source (1 in 4 frames skipped); emit `RealTimeViolation` event |
| Node behind target + `ActiveFrac > 90%` + threads underutilised | Sequential bottleneck — emit advisory violation; recommend a faster codec preset |
| Node behind target + `StalledFrac > 50%` | Downstream bottleneck — do not act on this node; the control loop addresses the actual bottleneck downstream |

**Thread budget:** the total CPU threads available across all encoder nodes defaults to `runtime.NumCPU()` minus 2 reserved for the Go runtime. Hardware-accelerated nodes (NVENC, VideoToolbox, etc.) are exempt and do not consume the CPU budget.

**`fps_target` per node:** set in the Inspector for source and encoder nodes (`fps_target` field). Nodes without a target are excluded from real-time control.

**Observability:** real-time violations are emitted as `RealTimeViolation` events on the event bus and as Prometheus metrics (`mediamolder_node_fps_deficit`, `mediamolder_pipeline_realtime_satisfied`). Use `mediamolder perf` to watch the control loop in action.

---

## 6. Validation

Validation is built into both the CLI and the GUI. It runs in two phases:

**Phase A (static)** runs without opening any files and catches:
- Missing required fields
- Codec ↔ container incompatibilities (e.g. VP9 in an MP4 container)
- Filter arity errors (wrong number of input pads)
- Security constraint violations

**Phase B (probe-assisted)** opens each input and checks for:
- Interlaced sources routed to a progressive encoder without a deinterlace filter
- HDR content (PQ/HLG transfer characteristics) being encoded without tone-mapping
- VFR input delivered to a fixed-rate encoder without an `fps` filter
- Pixel format mismatches (e.g. 10-bit input to an 8-bit-only encoder)
- Audio sample rate or channel layout mismatches

Many issues include a **Fix suggestion**. The CLI prints the exact filter name and parameters to add. The GUI can apply fixes with a single click.

**CI integration example:**

```sh
# Static-only — fast, no file I/O required
mediamolder validate --no-probe job.json
echo "Exit: $?"

# Full probe-assisted, fail on warnings too
mediamolder validate --strict job.json

# Machine-readable report for parsing in scripts
mediamolder validate --json job.json | jq '.issues[] | select(.severity == "ERROR")'
```

---

## 7. Graphical user interface

The GUI is a browser-based visual editor. Start it with `mediamolder gui` (see [§3.8](#38-gui)).

### 7.1 Launching the GUI

```sh
mediamolder gui
# → opens http://localhost:7042 in the default browser
```

To use a different port (e.g. if 7042 is taken):

```sh
mediamolder gui --port 9000
```

If the browser does not open automatically, navigate manually to the printed URL. A browser that supports ES Modules and CSS Variables is required (any current Chrome, Firefox, Safari, or Edge).

---

### 7.2 Canvas

The central canvas is a panning/zooming graph editor. The current graph name (or filename) is shown in the toolbar.

**Navigation:**
- **Pan** — click and drag on empty canvas space, or use the scroll wheel to zoom and hold the middle mouse button to pan
- **Zoom** — scroll wheel, or the zoom controls on the minimap (bottom-right when enabled)
- **Fit to screen** — double-click on empty canvas

**Selecting:**
- Click a node to select it and open its properties in the Inspector
- Click an edge (connection) to select it; a popover appears showing stream attributes
- Drag on empty canvas to box-select multiple nodes

**Moving:**
- Drag selected nodes; multi-select with click-and-drag or Shift-click, then drag

**Stream attribute popover:**
Hover over any edge to see the full set of technical attributes inferred for that stream — resolution, pixel format, frame rate, color space, sample rate, channel layout, codec, profile, bit rate, and more. The values are traced upstream through the graph: the closest node that constrains an attribute wins. Click an edge to pin the popover open.

**Canvas stats** (bottom-centre): shows `N nodes · M edges` for the current graph.

---

### 7.3 Palette (left sidebar)

The palette lists all nodes available to drag onto the canvas. Toggle it with the **Palette** button in the View section of the toolbar.

**Categories:**
- **Sources** — Input (file/URL/device)
- **Sinks** — Output (file/URL)
- **Filters** — All libavfilter filters (Video, Audio, Subtitles, Image2, Null, etc.), grouped into subcategories
- **Encoders** — All libavcodec encoders, grouped by codec family
- **Processors** — Registered Go processor nodes

**Using the palette:**
1. Type in the **search box** at the top to filter by name or description
2. Expand / collapse category headers by clicking them
3. The palette defaults to **Common** entries (marked as frequent-use); switch to **All** in the subcategory headers to see every node
4. **Drag** any entry from the palette onto the canvas to create a node
5. Hardware encoder entries are highlighted when the corresponding GPU backend is detected; click the hardware chip icon to open the **Hardware dialog**

---

### 7.4 Inspector (right sidebar)

Selecting a node opens its configuration in the Inspector. Toggle the panel with the **Inspector** button in the View section of the toolbar.

**Input node Inspector:**
- **URL** — file path or media URL; use **Browse…** to open the file browser
- **Stream selection** — which streams (video, audio, subtitle) to import
- **Probe** — click **Get properties** to run `avformat_find_stream_info` and populate stream metadata for the edge attribute popover
- **Hardware decoding** — choose a HW acceleration backend and optional device override
- **Timing** — `duration_us`, `ts_offset_us`, `loop`
- **Capture options** — device-specific AVOptions (frame rate, video size, pixel format, sample rate) shown when the format is a capture device
- **Subtitle charset** — set `subtitle_charenc` for non-UTF-8 text subtitles
- **Network options** — RTSP transport, reconnect settings shown for network URLs

**Filter node Inspector:**
- **Filter type** — select from the full filter list; loaded from the live libavfilter registry
- Typed controls for common parameters; an **Advanced** section with all remaining AVOptions grouped and filterable by name

**Encoder node Inspector:**
- **Codec** — select an encoder; the form updates to show codec-specific controls:
  - Preset dropdown
  - Rate control mode (CRF/CQ/Q/bitrate/ABR)
  - Quality value (CRF range, bitrate, etc.) with codec-native min/max
  - Keyframe interval (GOP)
  - **Raw options** — `x264-params`, `x265-params`, `svtav1-params`, etc. for verbatim parameter blobs
  - **Advanced** — full option schema grouped by Threading, Quality, Color, Motion, Profile/Level, GOP & frames, Other; searchable
- All values come from the live libavcodec option schema; leaving a field empty uses the library default

**Output node Inspector:**
- **URL** — output file path; use **Browse…** to pick a save location
- **Format** — force muxer (optional; auto-detected from extension)
- **Codecs** — `codec_video`, `codec_audio`, `codec_subtitle`
- **Codec tags** — `codec_tag_video`, `codec_tag_audio`
- **Bitstream filters** — video, audio, subtitle
- **Metadata** — key-value pairs embedded in the container
- **Chapters** — chapter table with start/end times (seconds) and title
- **Per-stream overrides** — disposition flags (forced, hearing-impaired), per-stream metadata, encoder override
- **HLS / DASH / Tee sections** — typed forms for streaming output options (segment duration, playlist type, variant stream maps, etc.)
- **Timing options** — start/end time, output duration limit
- **Muxer options** — raw AVOptions for the muxer

---

### 7.5 Toolbar reference

| Button / Control | Action |
|---|---|
| **New** | Clear the canvas (prompts if there are unsaved changes) |
| **Open…** | Open a job JSON file from disk (File System Access API on supported browsers, otherwise the server file browser) |
| **FFmpeg →** | Open the Import FFmpeg dialog — paste an FFmpeg command and convert it to a graph |
| **Graph: \<dropdown\>** | Switch between built-in example graphs; discards unsaved changes after confirmation |
| **Save** | Write the current graph back to the same file (disabled for examples and when there are no unsaved changes) |
| **Save As…** | Write the current graph to a new file |
| **→ FFmpeg** | Open the Export FFmpeg dialog — shows the current graph rendered as an equivalent FFmpeg command line |
| *(spacer)* | |
| **Auto layout** | Rearrange nodes left-to-right using a dagre layout algorithm |
| **View: Palette** | Toggle the palette sidebar |
| **View: Inspector** | Toggle the inspector sidebar |
| **View: Minimap** | Toggle the minimap (bottom-right corner of canvas) |
| **Labels: Verbose / Compact** | Switch node label density; Verbose shows all fields, Compact shows a concise summary |
| **Real-time** *(checkbox)* | Enable adaptive real-time mode for the next run — see [§5.12](#512-real-time-mode). Toggling updates `global_options.realtime` in the graph (persisted on Save) |
| **Run** | Send the current graph to the backend and start encoding |
| **Stop** | Cancel the running job cleanly |
| **Show log / Hide log** | Toggle the Run panel at the bottom of the screen (enabled only while a job is running or has finished) |
| **Validate** | Probe inputs and run full Phase A + Phase B validation; opens the Validate panel with results. The button turns red and shows a badge count when errors or warnings are present |
| **Help** | Open the Help dialog (or press **?**) |
| **Assets** | Open the Asset Manager; badge shows the number of registered assets |

---

### 7.6 Run panel

The Run panel appears at the bottom of the screen when **Show log** is toggled on during or after a run.

**Contents:**
- **Per-node metrics** — live packet counts, FPS, and frame lag for each active node
- **Errors** — nodes that encountered errors get a red border in the canvas; the Run panel lists the error message and location
- **Log entries** — warning and info messages from the backend
- **Progress** — elapsed time and estimated completion based on input duration

The **Stop** button in the toolbar cancels the running job; MediaMolder flushes in-progress frames before closing all outputs.

**Per-node performance overlay (while running):**

Each active node displays a three-segment activity bar and live metrics that update at ~2 Hz:

| Indicator | Meaning |
|---|---|
| Green segment | Active fraction — codec or I/O work underway |
| Yellow segment | Idle fraction — waiting for the next frame to arrive |
| Red segment | Stalled fraction — output channel full, blocked on a slow downstream node |
| **FPS badge** | Actual frames/sec over a sliding window |
| **Deficit badge** (amber/red) | `fps_target − fps_actual`; appears when the node is behind its target |
| **Thread badge** | Configured thread count and live busy count (where available) |

The performance data is streamed from the `/perf/stream` SSE endpoint. Use the `mediamolder perf` CLI for a terminal table showing the same data.

**Other node status indicators:**
- A red border indicates an error on that node
- Validation issue badges (e.g. `2E 1W`) appear on nodes with validation errors/warnings — click to open the Validate panel

---

### 7.7 Validate panel

Click **Validate** in the toolbar to open the Validate panel. The panel shows all issues found for the current graph, sorted by severity (ERRORs first, then WARNINGs, then INFOs).

Each issue displays:
- **Severity badge** — `ERROR` (red), `WARNING` (amber), `INFO` (blue)
- **Code** — machine-readable issue identifier (e.g. `VIDEO_INTERLACED_NO_DEINTERLACE`)
- **Location** — the node or edge where the problem was detected
- **Message** — plain-English description of the problem
- **Suggestion** — how to fix it
- **Apply Fix button** (when available) — one-click repair:
  - `InsertFilterFix` — creates a new filter node (e.g. `yadif`, `zscale`, `fps`, `aformat`) and re-wires the relevant edges automatically
  - `SetOutputFieldFix` — updates a field on the output node (e.g. sets `codec_tag_video` to `"hvc1"`)

Inline node badges (e.g. `2E 1W`) appear on each canvas node that has validation issues. Hover a badge to see the issue list.

Auto-validation runs in the background on every graph change (Phase A static checks, debounced 300 ms) so the error count on the Validate button stays current as you edit.

---

### 7.8 File browser

When the browser does not support the File System Access API (or when saving to server-side paths), the GUI opens a built-in file browser backed by the Go server's local filesystem.

- **Navigate** using the directory listing; click folders to enter them
- **New folder** button creates a directory
- **Select** a file by clicking its name; the path is written back into the Inspector URL field

---

### 7.9 Asset manager

The Asset Manager registers named assets — fonts, ML models, LUTs, and other supplementary files — that filter nodes can reference by key instead of by absolute path. Click **Assets** in the toolbar to open it.

Assets are stored in the JSON under `job.assets` as a `Record<name, AssetRef>`. Each asset has:
- **Name** — the key referenced in filter params
- **Path** — absolute file path
- **Kind** — `font`, `model`, `lut`, or `other`
- **Description** — optional human-readable note

The Assets badge in the toolbar shows how many assets are registered.

---

### 7.10 Hardware dialog

Click the chip icon in the Palette (next to any hardware encoder entry) or go through the **Hardware** section to open the Hardware dialog. It shows the detected capabilities of each GPU backend:

- Available NVENC / NVDEC codecs with max resolution, B-frame support, lookahead, 10-bit, YUV 4:4:4, lossless
- VAAPI / QSV encoder and decoder profiles
- AMF codec capabilities
- Static capability summary (maximum sessions, max bitrate per codec)
- CUDA architecture string and compute SM version (NVIDIA)

Use this to confirm that your GPU supports the encoder settings you've chosen before running.

---

### 7.11 Import / Export FFmpeg commands

**Import FFmpeg → graph (FFmpeg → button):**
1. Click **FFmpeg →** in the toolbar
2. Paste any FFmpeg command line (with or without the leading `ffmpeg` word)
3. Click **Convert** — the current graph is replaced with the imported graph

**Export graph → FFmpeg (→ FFmpeg button):**
1. Click **→ FFmpeg** in the toolbar
2. The dialog shows the closest equivalent FFmpeg command line for the current graph
3. Click **Copy** to copy it to the clipboard

Not every MediaMolder feature has a direct FFmpeg CLI equivalent (e.g. per-node error policies, named assets). The export is best-effort; the canonical representation is always the JSON file.

---

### 7.12 Keyboard shortcuts

| Key | Action |
|---|---|
| `?` or `Shift+/` | Open / close the Help dialog |
| `Esc` | Close the currently open dialog |
| `Backspace` / `Delete` | Delete the selected node or edge (not active when focus is in an input field) |

React Flow canvas shortcuts (built-in):
- **Ctrl/Cmd + A** — select all nodes
- **Ctrl/Cmd + scroll** — zoom
- **Space + drag** — pan

---

## 8. Tips and troubleshooting

**"Cannot find frontend/dist"**
Build the frontend first: `cd frontend && npm run build`. Then run `mediamolder gui` from the repository root, or pass `--static` with the path to the built `dist/` folder.

**"parse config: unknown field …"**
Run `mediamolder inspect job.json` to see the parsed config. Unknown fields are silently dropped by the JSON decoder; use `mediamolder validate` to catch structural problems. Check the [JSON config reference](json-config-reference.md) for the exact field names.

**Encoder not found**
Run `mediamolder list-codecs` to see every encoder available in the linked libavcodec build. GPU encoders (nvenc, vaapi, qsv, videotoolbox) are only present when FFmpeg was compiled with the corresponding backend enabled.

**Validation passes but the run fails immediately**
Use `mediamolder inspect` to check the resolved config. Look for unexpected `null` values or missing nodes. Use `mediamolder validate` (without `--no-probe`) to catch codec/format mismatches before running.

**Interlaced video looks combed on output**
`mediamolder validate` will report `VIDEO_INTERLACED_NO_DEINTERLACE` and suggest adding a `yadif` filter. In the GUI, click **Apply Fix** on that issue to insert the filter automatically.

**HDR content looks washed out in SDR output**
Add a `zscale` filter to linearise the signal followed by a `tonemap` filter to map highlights. The validate command reports this as `VIDEO_HDR_NO_TONEMAP` with the exact filter chain to add. The GUI can insert both nodes with a single click.

**HEVC in MP4 won't play in QuickTime**
Add `"codec_tag_video": "hvc1"` to the output. The validate command reports this as `HEVC_MP4_MISSING_HVC1_TAG` and the GUI can apply it as a one-click fix.

**Performance**
- Set `global_options.threads` to the number of physical cores for CPU-bound jobs
- Use a hardware backend (`hw_accel: "cuda"` / `"vaapi"` / `"qsv"` / `"videotoolbox"`) for encode-heavy workloads
- For multi-output jobs with an identical source, use a `split` filter before the two encode branches rather than opening the input twice
- Use `codec_video: "copy"` whenever re-encoding is not needed — remuxing is near-instantaneous

**Saving from the GUI for use with the CLI**
The JSON exported by **Save** or **Save As…** is a standard MediaMolder graph config. Run it directly:
```sh
mediamolder run my_job.json
```
The `graph.ui` block (node positions) is ignored at runtime.

**Checking what version of libav you have**
```sh
mediamolder version
```

---

## Related documentation

| Document | Contents |
|---|---|
| [concepts-and-graph-basics.md](concepts-and-graph-basics.md) | Core concepts, terminology, and design principles |
| [json-config-reference.md](json-config-reference.md) | Complete field-by-field JSON schema reference |
| [ffmpeg-migration-guide.md](ffmpeg-migration-guide.md) | FFmpeg command → JSON mapping table with examples |
| [hardware-acceleration.md](hardware-acceleration.md) | Hardware setup, zero-copy paths, device context management |
| [subtitles.md](subtitles.md) | Subtitle format support, burn-in, passthrough, charset handling |
| [error-handling.md](error-handling.md) | Error policy, retry, fallback |
| [observability.md](observability.md) | Event bus, metrics, SSE API |
| [graph-state-machine.md](graph-state-machine.md) | Graph lifecycle, pause/resume, seek |
| [build_and_packaging.md](build_and_packaging.md) | Static linking, cross-compilation, packaging |
| [graph_validation_design.md](graph_validation_design.md) | Validation architecture and issue code catalogue |
