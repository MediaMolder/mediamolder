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
| `make test` / `make test-static` | Run the test suite | Shared / static |
| `make frontend-install` | `npm install` in `frontend/` | — |
| `make frontend-build` | Build React app + copy into `internal/gui/dist/` | — |
| `make frontend-dev` | Vite dev server (hot reload) | — |
| `make gui-dev` | Go backend in dev mode (proxies to Vite) | Shared |
| `make clean` | `go clean` + remove `frontend/dist`, `internal/gui/dist` | — |

Windows users run the equivalent commands by hand from PowerShell — see
[build/windows.md](build/windows.md).

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
