//go:build with_onnx

// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"testing"

	"github.com/MediaMolder/MediaMolder/face"
)

// TestFaceDetectRegistered confirms the processor is wired into the registry under with_onnx.
func TestFaceDetectRegistered(t *testing.T) {
	p, err := Get("face_detect")
	if err != nil {
		t.Fatalf("Get(face_detect): %v", err)
	}
	if p == nil {
		t.Fatal("Get(face_detect) returned nil processor")
	}
}

// TestFaceDetectInitCapabilityGated asserts Init mirrors face.Capable(): it succeeds when the
// models are present and fails with an actionable error when they are not — so a misconfigured
// graph fails at construction, not silently mid-run.
func TestFaceDetectInitCapabilityGated(t *testing.T) {
	err := (&FaceDetect{}).Init(map[string]any{})
	if face.Capable() {
		if err != nil {
			t.Fatalf("Init with models available: unexpected error %v", err)
		}
	} else if err == nil {
		t.Fatal("Init must error when face models are unavailable")
	}
}

// TestFaceDetectInitParams checks the param parsing (every / conf / embeddings) is applied.
func TestFaceDetectInitParams(t *testing.T) {
	if !face.Capable() {
		t.Skip("face models unavailable; param parsing is exercised after the capability gate")
	}
	p := &FaceDetect{}
	if err := p.Init(map[string]any{"every": 5.0, "conf": 0.7, "embeddings": true}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if p.every != 5 || p.conf != 0.7 || !p.embed {
		t.Errorf("params not applied: every=%d conf=%v embed=%v", p.every, p.conf, p.embed)
	}
}
