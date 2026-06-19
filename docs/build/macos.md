# Build MediaMolder on macOS

This guide covers building the `mediamolder` binary (CLI + library) and the
embedded GUI on macOS, plus the day-to-day rebuild loops after editing code.

See also: [Cross-platform overview](../build-and-packaging.md) ·
[Linux](linux.md) · [Windows](windows.md).

## 1. Prerequisites

Install once:

```bash
# Xcode command-line tools (provides clang, make, git)
xcode-select --install

# Homebrew packages
brew install go ffmpeg pkg-config

# Node.js — only if you want to build the GUI
brew install node
```

Verify:

```bash
go version          # 1.25 or later
pkg-config --version
ffmpeg -version     # 8.1 or later
node --version      # 20 or later (GUI only)
```

## 2. Get the source

In Terminal:

```bash
git clone https://github.com/MediaMolder/mediamolder.git
cd mediamolder
```

## 3. Build the binary

Choose **one** of the two options below depending on which FFmpeg you want
MediaMolder to use. You do not need both.

**Option A** is the right choice for most people: it links against the FFmpeg
you already have installed (the one that runs when you type `ffmpeg` in your
terminal — installed by Homebrew in step 1).

**Option B** is for situations where you need to control the exact FFmpeg
version or build options — for example:
- The Homebrew FFmpeg is too old and you've built a newer one from source.
- You need FFmpeg compiled with codecs or flags that Homebrew doesn't enable.
- You want a fully self-contained binary that runs on machines without Homebrew.
- You're working on MediaMolder and FFmpeg side-by-side and need to test against
  unreleased FFmpeg changes.

### Option A — default (Homebrew) FFmpeg

Links against the Homebrew FFmpeg via `pkg-config`.
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

Use this to link against a different FFmpeg than the one Homebrew provides.

#### B1. Custom shared FFmpeg (still uses pkg-config)

If you have a compiled FFmpeg source tree that publishes `.pc` files, point
`pkg-config` at it:

```bash
export PKG_CONFIG_PATH=/path/to/ffmpeg/build/lib/pkgconfig:$PKG_CONFIG_PATH
go build -o mediamolder ./cmd/mediamolder
```

#### B2. Static FFmpeg — fully self-contained binary (`ffstatic` tag)

Links FFmpeg's `.a` archives directly into the binary so it runs anywhere
without Homebrew or a system FFmpeg installed.

Place the FFmpeg source tree as a sibling of `mediamolder/`:

```text
parent/
├── ffmpeg/         ← your FFmpeg checkout
└── mediamolder/
```

Compile FFmpeg as static archives (run once; repeat after FFmpeg upgrades):

```bash
cd ../ffmpeg
./configure --disable-shared --enable-static --enable-gpl \
            --enable-libx264 --enable-libx265 \
            --disable-doc --disable-programs
make -j$(sysctl -n hw.ncpu)
cd ../mediamolder
```

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
make build-gui                  # Option A — default (Homebrew) FFmpeg
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
- `gcc`/`clang` path and version
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
| `whisper_stt` (speech-to-text) | `with_whisper` | whisper.cpp / `libwhisper` | `model` param → ggml model path |
| `yolo_v8` (object detection) | `with_onnx` | ONNX Runtime shared lib | `ONNXRUNTIME_SHARED_LIBRARY_PATH` |
| `vidi_analyzer` (multimodal) | *(none)* | a running Vidi 2.5 service | `service_url` param |
| `twelvelabs_*` (cloud understanding) | *(none)* | TwelveLabs API key | `TWELVELABS_API_KEY` |

> `whisper_stt` binds **whisper.cpp** (`ggml-org/whisper.cpp`), **not** the
> OpenAI Python `whisper` package — the latter does not produce `libwhisper`.

### whisper_stt (whisper.cpp)

```bash
brew install cmake                          # build tool for whisper.cpp

# 1. Clone + build whisper.cpp (Metal-accelerated on Apple Silicon)
git clone https://github.com/ggml-org/whisper.cpp
cmake -S whisper.cpp -B whisper.cpp/build
cmake --build whisper.cpp/build -j
cmake --install whisper.cpp/build           # installs whisper.pc for pkg-config
# Homebrew's prefix is already on PKG_CONFIG_PATH; for a custom prefix:
#   export PKG_CONFIG_PATH=<prefix>/lib/pkgconfig:$PKG_CONFIG_PATH

# 2. Build MediaMolder with the node compiled in
make build-whisper                          # = go build -tags=with_whisper ./...
# Static FFmpeg + a sibling whisper.cpp tree at ../whisper.cpp (next to the
# mediamolder checkout) instead:
#   CGO_LDFLAGS_ALLOW='.*' go build -tags=ffstatic,with_whisper ./...

# 3. Fetch a model (you supply this — MediaMolder ships none)
./whisper.cpp/models/download-ggml-model.sh base.en

# 4. Run the gated tests against it
export WHISPER_TEST_MODEL=$PWD/whisper.cpp/models/ggml-base.en.bin
make test-whisper
```

Pass the model path in the node's `model` param. Usage, params, and output
formats: [Whisper Speech-to-Text Guide](../whisper-stt-guide.md). Without the
tag, a config using `whisper_stt` fails with `unknown processor "whisper_stt"`.

### yolo_v8 (ONNX Runtime)

```bash
brew install onnxruntime
export ONNXRUNTIME_SHARED_LIBRARY_PATH=$(brew --prefix onnxruntime)/lib/libonnxruntime.dylib

go build -tags=with_onnx ./cmd/mediamolder  # add ffstatic too for a static FFmpeg link
```

You also need a `.onnx` model and a labels file — see the
[YOLOv8 Guide](../yolov8-guide.md).

### vidi_analyzer / twelvelabs_*

No build tag. `vidi_analyzer` needs a running
[Vidi 2.5](https://github.com/bytedance/vidi) service (pass its `service_url`);
the `twelvelabs_*` nodes need a [TwelveLabs](https://twelvelabs.io) API key via
`TWELVELABS_API_KEY`, the `api_key` param, or
`~/.config/mediamolder/twelvelabs.json`. See the
[Vidi 2.5](../vidi-guide.md) and [TwelveLabs](../twelvelabs.md) guides.

## Troubleshooting

- **`pkg-config: command not found`** — `brew install pkg-config`.
- **`Package libavcodec was not found`** — Homebrew FFmpeg is missing or
  outdated; `brew install ffmpeg` (must be 8.1+).
- **`ld: library not found for -lavcodec` with `ffstatic`** — the
  `../ffmpeg/libav*/*.a` files don't exist. Re-run `make` in the FFmpeg
  source tree. Only applies to Option B2.
- **Apple Silicon vs. Intel mismatch** — your FFmpeg `.a` archives must be
  built for the same architecture you're compiling Go for. Check with
  `file ../ffmpeg/libavcodec/libavcodec.a`. Only applies to Option B2.
