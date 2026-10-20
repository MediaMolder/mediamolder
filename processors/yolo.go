// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

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
