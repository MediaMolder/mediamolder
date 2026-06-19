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

## Make targets (Unix)

The `Makefile` ships these top-level targets:

| Target | Output | FFmpeg |
| --- | --- | --- |
| `make build` | `go build ./...` | Shared via `pkg-config` |
| `make build-static` | `go build -tags=ffstatic ./...` | Static via `../ffmpeg` |
| `make build-gui` | `mediamolder` with embedded GUI | Shared |
| `make build-gui-static` | `mediamolder` with embedded GUI | Static |
| `make build-whisper` / `make test-whisper` | `go build -tags=with_whisper ./...` — opt-in `whisper_stt` (needs libwhisper) | Shared |
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
| `vidi_analyzer` (multimodal) | *(none)* | Vidi 2.5 Python inference service | [`bytedance/vidi`](https://github.com/bytedance/vidi) | `service_url` param → the running service |
| `twelvelabs_*` (cloud understanding) | *(none)* | TwelveLabs cloud API | [twelvelabs.io](https://twelvelabs.io) | `TWELVELABS_API_KEY` env (or `api_key` param / `~/.config/mediamolder/twelvelabs.json`) |

> **`whisper_stt` uses whisper.cpp, not the OpenAI Python `whisper` package.**
> The node is a cgo binding to `libwhisper`; `github.com/openai/whisper` is the
> PyTorch implementation and does **not** produce the library/headers this build
> links against.

### Example: enable `whisper_stt`

```bash
# 1. Clone + build whisper.cpp (produces libwhisper + whisper.pc on install)
git clone https://github.com/ggml-org/whisper.cpp
cmake -S whisper.cpp -B whisper.cpp/build
cmake --build whisper.cpp/build -j
cmake --install whisper.cpp/build          # installs whisper.pc for pkg-config
# custom prefix? export PKG_CONFIG_PATH=<prefix>/lib/pkgconfig

# 2. Build MediaMolder with the node compiled in
make build-whisper                          # = go build -tags=with_whisper ./...
# static FFmpeg + a local sibling tree at ../../whisper.cpp instead:
#   CGO_LDFLAGS_ALLOW='.*' go build -tags=ffstatic,with_whisper ./...

# 3. Fetch a model (you supply this — MediaMolder ships none)
./whisper.cpp/models/download-ggml-model.sh base.en

# 4. Point the node at the model; run the gated tests against it
export WHISPER_TEST_MODEL=$PWD/whisper.cpp/models/ggml-base.en.bin
make test-whisper
```

Full per-node setup lives in the feature guides:
[Whisper](whisper-stt-guide.md), [YOLOv8](yolov8-guide.md),
[Vidi 2.5](vidi-guide.md), [TwelveLabs](twelvelabs.md).

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
