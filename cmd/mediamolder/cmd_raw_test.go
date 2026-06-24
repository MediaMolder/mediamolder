// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !with_libraw

package main

import (
	"strings"
	"testing"
)

// In the default (no-LibRaw) build, the raw commands must fail with an actionable message that
// names the with_libraw build tag — never a confusing nil-result or a crash.

func TestRawDecodeUnsupported(t *testing.T) {
	err := cmdRawDecode([]string{"photo.dng"})
	if err == nil {
		t.Fatal("raw-decode should error without LibRaw")
	}
	if !strings.Contains(err.Error(), "with_libraw") {
		t.Errorf("error should point at the with_libraw build: %v", err)
	}
}

func TestRawSetupNotReady(t *testing.T) {
	if err := cmdRawSetup(nil); err == nil {
		t.Error("raw-setup should exit non-zero without LibRaw")
	}
}
