// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestDetectYOLOLayout(t *testing.T) {
	tests := []struct {
		name    string
		shape   []int64
		want    YOLOLayout
		wantErr string
	}{
		{name: "coco raw", shape: []int64{1, 84, 8400}, want: YOLOLayoutRaw},
		{name: "e2e default max_det", shape: []int64{1, 300, 6}, want: YOLOLayoutE2E},
		{name: "5-class raw", shape: []int64{1, 9, 2100}, want: YOLOLayoutRaw},
		{name: "2-class raw", shape: []int64{1, 6, 8400}, want: YOLOLayoutRaw},
		{name: "e2e max_det 100", shape: []int64{1, 100, 6}, want: YOLOLayoutE2E},
		{name: "e2e max_det 5", shape: []int64{1, 5, 6}, want: YOLOLayoutE2E},
		{name: "e2e square", shape: []int64{1, 6, 6}, want: YOLOLayoutE2E},
		{name: "e2e max_det 1", shape: []int64{1, 1, 6}, want: YOLOLayoutE2E},
		{name: "pose-shaped raw accepted", shape: []int64{1, 56, 8400}, want: YOLOLayoutRaw},
		{name: "rank 2", shape: []int64{84, 8400}, wantErr: "unsupported output shape"},
		{name: "rank 4", shape: []int64{1, 1, 84, 8400}, wantErr: "unsupported output shape"},
		{name: "batch 2", shape: []int64{2, 84, 8400}, wantErr: "unsupported output shape"},
		{name: "dynamic batch", shape: []int64{-1, 84, 8400}, wantErr: "unsupported output shape"},
		{name: "dynamic channels", shape: []int64{1, -1, 8400}, wantErr: "unsupported output shape"},
		{name: "dynamic preds", shape: []int64{1, 84, -1}, wantErr: "unsupported output shape"},
		{name: "pose e2e", shape: []int64{1, 300, 56}, wantErr: "pose/OBB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DetectYOLOLayout(tt.shape)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("DetectYOLOLayout(%v) error = nil, want %q", tt.shape, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("DetectYOLOLayout(%v) error = %q, want substring %q", tt.shape, err, tt.wantErr)
				}
				if tt.wantErr != "pose/OBB" && strings.Contains(err.Error(), "pose/OBB") {
					t.Fatalf("DetectYOLOLayout(%v) error = %q, want a generic shape error", tt.shape, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("DetectYOLOLayout(%v) error = %v", tt.shape, err)
			}
			if got != tt.want {
				t.Fatalf("DetectYOLOLayout(%v) = %v, want %v", tt.shape, got, tt.want)
			}
		})
	}
}

func e2eRaw(rows ...[6]float32) []float32 {
	raw := make([]float32, len(rows)*6)
	for i, row := range rows {
		copy(raw[i*6:(i+1)*6], row[:])
	}
	return raw
}

func TestParseYOLOE2EOutput_Basic(t *testing.T) {
	labels := []string{"cat", "dog", "bird"}
	raw := e2eRaw([6]float32{270, 270, 370, 370, 0.95, 1})
	dets := ParseYOLOE2EOutput(raw, 1, 640, 0.5, labels, 640, 640)
	if len(dets) != 1 {
		t.Fatalf("got %d detections, want 1", len(dets))
	}
	d := dets[0]
	if d.Label != "dog" {
		t.Errorf("label = %q, want dog", d.Label)
	}
	if d.ClassID != 1 {
		t.Errorf("class_id = %d, want 1", d.ClassID)
	}
	if math.Abs(d.Confidence-0.95) > 1e-6 {
		t.Errorf("confidence = %f, want 0.95", d.Confidence)
	}
	want := [4]float64{270, 270, 370, 370}
	for i := range want {
		if math.Abs(d.BBox[i]-want[i]) > 1e-3 {
			t.Errorf("bbox[%d] = %f, want %f", i, d.BBox[i], want[i])
		}
	}
}

func TestParseYOLOE2EOutput_ConfidenceFilter(t *testing.T) {
	raw := e2eRaw(
		[6]float32{10, 10, 20, 20, 0.49, 0},
		[6]float32{30, 30, 40, 40, 0.5, 0},
	)
	dets := ParseYOLOE2EOutput(raw, 2, 640, 0.5, []string{"person"}, 640, 640)
	if len(dets) != 1 {
		t.Fatalf("got %d detections, want 1 (score equal to the threshold kept)", len(dets))
	}
	if dets[0].ClassID != 0 || dets[0].Label != "person" {
		t.Fatalf("kept det = %+v, want class 0 person", dets[0])
	}
	if math.Abs(dets[0].Confidence-0.5) > 1e-6 {
		t.Errorf("confidence = %f, want 0.5", dets[0].Confidence)
	}
}

func TestParseYOLOE2EOutput_Letterbox1920x1080(t *testing.T) {
	// 1920×1080 into 640. scale = 640/1920 = 1/3, the height pad is 140.
	// Model box [100, 140, 200, 200] maps to [300, 0, 600, 180].
	raw := e2eRaw([6]float32{100, 140, 200, 200, 0.9, 0})
	dets := ParseYOLOE2EOutput(raw, 1, 640, 0.25, []string{"person"}, 1920, 1080)
	if len(dets) != 1 {
		t.Fatalf("got %d detections, want 1", len(dets))
	}
	want := [4]float64{300, 0, 600, 180}
	for i := range want {
		if math.Abs(dets[0].BBox[i]-want[i]) > 1e-4 {
			t.Errorf("bbox[%d] = %f, want %f", i, dets[0].BBox[i], want[i])
		}
	}
}

func TestParseYOLOE2EOutput_FloatLetterboxOffset(t *testing.T) {
	// 8×3 into 11. scale = 11/8, scaled height rounds to 4, float offset is 3.5.
	// Integer (target-new)/2 would be 3 and would move y1 off 0.
	raw := e2eRaw([6]float32{0, 3.5, 11, 4.875, 0.8, 0})
	dets := ParseYOLOE2EOutput(raw, 1, 11, 0.25, nil, 8, 3)
	if len(dets) != 1 {
		t.Fatalf("got %d detections, want 1", len(dets))
	}
	want := [4]float64{0, 0, 8, 1}
	for i := range want {
		if math.Abs(dets[0].BBox[i]-want[i]) > 1e-4 {
			t.Errorf("bbox[%d] = %f, want %f", i, dets[0].BBox[i], want[i])
		}
	}
}

func TestParseYOLOE2EOutput_ShortSlice(t *testing.T) {
	raw := e2eRaw([6]float32{1, 1, 2, 2, 0.9, 0})
	dets := ParseYOLOE2EOutput(raw, 2, 640, 0.25, nil, 640, 640)
	if dets != nil {
		t.Fatalf("short slice = %#v, want nil", dets)
	}
	if got := ParseYOLOE2EOutput(raw, 0, 640, 0.25, nil, 640, 640); got != nil {
		t.Fatalf("maxDet 0 = %#v, want nil", got)
	}
}

func TestParseYOLOE2EOutput_ZeroPaddedTail(t *testing.T) {
	raw := e2eRaw(
		[6]float32{10, 10, 40, 40, 0.9, 2},
		[6]float32{0, 0, 0, 0, 0, 0},
		[6]float32{0, 0, 0, 0, 0, 0},
	)
	dets := ParseYOLOE2EOutput(raw, 3, 640, 0.25, []string{"a", "b", "c"}, 640, 640)
	if len(dets) != 1 {
		t.Fatalf("got %d detections, want 1", len(dets))
	}
	if dets[0].Label != "c" || dets[0].ClassID != 2 {
		t.Fatalf("det = %+v, want class 2 c", dets[0])
	}
}

func TestParseYOLOE2EOutput_NonFinite(t *testing.T) {
	raw := e2eRaw(
		[6]float32{10, 10, 20, 20, 0.9, 0},
		[6]float32{float32(math.NaN()), 10, 20, 20, 0.9, 0},
		[6]float32{10, 10, 20, 20, float32(math.Inf(1)), 0},
		[6]float32{30, 30, 50, 50, 0.8, 1},
	)
	dets := ParseYOLOE2EOutput(raw, 4, 640, 0.25, []string{"a", "b"}, 640, 640)
	if len(dets) != 2 {
		t.Fatalf("got %d detections, want 2", len(dets))
	}
	if dets[0].ClassID != 0 || dets[1].ClassID != 1 {
		t.Fatalf("classes = %d, %d, want 0, 1", dets[0].ClassID, dets[1].ClassID)
	}
}

func TestParseYOLOE2EOutput_ClassID(t *testing.T) {
	if yoloE2EMaxClassID != 10000 {
		t.Fatalf("e2e class cap = %d, want 10000", yoloE2EMaxClassID)
	}
	labels := []string{"zero", "one"}
	two63 := float32(math.Ldexp(1, 63))
	raw := e2eRaw(
		[6]float32{1, 1, 2, 2, 0.9, 1},
		[6]float32{1, 1, 2, 2, 0.9, float32(1.0000001)},
		[6]float32{1, 1, 2, 2, 0.9, 1.9},
		[6]float32{1, 1, 2, 2, 0.9, -1},
		[6]float32{1, 1, 2, 2, 0.9, 9999},
		[6]float32{1, 1, 2, 2, 0.9, 10000},
		[6]float32{1, 1, 2, 2, 0.9, two63},
		[6]float32{1, 1, 2, 2, 0.9, 5},
	)
	dets := ParseYOLOE2EOutput(raw, 8, 64, 0.25, labels, 64, 64)
	if len(dets) != 5 {
		t.Fatalf("got %d detections, want 5 (negative, 10000, and 2^63 dropped)", len(dets))
	}
	wantID := []int{1, 1, 1, 9999, 5}
	wantLabel := []string{"one", "one", "one", "9999", "5"}
	for i := range wantID {
		if dets[i].ClassID != wantID[i] || dets[i].Label != wantLabel[i] {
			t.Errorf("det[%d] = class %d %q, want class %d %q", i, dets[i].ClassID, dets[i].Label, wantID[i], wantLabel[i])
		}
	}
}

func TestNMSPerClass(t *testing.T) {
	box := [4]float64{0, 0, 10, 10}
	overlap := [4]float64{1, 1, 11, 11}
	far := [4]float64{100, 100, 110, 110}

	different := []Detection{
		{Label: "person", ClassID: 0, Confidence: 0.9, BBox: box},
		{Label: "person", ClassID: 1, Confidence: 0.8, BBox: overlap},
	}
	if got := NMSPerClass(different, 0.5); len(got) != 2 {
		t.Fatalf("different classes: got %d, want 2", len(got))
	}

	sameClass := []Detection{
		{Label: "cat", ClassID: 0, Confidence: 0.9, BBox: box},
		{Label: "dog", ClassID: 0, Confidence: 0.8, BBox: overlap},
		{Label: "dog", ClassID: 0, Confidence: 0.7, BBox: far},
	}
	got := NMSPerClass(sameClass, 0.5)
	if len(got) != 2 {
		t.Fatalf("same class overlap: got %d, want 2", len(got))
	}
	if got[0].Label != "cat" || got[1].BBox != far {
		t.Fatalf("kept = %+v, %+v", got[0], got[1])
	}

	numeric := []Detection{
		{Label: "0", ClassID: 0, Confidence: 0.9, BBox: box},
		{Label: "1", ClassID: 1, Confidence: 0.8, BBox: overlap},
	}
	if got := NMSPerClass(numeric, 0.5); len(got) != 2 {
		t.Fatalf("labels 0 and 1: got %d, want 2", len(got))
	}

	if got := NMSPerClass(nil, 0.5); got != nil {
		t.Fatalf("empty = %#v, want nil", got)
	}
}

func TestYOLOv8Alias_ClassAgnosticNMSAndUnknownLabel(t *testing.T) {
	numClasses := 2
	numPreds := 2
	raw := buildYOLOv8Raw(numClasses, numPreds,
		[][4]float32{{50, 50, 40, 40}, {52, 52, 40, 40}},
		[][]float32{{0.95, 0.1}, {0.1, 0.9}},
	)
	dets := ParseYOLOv8Output(raw, numClasses, numPreds, 640, 0.5, nil, 640, 640)
	if len(dets) != 2 {
		t.Fatalf("parsed %d, want 2", len(dets))
	}
	if dets[0].Label != "unknown" || dets[1].Label != "unknown" {
		t.Fatalf("labels = %q, %q, want unknown", dets[0].Label, dets[1].Label)
	}
	if dets[0].ClassID != 0 || dets[1].ClassID != 1 {
		t.Fatalf("class ids = %d, %d, want 0, 1", dets[0].ClassID, dets[1].ClassID)
	}
	if got := NMS(dets, 0.5); len(got) != 1 {
		t.Fatalf("class-agnostic NMS kept %d, want 1", len(got))
	}
	if got := NMSPerClass(dets, 0.5); len(got) != 2 {
		t.Fatalf("per-class NMS kept %d, want 2", len(got))
	}
}

func TestParseYOLOv8Output_SetsClassID(t *testing.T) {
	raw := buildYOLOv8Raw(3, 1,
		[][4]float32{{320, 320, 100, 100}},
		[][]float32{{0.1, 0.95, 0.05}},
	)
	dets := ParseYOLOv8Output(raw, 3, 1, 640, 0.5, []string{"cat", "dog", "bird"}, 640, 640)
	if len(dets) != 1 {
		t.Fatalf("got %d, want 1", len(dets))
	}
	if dets[0].ClassID != 1 || dets[0].Label != "dog" {
		t.Fatalf("det = class %d %q, want 1 dog", dets[0].ClassID, dets[0].Label)
	}

	raw = buildYOLOv8Raw(3, 1,
		[][4]float32{{100, 100, 50, 50}},
		[][]float32{{0.1, 0.1, 0.9}},
	)
	dets = ParseYOLOv8Output(raw, 3, 1, 640, 0.5, []string{"a"}, 640, 640)
	if len(dets) != 1 || dets[0].Label != "unknown" || dets[0].ClassID != 2 {
		t.Fatalf("det = %+v, want unknown class 2", dets)
	}
}

func TestDetectionClassIDJSON(t *testing.T) {
	for _, id := range []int{0, -1} {
		b, err := json.Marshal(Detection{Label: "x", ClassID: id, Confidence: 0.5, BBox: [4]float64{1, 2, 3, 4}})
		if err != nil {
			t.Fatal(err)
		}
		want := fmt.Sprintf(`"class_id":%d`, id)
		if !strings.Contains(string(b), want) {
			t.Fatalf("json = %s, want substring %s", b, want)
		}
	}
}

var coco80 = []string{
	"person", "bicycle", "car", "motorcycle", "airplane", "bus", "train", "truck", "boat", "traffic light",
	"fire hydrant", "stop sign", "parking meter", "bench", "bird", "cat", "dog", "horse", "sheep", "cow",
	"elephant", "bear", "zebra", "giraffe", "backpack", "umbrella", "handbag", "tie", "suitcase", "frisbee",
	"skis", "snowboard", "sports ball", "kite", "baseball bat", "baseball glove", "skateboard", "surfboard", "tennis racket", "bottle",
	"wine glass", "cup", "fork", "knife", "spoon", "bowl", "banana", "apple", "sandwich", "orange",
	"broccoli", "carrot", "hot dog", "pizza", "donut", "cake", "chair", "couch", "potted plant", "bed",
	"dining table", "toilet", "tv", "laptop", "mouse", "remote", "keyboard", "cell phone", "microwave", "oven",
	"toaster", "sink", "refrigerator", "book", "clock", "vase", "scissors", "teddy bear", "hair drier", "toothbrush",
}

func namesDict(names []string) string {
	var b strings.Builder
	b.WriteString("{")
	for i, name := range names {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%d: '%s'", i, name)
	}
	b.WriteString("}")
	return b.String()
}

func TestParseYOLONames_COCO80(t *testing.T) {
	if len(coco80) != 80 {
		t.Fatalf("fixture has %d names, want 80", len(coco80))
	}
	got, err := parseYOLONames(namesDict(coco80), 80)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 80 || got[0] != "person" || got[79] != "toothbrush" {
		t.Fatalf("got len %d first %q last %q", len(got), got[0], got[79])
	}
}

func TestParseYOLONames_QuotesCommasColons(t *testing.T) {
	in := "{0: 'say \"hi\", now: yes', 1: \"a, b: c\", 2: 'person\\'s'}"
	got, err := parseYOLONames(in, 80)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`say "hi", now: yes`, "a, b: c", "person's"}
	if len(got) != len(want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("name[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestParseYOLONames_Truncated(t *testing.T) {
	for _, in := range []string{"", "{", "{0: 'person'", "{0: 'person", "{0: "} {
		if _, err := parseYOLONames(in, 80); err == nil || !strings.Contains(err.Error(), "truncated") {
			t.Fatalf("parse(%q) error = %v, want truncated", in, err)
		}
	}
}

func TestParseYOLONames_Oversized(t *testing.T) {
	over := "{}" + strings.Repeat(" ", yoloNamesMaxBytes)
	if _, err := parseYOLONames(over, 80); err == nil || !strings.Contains(err.Error(), "1 MiB") {
		t.Fatalf("oversized error = %v", err)
	}
	exact := "{}" + strings.Repeat(" ", yoloNamesMaxBytes-2)
	if len(exact) != yoloNamesMaxBytes {
		t.Fatalf("fixture length = %d, want %d", len(exact), yoloNamesMaxBytes)
	}
	got, err := parseYOLONames(exact, 80)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %#v, want empty", got)
	}
}

func TestParseYOLONames_OutOfRange(t *testing.T) {
	cases := []string{
		"{10000: 'x'}",
		"{-1: 'x'}",
		"{0: 'a', 80: 'b'}",
		"{999999999999999999999: 'x'}",
	}
	for _, in := range cases {
		if _, err := parseYOLONames(in, 80); err == nil || !strings.Contains(err.Error(), "outside") {
			t.Fatalf("parse(%q) error = %v, want outside", in, err)
		}
	}
}

func TestParseYOLONames_E2ECap(t *testing.T) {
	names := make([]string, 10000)
	for i := range names {
		names[i] = "c"
	}
	got, err := parseYOLONames(namesDict(names), 10000)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 10000 || got[9999] != "c" {
		t.Fatalf("len = %d last = %q", len(got), got[9999])
	}
	// A 10001st occurrence still in range trips the entry cap. Index 10000
	// itself is rejected by the range check, which is the same 10_000 cap.
	over := strings.TrimSuffix(namesDict(names), "}") + ", 0: 'c'}"
	if _, err := parseYOLONames(over, 10000); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("10001 entries error = %v, want exceeds", err)
	}
	if _, err := parseYOLONames("{10000: 'x'}", 10000); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("index 10000 error = %v, want outside", err)
	}
}

func TestParseYOLONames_DuplicateLastWinsAndHole(t *testing.T) {
	got, err := parseYOLONames("{0: 'a', 0: 'b'}", 80)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "b" {
		t.Fatalf("got %#v, want [b]", got)
	}
	if _, err := parseYOLONames("{0: 'a', 2: 'c'}", 80); err == nil || !strings.Contains(err.Error(), "missing class 1") {
		t.Fatalf("hole error = %v", err)
	}
	got, err = parseYOLONames("{\n\t0: 'person',\r\n}", 80)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "person" {
		t.Fatalf("got %#v", got)
	}
}

func FuzzParseYOLONames(f *testing.F) {
	f.Add("{0: 'person', 1: 'bicycle'}")
	f.Add("{0: 'say \"hi\", now: yes'}")
	f.Add("{")
	f.Add("")
	f.Add("{}")
	f.Add("{0: 'a',}")
	f.Add("not a dict")
	f.Fuzz(func(t *testing.T, s string) {
		_, _ = parseYOLONames(s, 80)
		_, _ = parseYOLONames(s, yoloE2EMaxClassID)
	})
}

func FuzzParseYOLOE2EOutput(f *testing.F) {
	var seed []byte
	put := func(v float32) {
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], math.Float32bits(v))
		seed = append(seed, b[:]...)
	}
	for _, v := range []float32{10, 20, 30, 40, 0.9, 1} {
		put(v)
	}
	put(float32(math.NaN()))
	f.Add(seed, 1)
	f.Add([]byte{}, 0)
	f.Add([]byte{0xff, 0xff, 0xff, 0xff}, 4)
	f.Add(seed[:8], math.MaxInt)
	f.Fuzz(func(t *testing.T, b []byte, maxDet int) {
		n := len(b) / 4
		raw := make([]float32, n)
		for i := 0; i < n; i++ {
			raw[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4 : i*4+4]))
		}
		_ = ParseYOLOE2EOutput(raw, maxDet, 640, 0.25, []string{"person", "bicycle"}, 1920, 1080)
	})
}
