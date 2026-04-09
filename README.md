# MediaMolder

A modern, Go-native media processing engine built on libav*.

MediaMolder replaces the FFmpeg command-line toolchain with a structured, declarative JSON pipeline model — while preserving 100% of libav* capabilities. It is not a wrapper of the `ffmpeg` CLI; it is a ground-up redesign of the high-level orchestration layer.

---

## Prerequisites

- **Go 1.23+**
- **FFmpeg 8.1+** (libavcodec 62.x, libavformat 62.x, libavfilter 11.x, libavutil 60.x)
  - Either a system install (via Homebrew, apt, etc.) with `pkg-config` available, **or** a source build in `~/ffmpeg`
- **pkg-config** (if using system FFmpeg)
- **Git LFS** (for the media test corpus): `git lfs install`

---

## Installation

### From source

```sh
git clone https://github.com/MediaMolder/MediaMolder.git
cd MediaMolder
```

**Using system FFmpeg (via pkg-config):**
```sh
go build ./...
```

**Using a local FFmpeg source build:**
```sh
FFMPEG_SRC=/path/to/ffmpeg make build
```

**Install the CLI:**
```sh
go install ./cmd/mediamolder
```

---

## Quickstart

Create a file `transcode.json`:
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
    "nodes": [
      {
        "id": "scale",
        "type": "filter",
        "filter": "scale",
        "params": { "width": 1280, "height": 720 }
      }
    ],
    "edges": [
      { "from": "src:v:0",       "to": "scale:default", "type": "video" },
      { "from": "scale:default", "to": "out:v",          "type": "video" },
      { "from": "src:a:0",       "to": "out:a",          "type": "audio" }
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

Run it:
```sh
mediamolder run transcode.json
```

Run with JSON progress output:
```sh
mediamolder run --json transcode.json
```

Inspect the resolved pipeline graph without running:
```sh
mediamolder inspect transcode.json
```

Convert an FFmpeg command to MediaMolder JSON:
```sh
mediamolder convert-cmd "ffmpeg -i input.mp4 -vf scale=1280:720 -c:v libx264 -c:a aac output.mp4"
```

List available codecs, filters, or formats:
```sh
mediamolder list-codecs
mediamolder list-filters
mediamolder list-formats
mediamolder list-codecs --json   # JSON output
```

### Multi-input overlay example

```json
{
  "schema_version": "1.0",
  "inputs": [
    { "id": "bg", "url": "background.mp4", "streams": [{"input_index": 0, "type": "video", "track": 0}] },
    { "id": "fg", "url": "overlay.png", "streams": [{"input_index": 0, "type": "video", "track": 0}] }
  ],
  "graph": {
    "nodes": [
      { "id": "ov", "type": "filter", "filter": "overlay", "params": {"x": 10, "y": 10} }
    ],
    "edges": [
      { "from": "bg:v:0", "to": "ov:default", "type": "video" },
      { "from": "fg:v:0", "to": "ov:overlay", "type": "video" },
      { "from": "ov:default", "to": "out:v", "type": "video" }
    ]
  },
  "outputs": [
    { "id": "out", "url": "composited.mp4", "codec_video": "libx264" }
  ]
}
```

### Multi-output (adaptive bitrate) example

```json
{
  "schema_version": "1.0",
  "inputs": [
    { "id": "src", "url": "input.mp4", "streams": [{"input_index": 0, "type": "video", "track": 0}] }
  ],
  "graph": {
    "nodes": [
      { "id": "split", "type": "filter", "filter": "split" },
      { "id": "hd", "type": "filter", "filter": "scale", "params": {"w": "1920", "h": "1080"} },
      { "id": "sd", "type": "filter", "filter": "scale", "params": {"w": "640", "h": "480"} }
    ],
    "edges": [
      { "from": "src:v:0", "to": "split:default", "type": "video" },
      { "from": "split:out0", "to": "hd:default", "type": "video" },
      { "from": "split:out1", "to": "sd:default", "type": "video" },
      { "from": "hd:default", "to": "out_hd:v", "type": "video" },
      { "from": "sd:default", "to": "out_sd:v", "type": "video" }
    ]
  },
  "outputs": [
    { "id": "out_hd", "url": "output_1080p.mp4", "codec_video": "libx264" },
    { "id": "out_sd", "url": "output_480p.mp4", "codec_video": "libx264" }
  ]
}
```

---

## Documentation

- [JSON Config Reference](docs/json-config-reference.md)
- [Pipeline State Machine](docs/pipeline-state-machine.md)
- [Clock & Sync](docs/clock-and-sync.md)
- [Event Bus](docs/event-bus.md)
- [FFmpeg Migration Guide](docs/ffmpeg-migration-guide.md)
- [Project Specification](docs/spec_v2.md)
- [Build Plan](docs/build_plan.md)

---

## License

LGPL-2.1-or-later. See [LICENSE](LICENSE) and [LICENSING.md](LICENSING.md) for details.
