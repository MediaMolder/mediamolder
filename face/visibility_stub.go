// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !with_onnx

package face

import "image"

// VisibilityAvailable is unavailable without the `with_onnx` build tag (the same
// ErrUnsupported sentinel the rest of the seam reports).
func VisibilityAvailable() error { return ErrUnsupported }

// AssessFaceVisibility is unavailable without the `with_onnx` build tag.
func AssessFaceVisibility(_ image.Image, _ [4]int) (float32, error) {
	return 0, ErrUnsupported
}
