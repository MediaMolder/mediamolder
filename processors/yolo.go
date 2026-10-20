// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// errYOLODynamic means the model shape is not concrete. New yolo fails
// Init with this message. The yolo_v8 alias falls back to the fixed
// input_size / num_classes formula instead.
var errYOLODynamic = errors.New("output shape is dynamic; re-export without dynamic=True")

// yoloMaxFloats is the largest tensor Init will allocate. A model that
// declares a larger shape fails with an error instead of panicking in make.
const yoloMaxFloats int64 = 64 << 20

// yoloFloatCount returns the element count of a dense float tensor.
// A non-positive dimension or a product above yoloMaxFloats is an error.
func yoloFloatCount(dims ...int64) (int, error) {
	n := int64(1)
	for _, d := range dims {
		if d < 1 || n > yoloMaxFloats/d {
			return 0, fmt.Errorf("shape %v exceeds %d elements", dims, yoloMaxFloats)
		}
		n *= d
	}
	return int(n), nil
}

// YOLOLayout is the detection-head layout of a model output tensor.
// Later heads (pose, OBB) can be added without renumbering these values.
type YOLOLayout int

const (
	// YOLOLayoutRaw is [1, 4+nc, N], centre-size boxes, NMS required.
	YOLOLayoutRaw YOLOLayout = iota
	// YOLOLayoutE2E is [1, maxDet, 6], corner boxes, NMS already applied.
	YOLOLayoutE2E
)

const (
	yoloNamesMaxBytes = 1 << 20
	yoloE2EMaxClassID = 10000
)

// DetectYOLOLayout classifies a rank-3 output shape [1, A, B].
// A dynamic dimension (-1) is rejected. B == 6 is end-to-end, including
// a square [1, 6, 6]. Otherwise B > A with A >= 5 is the raw head.
// A wider trailing dimension that is not raw is reported as pose/OBB.
func DetectYOLOLayout(outShape []int64) (YOLOLayout, error) {
	if len(outShape) != 3 {
		return 0, fmt.Errorf("yolo: unsupported output shape %v", outShape)
	}
	for _, d := range outShape {
		if d < 0 {
			return 0, fmt.Errorf("yolo: unsupported output shape %v", outShape)
		}
	}
	if outShape[0] != 1 {
		return 0, fmt.Errorf("yolo: unsupported output shape %v", outShape)
	}
	a, b := outShape[1], outShape[2]
	if b == 6 {
		return YOLOLayoutE2E, nil
	}
	if b > a && a >= 5 {
		return YOLOLayoutRaw, nil
	}
	if b > 6 && b <= a {
		return 0, fmt.Errorf("yolo: unsupported pose/OBB output shape %v", outShape)
	}
	return 0, fmt.Errorf("yolo: unsupported output shape %v", outShape)
}

// ParseYOLOE2EOutput decodes an end-to-end detection tensor.
//
// raw holds maxDet rows of [x1, y1, x2, y2, confidence, class_id] in
// model-input pixels. Rows below confThresh, non-finite rows, and class
// ids outside [0, 10000) are dropped. A class id is truncated toward zero.
// The label is labels[classID] when that entry exists, otherwise the
// decimal class index. Boxes are mapped back through the same float
// letterbox used by ParseYOLOv8Output.
func ParseYOLOE2EOutput(raw []float32, maxDet, inputSize int, confThresh float64, labels []string, origW, origH int) []Detection {
	if maxDet < 1 || len(raw)/6 < maxDet {
		return nil
	}
	unX, unY := unletterbox(inputSize, origW, origH)
	var dets []Detection
	for i := 0; i < maxDet; i++ {
		row := raw[i*6 : i*6+6]
		x1, y1, x2, y2 := float64(row[0]), float64(row[1]), float64(row[2]), float64(row[3])
		score := float64(row[4])
		if !finite(x1) || !finite(y1) || !finite(x2) || !finite(y2) || !finite(score) {
			continue
		}
		if score < confThresh {
			continue
		}
		classID, ok := e2eClassID(float64(row[5]))
		if !ok {
			continue
		}
		dets = append(dets, Detection{
			Label:      yoloLabel(labels, classID),
			ClassID:    classID,
			Confidence: score,
			BBox:       [4]float64{unX(x1), unY(y1), unX(x2), unY(y2)},
		})
	}
	return dets
}

func finite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

// e2eClassID truncates a class id toward zero. Values that are not finite,
// negative, or >= 10000 are rejected so the conversion cannot overflow.
func e2eClassID(v float64) (int, bool) {
	if !finite(v) || v < 0 || v >= yoloE2EMaxClassID {
		return 0, false
	}
	return int(v), true
}

// yoloLabel returns the display name for a class. A missing entry is the
// decimal index, never "unknown".
func yoloLabel(labels []string, classID int) string {
	if classID >= 0 && classID < len(labels) {
		return labels[classID]
	}
	return strconv.Itoa(classID)
}

// NMSPerClass is greedy NMS that suppresses a box only when it overlaps a
// higher-confidence box of the same ClassID. Distinct classes both survive,
// including when their labels are the digits of those class ids.
func NMSPerClass(dets []Detection, iouThresh float64) []Detection {
	if len(dets) == 0 {
		return nil
	}
	sorted := make([]Detection, len(dets))
	copy(sorted, dets)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Confidence > sorted[j].Confidence
	})
	var kept []Detection
	for _, d := range sorted {
		suppress := false
		for _, k := range kept {
			if d.ClassID == k.ClassID && IoU(d.BBox, k.BBox) > iouThresh {
				suppress = true
				break
			}
		}
		if !suppress {
			kept = append(kept, d)
		}
	}
	return kept
}

// parseYOLONames reads an Ultralytics `names` metadata string, a Python
// dict such as `{0: 'person', 1: 'bicycle'}`. Indices must be a dense
// range starting at 0 and each must lie in [0, maxEntries). The input is
// capped at 1 MiB. A truncated or oversized string is an error.
func parseYOLONames(s string, maxEntries int) ([]string, error) {
	if maxEntries < 1 {
		return nil, fmt.Errorf("yolo: names maxEntries must be positive")
	}
	if len(s) > yoloNamesMaxBytes {
		return nil, fmt.Errorf("yolo: names metadata exceeds 1 MiB")
	}
	p := nameParser{s: s}
	p.skip()
	if !p.consume('{') {
		return nil, fmt.Errorf("yolo: names metadata is truncated")
	}
	seen := make(map[int]string)
	entries := 0
	for {
		p.skip()
		if p.consume('}') {
			break
		}
		if entries > 0 {
			if !p.consume(',') {
				return nil, fmt.Errorf("yolo: names metadata is truncated")
			}
			p.skip()
			if p.consume('}') {
				break
			}
		}
		if p.eof() {
			return nil, fmt.Errorf("yolo: names metadata is truncated")
		}
		idx, ok := p.index()
		if !ok {
			return nil, fmt.Errorf("yolo: names metadata is truncated")
		}
		if idx < 0 || idx >= maxEntries {
			return nil, fmt.Errorf("yolo: names index %d outside [0, %d)", idx, maxEntries)
		}
		p.skip()
		if !p.consume(':') {
			return nil, fmt.Errorf("yolo: names metadata is truncated")
		}
		p.skip()
		name, ok := p.quoted()
		if !ok {
			return nil, fmt.Errorf("yolo: names metadata is truncated")
		}
		entries++
		if entries > maxEntries {
			return nil, fmt.Errorf("yolo: names metadata exceeds %d entries", maxEntries)
		}
		seen[idx] = name
	}
	p.skip()
	if !p.eof() {
		return nil, fmt.Errorf("yolo: names metadata is invalid")
	}
	names := make([]string, len(seen))
	for i := 0; i < len(names); i++ {
		name, ok := seen[i]
		if !ok {
			return nil, fmt.Errorf("yolo: names metadata is missing class %d", i)
		}
		names[i] = name
	}
	return names, nil
}

type nameParser struct {
	s string
	i int
}

func (p *nameParser) eof() bool { return p.i >= len(p.s) }

func (p *nameParser) skip() {
	for p.i < len(p.s) {
		switch p.s[p.i] {
		case ' ', '\t', '\n', '\r':
			p.i++
		default:
			return
		}
	}
}

func (p *nameParser) consume(c byte) bool {
	if p.i < len(p.s) && p.s[p.i] == c {
		p.i++
		return true
	}
	return false
}

func (p *nameParser) index() (int, bool) {
	neg := p.consume('-')
	if p.eof() || p.s[p.i] < '0' || p.s[p.i] > '9' {
		return 0, false
	}
	var n int64
	overflow := false
	for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
		d := int64(p.s[p.i] - '0')
		// Compare before n*10+d. On 64-bit, n is already int64, so a check
		// after the multiply never sees a value above MaxInt.
		if n > (int64(math.MaxInt)-d)/10 {
			overflow = true
			p.i++
			for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
				p.i++
			}
			break
		}
		n = n*10 + d
		p.i++
	}
	if overflow {
		return math.MaxInt, true
	}
	if neg {
		n = -n
	}
	return int(n), true
}

func (p *nameParser) quoted() (string, bool) {
	if p.eof() || (p.s[p.i] != '\'' && p.s[p.i] != '"') {
		return "", false
	}
	q := p.s[p.i]
	p.i++
	var b strings.Builder
	for p.i < len(p.s) {
		c := p.s[p.i]
		p.i++
		if c == '\\' {
			if p.eof() {
				return "", false
			}
			b.WriteByte(p.s[p.i])
			p.i++
			continue
		}
		if c == q {
			return b.String(), true
		}
		b.WriteByte(c)
	}
	return "", false
}

// yoloIO is one concrete tensor from an ONNX model, detached from the
// runtime types so layout checks can run without ONNX Runtime.
type yoloIO struct {
	Name     string
	Dims     []int64
	DataType string
}

// yoloModel is the decoded shape of a detection model.
type yoloModel struct {
	Layout     YOLOLayout
	InputName  string
	OutputName string
	InputSize  int
	NumClasses int
	NumPreds   int
	MaxDet     int
}

// interpretYOLOIO checks a single-input, single-output detection model.
// inputSize and numClasses are the user params; 0 means unset.
// namesOK is false when names were absent or failed to parse. A failed
// parse is not reported here. On an end-to-end head, numClasses is
// checked only when names parsed.
func interpretYOLOIO(inputs, outputs []yoloIO, task string, taskSet bool, names []string, namesOK bool, inputSize, numClasses int, inputName, outputName, layout string) (yoloModel, error) {
	if taskSet && task != "detect" {
		return yoloModel{}, fmt.Errorf("task %q is not detect", task)
	}
	if len(inputs) != 1 || len(outputs) != 1 {
		return yoloModel{}, fmt.Errorf("model has %d inputs and %d outputs, want 1 and 1", len(inputs), len(outputs))
	}
	in, out := inputs[0], outputs[0]
	if dynamicDims(in.Dims) || dynamicDims(out.Dims) {
		return yoloModel{}, errYOLODynamic
	}
	if in.DataType != "float32" {
		return yoloModel{}, fmt.Errorf("input data type %s, want float32", in.DataType)
	}
	if out.DataType != "float32" {
		return yoloModel{}, fmt.Errorf("output data type %s, want float32", out.DataType)
	}
	if len(in.Dims) != 4 || in.Dims[1] != 3 {
		return yoloModel{}, fmt.Errorf("input shape %v, want [1, 3, S, S]", in.Dims)
	}
	if in.Dims[0] != 1 {
		return yoloModel{}, fmt.Errorf("input batch %d, want 1", in.Dims[0])
	}
	if in.Dims[2] != in.Dims[3] || in.Dims[2] < 1 {
		return yoloModel{}, fmt.Errorf("input shape %v is not square", in.Dims)
	}
	if len(out.Dims) != 3 {
		return yoloModel{}, fmt.Errorf("output shape %v, want rank 3", out.Dims)
	}
	if out.Dims[0] != 1 {
		return yoloModel{}, fmt.Errorf("output batch %d, want 1", out.Dims[0])
	}
	yoloLayout, err := DetectYOLOLayout(out.Dims)
	if err != nil {
		return yoloModel{}, err
	}
	switch layout {
	case "", "auto":
	case "raw":
		if yoloLayout != YOLOLayoutRaw {
			return yoloModel{}, fmt.Errorf("layout %q does not match output shape %v", layout, out.Dims)
		}
	case "e2e":
		if yoloLayout != YOLOLayoutE2E {
			return yoloModel{}, fmt.Errorf("layout %q does not match output shape %v", layout, out.Dims)
		}
	default:
		return yoloModel{}, fmt.Errorf("layout %q is not auto, raw, or e2e", layout)
	}
	s := int(in.Dims[2])
	if inputSize > 0 && inputSize != s {
		return yoloModel{}, fmt.Errorf("input_size %d does not match model (%d)", inputSize, s)
	}
	spec := yoloModel{
		Layout:     yoloLayout,
		InputName:  in.Name,
		OutputName: out.Name,
		InputSize:  s,
	}
	if inputName != "" {
		spec.InputName = inputName
	}
	if outputName != "" {
		spec.OutputName = outputName
	}
	if yoloLayout == YOLOLayoutE2E {
		spec.MaxDet = int(out.Dims[1])
		if namesOK {
			spec.NumClasses = len(names)
			if numClasses > 0 && numClasses != len(names) {
				return yoloModel{}, fmt.Errorf("num_classes %d does not match model (%d)", numClasses, len(names))
			}
		}
		return spec, nil
	}
	spec.NumClasses = int(out.Dims[1]) - 4
	spec.NumPreds = int(out.Dims[2])
	if spec.NumClasses < 1 || spec.NumPreds < 1 {
		return yoloModel{}, fmt.Errorf("output shape %v has no classes", out.Dims)
	}
	if namesOK && len(names) != spec.NumClasses {
		return yoloModel{}, fmt.Errorf("names list has %d classes, model has %d", len(names), spec.NumClasses)
	}
	if numClasses > 0 && numClasses != spec.NumClasses {
		return yoloModel{}, fmt.Errorf("num_classes %d does not match model (%d)", numClasses, spec.NumClasses)
	}
	return spec, nil
}

func dynamicDims(dims []int64) bool {
	for _, d := range dims {
		if d < 0 {
			return true
		}
	}
	return false
}

// yoloExternalDataError names the sibling .onnx.data file when err is an
// ONNX Runtime external-data failure. A nil result means err is some
// other failure and the caller should keep it.
func yoloExternalDataError(name, modelPath string, err error) error {
	if err == nil || !yoloExternalData(err) {
		return nil
	}
	return fmt.Errorf("%s: model uses external data (%s); re-export a single-file ONNX: %w", name, modelPath+".data", err)
}

func yoloExternalData(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "external data") || strings.Contains(s, "external_data")
}

// decodeYOLO turns a model output into detections. legacy keeps the
// yolo_v8 label rule ("unknown" on a miss) and agnostic selects
// class-agnostic NMS. An end-to-end head is already de-duplicated.
func decodeYOLO(layout YOLOLayout, raw []float32, numClasses, numPreds, maxDet, inputSize int, confThresh, iouThresh float64, labels []string, origW, origH int, legacy, agnostic bool) []Detection {
	var dets []Detection
	if layout == YOLOLayoutE2E {
		dets = ParseYOLOE2EOutput(raw, maxDet, inputSize, confThresh, labels, origW, origH)
		if legacy {
			for i := range dets {
				if dets[i].ClassID < 0 || dets[i].ClassID >= len(labels) {
					dets[i].Label = "unknown"
				}
			}
		}
		return dets
	}
	dets = ParseYOLOv8Output(raw, numClasses, numPreds, inputSize, confThresh, labels, origW, origH)
	if !legacy {
		for i := range dets {
			if dets[i].ClassID < 0 || dets[i].ClassID >= len(labels) {
				dets[i].Label = strconv.Itoa(dets[i].ClassID)
			}
		}
	}
	if agnostic {
		return NMS(dets, iouThresh)
	}
	return NMSPerClass(dets, iouThresh)
}

// yoloResultMetadata builds the Process result. The alias returns nil
// when nothing was found. yolo returns a non-nil metadata value with an
// empty detection list, which JSON still omits.
func yoloResultMetadata(legacy bool, dets []Detection) *Metadata {
	if legacy {
		if len(dets) == 0 {
			return nil
		}
		return &Metadata{Detections: dets}
	}
	if dets == nil {
		dets = []Detection{}
	}
	return &Metadata{Detections: dets}
}
