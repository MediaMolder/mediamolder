# FFmpeg Migration Guide

Convert FFmpeg CLI commands to MediaMolder JSON configs using `convert-cmd`:

```sh
mediamolder convert-cmd "ffmpeg -i input.mp4 -c:v libx264 out.mp4"
```

## Common conversions

| # | FFmpeg CLI | Notes |
|---|-----------|-------|
| 1 | `ffmpeg -i in.mp4 -c:v libx264 -c:a aac out.mp4` | Simple transcode |
| 2 | `ffmpeg -i in.mp4 -c copy out.mkv` | Stream copy |
| 3 | `ffmpeg -i in.mp4 -vf scale=1280:720 -c:v libx264 out.mp4` | Scale filter |
| 4 | `ffmpeg -i in.mp4 -vf "scale=640:480,fps=30" -c:v libx264 out.mp4` | Filter chain |
| 5 | `ffmpeg -i in.mp4 -af "volume=2.0" -c:a aac out.mp4` | Audio filter |
| 6 | `ffmpeg -i in.mp4 -an -c:v libx264 out.mp4` | Strip audio |
| 7 | `ffmpeg -i in.mp4 -vn -c:a aac out.mp3` | Audio only |
| 8 | `ffmpeg -i in.mp4 -f matroska -c:v libx264 out.mkv` | Set format |
| 9 | `ffmpeg -i in.mp4 -b:v 2M -b:a 128k -c:v libx264 out.mp4` | Set bitrate |
| 10 | `ffmpeg -i in.mp4 -r 30 -c:v libx264 out.mp4` | Set framerate |
| 11 | `ffmpeg -i in.mp4 -c:v libx265 -c:a aac out.mp4` | HEVC |
| 12 | `ffmpeg -i in.mp4 -c:v libvpx-vp9 -c:a libopus out.webm` | VP9+Opus |
| 13 | `ffmpeg -i in.mp4 -vf "drawtext=text='Hello'" -c:v libx264 out.mp4` | Text overlay |
| 14 | `ffmpeg -i in.mp4 -vf "crop=640:480,scale=320:240" -c:v libx264 out.mp4` | Crop+scale |
| 15 | `ffmpeg -i in.mp4 -af loudnorm -c:a aac out.mp4` | Audio normalize |
| 16 | `ffmpeg -i in.mp4 -vf scale=1920:1080 -af loudnorm -c:v libx264 -c:a aac out.mp4` | V+A filters |
| 17 | `ffmpeg -y -i in.mp4 -c:v libx264 out.mp4` | Overwrite (ignored) |
| 18 | `ffmpeg -i in.mp4 -vf "scale=1280:720,pad=1920:1080" -c:v libx264 out.mp4` | Letterbox |
| 19 | `ffmpeg -i in.mp4 -vf "scale=640:480,pad=640:480,fps=24" -c:v libx264 out.mp4` | 3-filter chain |
| 20 | `ffmpeg -i "my file.mp4" -c:v libx264 "output.mp4"` | Quoted paths |

## Key differences from FFmpeg CLI

| Feature | FFmpeg CLI | MediaMolder |
|---------|-----------|-------------|
| Config format | CLI arguments | JSON (structured, versionable) |
| Filter graphs | String-based | DAG with typed edges |
| Stream selection | `-map 0:v:0` | `"streams"` array in input |
| Error handling | Global flags | Per-node `error_policy` |
| Observability | stderr progress | Event bus + metrics API |
| State control | None (run to end) | Start/Pause/Resume/Seek |

---

## FFmpeg JSON Command Format Reference

MediaMolder drives FFmpeg via the `json_command_patches` branch, which adds
three new options to `ffmpeg`:

| Option | Purpose |
|---|---|
| `-json_cmd <file>` | Execute FFmpeg using a JSON config file instead of CLI args |
| `-json_cmd_print <file>` | Parse JSON and print the equivalent shell command |
| `-json_cmd_gen [args...]` | Convert a CLI invocation to JSON, printed to stdout |

## Why JSON

- Avoids shell-escaping issues with filter graphs, metadata, and special
  characters
- Enables programmatic generation from Go (no shell injection risks)
- `map` and other repeated options become JSON arrays instead of repeated flags
- Config files are versionable and diffable
- Round-trip: generate JSON from an existing command with `-json_cmd_gen`,
  then verify with `-json_cmd_print`

## JSON Schema

```json
{
    "global_options": { "key": value, ... },
    "inputs":  [ { "url": "...", "options": { ... } }, ... ],
    "outputs": [ { "url": "...", "options": { ... } }, ... ],
    "decoders": [ ... ]
}
```

All fields are optional. `global_options` keys are option names without the
leading `-`. Option values can be:

| Go/JSON type | Meaning |
|---|---|
| `true` | Boolean flag (e.g. `"y": true` → `-y`) |
| `false` / `null` | Option is omitted |
| `"string"` | String value |
| `42` (number) | Numeric value, passed as string |
| `["a","b"]` | Repeated option (e.g. `"map": ["0:v","0:a"]`) |

## Converting an Existing Command

Use `-json_cmd_gen` to convert any existing FFmpeg command to JSON:

```bash
ffmpeg -json_cmd_gen \
    -y -ss 00:01:00 -i input.mp4 \
    -map 0:v -map 0:a -c:v libx264 -crf 23 -c:a aac \
    output.mp4
```

Output:
```json
{
    "global_options": { "y": true },
    "inputs":  [ { "url": "input.mp4", "options": { "ss": "00:01:00" } } ],
    "outputs": [ {
        "url": "output.mp4",
        "options": {
            "map": ["0:v", "0:a"],
            "c:v": "libx264",
            "crf": "23",
            "c:a": "aac"
        }
    }]
}
```

## Verifying a JSON Config

```bash
ffmpeg -json_cmd_print config.json
```

Prints the equivalent shell command with proper quoting applied — useful for
debugging option ordering before running.

## MediaMolder Mapping

MediaMolder's `pipeline.Config` (schema version `"1.0"`) is _not_ the same as
FFmpeg's `-json_cmd` format. MediaMolder's config describes a declarative
pipeline graph; the `av` package translates it into FFmpeg API calls at
runtime. The `json_command_patches` branch is used only to build the FFmpeg
libraries; MediaMolder does not invoke the `ffmpeg` binary at runtime.

The examples in `testdata/ffmpeg-json-examples/` are reference material for
understanding what FFmpeg JSON commands look like, for use when designing
MediaMolder schema evolution or debugging pipeline behaviour by cross-checking
against an equivalent CLI invocation.

## Examples

See [`testdata/ffmpeg-json-examples/`](../testdata/ffmpeg-json-examples/) for
annotated examples:

| File | Demonstrates |
|---|---|
| `json_cmd_simple.json` | Basic transcode with x264 + AAC |
| `json_cmd_trim_copy.json` | Stream copy with seek/trim |
| `json_cmd_complex.json` | Scale + volume via `-filter_complex` |
| `json_cmd_filter_chain.json` | Simple `-vf` filter chain |
| `json_cmd_filter_graph.json` | Multi-output filter graph with `split` |
| `json_cmd_image_sequence.json` | Export frames to PNG sequence |
| `json_cmd_lavfi_input.json` | Lavfi virtual input (null audio) |
| `json_cmd_multi_io.json` | Multiple inputs and multiple outputs |
| `filter_complex` | FATE test: `color` source via `-filter_complex` |
| `lavfi` | FATE test: `color` source via `-lavfi` |
| `print_simple` | Round-trip printing: basic seek/encode |
| `print_arrays` | Round-trip printing: repeated `-map` options |
| `value_types` | Value type examples (`bool`, `int`, `null`) |
