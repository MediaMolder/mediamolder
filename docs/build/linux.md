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
./configure --disable-shared --enable-static --enable-gpl \
            --enable-libx264 --enable-libx265 \
            --disable-doc --disable-programs
make -j$(nproc)
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
