// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build with_whisper && !whisperstatic

package av

// Dynamic linking against a system-installed whisper.cpp (libwhisper).
// Enabled only with the "with_whisper" build tag; without it the whisper.cpp
// wrapper and the whisper_stt processor are not compiled, so a plain build
// needs neither the library nor a model.
//
// whisper.cpp installs a pkg-config file (whisper.pc) when built with
//   cmake -B build && cmake --build build -j && cmake --install build
// If it is in a non-standard prefix, point pkg-config at it, e.g.
//   PKG_CONFIG_PATH=/opt/whisper/lib/pkgconfig go build -tags=with_whisper ./...
// For a non-pkg-config install, override flags via CGO_CFLAGS / CGO_LDFLAGS.

// #cgo pkg-config: whisper
//
// #include <whisper.h>
import "C"
