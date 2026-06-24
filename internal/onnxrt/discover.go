// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

// Package onnxrt centralises locating and initialising the ONNX Runtime shared library so the
// face and processors (yolo_v8) ONNX paths share one discovery and a single process-global
// init. The discovery here is pure (no ONNX dependency) and compiles in every build; the
// runtime init lives in runtime.go behind the with_onnx build tag.
package onnxrt

import (
	"os"
	"path/filepath"
	"runtime"
)

// EnvLib is the environment variable naming the ONNX Runtime shared library.
const EnvLib = "ONNXRUNTIME_SHARED_LIBRARY_PATH"

// LibPath resolves the ONNX Runtime shared-library path in priority order:
//  1. override (e.g. a node's "ort_lib" param),
//  2. the ONNXRUNTIME_SHARED_LIBRARY_PATH environment variable,
//  3. auto-discovery in the platform's standard install locations, by the correct library name
//     (libonnxruntime.dylib on macOS, libonnxruntime.so on Linux).
//
// It returns "" only when none of those locate a library, in which case the loader falls back to
// its built-in default name (which is wrong on macOS/Windows) and surfaces a clear dlopen error.
func LibPath(override string) string {
	if override != "" {
		return override
	}
	if p := os.Getenv(EnvLib); p != "" {
		return p
	}
	for _, c := range candidatePaths() {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
	}
	// Version-suffixed fallback (e.g. libonnxruntime.1.20.1.dylib, libonnxruntime.so.1) when the
	// unversioned symlink is absent. The last match sorts highest — usually the newest version.
	for _, g := range globs() {
		if m, _ := filepath.Glob(g); len(m) > 0 {
			return m[len(m)-1]
		}
	}
	return ""
}

// candidatePaths lists the standard unversioned library locations per OS.
func candidatePaths() []string {
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

// globs lists version-suffixed fallbacks searched when no unversioned library exists.
func globs() []string {
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
