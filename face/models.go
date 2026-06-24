// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package face

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// EnvModelsDir is the environment variable a host application sets to the directory holding
// the bundled face models. A host application ships these and points this at them, so face
// analysis works with nothing user-installed.
const EnvModelsDir = "MEDIAMOLDER_FACE_MODELS"

var (
	cfgMu       sync.RWMutex
	modelsDir   string // overrides EnvModelsDir when set via SetModelsDir
	onnxLibPath string // overrides ONNXRUNTIME_SHARED_LIBRARY_PATH + auto-discovery (SetONNXLib)
)

// SetModelsDir overrides the bundled-models directory (takes precedence over EnvModelsDir).
// Call it once at startup; passing "" clears the override and falls back to the env var.
func SetModelsDir(dir string) {
	cfgMu.Lock()
	modelsDir = dir
	cfgMu.Unlock()
}

// SetONNXLib overrides the ONNX Runtime shared-library path (takes precedence over the
// ONNXRUNTIME_SHARED_LIBRARY_PATH env var and auto-discovery). Pass "" to clear. Use this when
// the runtime is installed somewhere non-standard; otherwise it is found automatically.
func SetONNXLib(path string) {
	cfgMu.Lock()
	onnxLibPath = path
	cfgMu.Unlock()
}

// onnxLibOverridePath returns the SetONNXLib override (passed to onnxrt.Init in with_onnx builds).
func onnxLibOverridePath() string {
	cfgMu.RLock()
	p := onnxLibPath
	cfgMu.RUnlock()
	return p
}

// resolveModelsDir returns the configured models directory: the SetModelsDir override if
// set, else EnvModelsDir, else "" (which makes the models unavailable → Capable()==false).
func resolveModelsDir() string {
	cfgMu.RLock()
	d := modelsDir
	cfgMu.RUnlock()
	if d != "" {
		return d
	}
	return os.Getenv(EnvModelsDir)
}

// ModelSpec describes one bundled ONNX model: its filename within the models directory, the
// SHA-256 it must hash to (lower-case hex — exiftool-style pin/verify), the ONNX I/O tensor
// names, the square input dimension, and how to turn an image into the input tensor.
type ModelSpec struct {
	Filename   string
	SHA256     string // pinned; verified on load. "" disables verification (tests only).
	InputName  string
	OutputName string
	InputSize  int

	// MaxDet selects the detector output layout. >0 ⇒ an end-to-end-NMS export with output
	// [1, MaxDet, nmsDetAttrs] (parseYOLOv8FaceNMSOutput). 0 ⇒ the raw transposed
	// [1, 4+1+5*kptDim, numPreds] layout (parseYOLOv8FaceOutput + faceNMS).
	MaxDet int

	// Preprocessing (applied per pixel, per channel): out = (channel*255-scaled? see below).
	// SwapRB feeds B,G,R instead of R,G,B. Scale multiplies the 0–255 value; Mean is
	// subtracted after scaling. Detector default = RGB,1/255,0 (→[0,1], matches YOLOv8);
	// SFace default = RGB,1,0 (→[0,255]).
	SwapRB bool
	Scale  float64
	Mean   [3]float64
}

// DefaultDetectSpec / DefaultEmbedSpec are the standard YOLOv8-face + SFace specifications.
// The integration test (//go:build with_onnx) is the source of truth: it loads the real
// models and fails loudly if the I/O here is wrong. See scripts/fetch-face-models.sh.
//
// The default detector is akanametov/yolo-face v1.0.0 yolov8n-face.onnx (AGPL-3.0): an
// Ultralytics 8.3.241 YOLOv8n-pose model trained on WIDERFace, exported with end-to-end NMS.
// Verified by ONNX metadata/graph inspection: task=pose, kpt_shape=[5,3], names={0:'face'},
// input images[1,3,640,640], output output0[1,300,21] → MaxDet=300, nmsDetAttrs=21. The
// detector is swappable behind this API for a raw-output export (MaxDet=0) or YuNet (MIT),
// with the same Face contract and no downstream change.
var (
	DefaultDetectSpec = ModelSpec{
		Filename:   "yolov8n-face.onnx",
		SHA256:     "06b941fd5792be624ad18f2df9ede0a021c4df165dd418204d978c20fd555928",
		InputName:  "images",
		OutputName: "output0",
		InputSize:  640,
		MaxDet:     300,         // end-to-end-NMS export
		Scale:      1.0 / 255.0, // YOLOv8 expects RGB in [0,1]
	}
	// SFace, OpenCV-Zoo face_recognition_sface_2021dec.onnx (Apache-2.0). Verified by ONNX
	// graph inspection: input "data" [1,3,112,112], output "fc1" [1,128].
	DefaultEmbedSpec = ModelSpec{
		Filename:   "sface.onnx",
		SHA256:     "0ba9fbfa01b5270c96627c4ef784da859931e02f04419c829e83484087c34e79",
		InputName:  "data",
		OutputName: "fc1",
		InputSize:  alignSize, // 112
		Scale:      1.0,       // SFace expects RGB in [0,255]
	}
)

// CheckModels reports whether both bundled face models are present in dir and match their
// pinned SHA-256. It is pure file IO (no ONNX runtime), so `mediamolder face-setup` can
// validate the models even in a binary built without the with_onnx tag. nil ⇒ both are ready;
// otherwise the error names the missing or mismatched model.
func CheckModels(dir string) error {
	for _, spec := range []ModelSpec{DefaultDetectSpec, DefaultEmbedSpec} {
		if _, err := loadVerified(dir, spec); err != nil {
			return err
		}
	}
	return nil
}

// loadVerified reads spec.Filename from dir and returns its bytes only if the content hashes
// to spec.SHA256 (when pinned). A mismatch is a hard error — a tampered or wrong model never
// loads. With an empty dir or SHA256, it behaves predictably (missing dir → not-found error;
// empty SHA256 → skip verification, used only by unit tests).
func loadVerified(dir string, spec ModelSpec) ([]byte, error) {
	if dir == "" {
		return nil, fmt.Errorf("face: no models directory configured (set %s or call SetModelsDir)", EnvModelsDir)
	}
	path := filepath.Join(dir, spec.Filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("face: read model: %w", err)
	}
	if spec.SHA256 != "" {
		sum := sha256.Sum256(data)
		got := hex.EncodeToString(sum[:])
		if !strings.EqualFold(got, spec.SHA256) {
			return nil, fmt.Errorf("face: model %s SHA-256 mismatch: got %s, want %s", spec.Filename, got, spec.SHA256)
		}
	}
	return data, nil
}

// inputTensor converts an image to a [1,3,H,W] NCHW float32 tensor per the spec's
// preprocessing (channel order, scale, mean). The image must already be spec.InputSize²
// (the detector letterboxes upstream; the embedder is fed an aligned 112×112 crop), so this
// does no resizing — it samples pixel-for-pixel and is fully deterministic.
func inputTensor(img *image.RGBA, spec ModelSpec) []float32 {
	n := spec.InputSize
	plane := n * n
	out := make([]float32, 3*plane)
	scale := spec.Scale
	if scale == 0 {
		scale = 1
	}
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			c := img.RGBAAt(x, y)
			ch0, ch1, ch2 := float64(c.R), float64(c.G), float64(c.B)
			if spec.SwapRB {
				ch0, ch2 = ch2, ch0
			}
			off := y*n + x
			out[off] = float32(ch0*scale - spec.Mean[0])
			out[plane+off] = float32(ch1*scale - spec.Mean[1])
			out[2*plane+off] = float32(ch2*scale - spec.Mean[2])
		}
	}
	return out
}
