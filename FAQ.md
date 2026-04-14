# Frequently Asked Questions

---

## General

### Why does this project exist? Why not just contribute to FFmpeg?

The architecture of MediaMolder is radically different than FFmpeg's interface and orchestration layer. Rather than proposing incremental changes to a large, established C codebase, it is faster to build and test these ideas as an independent project. That's the beauty of open source! The FFmpeg project is welcome to adopt any of these ideas at any time. To get a sense for how new ideas are reviewed, discussed, and adopted or rejected by the maintainers of the FFmpeg project, see https://lists.ffmpeg.org/archives/list/ffmpeg-devel@ffmpeg.org/.

### Is this a fork of FFmpeg?

No. MediaMolder was inspired by FFmpeg, but it does not copy or incorporate FFmpeg source code into its own codebase. It links to the same libav* shared libraries that FFmpeg uses (libavcodec, libavformat, libavfilter, libavutil, libswscale, libswresample), but the orchestration engine was written from scratch in Go.

### What exactly does MediaMolder replace? What does it keep?

MediaMolder replaces FFmpeg's **orchestration layer** — the CLI, the command-line parser, the filtergraph string parser, and the pipeline execution loop. It keeps the **media processing libraries** (libav*) that do the actual demuxing, decoding, filtering, encoding, and muxing. Every codec, format, filter, and hardware accelerator available in your FFmpeg build is available in MediaMolder.

### Can MediaMolder do everything FFmpeg can?

MediaMolder can access every codec, filter, format, and device that the linked libav* libraries support. However, some advanced FFmpeg CLI features (such as complex multi-output filtergraph syntax, certain stream specifiers, or niche CLI-only options) may not yet have a direct equivalent in the JSON pipeline config. The `convert-cmd` tool will report unsupported options when converting.

---

## Usage

### How do I convert my existing FFmpeg commands to MediaMolder configs?

Use the built-in converter:

```sh
mediamolder convert-cmd "ffmpeg -i input.mp4 -vf scale=1280:720 -c:v libx264 -c:a aac output.mp4"
```

This parses the FFmpeg command and outputs the equivalent MediaMolder JSON config to stdout. See the [FFmpeg Migration Guide](docs/ffmpeg-migration-guide.md) for a detailed mapping of common FFmpeg options to JSON fields.

### Can I use MediaMolder as a Go library?

Yes. Import the `pipeline` and `av` packages directly. Create a `pipeline.Config` struct (or parse one from JSON), then run it with the pipeline engine. The CLI is a thin wrapper around this library interface.

### Does MediaMolder support hardware acceleration?

Yes. Set `global_options.hw_accel` in your JSON config to one of: `cuda` (NVIDIA), `vaapi` (Intel/AMD on Linux), `qsv` (Intel Quick Sync), or `videotoolbox` (macOS). Optionally set `global_options.hw_device` to specify a device path (e.g. `/dev/dri/renderD128` for VAAPI). Compatible filters (scale, overlay, transpose, etc.) are automatically mapped to their hardware equivalents. If hardware acceleration is unavailable, the pipeline falls back to software processing.

### How do I handle files with multiple audio or subtitle tracks?

List each track in the `streams` array of the input, specifying the `type` and `track` index:

```json
"streams": [
  { "input_index": 0, "type": "video", "track": 0 },
  { "input_index": 0, "type": "audio", "track": 0 },
  { "input_index": 0, "type": "audio", "track": 1 },
  { "input_index": 0, "type": "subtitle", "track": 0 }
]
```

Then route each stream to the desired output using edges in the graph.

---

## Building and Compatibility

### Which FFmpeg version do I need?

FFmpeg 8.1 or later (libavcodec 62.x, libavformat 62.x, libavfilter 11.x, libavutil 60.x). MediaMolder checks library versions at startup and will exit with a clear error if the requirement is not met.

### Can I use a system-installed FFmpeg?

Yes. The default build uses `pkg-config` to find system-installed libav* development libraries. On macOS: `brew install ffmpeg`. On Debian/Ubuntu: `apt-get install libavcodec-dev libavformat-dev libavfilter-dev libavutil-dev libswscale-dev libswresample-dev`. On Fedora: `dnf install ffmpeg-devel`.

### Does MediaMolder work on Windows?

Yes, via MSYS2 or WSL. Under MSYS2, install `mingw-w64-x86_64-ffmpeg` and build with the MinGW toolchain. Native Windows builds require a working CGo environment with access to the libav* development headers and libraries.

### Why does the build require CGo?

MediaMolder calls the libav* C libraries directly through Go's CGo interface. There is no pure-Go alternative for these libraries — they contain decades of hand-optimized codec implementations, SIMD routines, and hardware acceleration interfaces. CGo is the bridge that makes this possible without reimplementing any of that work.

---

## Licensing

### Why LGPL and not MIT or Apache?

The libav* libraries that MediaMolder links to are licensed under LGPL-2.1-or-later (in their default configuration). Using the same license keeps the legal picture simple: there is a single license for the entire stack when using dynamic linking. Note that if you create a build of MediaMolder using a build of FFmpeg that contains GPL libraries (x264, x265, etc.), your build of MediaMolder will be subject to the terms of the GPL license. See [LICENSING.md](LICENSING.md).

### Can I use MediaMolder in a commercial product?

We are not attorneys, and this is not legal advice. Under LGPL-2.1-or-later, you should be able to use MediaMolder in commercial and proprietary applications provided you either dynamically link (so end users can replace the shared libraries) or comply with LGPL re-linking requirements. See [LICENSING.md](LICENSING.md) for the full details.

### What happens if my FFmpeg build enables GPL or nonfree codecs?

The license of the combined binary escalates based on FFmpeg's build flags:

| FFmpeg build flags | Combined binary license |
|-|-|
| (default) | LGPL-2.1-or-later |
| `--enable-version3` | LGPL-3.0-or-later |
| `--enable-gpl` | GPL-2.0-or-later |
| `--enable-gpl --enable-version3` | GPL-3.0-or-later |
| `--enable-nonfree` | **Cannot be distributed** |

MediaMolder detects the linked FFmpeg configuration at startup and prints a license warning if escalation applies. Run `mediamolder version` to see the detected license level.

### Are pre-compiled binaries available?

No. MediaMolder does not distribute pre-compiled binaries due to patent licensing concerns around media codecs. You are responsible for understanding and obtaining licenses for any applicable patents. Build from source with your own FFmpeg installation.

---

## Project Status

### Is MediaMolder production-ready?

Not yet. The current release is v1.0.0-rc.1 (release candidate). The core pipeline engine, JSON config parser, and FFmpeg CLI converter are functional, but the project has not yet reached a stable 1.0 release. It has not been tested on a wide variety of hardware and OS versions. Expect rough edges.

### Will the JSON schema change?

The current schema version is `1.0`. While the project is pre-1.0, breaking changes to the schema are possible but will be accompanied by a version bump and migration guidance. After a stable 1.0 release, the schema will follow semantic versioning — new fields may be added, but existing fields will not be removed or have their meaning changed within a major version.

### How can I contribute?

Fork the repo, make your changes, and open a pull request against `main`. Sign off every commit with `git commit -s` (DCO). See [Contribution & Governance](docs/contribution_and_governance.md) for the full guidelines.

## Support

### Do you offer support?

At this point, MediaMolder is for skilled developers who are interested in exploring better ways to orchestrate media processing. It is open-source, which allows developers to trace and fix bugs (and ideally, contribute those bug-fixes back to this project). If you want to report a bug or request an improvement, go to https://github.com/MediaMolder/mediamolder/issues and open a new issue.
