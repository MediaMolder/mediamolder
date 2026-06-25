// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package face

import (
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadVerified(t *testing.T) {
	dir := t.TempDir()
	content := []byte("not-a-real-onnx-model-but-bytes-are-bytes")
	if err := os.WriteFile(filepath.Join(dir, "m.onnx"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	good := hex.EncodeToString(sum[:])

	t.Run("matching hash loads", func(t *testing.T) {
		data, err := loadVerified(dir, ModelSpec{Filename: "m.onnx", SHA256: good})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(data) != string(content) {
			t.Errorf("content mismatch")
		}
	})
	t.Run("wrong hash is rejected", func(t *testing.T) {
		_, err := loadVerified(dir, ModelSpec{Filename: "m.onnx", SHA256: "00" + good[2:]})
		if err == nil {
			t.Fatal("expected SHA-256 mismatch error, got nil")
		}
	})
	t.Run("empty hash skips verification", func(t *testing.T) {
		if _, err := loadVerified(dir, ModelSpec{Filename: "m.onnx"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("missing dir errors", func(t *testing.T) {
		if _, err := loadVerified("", ModelSpec{Filename: "m.onnx"}); err == nil {
			t.Fatal("expected error for empty dir")
		}
	})
	t.Run("missing file errors", func(t *testing.T) {
		if _, err := loadVerified(dir, ModelSpec{Filename: "nope.onnx"}); err == nil {
			t.Fatal("expected error for missing file")
		}
	})
}

func TestSetModelsDirPrecedence(t *testing.T) {
	t.Setenv(EnvModelsDir, "/from/env")
	SetModelsDir("/override")
	defer SetModelsDir("")
	if got := resolveModelsDir(); got != "/override" {
		t.Errorf("override should win: got %q", got)
	}
	SetModelsDir("")
	if got := resolveModelsDir(); got != "/from/env" {
		t.Errorf("env fallback: got %q", got)
	}
}

func TestSetONNXLib(t *testing.T) {
	defer SetONNXLib("")
	SetONNXLib("/opt/x/libonnxruntime.dylib")
	if got := onnxLibOverridePath(); got != "/opt/x/libonnxruntime.dylib" {
		t.Errorf("override should be returned: got %q", got)
	}
	SetONNXLib("")
	if got := onnxLibOverridePath(); got != "" {
		t.Errorf("cleared override should be empty: got %q", got)
	}
}

func TestInputTensorLayoutAndScale(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.SetRGBA(0, 0, color.RGBA{R: 255, G: 0, B: 0, A: 255}) // top-left red
	img.SetRGBA(1, 1, color.RGBA{R: 0, G: 0, B: 255, A: 255}) // bottom-right blue

	// RGB, scale 1/255 ⇒ [0,1]. NCHW: R-plane, G-plane, B-plane each 2*2.
	tr := inputTensor(img, ModelSpec{InputSize: 2, Scale: 1.0 / 255.0})
	const plane = 4
	if tr[0] != 1.0 { // R plane, (0,0)
		t.Errorf("R[0,0] = %v, want 1.0", tr[0])
	}
	if tr[2*plane+3] != 1.0 { // B plane, (1,1)
		t.Errorf("B[1,1] = %v, want 1.0", tr[2*plane+3])
	}
	if tr[plane+0] != 0.0 { // G plane, (0,0)
		t.Errorf("G[0,0] = %v, want 0.0", tr[plane+0])
	}

	// SwapRB feeds B,G,R: the red pixel's value should now land in the *third* plane.
	sw := inputTensor(img, ModelSpec{InputSize: 2, Scale: 1.0 / 255.0, SwapRB: true})
	if sw[2*plane+0] != 1.0 {
		t.Errorf("swapRB: third plane (0,0) = %v, want 1.0 (the red channel)", sw[2*plane+0])
	}
	if sw[0] != 0.0 {
		t.Errorf("swapRB: first plane (0,0) = %v, want 0.0", sw[0])
	}
}
