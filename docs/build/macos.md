# Build MediaMolder on macOS

This guide covers building the `mediamolder` binary (CLI + library) and the
embedded GUI on macOS, plus the day-to-day rebuild loops after editing code.

See also: [Cross-platform overview](../build_and_packaging.md) ·
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

## 6. Run the tests

```bash
make test                       # Option A — default FFmpeg
make test-static                # Option B2 — static FFmpeg
go test ./pipeline/...          # narrow to one package
```

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
