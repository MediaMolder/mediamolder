// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build with_whisper && whisperstatic

package av

// Static linking against a local whisper.cpp source tree built with CMake,
// placed as a sibling of the mediamolder checkout (../../whisper.cpp), mirroring
// the FFmpeg/x264 layout used by cgo_flags_static.go.
//
// Selected by the dedicated "whisperstatic" tag — INDEPENDENT of "ffstatic"
// (which only governs FFmpeg). That lets you mix static FFmpeg with a dynamic
// libwhisper (ffstatic,with_whisper) or link both statically
// (ffstatic,with_whisper,whisperstatic). Requires a STATIC whisper.cpp build
// (CMake -DBUILD_SHARED_LIBS=OFF, which produces the .a archives below):
//   git clone https://github.com/ggml-org/whisper.cpp ../../whisper.cpp
//   cmake -S ../../whisper.cpp -B ../../whisper.cpp/build -DBUILD_SHARED_LIBS=OFF && \
//     cmake --build ../../whisper.cpp/build -j
//   CGO_LDFLAGS_ALLOW='.*' go build -tags=with_whisper,whisperstatic ./...

// #cgo CFLAGS: -I${SRCDIR}/../../whisper.cpp/include -I${SRCDIR}/../../whisper.cpp/ggml/include
// #cgo LDFLAGS: -L${SRCDIR}/../../whisper.cpp/build/src -lwhisper
// #cgo LDFLAGS: -L${SRCDIR}/../../whisper.cpp/build/ggml/src -lggml -lggml-base -lggml-cpu
// #cgo darwin LDFLAGS: -L${SRCDIR}/../../whisper.cpp/build/ggml/src/ggml-blas -lggml-blas
// #cgo darwin LDFLAGS: -L${SRCDIR}/../../whisper.cpp/build/ggml/src/ggml-metal -lggml-metal
// #cgo darwin LDFLAGS: -framework Accelerate -framework Metal -framework MetalKit -framework Foundation -framework CoreFoundation
// #cgo linux LDFLAGS: -lm -lpthread -lstdc++
//
// #include <whisper.h>
import "C"
