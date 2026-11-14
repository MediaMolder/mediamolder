//go:build !with_onnx

// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package detect

// Available returns ErrUnsupported in a build without the with_onnx tag.
func Available() error { return ErrUnsupported }

// Open returns ErrUnsupported in a build without the with_onnx tag.
func Open(cfg Config) (*Detector, error) { return nil, ErrUnsupported }
