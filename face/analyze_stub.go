// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !with_onnx

package face

import "image"

// This is the default build: no ONNX Runtime, no models. Capable reports false and the
// Analyze* entry points error cleanly, so callers (including a host application's face seam)
// compile and run with no ML dependency. Build with `-tags with_onnx` (and bundle the models)
// to enable real analysis.

// Capable reports whether this build can analyze faces. The default build cannot.
func Capable() bool { return false }

// Analyze is unavailable without the `with_onnx` build tag.
func Analyze(path string) ([]Face, error) { return nil, ErrUnsupported }

// AnalyzeImage is unavailable without the `with_onnx` build tag.
func AnalyzeImage(img image.Image) ([]Face, error) { return nil, ErrUnsupported }

// DetectImage is unavailable without the `with_onnx` build tag.
func DetectImage(img image.Image) ([]Face, error) { return nil, ErrUnsupported }

// AnalyzeImageOpts is unavailable without the `with_onnx` build tag.
func AnalyzeImageOpts(img image.Image, o Options) ([]Face, error) { return nil, ErrUnsupported }
