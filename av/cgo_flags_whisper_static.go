// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build with_whisper && ffstatic

package av

// Static linking against a local whisper.cpp source tree built with CMake,
// placed as a sibling of the mediamolder checkout (../../whisper.cpp), mirroring
// the FFmpeg/x264 layout used by cgo_flags_static.go. Build with:
//   git clone https://github.com/ggml-org/whisper.cpp ../../whisper.cpp
//   cmake -S ../../whisper.cpp -B ../../whisper.cpp/build && \
//     cmake --build ../../whisper.cpp/build -j
//   CGO_LDFLAGS_ALLOW='.*' go build -tags=ffstatic,with_whisper ./...

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
