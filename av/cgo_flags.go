// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !ffstatic

package av

// NOTE (macOS): When using the normal (pkg-config) build, Apple's ld64
// commonly prints:
//     ld: warning: ignoring duplicate libraries: '-lavutil', '-lswscale'
// This is harmless — the linker just drops the duplicates. The project's
// Makefile sets CGO_LDFLAGS=-Wl,-no_warn_duplicate_libraries on Darwin
// (via the environment, which bypasses cgo's LDFLAGS restrictions) so that
// `make build` (and `make build-gui` etc.) produce a clean build.
// Plain `go build ./cmd/mediamolder` will still emit the warning on macOS.
//
// NOTE (Linux): libm must be linked explicitly. FFmpeg inline math (swscale,
// av_image_*) and the Laplacian-variance focus measure reference lround/etc;
// on Linux these come from libm.so, whereas macOS auto-links libm via
// libSystem. Hence the `#cgo linux LDFLAGS: -lm` below.

// #cgo pkg-config: libavcodec libavformat libavdevice libavfilter libavutil libswscale libswresample
// #cgo linux LDFLAGS: -lm
//
// #include "libavcodec/avcodec.h"
// #include "libavformat/avformat.h"
// #include "libavdevice/avdevice.h"
// #include "libavfilter/avfilter.h"
// #include "libavutil/avutil.h"
// #include "libswscale/swscale.h"
// #include "libswresample/swresample.h"
import "C"
