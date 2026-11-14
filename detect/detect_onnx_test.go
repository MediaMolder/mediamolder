//go:build with_onnx

// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package detect

import (
	"crypto/sha256"
	"encoding/hex"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/face"
	"github.com/MediaMolder/MediaMolder/internal/onnxrt"
)

// These tests run the real models and skip when an input is absent:
//
//	MEDIAMOLDER_DETECT_YOLO_MODEL    a YOLO detect-task ONNX file (else /models/yolo26n.onnx)
//	MEDIAMOLDER_FACE_MODELS          dir with the face models (scripts/fetch-face-models.sh)
//	MEDIAMOLDER_FACE_TEST_IMAGE      a photo with one clearly visible person and face
//	ONNXRUNTIME_SHARED_LIBRARY_PATH  the onnxruntime shared library
//
// then run: go test -tags with_onnx ./detect/

const (
	envObjectModel = "MEDIAMOLDER_DETECT_YOLO_MODEL"
	envTestImage   = "MEDIAMOLDER_FACE_TEST_IMAGE"

	// The 1280 export of yolo26n the input-size test is pinned to.
	yolo26n1280SHA256 = "81c934bf5599fd048035c6b820775a4590fe2184e2b4bc4d3f4140275ea9d82c"
)

func needRuntime(t *testing.T) {
	t.Helper()
	if err := onnxrt.Init(""); err != nil {
		t.Skipf("onnxruntime not available: %v", err)
	}
}

func objectModel(t *testing.T) string {
	t.Helper()
	path := os.Getenv(envObjectModel)
	if path == "" {
		path = "/models/yolo26n.onnx"
	}
	if _, err := os.Stat(path); err != nil {
		t.Skipf("object model not found at %s (set %s)", path, envObjectModel)
	}
	return path
}

func faceModels(t *testing.T) string {
	t.Helper()
	dir := os.Getenv(face.EnvModelsDir)
	if dir == "" {
		t.Skipf("set %s to run the face tests", face.EnvModelsDir)
	}
	if err := face.CheckModels(dir); err != nil {
		t.Skipf("face models not usable in %s: %v", dir, err)
	}
	return dir
}

func testPicture(t *testing.T) image.Image {
	t.Helper()
	path := os.Getenv(envTestImage)
	if path == "" {
		t.Skipf("set %s to a photo with one visible person and face", envTestImage)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Skipf("test image: %v", err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return img
}

func open(t *testing.T, cfg Config) *Detector {
	t.Helper()
	d, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func inside(b Box, img image.Image) bool {
	r := img.Bounds()
	return b.X >= 0 && b.Y >= 0 && b.X+b.W <= float64(r.Dx()) && b.Y+b.H <= float64(r.Dy())
}

func TestAvailable(t *testing.T) {
	if err := Available(); err != nil {
		t.Fatalf("Available = %v", err)
	}
	if _, err := Open(Config{}); err == nil {
		t.Fatal("Open with no models succeeded")
	}
}

func TestObjectInputSize(t *testing.T) {
	model := objectModel(t)
	data, err := os.ReadFile(model)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != yolo26n1280SHA256 {
		t.Skipf("%s is not the pinned 1280 export (SHA-256 %s)", model, got)
	}
	needRuntime(t)
	d := open(t, Config{ObjectModel: model})
	if got := d.ObjectInputSize(); got != 1280 {
		t.Fatalf("ObjectInputSize = %d, want 1280", got)
	}
}

func TestDetectPersonAndFace(t *testing.T) {
	model := objectModel(t)
	dir := faceModels(t)
	img := testPicture(t)
	needRuntime(t)
	d := open(t, Config{ObjectModel: model, FaceModels: dir})
	if !d.HasObjects() || !d.HasFaces() || d.ObjectInputSize() <= 0 {
		t.Fatalf("HasObjects %v, HasFaces %v, input size %d", d.HasObjects(), d.HasFaces(), d.ObjectInputSize())
	}

	all := Want{Persons: true, Balls: true, Faces: true}
	res, err := d.Detect(img, all)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Persons) == 0 || len(res.Faces) == 0 {
		t.Fatalf("found %d persons and %d faces, want at least one of each", len(res.Persons), len(res.Faces))
	}
	for kind, boxes := range map[string][]Box{"person": res.Persons, "ball": res.Balls, "face": res.Faces} {
		for i, b := range boxes {
			if !inside(b, img) {
				t.Errorf("%s %d lies outside the image: %+v", kind, i, b)
			}
		}
	}
	for i, f := range res.Faces {
		if f.Score < 0.6 {
			t.Errorf("face %d score %v is below the 0.6 floor", i, f.Score)
		}
		if f.Mouth == nil {
			t.Fatalf("face %d has no mouth", i)
		}
		if m := f.Mouth; m[0] < f.X || m[0] > f.X+f.W || m[1] < f.Y || m[1] > f.Y+f.H {
			t.Errorf("face %d mouth %v lies outside its box %+v", i, *m, f)
		}
	}
	t.Logf("%d persons, %d balls, %d faces", len(res.Persons), len(res.Balls), len(res.Faces))

	again, err := d.Detect(img, all)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res, again) {
		t.Fatalf("two calls on the same image differ:\n%+v\n%+v", res, again)
	}
}

func TestOpenFaceOnly(t *testing.T) {
	dir := faceModels(t)
	img := testPicture(t)
	needRuntime(t)
	d := open(t, Config{FaceModels: dir})
	if d.HasObjects() || !d.HasFaces() || d.ObjectInputSize() != 0 {
		t.Fatalf("HasObjects %v, HasFaces %v, input size %d", d.HasObjects(), d.HasFaces(), d.ObjectInputSize())
	}
	res, err := d.Detect(img, Want{Faces: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Faces) == 0 {
		t.Fatal("no face found")
	}
	if _, err := d.Detect(img, Want{Persons: true}); err == nil || !strings.Contains(err.Error(), "object model") {
		t.Fatalf("persons on a face-only detector: %v", err)
	}
}

func TestOpenObjectOnly(t *testing.T) {
	model := objectModel(t)
	img := testPicture(t)
	needRuntime(t)
	d := open(t, Config{ObjectModel: model})
	if !d.HasObjects() || d.HasFaces() {
		t.Fatalf("HasObjects %v, HasFaces %v", d.HasObjects(), d.HasFaces())
	}
	res, err := d.Detect(img, Want{Persons: true, Balls: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Persons) == 0 {
		t.Fatal("no person found")
	}
	if _, err := d.Detect(img, Want{Faces: true}); err == nil || !strings.Contains(err.Error(), "face models") {
		t.Fatalf("faces on an object-only detector: %v", err)
	}
}

func TestOpenMissingEmbedder(t *testing.T) {
	src := faceModels(t)
	dir := t.TempDir()
	data, err := os.ReadFile(filepath.Join(src, face.DefaultDetectSpec.Filename))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, face.DefaultDetectSpec.Filename), data, 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := Open(Config{FaceModels: dir})
	if d != nil || err == nil || !strings.HasPrefix(err.Error(), "face models:") {
		t.Fatalf("Open without the embedder = %v, %v", d, err)
	}
}

func TestOpenWrongObjectFile(t *testing.T) {
	model := filepath.Join(t.TempDir(), "m.onnx")
	if err := os.WriteFile(model, []byte("not a model"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := Open(Config{ObjectModel: model})
	if d != nil || err == nil || !strings.HasPrefix(err.Error(), "object model:") {
		t.Fatalf("Open with a non-model file = %v, %v", d, err)
	}
}

func TestOpenSecondFaceDirectory(t *testing.T) {
	dir := faceModels(t)
	needRuntime(t)
	open(t, Config{FaceModels: dir})

	other := t.TempDir()
	for _, name := range []string{face.DefaultDetectSpec.Filename, face.DefaultEmbedSpec.Filename} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(other, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	d, err := Open(Config{FaceModels: other})
	if d != nil || err == nil || !strings.HasPrefix(err.Error(), "face models:") {
		t.Fatalf("Open from a second directory = %v, %v", d, err)
	}
	open(t, Config{FaceModels: dir})
}
