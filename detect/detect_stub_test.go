//go:build !with_onnx

// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package detect

import (
	"errors"
	"testing"
)

func TestStubUnsupported(t *testing.T) {
	if err := Available(); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Available = %v, want ErrUnsupported", err)
	}
	d, err := Open(Config{ObjectModel: "yolo.onnx", FaceModels: "models"})
	if d != nil || !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Open = %v, %v; want nil, ErrUnsupported", d, err)
	}
}
