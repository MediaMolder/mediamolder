# Build MediaMolder on Linux

This guide covers building the `mediamolder` binary (CLI + library) and the
embedded GUI on Debian/Ubuntu and Fedora/RHEL, plus the day-to-day rebuild
loops after editing code.

See also: [Cross-platform overview](../build_and_packaging.md) ·
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

```bash
git clone https://github.com/MediaMolder/mediamolder.git
cd mediamolder
```

## 3. Build the CLI / library (default FFmpeg)

Uses the system FFmpeg via `pkg-config`:

```bash
make build                  # equivalent to: go build ./...
go build -o mediamolder ./cmd/mediamolder
./mediamolder version
```

## 4. Build with a custom FFmpeg

### 4a. Custom shared FFmpeg (still uses pkg-config)

Point `pkg-config` at your custom build's `.pc` files:

```bash
export PKG_CONFIG_PATH=/path/to/ffmpeg/build/lib/pkgconfig:$PKG_CONFIG_PATH
export LD_LIBRARY_PATH=/path/to/ffmpeg/build/lib:$LD_LIBRARY_PATH
go build -o mediamolder ./cmd/mediamolder
```

### 4b. Static FFmpeg from source (`ffstatic` tag)

Lay the FFmpeg source tree out as a sibling of `mediamolder/`:

```text
parent/
├── ffmpeg/         ← compiled with ./configure && make
└── mediamolder/
```

Compile FFmpeg statically (do this once per FFmpeg upgrade):

```bash
cd ../ffmpeg
./configure --disable-shared --enable-static --enable-gpl \
            --enable-libx264 --enable-libx265 \
            --disable-doc --disable-programs
make -j$(nproc)
cd ../mediamolder
```

Build mediamolder against it:

```bash
make build-static                                  # CLI/library
# or
go build -tags=ffstatic -o mediamolder ./cmd/mediamolder
```

To override the location of the FFmpeg source tree (default is `../ffmpeg`):

```bash
export FFMPEG_SRC=/path/to/ffmpeg
CGO_CFLAGS="-I${FFMPEG_SRC}" \
CGO_LDFLAGS="-L${FFMPEG_SRC}/libavcodec -L${FFMPEG_SRC}/libavformat \
             -L${FFMPEG_SRC}/libavfilter -L${FFMPEG_SRC}/libavutil \
             -L${FFMPEG_SRC}/libswscale -L${FFMPEG_SRC}/libswresample" \
go build -tags=ffstatic -o mediamolder ./cmd/mediamolder
```

## 5. Build the GUI

The GUI is a React/Vite app embedded into the Go binary via `//go:embed`.

```bash
# One-time: install JS dependencies
cd frontend && npm ci && cd ..

# Build everything (frontend + Go binary with embedded assets)
make build-gui                  # default FFmpeg
# or
make build-gui-static           # static FFmpeg via ../ffmpeg

./mediamolder gui               # opens the GUI in your browser
```

`make build-gui` runs `frontend-build` (compiles `frontend/dist/`, copies
into `internal/gui/dist/`) then `go build -o mediamolder ./cmd/mediamolder`.

## 6. Rebuild loops after code changes

Pick the shortest sequence that covers your edit:

| You changed … | Run |
| --- | --- |
| Go code only (anything outside `frontend/`) | `make build` (or `make build-gui` to also re-embed) |
| Frontend code (`frontend/src/**`) | `make build-gui` (rebuilds React + re-embeds + re-links binary) |
| `frontend/package.json` (added/upgraded a JS package) | `cd frontend && npm install && cd ..` then `make build-gui` |
| Nothing — you just want a fresh binary | `make build` |

Step 4 (`go build`) is required after frontend edits because the production
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

## 7. Run the tests

```bash
make test                       # default FFmpeg
make test-static                # static FFmpeg
go test ./pipeline/...          # narrow to one package
```

## Troubleshooting

- **`Package libavcodec was not found`** — install `libavcodec-dev` (Debian)
  or `ffmpeg-devel` (Fedora). Check `pkg-config --modversion libavcodec`.
- **Distro FFmpeg too old** — minimum supported is 8.1. Build FFmpeg from
  source (see §4b) and link via `ffstatic` or a custom `PKG_CONFIG_PATH`.
- **`/usr/bin/ld: cannot find -lavcodec` with `ffstatic`** — the
  `../ffmpeg/libav*/*.a` files don't exist. Re-run `make` in the FFmpeg
  source tree.
- **Runtime: `error while loading shared libraries: libavcodec.so.X`** —
  your custom shared FFmpeg isn't on `LD_LIBRARY_PATH`. Either set it or
  use `ffstatic` for a self-contained binary.
