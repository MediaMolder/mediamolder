// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build with_onnx

package face

import (
	"os"
	"testing"
)

// TestAnalyzeIntegration runs the real detect→align→embed pipeline against bundled models and
// asserts cross-run determinism (the reproducibility acceptance check, mirroring the av
// ToRGBA determinism test). It is gated on environment so the default and model-less CI stay
// green: configure
//
//	MEDIAMOLDER_FACE_MODELS          dir with the .onnx models (scripts/fetch-face-models.sh)
//	MEDIAMOLDER_FACE_TEST_IMAGE      a photo containing a known face
//	ONNXRUNTIME_SHARED_LIBRARY_PATH  the onnxruntime shared library
//
// then run: go test -tags with_onnx ./face/ -run Integration
func TestAnalyzeIntegration(t *testing.T) {
	if resolveModelsDir() == "" {
		t.Skip("set MEDIAMOLDER_FACE_MODELS to run the face integration test")
	}
	imgPath := os.Getenv("MEDIAMOLDER_FACE_TEST_IMAGE")
	if imgPath == "" {
		t.Skip("set MEDIAMOLDER_FACE_TEST_IMAGE to a photo with a known face")
	}
	if !Capable() {
		t.Skipf("face pipeline not capable (models/onnxruntime missing) for %q", resolveModelsDir())
	}

	faces, err := Analyze(imgPath)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(faces) == 0 {
		t.Fatal("no faces detected in the fixture")
	}
	for i, f := range faces {
		if len(f.Embedding) != embedDim {
			t.Errorf("face %d: embedding len = %d, want %d", i, len(f.Embedding), embedDim)
		}
	}

	// Determinism: a second pass on the same input must be byte-identical. This is the
	// reproducible-embedding guarantee (run same-machine here; CI runs it across the OS
	// matrix to cover the cross-machine claim).
	again, err := Analyze(imgPath)
	if err != nil {
		t.Fatalf("Analyze (2nd pass): %v", err)
	}
	if len(again) != len(faces) {
		t.Fatalf("non-deterministic face count: %d vs %d", len(again), len(faces))
	}
	for i := range faces {
		if faces[i].BBox != again[i].BBox {
			t.Errorf("face %d: bbox differs across runs: %v vs %v", i, faces[i].BBox, again[i].BBox)
		}
		for k := range faces[i].Embedding {
			if faces[i].Embedding[k] != again[i].Embedding[k] {
				t.Errorf("face %d: embedding[%d] differs across runs (%v vs %v)", i, k, faces[i].Embedding[k], again[i].Embedding[k])
				break
			}
		}
	}
}

// TestAnalyzeImageSeam exercises the frame-level entry points against the same fixture decoded
// in-process, asserting they agree with the path-based Analyze and that DetectImage skips the
// embedding. Gated identically to TestAnalyzeIntegration.
func TestAnalyzeImageSeam(t *testing.T) {
	if resolveModelsDir() == "" {
		t.Skip("set MEDIAMOLDER_FACE_MODELS to run the face integration test")
	}
	imgPath := os.Getenv("MEDIAMOLDER_FACE_TEST_IMAGE")
	if imgPath == "" {
		t.Skip("set MEDIAMOLDER_FACE_TEST_IMAGE to a photo with a known face")
	}
	if !Capable() {
		t.Skipf("face pipeline not capable for %q", resolveModelsDir())
	}

	img, err := decodeRGBA(imgPath)
	if err != nil {
		t.Fatalf("decodeRGBA: %v", err)
	}

	viaImage, err := AnalyzeImage(img)
	if err != nil {
		t.Fatalf("AnalyzeImage: %v", err)
	}
	viaPath, err := Analyze(imgPath)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(viaImage) != len(viaPath) {
		t.Fatalf("AnalyzeImage found %d faces, Analyze found %d", len(viaImage), len(viaPath))
	}
	for i := range viaImage {
		if viaImage[i].BBox != viaPath[i].BBox {
			t.Errorf("face %d: bbox differs (image %v vs path %v)", i, viaImage[i].BBox, viaPath[i].BBox)
		}
	}

	detOnly, err := DetectImage(img)
	if err != nil {
		t.Fatalf("DetectImage: %v", err)
	}
	if len(detOnly) != len(viaImage) {
		t.Fatalf("DetectImage found %d faces, AnalyzeImage found %d", len(detOnly), len(viaImage))
	}
	for i, f := range detOnly {
		if f.Embedding != nil {
			t.Errorf("face %d: DetectImage must not embed, got len %d", i, len(f.Embedding))
		}
	}
}
