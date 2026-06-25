# Build & Packaging

MediaMolder is built from source on each supported platform. This page is a
cross-platform overview; follow the OS-specific guide for copy-and-paste
commands:

- **macOS** → [docs/build/macos.md](build/macos.md)
- **Linux (Debian/Ubuntu, Fedora/RHEL)** → [docs/build/linux.md](build/linux.md)
- **Windows (MSYS2)** → [docs/build/windows.md](build/windows.md)

## What gets built

The repo produces one artefact:

- **`mediamolder`** (`mediamolder.exe` on Windows) — a single Go binary
  containing the CLI, the pipeline runtime, the `compat/ffcli` FFmpeg-flag
  parser, and (when built with the GUI target) the embedded React web 
  Graphical User Interface (GUI) served by `mediamolder gui`.

The Go module also exposes the `av/`, `pipeline/`, `graph/`, etc. packages
as a library; consumers import them directly with `go get`.

## Toolchain matrix

| Tool | Minimum | Used for | Required when … |
| --- | --- | --- | --- |
| Go | 1.25 | Compiler | Always |
| FFmpeg | 8.1 | Media codec/format runtime | Always (linked via `pkg-config` or `ffstatic`) |
| `pkg-config` | — | Locating shared FFmpeg | Default builds |
| C toolchain | — | cgo | Always (Xcode CLT, gcc, MSYS2 MinGW64) |
| Node.js | 20 | Frontend bundler | Building the GUI |

## Two ways to link FFmpeg

| Mode | When to use | Build tag | Find FFmpeg via |
| --- | --- | --- | --- |
| **Shared** (default) | Day-to-day dev, CI, packagers who want to track the system FFmpeg | *(none)* | `pkg-config` (system / `PKG_CONFIG_PATH`) |
| **Static** | Self-contained binaries, CI matrix against a pinned FFmpeg, distribution | `ffstatic` (+ `ffstatic_windows_msys2` on Windows) | Sibling `../ffmpeg` source tree, or `CGO_CFLAGS` / `CGO_LDFLAGS` overrides |

The build-tag pair `av/cgo_flags.go` (default) and `av/cgo_flags_static.go`
(`//go:build ffstatic`) is what selects between the two.

> **Static builds: configure FFmpeg with `--disable-shared`.** The `ffstatic`
> flags add a `-L` for each `../ffmpeg/libav*/` dir. If that tree was configured
> `--enable-shared` it contains **both** `.a` and shared libs (`.dylib`/`.so`),
> and the linker prefers the shared one — so you silently get a *dynamic* FFmpeg
> link that needs those libs at run time. Always `make distclean` the FFmpeg tree
> before re-`configure`-ing static-only. The `ffstatic` flags also link only
> `x264`/`x265` (+ system libs), so a static FFmpeg must **not** enable the text
> libs (`--enable-libass`, `--enable-libfreetype`, `--enable-libharfbuzz`,
> `--enable-libfontconfig`, `--enable-libfribidi`, `--enable-libzimg`) — those
> cause undefined-symbol errors. Need subtitle burn-in/zimg? Use a shared FFmpeg.

## Make targets (Unix)

The `Makefile` ships these top-level targets:

| Target | Output | FFmpeg |
| --- | --- | --- |
| `make build` | `go build ./...` | Shared via `pkg-config` |
| `make build-static` | `go build -tags=ffstatic ./...` | Static via `../ffmpeg` |
| `make build-gui` | `mediamolder` with embedded GUI | Shared |
| `make build-gui-static` | `mediamolder` with embedded GUI | Static |
| `make build-whisper` / `make test-whisper` | `go build -tags=with_whisper ./...` — opt-in `whisper_stt` (needs libwhisper) | Shared |
| `make build-gui-whisper` | GUI binary, `ffstatic,with_whisper` (+ `EXTRA_TAGS`) | Static |
| `make build-gui-onnx` | GUI binary, `ffstatic,with_onnx` — `yolo_v8` + `face_detect`, no whisper | Static |
| `make build-debug` | `mediamolder` + `mediamolder-build.log` | Shared |
| `make build-gui-debug` | `mediamolder` + `mediamolder-build.log` | Shared (+ frontend) |
| `make check-deps` | Verify gcc + FFmpeg ≥ 8.1 headers | — |
| `make test` / `make test-static` | Run the test suite | Shared / static |
| `make frontend-install` | `npm install` in `frontend/` | — |
| `make frontend-build` | Build React app + copy into `internal/gui/dist/` | — |
| `make frontend-dev` | Vite dev server (hot reload) | — |
| `make gui-dev` | Go backend in dev mode (proxies to Vite) | Shared |
| `make clean` | `go clean` + remove `frontend/dist`, `internal/gui/dist` | — |

The `build-debug` and `build-gui-debug` targets capture the full compiler and
linker command log together with system environment details (Go version,
`pkg-config` output for every FFmpeg library, gcc path and version, relevant
`CGO_*` / `PKG_CONFIG_PATH` env vars) into `mediamolder-build.log`.  Share
that file when reporting a build failure — it contains everything needed to
reproduce the problem without back-and-forth.

```bash
make build-debug                   # headless build + log
make build-gui-debug               # GUI build + log
make build-gui-debug BUILD_TAGS=ffstatic   # static GUI build + log
make build-debug LOG=/tmp/my.log   # write log to a custom path
```

Windows users run the equivalent commands by hand from PowerShell — see
[build/windows.md](build/windows.md).

## Optional built-in nodes & their prerequisites

Most processors compile into every build. A few heavyweight or service-backed
[go_processor nodes](go-processor-nodes.md) are **opt-in** — either behind a
build tag (so they don't add a cgo dependency for people who don't need them) or
requiring an external runtime. Each has prerequisites you must install **before**
building or running, including environment variables.

| Node | Build tag | External dependency | Get it from | Runtime env / config |
| --- | --- | --- | --- | --- |
| `whisper_stt` (speech-to-text) | `with_whisper` | whisper.cpp → `libwhisper` | [`ggml-org/whisper.cpp`](https://github.com/ggml-org/whisper.cpp), built with CMake | `PKG_CONFIG_PATH` (locate `whisper.pc`); `model` param → ggml model path; `WHISPER_TEST_MODEL` for tests |
| `yolo_v8` (object detection) | `with_onnx` | ONNX Runtime shared lib | [onnxruntime releases](https://github.com/microsoft/onnxruntime/releases) or a package manager | `ONNXRUNTIME_SHARED_LIBRARY_PATH` (or `ort_lib` param); `model` param → `.onnx` path |
| `face_detect` (face detection + embeddings) | `with_onnx` | ONNX Runtime shared lib + the two face models | onnxruntime as above; models via `scripts/fetch-face-models.sh` | `ONNXRUNTIME_SHARED_LIBRARY_PATH`; `MEDIAMOLDER_FACE_MODELS` (dir) or `--models-dir` |
| `vidi_analyzer` (multimodal) | *(none)* | Vidi 2.5 Python inference service | [`bytedance/vidi`](https://github.com/bytedance/vidi) | `service_url` param → the running service |
| `twelvelabs_*` (cloud understanding) | *(none)* | TwelveLabs cloud API | [twelvelabs.io](https://twelvelabs.io) | `TWELVELABS_API_KEY` env (or `api_key` param / `~/.config/mediamolder/twelvelabs.json`) |

> **`whisper_stt` uses whisper.cpp, not the OpenAI Python `whisper` package.**
> The node is a cgo binding to `libwhisper`; `github.com/openai/whisper` is the
> PyTorch implementation and does **not** produce the library/headers this build
> links against.

### Example: enable `whisper_stt`

The whisper.cpp steps run from any workspace dir; the `make` steps run from the
**MediaMolder repo root** (`make build-whisper` only exists in its Makefile).

```bash
# 1. Clone + build whisper.cpp (produces libwhisper + whisper.pc on install)
git clone https://github.com/ggml-org/whisper.cpp
cmake -S whisper.cpp -B whisper.cpp/build
cmake --build whisper.cpp/build -j
sudo cmake --install whisper.cpp/build     # whisper.pc's prefix is /usr/local (needs sudo)
# no-sudo: reconfigure -DCMAKE_INSTALL_PREFIX="$HOME/.local", rebuild, install,
#   then export PKG_CONFIG_PATH="$HOME/.local/lib/pkgconfig:$PKG_CONFIG_PATH"

# 2. Fetch a model (you supply this — MediaMolder ships none) and note its path
./whisper.cpp/models/download-ggml-model.sh base.en
MODEL="$PWD/whisper.cpp/models/ggml-base.en.bin"

# 3. Build MediaMolder with the node compiled in — FROM THE MEDIAMOLDER REPO ROOT.
#    whisper.cpp's libs use @rpath, so build-whisper embeds
#    -Wl,-rpath,WHISPER_PREFIX/lib (default /usr/local).
cd /path/to/mediamolder
make build-whisper                          # custom prefix → make build-whisper WHISPER_PREFIX="$HOME/.local"

# 4. Run the gated tests against the model
WHISPER_TEST_MODEL="$MODEL" make test-whisper
```

### Stacking node tags with `EXTRA_TAGS`

Build tags stack, so one binary can carry several optional nodes. The
`build-whisper` / `build-gui-whisper` targets accept `EXTRA_TAGS`, **appended**
to their built-in tags — **comma-separated, no spaces**:

```bash
# build-gui-whisper already implies ffstatic,with_whisper.
make build-gui-whisper EXTRA_TAGS=with_onnx                 # + yolo_v8 + face_detect
make build-gui-whisper EXTRA_TAGS=with_onnx,whisperstatic   # + ONNX nodes + static libwhisper
make build-whisper      EXTRA_TAGS=with_onnx                 # headless, same nodes
```

`with_onnx` is one tag that enables **all** ONNX nodes (`yolo_v8`, `face_detect`,
and the `face-detect` CLI). A plain `go build` ignores `EXTRA_TAGS` — list the
tags yourself: `go build -tags=ffstatic,with_whisper,with_onnx ./cmd/mediamolder`.

### Models — download at run time, not build time

Models are **not** shipped and are loaded at run time. Build with the node's tag,
then download the model(s) and point an env var / param at them before running:

| Node | Download | Point at it with |
| --- | --- | --- |
| `whisper_stt` | a ggml/gguf model (`download-ggml-model.sh`) | `model` param |
| `face_detect` / `face-detect` | `scripts/fetch-face-models.sh` (SHA-256-verified) | `MEDIAMOLDER_FACE_MODELS` or `--models-dir` |
| `yolo_v8` | a YOLOv8 `.onnx` + labels | `model` + `labels_file` params |

Models can be large and (for the face detector) copyleft-licensed, so they are
**never committed**; `fetch-face-models.sh` defaults to the git-ignored
`testdata/face_models/`. See **Notes and licensing** below.

Full per-node setup lives in the feature guides:
[Whisper](whisper-stt-guide.md), [YOLOv8](yolov8-guide.md),
[Face Detection](face-detection-guide.md), [Vidi 2.5](vidi-guide.md),
[TwelveLabs](twelvelabs.md).

**Combining nodes.** The build tags stack — enable several opt-in nodes at once
by combining them, e.g. `-tags=ffstatic,with_whisper,with_onnx` (static FFmpeg +
`whisper_stt` + `yolo_v8`). For a GUI single-binary, append the extra tags to
`build-gui-whisper`:

```bash
make build-gui-whisper EXTRA_TAGS=with_onnx
```

ONNX Runtime is loaded at runtime, so `with_onnx` adds nothing to the build
besides the tag; you only need the onnxruntime shared library (and
`ONNXRUNTIME_SHARED_LIBRARY_PATH`) to actually *run* a `yolo_v8` node.

## Frontend embedding model

The GUI is a React/Vite app. `make frontend-build` compiles
`frontend/src/**` into `frontend/dist/`, then copies the result into
`internal/gui/dist/` so Go's `//go:embed` directive bakes the assets into
the final binary at compile time.

This means:

- After **any** edit to `frontend/src/**`, you must re-run the frontend
  build *and* re-link the Go binary.
- `make build-gui` does both in one step.
- For tight inner loops, run `make frontend-dev` + `make gui-dev` in two
  terminals — Vite serves the assets live and the Go backend proxies to
  it. No rebuild needed for frontend edits.

## Rebuild cheatsheet

| You changed … | macOS / Linux | Windows |
| --- | --- | --- |
| Go code only | `make build` (or `make build-gui` to refresh embed too) | `go build -o mediamolder.exe .\cmd\mediamolder\` |
| `frontend/src/**` | `make build-gui` | Re-run frontend build, copy into `internal\gui\dist\`, then `go build` |
| `frontend/package.json` | `make frontend-install && make build-gui` | `npm install` then frontend build + `go build` |
| Switched FFmpeg source tree | Re-run the same target you used last time (cgo will re-link) | Same |

## Run the tests

```bash
make test                # default (shared FFmpeg)
make test-static         # static FFmpeg via ../ffmpeg
go test ./pipeline/...   # narrow to a package
```

The full suite includes `TestSchemaSyncWithGoStructs` (gates the
`pipeline.*` ↔ `schema/v*.json` ↔ `frontend/src/lib/jobTypes.ts` invariant)
and `TestExamplesRun/*` (end-to-end example configs from
`testdata/examples/`).

## Notes and licensing

- The MediaMolder project does **not** distribute pre-compiled binaries
  because of patent licensing concerns surrounding video codecs (H.264,
  H.265, etc.). When you build from source, you are responsible for
  understanding and obtaining a licence for any patents that apply to the
  codecs you enable.
- The minimum supported FFmpeg version is **8.1**. CI runs against the
  latest 8.1.x patch release.
- The default build uses `pkg-config`, so any FFmpeg installation that
  publishes `.pc` files (Homebrew, apt, dnf, MSYS2) will work without
  additional configuration.
- **ML models are never shipped or committed.** They are downloaded by the
  operator and loaded as data at run time. The default face detector
  (YOLOv8-face) is **AGPL-3.0** and the embedder (SFace) is Apache-2.0; both are
  loaded as data (never linked), so they don't affect the binary's licence, but
  do **not** commit them to the repository. `scripts/fetch-face-models.sh`
  fetches into the git-ignored `testdata/face_models/` and SHA-256-verifies each
  file.
