# Whisper Speech-to-Text Guide

The `whisper_stt` [go_processor](go-processor-nodes.md) transcribes an audio
stream to timestamped text using [whisper.cpp](https://github.com/ggml-org/whisper.cpp)
— locally and offline, with no network calls. It consumes a decoded audio
stream, passes the audio through unchanged (so it can still be encoded or
muxed), and writes the transcript to a sidecar file (SRT / VTT / JSON / TXT)
while emitting one event per segment on the pipeline event bus.

It is built only when the **`with_whisper`** build tag is set. MediaMolder ships
neither the library nor any model — you supply both (see Prerequisites).

## Contents

- [Prerequisites](#prerequisites)
- [Building with whisper support](#building-with-whisper-support)
- [Pipeline configuration](#pipeline-configuration)
- [Output formats](#output-formats)
- [Reading the transcript](#reading-the-transcript)
- [How it works](#how-it-works)
- [Troubleshooting](#troubleshooting)
- [See also](#see-also)

## Prerequisites

### libwhisper

Build whisper.cpp and make it discoverable in one of two ways.

**System install (pkg-config):**

```bash
git clone https://github.com/ggml-org/whisper.cpp
cmake -S whisper.cpp -B whisper.cpp/build
cmake --build whisper.cpp/build -j
cmake --install whisper.cpp/build      # installs whisper.pc + libwhisper
```

If installed under a custom prefix, point pkg-config at it:
`PKG_CONFIG_PATH=/opt/whisper/lib/pkgconfig`.

**Local source tree (static):** place the built tree as a sibling of the
MediaMolder checkout — `../../whisper.cpp` — and combine the `ffstatic` and
`with_whisper` tags (see below). This mirrors the FFmpeg/x264 static layout.

### Model

Download a ggml/gguf model, e.g.:

```bash
./whisper.cpp/models/download-ggml-model.sh base.en
# → models/ggml-base.en.bin
```

`tiny`/`base` are fast; `small`/`medium`/`large` are more accurate and slower.
Use the `.en` variants for English-only audio.

## Building with whisper support

Run these from the **MediaMolder repo root** (`make build-whisper` lives in
MediaMolder's Makefile, not whisper.cpp's). See the platform guides
([macOS](build/macos.md), [Linux](build/linux.md), [Windows](build/windows.md))
for installing libwhisper first.

```bash
cd /path/to/mediamolder

# Dynamic linking (pkg-config finds FFmpeg + whisper). build-whisper embeds an
# rpath to WHISPER_PREFIX/lib (default /usr/local) so whisper.cpp's @rpath libs
# resolve at runtime:
make build-whisper
# custom install prefix → make build-whisper WHISPER_PREFIX="$HOME/.local"
# or, by hand (rpath via -extldflags so it is added once, not per cgo package):
#   CGO_LDFLAGS_ALLOW='.*' go build -tags=with_whisper \
#     -ldflags='-extldflags "-Wl,-rpath,/usr/local/lib"' ./...

# Static FFmpeg + local whisper.cpp tree at ../../whisper.cpp (advanced — needs a
# static whisper build, -DBUILD_SHARED_LIBS=OFF):
CGO_LDFLAGS_ALLOW='.*' go build -tags=ffstatic,with_whisper ./...
```

Without the tag, `whisper_stt` is simply not registered; a config that
references it fails with `unknown processor "whisper_stt"`. If `make
build-whisper` reports `No rule to make target 'build-whisper'`, you're not in
the MediaMolder repo root.

## Pipeline configuration

Route a decoded audio stream into a `whisper_stt` node. Minimal example:

```json
{
  "id": "stt",
  "type": "go_processor",
  "processor": "whisper_stt",
  "params": {
    "model": "/models/ggml-base.en.bin",
    "language": "en",
    "output_file": "/tmp/out.srt",
    "output_format": "srt"
  }
}
```

### Parameter reference

| Param             | Type   | Default            | Description |
|-------------------|--------|--------------------|-------------|
| `model`           | string | **(required)**     | Path to a ggml/gguf Whisper model |
| `language`        | string | `"auto"`           | Source language hint; `auto` detects |
| `task`            | string | `"transcribe"`     | `transcribe` or `translate` (to English) |
| `beam_size`       | int    | `0`                | `0`/`1` greedy; `>1` beam search |
| `word_timestamps` | bool   | `false`            | Request token-level timestamps |
| `threads`         | int    | `NumCPU()`         | Inference threads |
| `initial_prompt`  | string | `""`               | Context/biasing prompt |
| `output_file`     | string | `""`               | Sidecar path; empty = events only |
| `output_format`   | string | `"srt"`            | `srt` \| `vtt` \| `json` \| `txt` |

The audio stream is automatically resampled to whisper's required 16 kHz mono
float32, regardless of the source rate, format, or channel count.

## Output formats

- **srt** — numbered SubRip cues, `HH:MM:SS,mmm --> HH:MM:SS,mmm`.
- **vtt** — WebVTT with a `WEBVTT` header and `HH:MM:SS.mmm` separators.
- **txt** — plain transcript, one non-empty segment per line, no timing.
- **json** — array of `{ start, end, text, confidence }` (seconds; confidence is
  the mean per-token probability, 0–1) — the same shape as the per-segment
  event payload.

## GUI

In the web GUI, `whisper_stt` appears in the node palette as **"Whisper
speech-to-text"** (search: whisper, stt, speech, subtitle, …). Its Inspector
panel gives typed controls for every param — a file picker for the model, a
language box, transcribe/translate and output-format dropdowns, beam-size and
thread numbers, a word-timestamps toggle, an initial-prompt box, and a file
picker for the transcript — so you don't hand-edit JSON. The node carries an
audio port (pass-through) and an events port. (The node only appears when the
binary is built with the `with_whisper` tag.)

## Reading the transcript

Independent of `output_file`, every segment is published as a `Metadata` event
on the bus with `Custom["start"|"end"|"text"|"confidence"]` and a human
`LogMessage` (`[mm:ss.mmm] text`), shown in the GUI log panel / CLI. Chain an
`events` edge to a [`metadata_file_writer`](go-processor-nodes.md#metadata_file_writer)
to capture the raw event stream as JSONL.

## How it works

`whisper_stt` accumulates resampled audio during `Process` and runs a single
transcription pass in `Close` — whisper.cpp windows the buffer into 30 s chunks
internally. Because all inference happens after the per-frame progress bar
reaches 100 %, the node implements `AsyncMetadataProcessor` and posts
`whisper: transcribing N%` progress updates so the post-frames phase is not
silent. A cancelled job aborts the run via whisper's abort callback.

**Memory:** v1 buffers the whole stream — 16 kHz mono float32 is ≈ 0.23 GB per
hour of audio. This is fine for typical clips; a windowed/streaming variant is
future work.

## Troubleshooting

### `unknown processor "whisper_stt"`

The binary was built without the `with_whisper` tag. Rebuild with
`make build-whisper` (or add `-tags=with_whisper`).

### `Package 'whisper' not found` at build time

pkg-config cannot find `whisper.pc`. Install whisper.cpp (`cmake --install`) or
set `PKG_CONFIG_PATH` to its `lib/pkgconfig`. For the static path, ensure
`../../whisper.cpp/build` exists and use `-tags=ffstatic,with_whisper`.

### `whisper_stt: model file: no such file or directory`

The `model` path is wrong or unreadable. Download a ggml model and pass its
absolute path.

### Empty or garbled transcript

Confirm the audio actually reaches the node (the stream must be decoded, not
stream-copied), try a larger model, and set `language` explicitly rather than
relying on `auto` for short clips.

## See also

- [Go Processor Nodes](go-processor-nodes.md)
- [whisper.cpp](https://github.com/ggml-org/whisper.cpp)
