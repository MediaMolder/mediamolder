// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"math"
	"testing"
)

// --- IoU ---

func TestIoU_IdenticalBoxes(t *testing.T) {
	box := [4]float64{10, 10, 50, 50}
	got := IoU(box, box)
	if math.Abs(got-1.0) > 1e-9 {
		t.Fatalf("IoU of identical boxes = %f, want 1.0", got)
	}
}

func TestIoU_NoOverlap(t *testing.T) {
	a := [4]float64{0, 0, 10, 10}
	b := [4]float64{20, 20, 30, 30}
	got := IoU(a, b)
	if got != 0 {
		t.Fatalf("IoU of non-overlapping boxes = %f, want 0", got)
	}
}

func TestIoU_PartialOverlap(t *testing.T) {
	a := [4]float64{0, 0, 10, 10} // area = 100
	b := [4]float64{5, 5, 15, 15} // area = 100
	// Intersection: [5,5]→[10,10] = 5×5 = 25
	// Union: 100 + 100 - 25 = 175
	expected := 25.0 / 175.0
	got := IoU(a, b)
	if math.Abs(got-expected) > 1e-9 {
		t.Fatalf("IoU = %f, want %f", got, expected)
	}
}

func TestIoU_ContainedBox(t *testing.T) {
	outer := [4]float64{0, 0, 100, 100} // area = 10000
	inner := [4]float64{10, 10, 30, 30} // area = 400
	// Intersection = 400, Union = 10000
	expected := 400.0 / 10000.0
	got := IoU(outer, inner)
	if math.Abs(got-expected) > 1e-9 {
		t.Fatalf("IoU = %f, want %f", got, expected)
	}
}

func TestIoU_ZeroArea(t *testing.T) {
	a := [4]float64{5, 5, 5, 5} // zero area
	b := [4]float64{0, 0, 10, 10}
	got := IoU(a, b)
	if got != 0 {
		t.Fatalf("IoU with zero-area box = %f, want 0", got)
	}
}

// --- NMS ---

func TestNMS_Empty(t *testing.T) {
	got := NMS(nil, 0.5)
	if got != nil {
		t.Fatalf("NMS(nil) = %v, want nil", got)
	}
}

func TestNMS_SingleDetection(t *testing.T) {
	dets := []Detection{{Label: "cat", Confidence: 0.9, BBox: [4]float64{0, 0, 50, 50}}}
	got := NMS(dets, 0.5)
	if len(got) != 1 {
		t.Fatalf("NMS single det: got %d results, want 1", len(got))
	}
}

func TestNMS_SuppressOverlapping(t *testing.T) {
	dets := []Detection{
		{Label: "cat", Confidence: 0.9, BBox: [4]float64{0, 0, 50, 50}},
		{Label: "cat", Confidence: 0.8, BBox: [4]float64{2, 2, 52, 52}},       // nearly identical -> suppress
		{Label: "dog", Confidence: 0.7, BBox: [4]float64{200, 200, 300, 300}}, // far away -> keep
	}
	got := NMS(dets, 0.5)
	if len(got) != 2 {
		t.Fatalf("NMS: got %d results, want 2", len(got))
	}
	if got[0].Confidence != 0.9 {
		t.Errorf("first kept det confidence = %f, want 0.9", got[0].Confidence)
	}
	if got[1].Label != "dog" {
		t.Errorf("second kept det label = %q, want \"dog\"", got[1].Label)
	}
}

func TestNMS_KeepsNonOverlapping(t *testing.T) {
	dets := []Detection{
		{Confidence: 0.5, BBox: [4]float64{0, 0, 10, 10}},
		{Confidence: 0.6, BBox: [4]float64{100, 100, 110, 110}},
		{Confidence: 0.7, BBox: [4]float64{200, 200, 210, 210}},
	}
	got := NMS(dets, 0.5)
	if len(got) != 3 {
		t.Fatalf("NMS: got %d results, want 3 (no overlap)", len(got))
	}
}

// --- ParseYOLOv8Output ---

// buildYOLOv8Raw constructs a fake YOLOv8 output tensor in the correct
// transposed [1, 4+numClasses, numPreds] layout.
func buildYOLOv8Raw(numClasses, numPreds int, boxes [][4]float32, classScores [][]float32) []float32 {
	attrs := 4 + numClasses
	raw := make([]float32, attrs*numPreds)
	for i, box := range boxes {
		if i >= numPreds {
			break
		}
		// cx, cy, w, h
		raw[0*numPreds+i] = box[0]
		raw[1*numPreds+i] = box[1]
		raw[2*numPreds+i] = box[2]
		raw[3*numPreds+i] = box[3]
	}
	for i, scores := range classScores {
		if i >= numPreds {
			break
		}
		for c, s := range scores {
			if c >= numClasses {
				break
			}
			raw[(4+c)*numPreds+i] = s
		}
	}
	return raw
}

func TestParseYOLOv8Output_Basic(t *testing.T) {
	numClasses := 3
	numPreds := 4
	inputSize := 640
	labels := []string{"cat", "dog", "bird"}

	// One strong detection at centre of 640×640, class 1 ("dog").
	boxes := [][4]float32{
		{320, 320, 100, 100}, // cx=320, cy=320, w=100, h=100
		{0, 0, 0, 0},
		{0, 0, 0, 0},
		{0, 0, 0, 0},
	}
	classScores := [][]float32{
		{0.1, 0.95, 0.05}, // pred 0: class 1 wins with 0.95
		{0.01, 0.01, 0.01},
		{0.01, 0.01, 0.01},
		{0.01, 0.01, 0.01},
	}
	raw := buildYOLOv8Raw(numClasses, numPreds, boxes, classScores)

	// Test with same-size frame (no letterbox scaling needed).
	dets := ParseYOLOv8Output(raw, numClasses, numPreds, inputSize, 0.5, labels, 640, 640)
	if len(dets) != 1 {
		t.Fatalf("expected 1 detection, got %d", len(dets))
	}
	if dets[0].Label != "dog" {
		t.Errorf("label = %q, want \"dog\"", dets[0].Label)
	}
	if math.Abs(dets[0].Confidence-0.95) > 1e-6 {
		t.Errorf("confidence = %f, want 0.95", dets[0].Confidence)
	}
	// BBox should be approximately [270, 270, 370, 370] (cx=320 ± w/2=50).
	expectBox := [4]float64{270, 270, 370, 370}
	for j := 0; j < 4; j++ {
		if math.Abs(dets[0].BBox[j]-expectBox[j]) > 1.0 {
			t.Errorf("BBox[%d] = %f, want ~%f", j, dets[0].BBox[j], expectBox[j])
		}
	}
}

func TestParseYOLOv8Output_FiltersLowConfidence(t *testing.T) {
	numClasses := 2
	numPreds := 2
	raw := buildYOLOv8Raw(numClasses, numPreds,
		[][4]float32{{100, 100, 50, 50}, {200, 200, 50, 50}},
		[][]float32{{0.3, 0.2}, {0.1, 0.05}}, // both below 0.5
	)
	dets := ParseYOLOv8Output(raw, numClasses, numPreds, 640, 0.5, nil, 640, 640)
	if len(dets) != 0 {
		t.Fatalf("expected 0 detections below threshold, got %d", len(dets))
	}
}

func TestParseYOLOv8Output_LetterboxScaling(t *testing.T) {
	// Model input 640×640, original frame 1920×1080 (16:9).
	// scale = min(640/1920, 640/1080) = min(0.333, 0.593) = 0.333
	// newW = round(1920*0.333) = 640, newH = round(1080*0.333) = 360
	// offsetX = 0, offsetY = (640-360)/2 = 140

	numClasses := 1
	numPreds := 1
	// Detection at exact centre of the 640×640 model input.
	raw := buildYOLOv8Raw(numClasses, numPreds,
		[][4]float32{{320, 320, 64, 64}},
		[][]float32{{0.9}},
	)
	dets := ParseYOLOv8Output(raw, numClasses, numPreds, 640, 0.5, []string{"obj"}, 1920, 1080)
	if len(dets) != 1 {
		t.Fatalf("expected 1 detection, got %d", len(dets))
	}

	// Centre of letterboxed image (320, 320) maps to:
	// orig_x = (320 - 0) / 0.333 = ~960
	// orig_y = (320 - 140) / 0.333 = ~540
	// Box is 64×64 in model space → 64/0.333 = ~192 in orig space.
	// So [~864, ~444, ~1056, ~636].
	d := dets[0]
	midX := (d.BBox[0] + d.BBox[2]) / 2
	midY := (d.BBox[1] + d.BBox[3]) / 2
	if math.Abs(midX-960) > 5 {
		t.Errorf("centre X = %f, want ~960", midX)
	}
	if math.Abs(midY-540) > 5 {
		t.Errorf("centre Y = %f, want ~540", midY)
	}
}

func TestParseYOLOv8Output_UnknownLabel(t *testing.T) {
	numClasses := 3
	numPreds := 1
	raw := buildYOLOv8Raw(numClasses, numPreds,
		[][4]float32{{100, 100, 50, 50}},
		[][]float32{{0.1, 0.1, 0.9}}, // class 2 wins, but no label for it
	)
	dets := ParseYOLOv8Output(raw, numClasses, numPreds, 640, 0.5, []string{"a"}, 640, 640)
	if len(dets) != 1 {
		t.Fatalf("expected 1 detection, got %d", len(dets))
	}
	if dets[0].Label != "unknown" {
		t.Errorf("label = %q, want \"unknown\" (label index 2 > len(labels)=1)", dets[0].Label)
	}
}

func TestParseYOLOv8Output_ShortRaw(t *testing.T) {
	// Too-short raw slice should return nil, not panic.
	dets := ParseYOLOv8Output([]float32{1, 2, 3}, 80, 8400, 640, 0.5, nil, 640, 640)
	if dets != nil {
		t.Fatalf("expected nil for short raw, got %d dets", len(dets))
	}
}
