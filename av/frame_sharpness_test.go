// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

import "testing"

func TestFrameSharpness(t *testing.T) {
	sharp := NewTestFrame(t, 64, 48, 0) // YUV420P
	defer sharp.Close()
	FillTestFrameYChecker(sharp)

	flat := NewTestFrame(t, 64, 48, 0)
	defer flat.Close()
	FillTestFrameYFlat(flat, 128)

	sv, err := sharp.Sharpness()
	if err != nil {
		t.Fatal(err)
	}
	fv, err := flat.Sharpness()
	if err != nil {
		t.Fatal(err)
	}
	if fv != 0 {
		t.Fatalf("flat frame sharpness = %v, want 0 (no high-frequency content)", fv)
	}
	if sv <= fv {
		t.Fatalf("checkerboard sharpness %v should far exceed flat %v", sv, fv)
	}

	if _, err := (*Frame)(nil).Sharpness(); err == nil {
		t.Fatal("nil frame should error")
	}
}
