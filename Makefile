.PHONY: build build-static test test-static lint bench bench-static clean \
        frontend-install frontend-dev frontend-build gui gui-dev build-gui

# Default: use system FFmpeg via pkg-config (no special flags needed).
build:
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
build-gui: frontend-build
	go build -o mediamolder ./cmd/mediamolder
