# Camera-RAW Decode Guide

MediaMolder can develop camera-RAW files (NEF/CR2/CR3/ARW/RAF/ORF/RW2/PEF/SRW/DNG) to a full,
demosaiced **8-bit sRGB** raster via [LibRaw](https://www.libraw.org/) — the field-standard RAW
pipeline used by digiKam, Krita and ImageMagick. This is real RAW develop (demosaic + white
balance), not the camera's embedded JPEG preview.

> **Why a dedicated path?** libav — the engine's normal image decoder — is a *codec* library, not
> a RAW pipeline: it does not demosaic the colour-filter-array sensor data or apply white balance,
> so it renders camera RAW to a **black/garbled frame**. LibRaw fills that gap.

RAW develop is gated behind the `with_libraw` build tag, with a pure-Go stub in the default
build. We ship **no binary**: the bundled LibRaw is built from pinned, SHA-256-verified source by
`scripts/bundle-libraw.sh`.

## Contents

- [Supported formats](#supported-formats)
- [Building with LibRaw](#building-with-libraw)
- [CLI](#cli)
- [Graph node (`raw_decode`)](#graph-node-raw_decode)
- [GUI](#gui)
- [How it works](#how-it-works)
- [Troubleshooting](#troubleshooting)

## Supported formats

`.nef` (Nikon), `.cr2`/`.cr3` (Canon), `.arw` (Sony), `.raf` (Fujifilm), `.orf` (Olympus/OM),
`.rw2` (Panasonic), `.pef` (Pentax), `.srw` (Samsung), `.dng` (Adobe Digital Negative). LibRaw
reads more; this set is the develop path's committed contract (`raw.IsRAW`).

## Building with LibRaw

```bash
# 1. Build the bundled, pinned LibRaw from source (downloads + SHA-256-verifies it,
#    builds a self-contained static lib into third_party/libraw — gitignored).
scripts/bundle-libraw.sh

# 2a. CLI / library:
CGO_LDFLAGS_ALLOW='.*' go build -tags with_libraw ./cmd/mediamolder
#  or:  make build-libraw

# 2b. GUI single binary (static FFmpeg + statically-linked LibRaw):
make build-gui-libraw
#  compose with other nodes:  make build-gui-libraw EXTRA_TAGS=with_whisper
```

LibRaw is linked **statically**, so — unlike whisper — there is no runtime library to locate and
no rpath. Check readiness any time with `mediamolder raw-setup`.

## CLI

```bash
# Diagnose: is LibRaw built in? Prints the exact build command if not. Exits 0 only when ready.
mediamolder raw-setup

# Develop a RAW to a full-resolution sRGB image (PNG by default; JPEG with --format jpeg):
mediamolder raw-decode photo.cr2                 # → photo.png
mediamolder raw-decode photo.nef -o out.jpg      # JPEG (quality via --quality, default 92)
```

In a build without LibRaw, both commands fail with an actionable message naming `with_libraw` —
never a crash or a black image.

## Graph node (`raw_decode`)

`raw_decode` is a [FrameSource](go-processor-nodes.md) `go_processor`: it develops one RAW file
and emits a single full-resolution RGBA video frame into the graph, so you can scale, sharpen,
watermark, and encode a real develop. It needs no graph inputs — the file is a parameter.

```json
{
  "nodes": [
    { "id": "raw", "type": "go_processor", "processor": "raw_decode",
      "params": { "input": "photo.dng" } },
    { "id": "scale", "type": "filter", "filter": "scale", "params": { "w": "2048", "h": "-1" } },
    { "id": "out", "type": "encoder", "params": { "codec": "mjpeg" } }
  ],
  "edges": [
    { "from": "raw", "to": "scale" },
    { "from": "scale", "to": "out" }
  ]
}
```

| Param   | Meaning                                              |
|---------|------------------------------------------------------|
| `input` | Path to the camera-RAW file to develop (required).   |

The develop parameters are fixed (see [How it works](#how-it-works)); the node exposes only the
file. The node appears in `mediamolder list-processors` and the GUI palette **only** in a
`with_libraw` build.

## GUI

Build the GUI with LibRaw (`make build-gui-libraw`) and the `raw_decode` node appears in the
palette under *Processors*. Its Inspector offers a file browser filtered to RAW extensions and a
summary of the develop; if the running binary lacks LibRaw, a banner says so. The frontend reads
`GET /api/raw-capabilities` (`{"capable": bool, "version": "0.21.3"}`) to detect support.

## How it works

LibRaw is driven with a **fixed, deterministic** parameter set, so output depends only on the
input file and the pinned LibRaw version — not on host state or histogram statistics:

| Parameter        | Value         | Why                                                      |
|------------------|---------------|---------------------------------------------------------|
| `output_bps`     | 8             | matches the engine's 8-bit sRGB canonical raster        |
| `output_color`   | sRGB          | same colour space as the libav path                     |
| `gamm`           | 1/2.4, 12.92  | sRGB transfer curve                                     |
| `no_auto_bright` | on            | disable histogram auto-exposure — predictable, faithful |
| `use_camera_wb`  | on            | as-shot white balance from the file                     |
| `use_auto_wb`    | off           | never compute white balance from image statistics       |
| `user_qual`      | AHD (3)       | one pinned demosaic algorithm                           |
| `highlight`      | clip          | deterministic highlight handling                        |
| `user_flip`      | 0 (none)      | **no rotation** — downstream nodes own orientation      |

`user_flip = 0` is load-bearing: the raster is returned **un-oriented**, exactly like the libav
path, so an output-side orientation step is not double-applied.

For the engine's design — the three decode intents, why dedup/thumbnail hashing stays on the
stable preview, and the future transparent-source path — see
[docs/architecture/raw-decode.md](architecture/raw-decode.md).

## Troubleshooting

### `unknown processor "raw_decode"` / `raw-decode` says LibRaw is missing
The binary was built without `with_libraw`. Run `scripts/bundle-libraw.sh`, then
`make build-gui-libraw` (GUI) or `go build -tags with_libraw ./cmd/mediamolder` (CLI). Confirm
with `mediamolder raw-setup`.

### `Package 'libraw' / cannot find -lraw` at build time
`third_party/libraw` is missing — run `scripts/bundle-libraw.sh` first.

### A RAW exports black
That is the libav path. Develop through `raw_decode` (or `raw-decode`), not a plain `input` node.

## See also

- [Go Processor Nodes](go-processor-nodes.md)
- [RAW decode design](architecture/raw-decode.md)
- [Build & Packaging](build-and-packaging.md)
