// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"math"
	"sort"
)

// ParseYOLOv8Output decodes raw YOLOv8 model output into detections.
//
// YOLOv8 detection models produce output in shape [1, 4+numClasses, numPreds]
// in column-major (transposed) layout. For a standard COCO model with 640×640
// input this is [1, 84, 8400].
//
// The raw slice has length (4+numClasses)*numPreds. For prediction i and
// attribute j, the value is at raw[j*numPreds + i].
//
// Attributes 0–3 are (cx, cy, w, h) in input-image pixel coordinates.
// Attributes 4..4+numClasses-1 are per-class confidence scores.
//
// origW and origH are the original frame dimensions before letterboxing.
// Returned BBox values are in original-frame pixel coordinates.
func ParseYOLOv8Output(raw []float32, numClasses, numPreds, inputSize int, confThresh float64, labels []string, origW, origH int) []Detection {
	attrCount := 4 + numClasses
	if len(raw) < attrCount*numPreds {
		return nil
	}

	// Compute letterbox reverse-mapping parameters.
	scale := math.Min(float64(inputSize)/float64(origW), float64(inputSize)/float64(origH))
	newW := math.Round(float64(origW) * scale)
	newH := math.Round(float64(origH) * scale)
	offsetX := (float64(inputSize) - newW) / 2.0
	offsetY := (float64(inputSize) - newH) / 2.0

	var dets []Detection
	for i := 0; i < numPreds; i++ {
		// Find best class.
		bestScore := float64(0)
		bestClass := 0
		for c := 0; c < numClasses; c++ {
			s := float64(raw[(4+c)*numPreds+i])
			if s > bestScore {
				bestScore = s
				bestClass = c
			}
		}
		if bestScore < confThresh {
			continue
		}

		// Extract box centre + size in model-input coordinates.
		cx := float64(raw[0*numPreds+i])
		cy := float64(raw[1*numPreds+i])
		w := float64(raw[2*numPreds+i])
		h := float64(raw[3*numPreds+i])

		// Convert to corner coordinates in model-input space.
		x1 := cx - w/2
		y1 := cy - h/2
		x2 := cx + w/2
		y2 := cy + h/2

		// Reverse letterbox: model coords → original frame pixels.
		x1 = (x1 - offsetX) / scale
		y1 = (y1 - offsetY) / scale
		x2 = (x2 - offsetX) / scale
		y2 = (y2 - offsetY) / scale

		// Clamp to frame bounds.
		x1 = math.Max(0, math.Min(x1, float64(origW)))
		y1 = math.Max(0, math.Min(y1, float64(origH)))
		x2 = math.Max(0, math.Min(x2, float64(origW)))
		y2 = math.Max(0, math.Min(y2, float64(origH)))

		label := "unknown"
		if bestClass < len(labels) {
			label = labels[bestClass]
		}

		dets = append(dets, Detection{
			Label:      label,
			Confidence: bestScore,
			BBox:       [4]float64{x1, y1, x2, y2},
		})
	}

	return dets
}

// NMS performs greedy non-maximum suppression. Detections are sorted by
// descending confidence and lower-confidence boxes that overlap a kept box
// by more than iouThresh are suppressed.
func NMS(dets []Detection, iouThresh float64) []Detection {
	if len(dets) == 0 {
		return nil
	}
	// Sort by confidence descending.
	sorted := make([]Detection, len(dets))
	copy(sorted, dets)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Confidence > sorted[j].Confidence
	})

	var kept []Detection
	for _, d := range sorted {
		suppress := false
		for _, k := range kept {
			if IoU(d.BBox, k.BBox) > iouThresh {
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

// IoU computes the intersection-over-union of two bounding boxes given as
// [x1, y1, x2, y2] in pixel coordinates.
func IoU(a, b [4]float64) float64 {
	ix1 := math.Max(a[0], b[0])
	iy1 := math.Max(a[1], b[1])
	ix2 := math.Min(a[2], b[2])
	iy2 := math.Min(a[3], b[3])

	iw := math.Max(0, ix2-ix1)
	ih := math.Max(0, iy2-iy1)
	inter := iw * ih

	areaA := (a[2] - a[0]) * (a[3] - a[1])
	areaB := (b[2] - b[0]) * (b[3] - b[1])
	union := areaA + areaB - inter
	if union <= 0 {
		return 0
	}
	return inter / union
}
