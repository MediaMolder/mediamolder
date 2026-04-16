//go:build with_onnx

// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/MediaMolder/MediaMolder/av"
	ort "github.com/yalue/onnxruntime_go"
)

var ortOnce sync.Once

// initONNXRuntime initialises the ONNX Runtime environment exactly once.
// libPath is the path to the onnxruntime shared library (e.g.
// libonnxruntime.so.1.24.1). If empty, the default search path is used.
func initONNXRuntime(libPath string) error {
	var initErr error
	ortOnce.Do(func() {
		if libPath != "" {
			ort.SetSharedLibraryPath(libPath)
		}
		initErr = ort.InitializeEnvironment()
	})
	return initErr
}

// YOLOv8Detector is a go_processor that runs YOLOv8 object detection via
// ONNX Runtime. It is only compiled when the "with_onnx" build tag is set.
//
// Required params:
//
//	"model"      — path to the YOLOv8 ONNX model file (required)
//
// Optional params:
//
//	"conf"        — confidence threshold, default 0.5
//	"iou"         — NMS IoU threshold, default 0.45
//	"input_size"  — model input dimension, default 640
//	"num_classes" — number of classes the model detects, default 80
//	"labels_file" — path to newline-separated class label file
//	"input_name"  — ONNX input tensor name, default "images"
//	"output_name" — ONNX output tensor name, default "output0"
//	"ort_lib"       — path to onnxruntime shared library (or set ONNXRUNTIME_SHARED_LIBRARY_PATH env)
//	"device"        — "cpu" (default) or "cuda" / "cuda:N"
//	"process_every" — run inference every N video frames, default 1 (every frame). Non-processed frames pass through with no metadata.
type YOLOv8Detector struct {
	session      *ort.AdvancedSession
	inputTensor  *ort.Tensor[float32]
	outputTensor *ort.Tensor[float32]

	labels       []string
	confThresh   float64
	iouThresh    float64
	inputSize    int
	numClasses   int
	numPreds     int    // derived: depends on inputSize
	processEvery uint64 // run inference every N frames (1 = every frame)
}

func (p *YOLOv8Detector) Init(params map[string]any) error {
	modelPath, _ := params["model"].(string)
	if modelPath == "" {
		return fmt.Errorf("yolo_v8: \"model\" param is required (path to .onnx file)")
	}
	if _, err := os.Stat(modelPath); err != nil {
		return fmt.Errorf("yolo_v8: model file: %w", err)
	}

	// Defaults.
	p.confThresh = 0.5
	p.iouThresh = 0.45
	p.inputSize = 640
	p.numClasses = 80
	p.processEvery = 1

	if v, ok := params["conf"].(float64); ok && v > 0 {
		p.confThresh = v
	}
	if v, ok := params["iou"].(float64); ok && v > 0 {
		p.iouThresh = v
	}
	if v, ok := params["input_size"].(float64); ok && v > 0 {
		p.inputSize = int(v)
	}
	if v, ok := params["num_classes"].(float64); ok && v > 0 {
		p.numClasses = int(v)
	}
	if v, ok := params["process_every"].(float64); ok && v >= 1 {
		p.processEvery = uint64(v)
	}

	// YOLOv8 prediction count for a given input size.
	// Default 640 → grid sizes 80²+40²+20² = 8400.
	s := p.inputSize
	p.numPreds = (s/8)*(s/8) + (s/16)*(s/16) + (s/32)*(s/32)

	// Load labels.
	if lf, ok := params["labels_file"].(string); ok && lf != "" {
		data, err := os.ReadFile(lf)
		if err != nil {
			return fmt.Errorf("yolo_v8: labels file: %w", err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if lbl := strings.TrimSpace(line); lbl != "" {
				p.labels = append(p.labels, lbl)
			}
		}
	}

	// ONNX Runtime init.
	ortLib, _ := params["ort_lib"].(string)
	if ortLib == "" {
		ortLib = os.Getenv("ONNXRUNTIME_SHARED_LIBRARY_PATH")
	}
	if err := initONNXRuntime(ortLib); err != nil {
		return fmt.Errorf("yolo_v8: onnxruntime init: %w", err)
	}

	// Tensor names.
	inputName := "images"
	outputName := "output0"
	if v, ok := params["input_name"].(string); ok && v != "" {
		inputName = v
	}
	if v, ok := params["output_name"].(string); ok && v != "" {
		outputName = v
	}

	// Pre-allocate tensors.
	inSize := int64(p.inputSize)
	inputShape := ort.NewShape(1, 3, inSize, inSize)
	inputData := make([]float32, 3*p.inputSize*p.inputSize)
	var err error
	p.inputTensor, err = ort.NewTensor(inputShape, inputData)
	if err != nil {
		return fmt.Errorf("yolo_v8: input tensor: %w", err)
	}

	outCols := int64(p.numPreds)
	outRows := int64(4 + p.numClasses)
	outputShape := ort.NewShape(1, outRows, outCols)
	p.outputTensor, err = ort.NewEmptyTensor[float32](outputShape)
	if err != nil {
		p.inputTensor.Destroy()
		return fmt.Errorf("yolo_v8: output tensor: %w", err)
	}

	// Session options (CUDA if requested).
	var opts *ort.SessionOptions
	device, _ := params["device"].(string)
	if strings.HasPrefix(device, "cuda") {
		opts, err = ort.NewSessionOptions()
		if err != nil {
			p.cleanup()
			return fmt.Errorf("yolo_v8: session options: %w", err)
		}
		defer opts.Destroy()
		cudaOpts, err := ort.NewCUDAProviderOptions()
		if err != nil {
			p.cleanup()
			return fmt.Errorf("yolo_v8: CUDA provider: %w", err)
		}
		defer cudaOpts.Destroy()
		if err := opts.AppendExecutionProviderCUDA(cudaOpts); err != nil {
			p.cleanup()
			return fmt.Errorf("yolo_v8: CUDA append: %w", err)
		}
	}

	p.session, err = ort.NewAdvancedSession(
		modelPath,
		[]string{inputName}, []string{outputName},
		[]ort.Value{p.inputTensor}, []ort.Value{p.outputTensor},
		opts,
	)
	if err != nil {
		p.cleanup()
		return fmt.Errorf("yolo_v8: session create: %w", err)
	}
	return nil
}

func (p *YOLOv8Detector) Process(frame *av.Frame, ctx ProcessorContext) (*av.Frame, *Metadata, error) {
	if ctx.MediaType != av.MediaTypeVideo {
		return frame, nil, nil
	}

	// Skip inference on non-Nth frames (pass frame through unchanged).
	if p.processEvery > 1 && ctx.FrameIndex%p.processEvery != 0 {
		return frame, nil, nil
	}

	// Preprocess: frame → float32 tensor.
	tensor, err := FrameToFloat32Tensor(frame, p.inputSize)
	if err != nil {
		return nil, nil, fmt.Errorf("yolo_v8: preprocess: %w", err)
	}

	// Copy into pre-allocated input tensor.
	inSlice := p.inputTensor.GetData()
	copy(inSlice, tensor)

	// Run inference.
	if err := p.session.Run(); err != nil {
		return nil, nil, fmt.Errorf("yolo_v8: inference: %w", err)
	}

	// Parse detections from output tensor.
	raw := p.outputTensor.GetData()
	dets := ParseYOLOv8Output(raw, p.numClasses, p.numPreds, p.inputSize,
		p.confThresh, p.labels, frame.Width(), frame.Height())
	dets = NMS(dets, p.iouThresh)

	var md *Metadata
	if len(dets) > 0 {
		md = &Metadata{Detections: dets}
	}
	return frame, md, nil
}

func (p *YOLOv8Detector) Close() error {
	p.cleanup()
	return nil
}

func (p *YOLOv8Detector) cleanup() {
	if p.session != nil {
		p.session.Destroy()
		p.session = nil
	}
	if p.outputTensor != nil {
		p.outputTensor.Destroy()
		p.outputTensor = nil
	}
	if p.inputTensor != nil {
		p.inputTensor.Destroy()
		p.inputTensor = nil
	}
}

func init() {
	Register("yolo_v8", func() Processor { return &YOLOv8Detector{} })
}
