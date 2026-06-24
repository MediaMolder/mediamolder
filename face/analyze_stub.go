// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !with_onnx

package face

// This is the default build: no ONNX Runtime, no models. Capable reports false and Analyze
// errors cleanly, so callers (and SyncsIt's faces seam) compile and run with no ML
// dependency. Build with `-tags with_onnx` (and bundle the models) to enable real analysis.

// Capable reports whether this build can analyze faces. The default build cannot.
func Capable() bool { return false }

// Analyze is unavailable without the `with_onnx` build tag.
func Analyze(path string) ([]Face, error) { return nil, ErrUnsupported }
