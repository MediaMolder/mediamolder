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

Inspect the resolved pipeline graph without running:
```sh
mediamolder inspect transcode.json
```

---

## Documentation

- [Project Specification](docs/spec_v3.md)
- [Build & Packaging](docs/build_and_packaging.md)
- [Roadmap](docs/roadmap.md)
- [Future Improvements](docs/future_improvements.md)
- [Contribution & Governance](docs/contribution_and_governance.md)
- [Licensing Guide](LICENSING.md)

---

## License

LGPL-2.1-or-later. See [LICENSE](LICENSE) and [LICENSING.md](LICENSING.md) for details.
