.PHONY: build test lint bench clean

FFMPEG_SRC ?= $(HOME)/ffmpeg
CGO_CFLAGS  := -I$(FFMPEG_SRC)
CGO_LDFLAGS := -L$(FFMPEG_SRC)/libavcodec \
               -L$(FFMPEG_SRC)/libavformat \
               -L$(FFMPEG_SRC)/libavfilter \
               -L$(FFMPEG_SRC)/libavutil \
               -L$(FFMPEG_SRC)/libswscale \
               -L$(FFMPEG_SRC)/libswresample \
               -lavcodec -lavformat -lavfilter -lavutil -lswscale -lswresample

export CGO_CFLAGS
export CGO_LDFLAGS

build:
	go build ./...

test:
	go test ./...

lint:
	golangci-lint run ./...

bench:
	go test -bench=. -benchmem ./...

clean:
	go clean ./...
