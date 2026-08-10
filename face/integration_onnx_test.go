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

// TestExpressionIntegration runs detect → expression against the bundled models: every
// detected face in the fixture must yield deterministic blendshape coefficients in [0,1]
// with a presence at or above the assessment threshold (a real detected face the model then
// disowns would mean the crop geometry is wrong). Gated like TestAnalyzeIntegration;
// additionally skips when the model bundle predates the OPTIONAL expression models.
func TestExpressionIntegration(t *testing.T) {
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
	if err := ExpressionAvailable(); err != nil {
		t.Skipf("expression models not bundled: %v", err)
	}

	img, err := decodeRGBA(imgPath)
	if err != nil {
		t.Fatalf("decodeRGBA: %v", err)
	}
	faces, err := DetectImage(img)
	if err != nil {
		t.Fatalf("DetectImage: %v", err)
	}
	if len(faces) == 0 {
		t.Fatal("no faces detected in the fixture")
	}
	for i, f := range faces {
		e, err := AssessFaceExpression(img, f.BBox, f.Landmarks)
		if err != nil {
			t.Fatalf("face %d: AssessFaceExpression: %v", i, err)
		}
		if e.Presence < exprPresenceThresh || e.Presence > 1 {
			t.Errorf("face %d: presence = %v, want [%v,1]", i, e.Presence, exprPresenceThresh)
		}
		for c, v := range e.Blendshapes {
			if v < 0 || v > 1 {
				t.Errorf("face %d: coefficient %s = %v, want [0,1]", i, BlendshapeNames[c], v)
			}
		}
		again, err := AssessFaceExpression(img, f.BBox, f.Landmarks)
		if err != nil || again != e {
			t.Errorf("face %d: non-deterministic expression (err %v)", i, err)
		}
		t.Logf("face %d bbox %v presence %.3f smile %.3f eyesOpen %.3f", i, f.BBox, e.Presence, e.Smile, e.EyesOpen)
	}
}

// TestVisibilityIntegration runs detect → visibility against the bundled models: every
// detected face in the fixture must yield a deterministic occlusion fraction in [0,1].
// Gated like TestAnalyzeIntegration; additionally skips (with the reason) when the model
// bundle predates the OPTIONAL visibility model — detect/embed capability must not imply it.
func TestVisibilityIntegration(t *testing.T) {
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
	if err := VisibilityAvailable(); err != nil {
		t.Skipf("visibility model not bundled: %v", err)
	}

	img, err := decodeRGBA(imgPath)
	if err != nil {
		t.Fatalf("decodeRGBA: %v", err)
	}
	faces, err := DetectImage(img)
	if err != nil {
		t.Fatalf("DetectImage: %v", err)
	}
	if len(faces) == 0 {
		t.Fatal("no faces detected in the fixture")
	}
	for i, f := range faces {
		occ, err := AssessFaceVisibility(img, f.BBox)
		if err != nil {
			t.Fatalf("face %d: AssessFaceVisibility: %v", i, err)
		}
		if occ < 0 || occ > 1 {
			t.Errorf("face %d: occlusion = %v, want [0,1]", i, occ)
		}
		again, err := AssessFaceVisibility(img, f.BBox)
		if err != nil || again != occ {
			t.Errorf("face %d: non-deterministic occlusion: %v then %v (err %v)", i, occ, again, err)
		}
		t.Logf("face %d bbox %v occlusion %.3f", i, f.BBox, occ)
	}
}
