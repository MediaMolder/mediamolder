// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !with_onnx

package face

import (
	"errors"
	"image"
	"testing"
)

// TestStubContract pins the default-build behaviour: the frame-level seam mirrors Analyze and
// reports ErrUnsupported, so callers (CLI, processor, downstream consumers) get one stable
// sentinel and compile with no ML dependency.
func TestStubContract(t *testing.T) {
	if Capable() {
		t.Fatal("Capable() must be false in the default (!with_onnx) build")
	}
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for name, fn := range map[string]func() ([]Face, error){
		"AnalyzeImage":     func() ([]Face, error) { return AnalyzeImage(img) },
		"DetectImage":      func() ([]Face, error) { return DetectImage(img) },
		"AnalyzeImageOpts": func() ([]Face, error) { return AnalyzeImageOpts(img, Options{Embed: true}) },
	} {
		faces, err := fn()
		if !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: err = %v, want ErrUnsupported", name, err)
		}
		if faces != nil {
			t.Errorf("%s: faces = %v, want nil", name, faces)
		}
	}
}

// TestVisibilityStubContract pins the default-build visibility seam: the same
// ErrUnsupported sentinel, a zero fraction, and no panic — so downstream callers treat
// "unavailable" and "not yet assessed" identically.
func TestVisibilityStubContract(t *testing.T) {
	if err := VisibilityAvailable(); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("VisibilityAvailable = %v, want ErrUnsupported", err)
	}
	occ, err := AssessFaceVisibility(image.NewRGBA(image.Rect(0, 0, 4, 4)), [4]int{1, 1, 2, 2})
	if !errors.Is(err, ErrUnsupported) || occ != 0 {
		t.Fatalf("AssessFaceVisibility = (%v, %v), want (0, ErrUnsupported)", occ, err)
	}
}
