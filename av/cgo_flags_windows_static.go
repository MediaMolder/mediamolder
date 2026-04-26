// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build ffstatic && windows && ffstatic_windows_msys2

package av

// Windows MSYS2/mingw64 helper for static linking against a local FFmpeg
// build that was configured with --enable-libx264 --enable-libx265
// --enable-libvpx --enable-libopus --enable-libass --enable-libfreetype etc.
//
// This file is **opt-in** via the additional build tag `ffstatic_windows_msys2`
// because it embeds a hardcoded MSYS2 install path and the exact set of
// transitive system libraries needed by a full MSYS2 mingw-w64 codec stack.
// It will not be compiled by the default `ffstatic` build.
//
// Typical use (from a PowerShell or cmd shell with mingw64 on PATH):
//
//	go build -tags "ffstatic,ffstatic_windows_msys2" ./...
//
// If your MSYS2 lives elsewhere, copy this file and adjust the `-LC:/...`
// path below, or drive the link line through pkg-config instead.

// #cgo LDFLAGS: -Wl,--start-group -lavfilter -lavformat -lavcodec -lswscale -lswresample -lavutil -Wl,--end-group
// #cgo LDFLAGS: -LC:/msys64/mingw64/lib
// #cgo LDFLAGS: -lass -lfribidi -lunibreak -lfontconfig -lexpat -lfreetype -lpng16 -lharfbuzz -lusp10 -lrpcrt4 -ldwrite
// #cgo LDFLAGS: -lglib-2.0 -lintl -lwinmm -lshlwapi -luuid -lpcre2-8 -lgraphite2 -lbrotlidec -lbrotlicommon
// #cgo LDFLAGS: -lvpx -llzma -ldav1d -laom -lmp3lame -lopus -lspeex -ltheoraenc -ltheoradec -lvorbisenc -lvorbis -logg -lx264 -lx265
// #cgo LDFLAGS: -lssl -lcrypto -lws2_32 -lgdi32 -lcrypt32 -luser32 -lbcrypt -lsecur32 -lncrypt
// #cgo LDFLAGS: -lstdc++ -lgcc_s -lgcc -latomic
// #cgo LDFLAGS: -lmfuuid -lstrmiids -lole32
import "C"
