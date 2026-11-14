//go:build with_onnx

// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/internal/onnxrt"
)

func TestYOLORegistration(t *testing.T) {
	yolo, err := Get("yolo")
	if err != nil {
		t.Fatal(err)
	}
	det := yolo.(*YOLODetector)
	if det.name != "yolo" || det.legacyV8 {
		t.Fatalf("yolo registration = name %q legacy %v", det.name, det.legacyV8)
	}
	alias, err := Get("yolo_v8")
	if err != nil {
		t.Fatal(err)
	}
	legacy := alias.(*YOLODetector)
	if legacy.name != "yolo_v8" || !legacy.legacyV8 {
		t.Fatalf("yolo_v8 registration = name %q legacy %v", legacy.name, legacy.legacyV8)
	}
}

func TestYOLOInitRejectsParamsBeforeRuntime(t *testing.T) {
	yolo, err := Get("yolo")
	if err != nil {
		t.Fatal(err)
	}
	err = yolo.Init(map[string]any{})
	if err == nil || !strings.Contains(err.Error(), `"model"`) || !strings.HasPrefix(err.Error(), "yolo:") {
		t.Fatalf("missing model: %v", err)
	}
	if strings.Contains(err.Error(), "onnxruntime") {
		t.Fatalf("missing model reached the runtime: %v", err)
	}

	alias, err := Get("yolo_v8")
	if err != nil {
		t.Fatal(err)
	}
	err = alias.Init(map[string]any{"model": filepath.Join(t.TempDir(), "missing.onnx")})
	if err == nil || !strings.HasPrefix(err.Error(), "yolo_v8:") || !strings.Contains(err.Error(), "model file") {
		t.Fatalf("missing file: %v", err)
	}

	model := filepath.Join(t.TempDir(), "m.onnx")
	if err := os.WriteFile(model, []byte("not a model"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		params map[string]any
		want   string
	}{
		{name: "pad high", params: map[string]any{"model": model, "pad_value": 256.0}, want: "pad_value"},
		{name: "pad fraction", params: map[string]any{"model": model, "pad_value": 1.5}, want: "pad_value"},
		{name: "pad negative", params: map[string]any{"model": model, "pad_value": -1.0}, want: "pad_value"},
		{name: "resize", params: map[string]any{"model": model, "resize": "bicubic"}, want: "resize"},
		{name: "layout", params: map[string]any{"model": model, "layout": "pose"}, want: "layout"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			p, err := Get("yolo")
			if err != nil {
				t.Fatal(err)
			}
			err = p.Init(tt.params)
			if err == nil || !strings.Contains(err.Error(), tt.want) || strings.Contains(err.Error(), "onnxruntime") {
				t.Fatalf("error = %v, want %q before the runtime", err, tt.want)
			}
		})
	}
}

func TestYOLOPrepareDefaults(t *testing.T) {
	model := filepath.Join(t.TempDir(), "m.onnx")
	if err := os.WriteFile(model, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	yolo := &YOLODetector{name: "yolo"}
	if err := yolo.prepare(map[string]any{"model": model}); err != nil {
		t.Fatal(err)
	}
	if yolo.pad != 114 || !yolo.bilinear || yolo.agnosticNMS || yolo.confThresh != 0.5 || yolo.iouThresh != 0.45 || yolo.processEvery != 1 {
		t.Fatalf("yolo defaults = pad %d bilinear %v agnostic %v conf %v iou %v every %d",
			yolo.pad, yolo.bilinear, yolo.agnosticNMS, yolo.confThresh, yolo.iouThresh, yolo.processEvery)
	}

	alias := &YOLODetector{name: "yolo_v8", legacyV8: true}
	if err := alias.prepare(map[string]any{
		"model":        model,
		"pad_value":    999.0,
		"resize":       "bilinear",
		"agnostic_nms": false,
		"layout":       "raw",
		"conf":         0.2,
		"iou":          0.3,
		"input_size":   320.0,
		"num_classes":  5.0,
		"input_name":   "",
	}); err != nil {
		t.Fatal(err)
	}
	if alias.pad != 0 || alias.bilinear || alias.agnosticNMS || alias.layoutParam != "raw" {
		t.Fatalf("alias preprocess = pad %d bilinear %v agnostic %v layout %q",
			alias.pad, alias.bilinear, alias.agnosticNMS, alias.layoutParam)
	}
	if alias.confThresh != 0.2 || alias.iouThresh != 0.3 || !alias.iouSet || alias.reqInputSize != 320 || alias.reqNumClasses != 5 {
		t.Fatalf("alias params = conf %v iou %v set %v size %d classes %d",
			alias.confThresh, alias.iouThresh, alias.iouSet, alias.reqInputSize, alias.reqNumClasses)
	}
	if alias.inputName != "" {
		t.Fatalf("empty input_name was stored as %q", alias.inputName)
	}
}

func TestYOLOAcceptDynamic(t *testing.T) {
	wrapped := fmt.Errorf("yolo: %w", errYOLODynamic)
	yolo := &YOLODetector{name: "yolo"}
	if err := yolo.acceptDynamic(wrapped); !errors.Is(err, errYOLODynamic) {
		t.Fatalf("new yolo kept a dynamic model: %v", err)
	}

	alias := &YOLODetector{name: "yolo_v8", legacyV8: true, layoutParam: "e2e"}
	err := alias.acceptDynamic(wrapped)
	if err == nil || !strings.Contains(err.Error(), "cannot be confirmed") {
		t.Fatalf("explicit e2e on a dynamic alias: %v", err)
	}

	alias = &YOLODetector{name: "yolo_v8", legacyV8: true, layoutParam: "auto", reqInputSize: 320, reqNumClasses: 5}
	if err := alias.acceptDynamic(wrapped); err != nil {
		t.Fatal(err)
	}
	// 320 → 40² + 20² + 10² = 2100.
	if alias.layout != YOLOLayoutRaw || alias.inputSize != 320 || alias.numClasses != 5 || alias.numPreds != 2100 {
		t.Fatalf("fallback = layout %v size %d classes %d preds %d", alias.layout, alias.inputSize, alias.numClasses, alias.numPreds)
	}
	if alias.inputName != "images" || alias.outputName != "output0" {
		t.Fatalf("fallback names = %s %s", alias.inputName, alias.outputName)
	}

	other := errors.New("two outputs")
	alias = &YOLODetector{name: "yolo_v8", legacyV8: true}
	if got := alias.acceptDynamic(other); got != other {
		t.Fatalf("non-dynamic error = %v", got)
	}
}

func TestYOLOPrefix(t *testing.T) {
	pose := fmt.Errorf("yolo: unsupported pose/OBB output shape [1 300 56]")
	yolo := &YOLODetector{name: "yolo"}
	if got := yolo.prefixYOLO(pose); got.Error() != pose.Error() {
		t.Fatalf("yolo prefix = %v", got)
	}
	alias := &YOLODetector{name: "yolo_v8"}
	got := alias.prefixYOLO(pose)
	if got.Error() != "yolo_v8: unsupported pose/OBB output shape [1 300 56]" {
		t.Fatalf("alias prefix = %v", got)
	}
	got = alias.prefixYOLO(errYOLODynamic)
	if !errors.Is(got, errYOLODynamic) || !strings.HasPrefix(got.Error(), "yolo_v8:") {
		t.Fatalf("dynamic prefix = %v", got)
	}
}

func TestYOLOInitModels(t *testing.T) {
	cases := []struct {
		node  string
		model string
	}{
		{node: "yolo_v8", model: "/models/yolov8n.onnx"},
		{node: "yolo", model: "/models/yolo11n.onnx"},
		{node: "yolo", model: "/models/yolo26n.onnx"},
	}
	for _, tt := range cases {
		t.Run(tt.model, func(t *testing.T) {
			if _, err := os.Stat(tt.model); err != nil {
				t.Skipf("model not found at %s", tt.model)
			}
			if err := onnxrt.Init(""); err != nil {
				t.Skipf("onnxruntime not available: %v", err)
			}
			p, err := Get(tt.node)
			if err != nil {
				t.Fatal(err)
			}
			if err := p.Init(map[string]any{"model": tt.model}); err != nil {
				t.Fatal(err)
			}
			if err := p.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestYOLODetectImageRejects(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))

	alias := &YOLODetector{name: "yolo_v8", legacyV8: true}
	if _, err := alias.DetectImage(img); err == nil || !strings.HasPrefix(err.Error(), "yolo_v8:") || !strings.Contains(err.Error(), "use yolo") {
		t.Fatalf("alias DetectImage: %v", err)
	}

	yolo := &YOLODetector{name: "yolo"}
	if _, err := yolo.DetectImage(img); err == nil || !strings.Contains(err.Error(), "before Init") {
		t.Fatalf("DetectImage before Init: %v", err)
	}
	if got := yolo.InputSize(); got != 0 {
		t.Fatalf("InputSize before Init = %d", got)
	}
}

// yoloTestModel returns a detect-task model for the yolo registration:
// MEDIAMOLDER_DETECT_YOLO_MODEL, else the first TestYOLOInitModels path
// that exists.
func yoloTestModel(t *testing.T) string {
	t.Helper()
	paths := []string{os.Getenv("MEDIAMOLDER_DETECT_YOLO_MODEL"), "/models/yolo26n.onnx", "/models/yolo11n.onnx"}
	for _, p := range paths {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Skip("no yolo model found (set MEDIAMOLDER_DETECT_YOLO_MODEL)")
	return ""
}

// firstFrame decodes the first video frame of path. The caller closes it.
func firstFrame(t *testing.T, path string) *av.Frame {
	t.Helper()
	input, err := av.OpenInput(path, nil)
	if err != nil {
		t.Skipf("test image: %v", err)
	}
	defer input.Close()
	vid := -1
	for i := 0; i < input.NumStreams(); i++ {
		if si, err := input.StreamInfo(i); err == nil && si.Type == av.MediaTypeVideo {
			vid = i
			break
		}
	}
	if vid < 0 {
		t.Fatalf("no video stream in %s", path)
	}
	dec, err := av.OpenDecoder(input, vid)
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	pkt, err := av.AllocPacket()
	if err != nil {
		t.Fatal(err)
	}
	defer pkt.Close()
	frame, err := av.AllocFrame()
	if err != nil {
		t.Fatal(err)
	}
	drained := false
	for dec.ReceiveFrame(frame) != nil {
		pkt.Unref()
		if err := input.ReadPacket(pkt); err != nil {
			if av.IsEOF(err) && !drained {
				_ = dec.Flush()
				drained = true
				continue
			}
			frame.Close()
			t.Fatalf("no frame decoded from %s: %v", path, err)
		}
		if pkt.StreamIndex() == vid {
			_ = dec.SendPacket(pkt)
		}
	}
	return frame
}

func TestYOLODetectImageMatchesProcess(t *testing.T) {
	model := yoloTestModel(t)
	imgPath := os.Getenv("MEDIAMOLDER_FACE_TEST_IMAGE")
	if imgPath == "" {
		t.Skip("set MEDIAMOLDER_FACE_TEST_IMAGE to a photo with a visible person")
	}
	if err := onnxrt.Init(""); err != nil {
		t.Skipf("onnxruntime not available: %v", err)
	}
	p, err := Get("yolo")
	if err != nil {
		t.Fatal(err)
	}
	yolo := p.(*YOLODetector)
	if err := yolo.Init(map[string]any{"model": model, "conf": 0.05}); err != nil {
		t.Fatal(err)
	}
	if yolo.InputSize() <= 0 {
		t.Fatalf("InputSize = %d", yolo.InputSize())
	}

	frame := firstFrame(t, imgPath)
	defer frame.Close()
	_, meta, err := yolo.Process(frame, ProcessorContext{MediaType: av.MediaTypeVideo})
	if err != nil {
		t.Fatal(err)
	}
	img, err := FrameToRGBA(frame)
	if err != nil {
		t.Fatal(err)
	}
	dets, err := yolo.DetectImage(img)
	if err != nil {
		t.Fatal(err)
	}
	if dets == nil {
		t.Fatal("DetectImage returned a nil slice")
	}
	if len(dets) == 0 {
		t.Fatal("no detections in the test image, so the comparison proves nothing")
	}
	if !reflect.DeepEqual(dets, meta.Detections) {
		t.Fatalf("DetectImage and Process differ:\n%+v\n%+v", dets, meta.Detections)
	}

	if err := yolo.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := yolo.DetectImage(img); err == nil || !strings.Contains(err.Error(), "after Close") {
		t.Fatalf("DetectImage after Close: %v", err)
	}
}
