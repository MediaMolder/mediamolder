//go:build with_onnx

// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/internal/onnxrt"
	ort "github.com/yalue/onnxruntime_go"
)

// YOLODetector runs YOLO object detection via ONNX Runtime.
// It is only compiled when the "with_onnx" build tag is set.
//
// The yolo registration reads the model shape, uses per-class NMS on a
// raw head, and skips NMS on an end-to-end head. The yolo_v8 registration
// is the permanent alias: class-agnostic NMS, "unknown" labels unless
// labels_file is set, the original black nearest-neighbour letterbox,
// and nil metadata when a frame has no detections.
type YOLODetector struct {
	name     string
	legacyV8 bool

	session      *ort.AdvancedSession
	inputTensor  *ort.Tensor[float32]
	outputTensor *ort.Tensor[float32]

	labels       []string
	confThresh   float64
	iouThresh    float64
	inputSize    int
	numClasses   int
	numPreds     int
	maxDet       int
	layout       YOLOLayout
	agnosticNMS  bool
	pad          uint8
	bilinear     bool
	processEvery uint64

	modelPath     string
	ortLib        string
	inputName     string
	outputName    string
	reqInputSize  int
	reqNumClasses int
	layoutParam   string
	iouSet        bool
}

func (p *YOLODetector) Init(params map[string]any) error {
	if p.name == "" {
		if p.legacyV8 {
			p.name = "yolo_v8"
		} else {
			p.name = "yolo"
		}
	}
	if err := p.prepare(params); err != nil {
		return err
	}
	if err := onnxrt.Init(p.ortLib); err != nil {
		return fmt.Errorf("%s: onnxruntime init: %w", p.name, err)
	}
	data, err := os.ReadFile(p.modelPath)
	if err != nil {
		return fmt.Errorf("%s: model file: %w", p.name, err)
	}
	if err := p.introspect(data); err != nil {
		if err = p.acceptDynamic(err); err != nil {
			return err
		}
	}
	if p.layout == YOLOLayoutE2E && p.iouSet {
		fmt.Fprintf(os.Stderr, "%s: iou is ignored for an end-to-end model\n", p.name)
	}
	return p.bindSession(data, params)
}

func (p *YOLODetector) prepare(params map[string]any) error {
	modelPath, _ := params["model"].(string)
	if modelPath == "" {
		return fmt.Errorf("%s: \"model\" param is required (path to .onnx file)", p.name)
	}
	if _, err := os.Stat(modelPath); err != nil {
		return fmt.Errorf("%s: model file: %w", p.name, err)
	}
	p.modelPath = modelPath
	p.confThresh = 0.5
	p.iouThresh = 0.45
	p.processEvery = 1
	p.agnosticNMS = p.legacyV8
	p.pad = 114
	p.bilinear = true
	if p.legacyV8 {
		p.pad = 0
		p.bilinear = false
	}
	p.ortLib, _ = params["ort_lib"].(string)
	if v, ok := params["conf"].(float64); ok && v > 0 {
		p.confThresh = v
	}
	if _, ok := params["iou"]; ok {
		p.iouSet = true
	}
	if v, ok := params["iou"].(float64); ok && v > 0 {
		p.iouThresh = v
	}
	if v, ok := params["input_size"].(float64); ok && v > 0 {
		p.reqInputSize = int(v)
	}
	if v, ok := params["num_classes"].(float64); ok && v > 0 {
		p.reqNumClasses = int(v)
	}
	if v, ok := params["process_every"].(float64); ok && v >= 1 {
		p.processEvery = uint64(v)
	}
	if v, ok := params["input_name"].(string); ok {
		p.inputName = v
	}
	if v, ok := params["output_name"].(string); ok {
		p.outputName = v
	}
	if v, ok := params["layout"].(string); ok {
		p.layoutParam = v
	}
	switch p.layoutParam {
	case "", "auto", "raw", "e2e":
	default:
		return fmt.Errorf("%s: layout %q is not auto, raw, or e2e", p.name, p.layoutParam)
	}
	if v, ok := params["agnostic_nms"].(bool); ok {
		p.agnosticNMS = v
	}
	if !p.legacyV8 {
		if v, ok := params["pad_value"].(float64); ok {
			if v < 0 || v > 255 || v != float64(int(v)) {
				return fmt.Errorf("%s: pad_value %v is outside 0–255", p.name, v)
			}
			p.pad = uint8(v)
		}
		if v, ok := params["resize"].(string); ok && v != "" {
			switch v {
			case "nearest":
				p.bilinear = false
			case "bilinear":
				p.bilinear = true
			default:
				return fmt.Errorf("%s: resize %q is not bilinear or nearest", p.name, v)
			}
		}
	}
	if lf, ok := params["labels_file"].(string); ok && lf != "" {
		data, err := os.ReadFile(lf)
		if err != nil {
			return fmt.Errorf("%s: labels file: %w", p.name, err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if lbl := strings.TrimSpace(line); lbl != "" {
				p.labels = append(p.labels, lbl)
			}
		}
	}
	return nil
}

// acceptDynamic keeps a yolo_v8 graph working when the export has no
// concrete shape. An explicit end-to-end layout cannot be confirmed
// from a dynamic graph, so that request still fails. New yolo fails
// on every dynamic shape.
func (p *YOLODetector) acceptDynamic(err error) error {
	if !p.legacyV8 || !errors.Is(err, errYOLODynamic) {
		return err
	}
	if p.layoutParam == "e2e" {
		return fmt.Errorf("%s: layout %q cannot be confirmed for a dynamic model; re-export without dynamic=True", p.name, p.layoutParam)
	}
	p.applyLegacyFormula()
	return nil
}

func (p *YOLODetector) applyLegacyFormula() {
	p.layout = YOLOLayoutRaw
	p.inputSize = 640
	if p.reqInputSize > 0 {
		p.inputSize = p.reqInputSize
	}
	p.numClasses = 80
	if p.reqNumClasses > 0 {
		p.numClasses = p.reqNumClasses
	}
	s := p.inputSize
	p.numPreds = (s/8)*(s/8) + (s/16)*(s/16) + (s/32)*(s/32)
	p.maxDet = 0
	if p.inputName == "" {
		p.inputName = "images"
	}
	if p.outputName == "" {
		p.outputName = "output0"
	}
}

func (p *YOLODetector) introspect(data []byte) error {
	inputs, outputs, err := ort.GetInputOutputInfoWithONNXData(data)
	if err != nil {
		if ext := yoloExternalDataError(p.name, p.modelPath, err); ext != nil {
			return ext
		}
		return fmt.Errorf("%s: %w: %v", p.name, errYOLODynamic, err)
	}
	meta, err := ort.GetModelMetadataWithONNXData(data)
	if err != nil {
		if ext := yoloExternalDataError(p.name, p.modelPath, err); ext != nil {
			return ext
		}
		return fmt.Errorf("%s: model metadata: %w", p.name, err)
	}
	defer meta.Destroy()

	task, taskSet, err := meta.LookupCustomMetadataMap("task")
	if err != nil {
		return fmt.Errorf("%s: model metadata: %w", p.name, err)
	}
	var names []string
	namesOK := false
	if !p.legacyV8 && len(outputs) == 1 {
		if layout, layoutErr := DetectYOLOLayout(outputs[0].Dimensions); layoutErr == nil {
			maxEntries := yoloE2EMaxClassID
			if layout == YOLOLayoutRaw && len(outputs[0].Dimensions) > 1 {
				maxEntries = int(outputs[0].Dimensions[1]) - 4
			}
			if maxEntries >= 1 {
				if s, ok, err := meta.LookupCustomMetadataMap("names"); err != nil {
					return fmt.Errorf("%s: model metadata: %w", p.name, err)
				} else if ok {
					parsed, err := parseYOLONames(s, maxEntries)
					if err == nil {
						names = parsed
						namesOK = true
					}
				}
			}
		}
	}
	spec, err := interpretYOLOIO(
		yoloIOs(inputs), yoloIOs(outputs),
		task, taskSet, names, namesOK,
		p.reqInputSize, p.reqNumClasses,
		p.inputName, p.outputName, p.layoutParam,
	)
	if err != nil {
		return p.prefixYOLO(err)
	}
	p.layout = spec.Layout
	p.inputSize = spec.InputSize
	p.numClasses = spec.NumClasses
	p.numPreds = spec.NumPreds
	p.maxDet = spec.MaxDet
	p.inputName = spec.InputName
	p.outputName = spec.OutputName
	if len(p.labels) == 0 && namesOK {
		p.labels = names
	}
	return nil
}

func yoloIOs(infos []ort.InputOutputInfo) []yoloIO {
	out := make([]yoloIO, len(infos))
	for i, info := range infos {
		dt := info.DataType.String()
		if info.DataType == ort.TensorElementDataTypeFloat {
			dt = "float32"
		}
		dims := make([]int64, len(info.Dimensions))
		copy(dims, info.Dimensions)
		out[i] = yoloIO{Name: info.Name, Dims: dims, DataType: dt}
	}
	return out
}

// prefixYOLO attaches the registered node name once. DetectYOLOLayout
// already starts its messages with "yolo:", so the alias replaces that
// word instead of stacking a second prefix.
func (p *YOLODetector) prefixYOLO(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.HasPrefix(msg, p.name+":") {
		return err
	}
	const head = "yolo: "
	if strings.HasPrefix(msg, head) {
		return fmt.Errorf("%s: %s", p.name, msg[len(head):])
	}
	return fmt.Errorf("%s: %w", p.name, err)
}

func (p *YOLODetector) bindSession(data []byte, params map[string]any) error {
	inSize := int64(p.inputSize)
	inCount, err := yoloFloatCount(1, 3, inSize, inSize)
	if err != nil {
		return fmt.Errorf("%s: input %w", p.name, err)
	}
	var outDims []int64
	if p.layout == YOLOLayoutE2E {
		outDims = []int64{1, int64(p.maxDet), 6}
	} else {
		outDims = []int64{1, int64(p.numClasses) + 4, int64(p.numPreds)}
	}
	if _, err := yoloFloatCount(outDims...); err != nil {
		return fmt.Errorf("%s: output %w", p.name, err)
	}
	inputShape := ort.NewShape(1, 3, inSize, inSize)
	p.inputTensor, err = ort.NewTensor(inputShape, make([]float32, inCount))
	if err != nil {
		return fmt.Errorf("%s: input tensor: %w", p.name, err)
	}
	outputShape := ort.NewShape(outDims...)
	p.outputTensor, err = ort.NewEmptyTensor[float32](outputShape)
	if err != nil {
		p.cleanup()
		return fmt.Errorf("%s: output tensor: %w", p.name, err)
	}

	var opts *ort.SessionOptions
	device, _ := params["device"].(string)
	if strings.HasPrefix(device, "cuda") {
		opts, err = ort.NewSessionOptions()
		if err != nil {
			p.cleanup()
			return fmt.Errorf("%s: session options: %w", p.name, err)
		}
		defer opts.Destroy()
		cudaOpts, err := ort.NewCUDAProviderOptions()
		if err != nil {
			p.cleanup()
			return fmt.Errorf("%s: CUDA provider: %w", p.name, err)
		}
		defer cudaOpts.Destroy()
		if err := opts.AppendExecutionProviderCUDA(cudaOpts); err != nil {
			p.cleanup()
			return fmt.Errorf("%s: CUDA append: %w", p.name, err)
		}
	}

	p.session, err = ort.NewAdvancedSessionWithONNXData(
		data,
		[]string{p.inputName}, []string{p.outputName},
		[]ort.Value{p.inputTensor}, []ort.Value{p.outputTensor},
		opts,
	)
	if err != nil {
		p.cleanup()
		if ext := yoloExternalDataError(p.name, p.modelPath, err); ext != nil {
			return ext
		}
		return fmt.Errorf("%s: session create: %w", p.name, err)
	}
	return nil
}

func (p *YOLODetector) Process(frame *av.Frame, ctx ProcessorContext) (*av.Frame, *Metadata, error) {
	if ctx.MediaType != av.MediaTypeVideo {
		return frame, nil, nil
	}
	if p.processEvery > 1 && ctx.FrameIndex%p.processEvery != 0 {
		return frame, nil, nil
	}

	var tensor []float32
	var err error
	if p.legacyV8 {
		tensor, err = FrameToFloat32Tensor(frame, p.inputSize)
	} else {
		img, err2 := FrameToRGBA(frame)
		if err2 != nil {
			err = err2
		} else {
			mode := ResizeNearest
			if p.bilinear {
				mode = ResizeBilinear
			}
			tensor = LetterboxTensor(img, p.inputSize, p.pad, mode)
		}
	}
	if err != nil {
		return nil, nil, fmt.Errorf("%s: preprocess: %w", p.name, err)
	}

	inSlice := p.inputTensor.GetData()
	if len(tensor) != len(inSlice) {
		return nil, nil, fmt.Errorf("%s: preprocess produced %d values, want %d", p.name, len(tensor), len(inSlice))
	}
	copy(inSlice, tensor)
	if err := p.session.Run(); err != nil {
		return nil, nil, fmt.Errorf("%s: inference: %w", p.name, err)
	}
	dets := decodeYOLO(p.layout, p.outputTensor.GetData(), p.numClasses, p.numPreds, p.maxDet, p.inputSize,
		p.confThresh, p.iouThresh, p.labels, frame.Width(), frame.Height(), p.legacyV8, p.agnosticNMS)
	return frame, yoloResultMetadata(p.legacyV8, dets), nil
}

func (p *YOLODetector) Close() error {
	p.cleanup()
	return nil
}

func (p *YOLODetector) cleanup() {
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
	Register("yolo", func() Processor {
		return &YOLODetector{name: "yolo"}
	})
	Register("yolo_v8", func() Processor {
		return &YOLODetector{name: "yolo_v8", legacyV8: true}
	})
}
