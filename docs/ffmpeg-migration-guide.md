# FFmpeg JSON Command Format — Migration Guide

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
