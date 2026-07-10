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
// #cgo windows LDFLAGS: -static-libgcc -Wl,-Bstatic -lstdc++ -lz -lwinpthread -Wl,-Bdynamic -lm -lws2_32
import "C"

// Windows note: plain -lstdc++/-lz would resolve to MinGW IMPORT libs and leave the binary
// dynamically importing libstdc++-6.dll/zlib1.dll — dead on any machine without MSYS2's bin dir
// on PATH. The -Wl,-Bstatic window forces the static archives (libstdc++.a, libz.a) so LibRaw
// adds no MinGW runtime DLLs beyond what every cgo build here already carries; -lws2_32 covers
// LibRaw's winsock references (a system DLL, fine to import). gcc's implicit trailing -lpthread
// still imports libwinpthread-1.dll — accepted: the av package's libav DLLs make a Windows
// binary dynamic regardless, and host applications that bundle mediamolder already stage the
// transitive DLL closure (objdump -p), which carries winpthread automatically.
