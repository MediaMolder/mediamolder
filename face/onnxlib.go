// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build with_onnx

package face

import (
	"os"
	"path/filepath"
	"runtime"
)

// resolveONNXLib returns the path to the ONNX Runtime shared library, in priority order:
//  1. an explicit override set via [SetONNXLib] (e.g. the face_detect "ort_lib" param),
//  2. the ONNXRUNTIME_SHARED_LIBRARY_PATH environment variable,
//  3. auto-discovery in the platform's standard install locations (so a plain
//     `brew install onnxruntime` / distro package is found with no configuration).
//
// It returns "" only when none of those locate a library, in which case the loader falls back
// to its built-in default name (which is wrong on macOS/Windows) and surfaces a clear dlopen
// error. Auto-discovery deliberately uses the platform-correct library name (libonnxruntime.dylib
// on macOS, libonnxruntime.so on Linux) rather than that default.
func resolveONNXLib() string {
	if p := onnxLibOverridePath(); p != "" {
		return p
	}
	if p := os.Getenv(EnvONNXRuntimeLib); p != "" {
		return p
	}
	for _, c := range onnxCandidatePaths() {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
	}
	// Version-suffixed fallback (e.g. libonnxruntime.1.20.1.dylib, libonnxruntime.so.1) when the
	// unversioned symlink is absent. The last match sorts highest — usually the newest version.
	for _, g := range onnxGlobs() {
		if m, _ := filepath.Glob(g); len(m) > 0 {
			return m[len(m)-1]
		}
	}
	return ""
}

// onnxCandidatePaths lists the standard unversioned library locations per OS.
func onnxCandidatePaths() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"/opt/homebrew/lib/libonnxruntime.dylib",                 // Apple-silicon Homebrew
			"/opt/homebrew/opt/onnxruntime/lib/libonnxruntime.dylib", // keg-only fallback
			"/usr/local/lib/libonnxruntime.dylib",                    // Intel Homebrew / manual
			"/usr/local/opt/onnxruntime/lib/libonnxruntime.dylib",    // keg-only fallback
		}
	case "linux":
		return []string{
			"/usr/lib/libonnxruntime.so",
			"/usr/local/lib/libonnxruntime.so",
			"/usr/lib/x86_64-linux-gnu/libonnxruntime.so",
			"/usr/lib/aarch64-linux-gnu/libonnxruntime.so",
		}
	}
	return nil // Windows: rely on the env/param/PATH; onnxruntime.dll has no canonical install dir
}

// onnxGlobs lists version-suffixed fallbacks searched when no unversioned library exists.
func onnxGlobs() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"/opt/homebrew/lib/libonnxruntime*.dylib",
			"/usr/local/lib/libonnxruntime*.dylib",
		}
	case "linux":
		return []string{
			"/usr/lib/libonnxruntime.so*",
			"/usr/local/lib/libonnxruntime.so*",
			"/usr/lib/*-linux-gnu/libonnxruntime.so*",
		}
	}
	return nil
}
