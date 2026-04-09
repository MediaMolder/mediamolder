# Build & Packaging

*Extracted from the [Project Specification](spec_v3.md).*

## Requirements

- Go 1.25.0 or later.
- FFmpeg 8.1+ development libraries (libavcodec, libavformat, libavfilter, libavutil, libswscale, libswresample).
- `pkg-config` for locating FFmpeg libraries (default build path).

## Build from Source (macOS)

```bash
# 1. Install FFmpeg headers + libraries (Homebrew)
brew install ffmpeg

# 2. Clone the repository
git clone https://github.com/tmvn/mediamolder.git
cd mediamolder

# 3. Build
go build ./cmd/mediamolder/

# 4. Install to $GOPATH/bin
go install ./cmd/mediamolder/

# 5. Verify
mediamolder version
```

## Build from Source (Linux — Debian/Ubuntu)

```bash
# 1. Install FFmpeg dev libraries
sudo apt-get update
sudo apt-get install -y libavcodec-dev libavformat-dev libavfilter-dev \
    libavutil-dev libswscale-dev libswresample-dev pkg-config

# 2. Clone and build
git clone https://github.com/tmvn/mediamolder.git
cd mediamolder
go build ./cmd/mediamolder/
go install ./cmd/mediamolder/
```

## Build from Source (Linux — Fedora/RHEL)

```bash
# 1. Install FFmpeg dev libraries (enable RPM Fusion)
sudo dnf install -y ffmpeg-devel pkg-config

# 2. Clone and build
git clone https://github.com/tmvn/mediamolder.git
cd mediamolder
go build ./cmd/mediamolder/
go install ./cmd/mediamolder/
```

## Build with Custom FFmpeg (ffstatic tag)

If you have FFmpeg built from source at a custom path:

```bash
# Point to your FFmpeg source tree
export FFMPEG_SRC=/path/to/ffmpeg

# Build with the ffstatic tag
CGO_CFLAGS="-I${FFMPEG_SRC}" \
CGO_LDFLAGS="-L${FFMPEG_SRC}/libavcodec -L${FFMPEG_SRC}/libavformat -L${FFMPEG_SRC}/libavfilter -L${FFMPEG_SRC}/libavutil -L${FFMPEG_SRC}/libswscale -L${FFMPEG_SRC}/libswresample" \
go build -tags ffstatic ./cmd/mediamolder/
```

## Build from Source (Windows)

Windows builds require MSYS2 or WSL with FFmpeg development files:

```bash
# MSYS2 approach
pacman -S mingw-w64-x86_64-ffmpeg mingw-w64-x86_64-pkg-config

# Then build normally
go build ./cmd/mediamolder/
```

## Run Tests

```bash
go test ./...
```

## Notes

- No pre-compiled binaries are distributed (patent license restrictions on compiled codec output).
- Docker images deferred to public release phase.
- Minimum FFmpeg version: 8.1. CI tests against FFmpeg 8.1.x (latest patch release).
- pkg-config based build for custom libav* installations.
