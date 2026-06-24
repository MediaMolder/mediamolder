// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !with_libraw

package raw

import (
	"errors"
	"testing"
)

// In the default build, RAW develop is unavailable: Capable reports false and Decode returns
// ErrUnsupported. The with_libraw build exercises the real decode in integration_libraw_test.go.

func TestStubNotCapable(t *testing.T) {
	if Capable() {
		t.Error("Capable() should be false in a !with_libraw build")
	}
}

func TestStubDecodeUnsupported(t *testing.T) {
	img, err := Decode("a.dng")
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("Decode error = %v, want ErrUnsupported", err)
	}
	if img != nil {
		t.Errorf("Decode image = %v, want nil", img)
	}
}
