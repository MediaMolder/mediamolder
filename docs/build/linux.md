# Build MediaMolder on Linux

This guide covers building the `mediamolder` binary (CLI + library) and the
embedded GUI on Debian/Ubuntu and Fedora/RHEL, plus the day-to-day rebuild
loops after editing code.

See also: [Cross-platform overview](../build-and-packaging.md) ·
[macOS](macos.md) · [Windows](windows.md).

## 1. Prerequisites

### Debian / Ubuntu

```bash
sudo apt-get update
sudo apt-get install -y \
    build-essential git pkg-config \
    libavcodec-dev libavformat-dev libavfilter-dev \
    libavutil-dev libswscale-dev libswresample-dev

# Go 1.25+ — apt's version is often too old; prefer the official tarball:
#   https://go.dev/dl/

# Node.js 20+ — only if you want to build the GUI
curl -fsSL https://deb.nodesource.com/setup_20.x | sudo -E bash -
sudo apt-get install -y nodejs
```

> **Ubuntu < 24.04**: distro FFmpeg may be older than 8.1. Either upgrade,
> use a PPA, or build FFmpeg from source (see §4b).

### Fedora / RHEL

FFmpeg is not in the default Fedora repositories — enable
[RPM Fusion](https://rpmfusion.org/) first, then:

```bash
sudo dnf install -y gcc git pkg-config ffmpeg-devel nodejs
```

### Verify

```bash
go version          # 1.25 or later
pkg-config --version
ffmpeg -version     # 8.1 or later
node --version      # 20 or later (GUI only)
```

## 2. Get the source

In your terminal:

```bash
git clone https://github.com/MediaMolder/mediamolder.git
cd mediamolder
```

## 3. Build the binary

Choose **one** of the two options below depending on which FFmpeg you want
MediaMolder to use. You do not need both.

**Option A** is the right choice for most people: it links against the FFmpeg
you already have installed (the one that runs when you type `ffmpeg` in your
terminal).

**Option B** is for situations where you need to control the exact FFmpeg
version or build options — for example:
- Your distro's FFmpeg is older than 8.1 and you've built a newer one from source.
- You need FFmpeg compiled with codecs or flags that your package manager doesn't enable.
- You want a fully self-contained binary that runs on machines without FFmpeg installed.
- You're working on MediaMolder and FFmpeg side-by-side and need to test against
  unreleased FFmpeg changes.

### Option A — default (system) FFmpeg

Links against the FFmpeg installed by your package manager via `pkg-config`.
This is the `ffmpeg` binary already on your `PATH`.

```bash
go build -o mediamolder ./cmd/mediamolder
./mediamolder version
```

Or via Make:

```bash
make build          # builds all packages
```

### Option B — a specific FFmpeg source tree

Use this to link against a different FFmpeg than the one your package manager
provides.

#### B1. Custom shared FFmpeg (still uses pkg-config)

If you have a compiled FFmpeg source tree that publishes `.pc` files, point
`pkg-config` at it:

```bash
export PKG_CONFIG_PATH=/path/to/ffmpeg/build/lib/pkgconfig:$PKG_CONFIG_PATH
export LD_LIBRARY_PATH=/path/to/ffmpeg/build/lib:$LD_LIBRARY_PATH
go build -o mediamolder ./cmd/mediamolder
```

The binary still depends on the shared `.so` files at runtime, so
`LD_LIBRARY_PATH` (or a system-wide `ldconfig` entry) must be set whenever
you run it.

#### B2. Static FFmpeg — fully self-contained binary (`ffstatic` tag)

Links FFmpeg's `.a` archives directly into the binary so it runs anywhere
without a system FFmpeg installed.

Place the FFmpeg source tree as a sibling of `mediamolder/`:

```text
parent/
├── ffmpeg/         ← your FFmpeg checkout
└── mediamolder/
```

Compile FFmpeg as static archives (run once; repeat after FFmpeg upgrades):

```bash
cd ../ffmpeg
make distclean 2>/dev/null || true          # clear any previous (shared) build first
./configure --disable-shared --enable-static --enable-gpl \
            --enable-libx264 --enable-libx265 \
            --disable-doc --disable-programs
make -j$(nproc)
cd ../mediamolder
```

> **`--disable-shared` is mandatory — the #1 cause of a "static" build that
> isn't.** If the FFmpeg tree was *ever* configured with `--enable-shared`, each
> `libav*/` dir holds **both** a `.a` archive **and** a `.so`. The linker prefers
> the `.so`, so the `ffstatic` build links FFmpeg **dynamically** — the binary
> then needs those libs (and their deps) at run time (`error while loading shared
> libraries: …`). Always `make distclean` before re-`configure`-ing static-only;
> confirm with `ls ../ffmpeg/libavcodec/*.so*` (should be empty).
>
> **Keep the static config minimal.** The `ffstatic` flags only add `x264`/`x265`
> + system libs — **not** the text/scaling libs. Do **not** add `--enable-libass`,
> `--enable-libfreetype`, `--enable-libfontconfig`, `--enable-libharfbuzz`,
> `--enable-libfribidi`, or `--enable-libzimg` to a **static** build: they cause
> undefined-symbol link errors. Need subtitle burn-in (libass) or zimg scaling?
> Use a **shared** FFmpeg (Option A or B1).

Build mediamolder, linking the static archives:

```bash
make build-static
# equivalent to:
go build -tags=ffstatic -o mediamolder ./cmd/mediamolder
```

If your FFmpeg tree is not at `../ffmpeg`, override the paths:

```bash
export FFMPEG_SRC=/path/to/ffmpeg
CGO_CFLAGS="-I${FFMPEG_SRC}" \
CGO_LDFLAGS="-L${FFMPEG_SRC}/libavcodec -L${FFMPEG_SRC}/libavformat \
             -L${FFMPEG_SRC}/libavfilter -L${FFMPEG_SRC}/libavutil \
             -L${FFMPEG_SRC}/libswscale -L${FFMPEG_SRC}/libswresample" \
go build -tags=ffstatic -o mediamolder ./cmd/mediamolder
```

## 4. Build the Graphical User Interface (GUI)

The GUI is a React/Vite app embedded into the Go binary via `//go:embed`.
When you run the GUI, mediamolder launches a local web server that 
hosts the user interface in your default browser.

```bash
# One-time: install JS dependencies
cd frontend && npm ci && cd ..

# Build everything (frontend + Go binary with embedded assets)
make build-gui                  # Option A — default FFmpeg
# or
make build-gui-static           # Option B2 — static FFmpeg via ../ffmpeg

./mediamolder gui               # opens the GUI in your browser
```

`make build-gui` compiles `frontend/dist/`, copies it into `internal/gui/dist/`,
then runs `go build -o mediamolder ./cmd/mediamolder`.

## 5. Rebuild loops after code changes

Pick the shortest sequence that covers your edit:

| You changed … | Run |
| --- | --- |
| Go code only (anything outside `frontend/`) | `make build` (or `make build-gui` to also re-embed) |
| Frontend code (`frontend/src/**`) | `make build-gui` (rebuilds React + re-embeds + re-links binary) |
| `frontend/package.json` (added/upgraded a JS package) | `cd frontend && npm install && cd ..` then `make build-gui` |
| Nothing — you just want a fresh binary | `make build` |

The `go build` step is required after frontend edits because the production
assets are baked into the binary at compile time.

### Faster GUI iteration: dev server

While actively editing the frontend, skip the embed-and-rebuild cycle:

```bash
# Terminal 1 — Vite hot-reload server on http://localhost:5173
make frontend-dev

# Terminal 2 — Go backend in dev mode (proxies to Vite, no embedded assets)
make gui-dev
```

Frontend edits reload instantly in your browser. Go code changes still
require restarting terminal 2.

## 6. Debug builds — capturing a full build log

If a build fails and the error isn't obvious, run the debug variant to
capture a complete log of compiler flags, linker invocations, and
environment details:

```bash
make build-debug          # headless binary + mediamolder-build.log
# or, if you're building the GUI:
make build-gui-debug
```

The log file includes:
- `go env` output (GOARCH, GOOS, CGO_ENABLED, …)
- `gcc` path and version
- `pkg-config` cflags/libs for every FFmpeg library
- `PKG_CONFIG_PATH`, `CGO_CFLAGS`, `CGO_LDFLAGS`, `FFMPEG_SRC`
- Full `-v -x` Go build output (every compiler + linker invocation)

You can customise the output path and add build tags:

```bash
make build-gui-debug LOG=/tmp/mediamolder.log
make build-debug BUILD_TAGS=ffstatic LOG=/tmp/static.log
```

Upload `mediamolder-build.log` when [opening a bug report](https://github.com/MediaMolder/mediamolder/issues). The file is safe to share — it contains no passwords or private keys.

## 7. Run the tests

```bash
make test                       # Option A — default FFmpeg
make test-static                # Option B2 — static FFmpeg
go test ./pipeline/...          # narrow to one package
```

## Optional built-in nodes

A few processors are **opt-in**: they sit behind a build tag (so they don't add
a cgo dependency for builds that don't need them) or need an external runtime
service. Install the prerequisites below **before** building or running.

| Node | Build tag | Needs | Runtime env / config |
| --- | --- | --- | --- |
| `whisper_stt` (speech-to-text) | `with_whisper` | whisper.cpp / `libwhisper` + a ggml model | `model` param → ggml model path |
| `yolo_v8` (object detection) | `with_onnx` | ONNX Runtime shared lib + a `.onnx` model | `ONNXRUNTIME_SHARED_LIBRARY_PATH`; `model` + `labels_file` params |
| `face_detect` (face detection + embeddings) | `with_onnx` | ONNX Runtime shared lib + the two face models | `ONNXRUNTIME_SHARED_LIBRARY_PATH`, `MEDIAMOLDER_FACE_MODELS` |
| `vidi_analyzer` (multimodal) | *(none)* | a running Vidi 2.5 service | `service_url` param |
| `twelvelabs_*` (cloud understanding) | *(none)* | TwelveLabs API key | `TWELVELABS_API_KEY` |

> `whisper_stt` binds **whisper.cpp** (`ggml-org/whisper.cpp`), **not** the
> OpenAI Python `whisper` package — the latter does not produce `libwhisper`.
>
> `with_onnx` enables **both** `yolo_v8` **and** `face_detect` (and the
> `mediamolder face-detect` CLI) — one tag, all ONNX nodes.

### Downloading models — how and when

ML models are **not** shipped with MediaMolder and are loaded at **run time**,
not build time. The order is always: **build with the node's tag → download the
model(s) → point an env var or param at them → run.** You only need models for
the node you actually use.

| Node | Download what | How | Point at it with |
| --- | --- | --- | --- |
| `whisper_stt` | a ggml/gguf speech model | `whisper.cpp/models/download-ggml-model.sh base.en` | the node's `model` param |
| `face_detect` / `face-detect` | YOLOv8-face + SFace `.onnx` | `./scripts/fetch-face-models.sh` (SHA-256-verified) | `MEDIAMOLDER_FACE_MODELS` (a dir) or `--models-dir` |
| `yolo_v8` | a YOLOv8 `.onnx` + labels file | export from Ultralytics / your own | the `model` + `labels_file` params |

Models can be large and (for the face detector) carry a **copyleft** licence, so
they are **never committed**. `scripts/fetch-face-models.sh` defaults to the
git-ignored `testdata/face_models/`.

### whisper_stt (whisper.cpp)

Run the whisper.cpp steps from any workspace dir; run the `make` steps from
the **MediaMolder repo root** (a different directory — `make build-whisper`
only exists in MediaMolder's Makefile).

```bash
sudo apt install cmake                       # Debian/Ubuntu  (Fedora/RHEL: sudo dnf install cmake)

# 1. Clone + build whisper.cpp
git clone https://github.com/ggml-org/whisper.cpp
cmake -S whisper.cpp -B whisper.cpp/build
cmake --build whisper.cpp/build -j
sudo cmake --install whisper.cpp/build       # installs to /usr/local (whisper.pc's prefix)
sudo ldconfig                                # refresh the runtime linker cache for /usr/local/lib
# No-sudo alternative — reconfigure to a writable prefix (use the SAME prefix when building):
#   cmake -S whisper.cpp -B whisper.cpp/build -DCMAKE_INSTALL_PREFIX="$HOME/.local"
#   cmake --build whisper.cpp/build -j && cmake --install whisper.cpp/build
#   export PKG_CONFIG_PATH="$HOME/.local/lib/pkgconfig:$PKG_CONFIG_PATH"

# 2. Fetch a model (you supply this — MediaMolder ships none) and note its path
./whisper.cpp/models/download-ggml-model.sh base.en
MODEL="$PWD/whisper.cpp/models/ggml-base.en.bin"

# 3. Build MediaMolder with the node compiled in — RUN FROM THE MEDIAMOLDER REPO
#    ROOT (not whisper.cpp). Embeds an rpath to WHISPER_PREFIX/lib (default
#    /usr/local) for runtime lookup.
cd /path/to/mediamolder                      # your MediaMolder checkout
make build-whisper                           # used ~/.local? → make build-whisper WHISPER_PREFIX="$HOME/.local"

# 4. Run the gated tests against the model
WHISPER_TEST_MODEL="$MODEL" make test-whisper
```

Pass the model path in the node's `model` param. Usage, params, and output
formats: [Whisper Speech-to-Text Guide](../whisper-stt-guide.md). Without the
tag, a config using `whisper_stt` fails with `unknown processor "whisper_stt"`.
If `make build-whisper` reports `No rule to make target 'build-whisper'`, you
are not in the MediaMolder repo root — `cd` there and retry.

### yolo_v8 (ONNX Runtime)

Download a release for your architecture from the
[ONNX Runtime releases](https://github.com/microsoft/onnxruntime/releases),
place `libonnxruntime.so` on your library path, then:

```bash
export ONNXRUNTIME_SHARED_LIBRARY_PATH=/usr/local/lib/libonnxruntime.so

go build -tags=with_onnx ./cmd/mediamolder   # add ffstatic too for a static FFmpeg link
```

You also need a `.onnx` model and a labels file — see the
[YOLOv8 Guide](../yolov8-guide.md).

### face_detect (ONNX Runtime + face models)

`face_detect` and the `mediamolder face-detect` CLI share the `with_onnx` tag
with `yolo_v8`, plus two bundled face models. Build with the tag, then fetch and
point at the models:

```bash
export ONNXRUNTIME_SHARED_LIBRARY_PATH=/usr/local/lib/libonnxruntime.so

# Fetch + SHA-256-verify both models into the git-ignored testdata/face_models/
./scripts/fetch-face-models.sh
export MEDIAMOLDER_FACE_MODELS="$PWD/testdata/face_models"

go build -tags=with_onnx ./cmd/mediamolder   # add ffstatic too for a static FFmpeg link
```

The detector (YOLOv8-face) is **AGPL-3.0** and the embedder (SFace) is
Apache-2.0; both are loaded as **data** at run time (never linked) and
SHA-256-verified on load. MediaMolder ships neither — keep them out of any
committed tree. See the [Face Detection Guide](../face-detection-guide.md).

### vidi_analyzer / twelvelabs_*

No build tag. `vidi_analyzer` needs a running
[Vidi 2.5](https://github.com/bytedance/vidi) service (pass its `service_url`);
the `twelvelabs_*` nodes need a [TwelveLabs](https://twelvelabs.io) API key via
`TWELVELABS_API_KEY`, the `api_key` param, or
`~/.config/mediamolder/twelvelabs.json`. See the
[Vidi 2.5](../vidi-guide.md) and [TwelveLabs](../twelvelabs.md) guides.

### Combining nodes in one binary

The build tags **stack**, so one binary can carry several optional nodes. Pass
extra node tags to a `make` target via `EXTRA_TAGS` — **comma-separated, no
spaces**. They are *appended* to the target's built-in tags.

```bash
# build-gui-whisper already implies `ffstatic,with_whisper`; EXTRA_TAGS adds to that:

make build-gui-whisper EXTRA_TAGS=with_onnx
#   → ffstatic,with_whisper,with_onnx          (whisper + yolo_v8 + face_detect)

# Multiple extra tags at once (comma-separated): ONNX nodes + a static libwhisper link
make build-gui-whisper EXTRA_TAGS=with_onnx,whisperstatic
#   → ffstatic,with_whisper,with_onnx,whisperstatic

# Headless (CLI) build with the same nodes
make build-whisper EXTRA_TAGS=with_onnx
```

`with_onnx` is a **single** tag that enables every ONNX node (`yolo_v8`,
`face_detect`, and the `face-detect` CLI) — you don't list them separately. Each
enabled node keeps its own runtime requirement: `libwhisper` for `whisper_stt`
(resolved by the embedded rpath), and the ONNX Runtime shared library
(`ONNXRUNTIME_SHARED_LIBRARY_PATH`) plus `MEDIAMOLDER_FACE_MODELS` for
`face_detect` — install/fetch those per the sections above **before** running.

> Plain `go build` doesn't read `EXTRA_TAGS`; list the tags yourself, e.g.
> `go build -tags=ffstatic,with_whisper,with_onnx ./cmd/mediamolder`.

## Troubleshooting

- **`error: gcc not found`** or **`undefined: cmdGUI`** — `gcc` is missing.
  Install `build-essential` (Debian/Ubuntu) or `gcc` (Fedora/RHEL).
- **`error: FFmpeg development headers not found`** or **`Package libavcodec was not found`** —
  install `libavcodec-dev` (Debian) or `ffmpeg-devel` (Fedora).
  Check `pkg-config --modversion libavcodec`.
- **`undefined: cmdGUI`** with no other errors visible — this is a cascading
  error from a CGO compilation failure earlier in the output (scroll up).
  The root cause is almost always missing or incompatible FFmpeg headers; see
  the two entries above.
- **Distro FFmpeg too old** — minimum supported is 8.1 (libavcodec 61).
  Build FFmpeg from source and use Option B1 (custom `PKG_CONFIG_PATH`) or B2
  (`ffstatic`).
- **`/usr/bin/ld: cannot find -lavcodec` with `ffstatic`** — the
  `../ffmpeg/libav*/*.a` files don't exist. Re-run `make` in the FFmpeg
  source tree.
- **Runtime: `error while loading shared libraries: libavcodec.so.X`** —
  your custom shared FFmpeg isn't on `LD_LIBRARY_PATH`. Either set it or
  use Option B2 (`ffstatic`) for a self-contained binary.
- **Runtime: `error while loading shared libraries: libharfbuzz.so…` (or
  `libass`, `libfontconfig`, …) from a "static" `ffstatic` build** — your FFmpeg
  tree was configured `--enable-shared`, so it has both `.a` and `.so` and the
  linker chose the `.so` (a *dynamic* link). `make distclean` in `../ffmpeg`,
  reconfigure `--disable-shared --enable-static` **without** the text/scaling
  libs (Option B2), rebuild it, then rebuild MediaMolder. Verify with
  `ls ../ffmpeg/libavcodec/*.so*` (should be empty).
- **`undefined reference to ass_*` / `hb_*` / `FcConfig*` / `zimg_*` with
  `ffstatic`** — your static FFmpeg enabled text/scaling libs the `ffstatic`
  flags don't link. Rebuild FFmpeg static **without** those `--enable-lib*`
  options, or use a shared FFmpeg (Option A/B1).
