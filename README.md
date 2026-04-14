# MediaMolder

A modern, Go-native media processing engine built on open source media libraries.

FFmpeg is an incredible open source project, incorporating many powerful open-source media processing libraries. 

FFmpeg has two distinct layers: 
- an **interface / orchestration layer** that provides a Command Line Interface (CLI), parses command strings, builds a media processing graph (pipeline), and runs the pipeline until all processing is completed, and
- a set of **media processing libraries** (libavcodec, libavformat, libavfilter, etc.) that do the actual media processing (container file parsing, analysis, demuxing, decoding, filtering, encoding, and muxing).

### 1. Project Overview
MediaMolder is an independent, open-source media processing engine written in Go. It provides a new orchestration layer on top of the libav* C media processing libraries — the same battle-tested libraries that power FFmpeg — replacing the CLI's command-line-driven, string-based architecture with a clean, declarative JSON pipeline model. It is not a wrapper around the ffmpeg binary; it is a ground-up redesign of the high-level engine that retains full media conversion capability through direct libav* bindings.

The goal is **maximum usability and operational reliability** while matching FFmpeg's functional capabilities.

Version 1.0 should be considered experimental.

### 2. Primary Objectives
- Innovate on media processing orchestration, execution, observability and reliability.
- Maintain identical media capabilities (formats, codecs, filters, devices, bitstream filters) through direct libav* bindings.
- Replace error-prone CLI string parsing and cryptic filtergraph syntax with a single, structured JSON file containing all commands and parameters for each job.
  - Avoid command-line escaping nightmares.
  - Avoid command-line length limitations.
  - Make it easier to construct and validate jobs programmatically.
  - A JSON is introspectable; treat jobs as data, not strings — store in databases, diff between versions, validate before execution.
- Provide a declarative, version-controlled configuration model (JSON pipeline configs, in-memory Go structs).
- Enable builds that are nearly identical in speed to FFmpeg.
  - Go is a fast, highly portable, compiled language. Though not quite as fast as compiled C code, the interface/orchestration layer is not where the compute-intensive operations happen. In theory, MediaMolder should have performance that should closely match builds of FFmpeg.
- Deliver first-class runtime observability, dynamic control, and resilience for live processing and long-running jobs.
- Make the engine trivially embeddable as a library in any Go program.
- Remain fully LGPL compliant (see [LICENSING.md](LICENSING.md)).
- To facilitate adoption and experimentation, provide a parser that converts any FFmpeg CLI command to a MediaMolder JSON pipeline config (see [FFmpeg Migration Guide](docs/ffmpeg-migration-guide.md)).
- Manage the project in a fair, open-minded way that attracts like-minded contributors.


### 3. Non-Goals
- Competing with the FFmpeg project. 
  - There is no intent or desire to fork or manage development of the media processing libraries that power FFmpeg. 
- Rewriting existing codec or filter processing libraries in Go.

---

## Prerequisites

- **Go 1.23+**
- **FFmpeg 8.1+** (libavcodec 62.x, libavformat 62.x, libavfilter 11.x, libavutil 60.x)
  - Either a system install (via Homebrew, apt, etc.) with `pkg-config` available, **or** a source build in a sibling directory (see static build below)
- **pkg-config** (if using system FFmpeg)
- **Git LFS** (for the media test corpus, when available): `git lfs install`

---

## Installation

### From source

```sh
git clone https://github.com/MediaMolder/mediamolder.git
cd mediamolder
```

**Using system FFmpeg (via pkg-config):**
```sh
go build ./...
```

**Using a local FFmpeg source build (static linking):**

Place your FFmpeg source tree as a sibling directory (i.e. `../ffmpeg` relative to the mediamolder checkout), then build with the `ffstatic` tag:
```sh
go build -tags=ffstatic ./...
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
        "params": { "w": "1280", "h": "720" }
      }
    ],
    "edges": [
      { "from": "src:v:0",  "to": "scale",  "type": "video" },
      { "from": "scale",    "to": "out:v",   "type": "video" },
      { "from": "src:a:0",  "to": "out:a",   "type": "audio" }
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
- [FFmpeg Migration Guide](docs/ffmpeg-migration-guide.md)
- [Pipeline State Machine](docs/pipeline-state-machine.md)
- [Clock & Sync](docs/clock-and-sync.md)
- [Event Bus](docs/event-bus.md)
- [Error Handling](docs/error-handling.md)
- [Hardware Acceleration](docs/hardware-acceleration.md)
- [Observability](docs/observability.md)
- [Build & Packaging](docs/build_and_packaging.md)
- [Contribution & Governance](docs/contribution_and_governance.md)
- [Project Specification](docs/specification.md)
- [Licensing](LICENSING.md)

---

## License

LGPL-2.1-or-later. See [LICENSE](LICENSE) and [LICENSING.md](LICENSING.md) for details.
