// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build with_libraw

package raw

// Static linking against the bundled LibRaw built by scripts/bundle-libraw.sh into
// third_party/libraw/{include,lib} (gitignored; we ship no binaries — see Design Principles).
// LibRaw is C++ with a C API, so we pull in the C++ runtime; zlib (a system library) is used for
// deflate-compressed DNG. The bundle is built with jpeg/jasper/lcms disabled — a faithful sRGB
// develop needs none of them — keeping the link self-contained. Mirrors av/cgo_flags_static.go.
//
// Build with: go build -tags with_libraw ./...   (run scripts/bundle-libraw.sh first)

// #cgo CFLAGS: -I${SRCDIR}/../third_party/libraw/include
// #cgo LDFLAGS: -L${SRCDIR}/../third_party/libraw/lib -lraw
// #cgo darwin LDFLAGS: -lc++ -lz -lm
// #cgo linux LDFLAGS: -lstdc++ -lz -lm
import "C"
