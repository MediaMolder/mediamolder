.PHONY: build build-static test test-static lint bench bench-static clean \
        frontend-install frontend-dev frontend-build gui gui-dev build-gui build-gui-static \
        check-deps build-debug build-gui-debug build-whisper test-whisper build-gui-whisper

# Detect macOS: Apple ld warns about duplicate -l flags when two CGO packages
# (av and PySceneDetect/internal) both link -lavutil and -lswscale. Pass
# -Wl,-no_warn_duplicate_libraries via the environment (bypasses CGO security
# restrictions on #cgo LDFLAGS). No-op on Linux (GNU ld silently deduplicates).
UNAME_S := $(shell uname -s)
ifeq ($(UNAME_S),Darwin)
CGO_LDFLAGS_NODUP := -Wl,-no_warn_duplicate_libraries
endif

# Default: use system FFmpeg via pkg-config (no special flags needed).
build: check-deps
	go build ./...

# Static: link against a local FFmpeg source tree (set FFMPEG_SRC to override).
build-static:
	CGO_LDFLAGS_ALLOW='.*' CGO_LDFLAGS='$(CGO_LDFLAGS_NODUP)' \
	  go build -tags=ffstatic ./...

test:
	go test ./...

test-static:
	CGO_LDFLAGS_ALLOW='.*' CGO_LDFLAGS='$(CGO_LDFLAGS_NODUP)' \
	  go test -tags=ffstatic ./...

# Whisper speech-to-text (whisper_stt node). Requires libwhisper: a system
# install discoverable via pkg-config (whisper.pc). whisper.cpp's dylibs/.so
# use @rpath, so we embed an rpath to WHISPER_PREFIX/lib for runtime lookup.
# WHISPER_PREFIX must match the prefix libwhisper was installed under (the
# whisper.pc default is /usr/local; override e.g. WHISPER_PREFIX=$HOME/.local).
# We ship neither the library nor any model — see docs/whisper-stt-guide.md.
#
# The rpath is passed via the Go linker's -extldflags (applied once to the
# final link) rather than CGO_LDFLAGS (recorded per-cgo-package, which would
# emit "duplicate -rpath ... ignored" warnings on the multi-cgo binary).
# EXTRA_TAGS appends more opt-in node tags to any whisper target, so one binary
# can carry several nodes, e.g.  make build-whisper EXTRA_TAGS=with_onnx.
WHISPER_PREFIX ?= /usr/local
comma := ,
build-whisper:
	CGO_LDFLAGS_ALLOW='.*' CGO_LDFLAGS='$(CGO_LDFLAGS_NODUP)' \
	  go build -tags=with_whisper$(if $(EXTRA_TAGS),$(comma)$(EXTRA_TAGS)) -ldflags='-extldflags "-Wl,-rpath,$(WHISPER_PREFIX)/lib"' ./...

# Set WHISPER_TEST_MODEL to a ggml model to exercise the integration tests;
# without it the tagged tests skip.
test-whisper:
	CGO_LDFLAGS_ALLOW='.*' CGO_LDFLAGS='$(CGO_LDFLAGS_NODUP)' \
	  go test -tags=with_whisper -ldflags='-extldflags "-Wl,-rpath,$(WHISPER_PREFIX)/lib"' ./av/... ./processors/...

lint:
	golangci-lint run ./...

bench:
	go test -bench=. -benchmem ./...

bench-static:
	CGO_LDFLAGS_ALLOW='.*' CGO_LDFLAGS='$(CGO_LDFLAGS_NODUP)' \
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

# ─── Debug builds (capture full environment + verbose compiler output) ───────
#
# Usage:
#   make build-debug          # headless binary, writes mediamolder-build.log
#   make build-gui-debug      # GUI binary (also runs frontend build), writes mediamolder-build.log
#
# The log file is safe to share: it contains compiler flags, library
# versions, and verbose linker output but no passwords or private keys.
# Upload it when reporting a build failure.

LOG ?= mediamolder-build.log

# Internal helper — collects environment info into $(LOG), then runs the
# actual go build with CGO_CFLAGS_ALLOW / CGO_LDFLAGS_ALLOW verbose output.
# Callers set BUILD_TAGS and BUILD_TARGET before including this recipe via
# the two public debug targets below.
_debug-preamble:
	@printf '=== mediamolder build debug log ===\n' > $(LOG)
	@printf 'date:         %s\n' "$$(date -u)" >> $(LOG)
	@printf 'uname:        %s\n' "$$(uname -a)" >> $(LOG)
	@printf 'go version:   %s\n' "$$(go version 2>&1)" >> $(LOG)
	@printf 'go env:\n' >> $(LOG)
	@go env >> $(LOG) 2>&1
	@printf '\ngcc version:  %s\n' "$$(gcc --version 2>&1 | head -1)" >> $(LOG)
	@printf 'cc path:      %s\n' "$$(command -v gcc 2>/dev/null || echo not found)" >> $(LOG)
	@printf '\npkg-config:\n' >> $(LOG)
	@for lib in libavcodec libavformat libavfilter libavutil libswscale libswresample; do \
	  ver=$$(pkg-config --modversion $$lib 2>/dev/null || echo NOT FOUND); \
	  cflags=$$(pkg-config --cflags $$lib 2>/dev/null || true); \
	  libs=$$(pkg-config --libs $$lib 2>/dev/null || true); \
	  printf '  %-22s %s\n    cflags: %s\n    libs:   %s\n' \
	    "$$lib" "$$ver" "$$cflags" "$$libs" >> $(LOG); \
	done
	@printf '\nnode version: %s\n' "$$(node --version 2>/dev/null || echo not found)" >> $(LOG)
	@printf 'npm version:  %s\n' "$$(npm --version 2>/dev/null || echo not found)" >> $(LOG)
	@printf '\nPKG_CONFIG_PATH: %s\n' "$${PKG_CONFIG_PATH:-<empty>}" >> $(LOG)
	@printf 'CGO_CFLAGS:      %s\n' "$${CGO_CFLAGS:-<empty>}" >> $(LOG)
	@printf 'CGO_LDFLAGS:     %s\n' "$${CGO_LDFLAGS:-<empty>}" >> $(LOG)
	@printf 'FFMPEG_SRC:      %s\n' "$${FFMPEG_SRC:-<empty>}" >> $(LOG)
	@printf '\n=== go build output ===\n' >> $(LOG)

build-debug: _debug-preamble
	@echo "Debug build log → $(LOG)"
	CGO_CFLAGS_ALLOW='.*' CGO_LDFLAGS_ALLOW='.*' \
	  go build -v -x $(if $(BUILD_TAGS),-tags=$(BUILD_TAGS)) \
	    -o mediamolder ./cmd/mediamolder >> $(LOG) 2>&1 && \
	  echo "Build succeeded." >> $(LOG) || \
	  { echo "Build FAILED — see $(LOG) for details"; exit 1; }
	@echo "Done. Share $(LOG) when reporting a build issue."

build-gui-debug: _debug-preamble
	@echo "Debug build log → $(LOG)"
	@printf '=== frontend build ===\n' >> $(LOG)
	cd frontend && npm run build >> ../$(LOG) 2>&1 || \
	  { echo "Frontend build FAILED — see $(LOG)"; exit 1; }
	@rm -rf internal/gui/dist && mkdir -p internal/gui/dist
	@cp -R frontend/dist/. internal/gui/dist/
	@printf '\n=== go build output ===\n' >> $(LOG)
	CGO_CFLAGS_ALLOW='.*' CGO_LDFLAGS_ALLOW='.*' \
	  go build -v -x $(if $(BUILD_TAGS),-tags=$(BUILD_TAGS)) \
	    -o mediamolder ./cmd/mediamolder >> $(LOG) 2>&1 && \
	  echo "Build succeeded." >> $(LOG) || \
	  { echo "Build FAILED — see $(LOG) for details"; exit 1; }
	@echo "Done. Share $(LOG) when reporting a build issue."

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
# NOTE: this does NOT include whisper_stt — use build-gui-whisper for that.
build-gui-static: frontend-build
	CGO_LDFLAGS_ALLOW='.*' CGO_LDFLAGS='$(CGO_LDFLAGS_NODUP)' \
	  go build -tags=ffstatic -o mediamolder ./cmd/mediamolder

# build-gui-static + the whisper_stt node: static FFmpeg plus a dynamic
# libwhisper (via pkg-config). Requires libwhisper installed (see
# docs/whisper-stt-guide.md); override WHISPER_PREFIX if it is not under
# /usr/local. Append more opt-in node tags with EXTRA_TAGS, e.g.
#   make build-gui-whisper EXTRA_TAGS=with_onnx      # also compile in yolo_v8
# (ONNX Runtime is loaded at runtime, so it is not needed to build — only to
# run a yolo_v8 node, via ONNXRUNTIME_SHARED_LIBRARY_PATH.) The "whisperstatic"
# tag (independent of ffstatic) links libwhisper statically instead — that needs
# a static whisper.cpp build.
build-gui-whisper: frontend-build
	CGO_LDFLAGS_ALLOW='.*' CGO_LDFLAGS='$(CGO_LDFLAGS_NODUP)' \
	  go build -tags=ffstatic,with_whisper$(if $(EXTRA_TAGS),$(comma)$(EXTRA_TAGS)) \
	  -ldflags='-extldflags "-Wl,-rpath,$(WHISPER_PREFIX)/lib"' \
	  -o mediamolder ./cmd/mediamolder
