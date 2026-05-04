.PHONY: build build-static test test-static lint bench bench-static clean \
        frontend-install frontend-dev frontend-build gui gui-dev build-gui build-gui-static \
        check-deps

# Default: use system FFmpeg via pkg-config (no special flags needed).
build: check-deps
	go build ./...

# Static: link against a local FFmpeg source tree (set FFMPEG_SRC to override).
build-static:
	go build -tags=ffstatic ./...

test:
	go test ./...

test-static:
	go test -tags=ffstatic ./...

lint:
	golangci-lint run ./...

bench:
	go test -bench=. -benchmem ./...

bench-static:
	go test -tags=ffstatic -bench=. -benchmem ./...

clean:
	go clean ./...
	rm -rf frontend/dist internal/gui/dist/assets internal/gui/dist/*.js internal/gui/dist/*.css

# Check CGO prerequisites (GCC + FFmpeg ≥ 8.1 / libavcodec ≥ 61).
# Runs before any Go build that depends on the av/ CGO package so that
# users get a clear error instead of a confusing "undefined: cmdGUI" cascade.
check-deps:
	@command -v gcc >/dev/null 2>&1 || \
		{ echo "error: gcc not found — install build-essential (Debian/Ubuntu) or gcc (Fedora/RHEL)"; exit 1; }
	@pkg-config --exists libavcodec libavformat libavfilter libavutil libswscale libswresample 2>/dev/null || \
		{ echo "error: FFmpeg development headers not found."; \
		  echo "  Debian/Ubuntu: sudo apt-get install libavcodec-dev libavformat-dev libavfilter-dev libavutil-dev libswscale-dev libswresample-dev"; \
		  echo "  Fedora/RHEL:   sudo dnf install ffmpeg-devel"; \
		  echo "  macOS:         brew install ffmpeg"; \
		  exit 1; }
	@avcodec_ver=$$(pkg-config --modversion libavcodec 2>/dev/null); \
	 major=$$(echo "$$avcodec_ver" | cut -d. -f1); \
	 if [ -n "$$major" ] && [ "$$major" -lt 61 ]; then \
	   echo "error: libavcodec $$avcodec_ver is too old — MediaMolder requires FFmpeg ≥ 8.1 (libavcodec ≥ 61)."; \
	   echo "  Install a newer FFmpeg or use 'make build-gui-static' with a local FFmpeg build."; \
	   exit 1; \
	 fi

# ─── GUI / Frontend ─────────────────────────────────────────────────────────

# Install npm dependencies (run once, or after package.json changes).
frontend-install:
	cd frontend && npm install

# Vite dev server with hot reload, proxying /api to localhost:8080.
frontend-dev:
	cd frontend && npm run dev

# Production build of the React app, then copy artifacts into the embed dir
# (internal/gui/dist) consumed by //go:embed.
frontend-build:
	cd frontend && npm run build
	rm -rf internal/gui/dist
	mkdir -p internal/gui/dist
	cp -R frontend/dist/. internal/gui/dist/

# Run the gui subcommand (assumes a previous frontend-build, or use gui-dev).
gui:
	go run ./cmd/mediamolder gui

# Run the gui backend in dev mode (no embedded assets; pair with `make frontend-dev`).
gui-dev:
	go run ./cmd/mediamolder gui --dev --no-open

# Full single-binary build with embedded production frontend.
build-gui: check-deps frontend-build
	go build -o mediamolder ./cmd/mediamolder

# Same as build-gui but linking against a local FFmpeg source tree.
build-gui-static: frontend-build
	go build -tags=ffstatic -o mediamolder ./cmd/mediamolder
