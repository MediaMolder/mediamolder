.PHONY: build build-static test test-static lint bench bench-static clean

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
