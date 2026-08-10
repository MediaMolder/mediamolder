// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !with_onnx

package face

import "image"

// ExpressionAvailable is unavailable without the `with_onnx` build tag (the same
// ErrUnsupported sentinel the rest of the seam reports).
func ExpressionAvailable() error { return ErrUnsupported }

// AssessFaceExpression is unavailable without the `with_onnx` build tag.
func AssessFaceExpression(_ image.Image, _ [4]int, _ [numLandmarks][2]int) (Expression, error) {
	return Expression{}, ErrUnsupported
}
