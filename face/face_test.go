// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package face

import (
	"bytes"
	"image"
	"image/color"
	"math"
	"testing"
)

// buildFaceOutput packs detections into the transposed [1, attrs, numPreds] layout YOLOv8
// emits (value for prediction i, attribute a at index a*numPreds+i), with kptDim=3.
func buildFaceOutput(numPreds int, preds []faceDetection) []float32 {
	const attrs = 5 + numLandmarks*3
	raw := make([]float32, attrs*numPreds)
	set := func(a, i int, v float64) { raw[a*numPreds+i] = float32(v) }
	for i, p := range preds {
		cx := (p.bbox[0] + p.bbox[2]) / 2
		cy := (p.bbox[1] + p.bbox[3]) / 2
		set(0, i, cx)
		set(1, i, cy)
		set(2, i, p.bbox[2]-p.bbox[0])
		set(3, i, p.bbox[3]-p.bbox[1])
		set(4, i, p.score)
		for k := 0; k < numLandmarks; k++ {
			set(5+k*3, i, p.landmarks[k][0])
			set(6+k*3, i, p.landmarks[k][1])
			set(7+k*3, i, 1.0) // visibility
		}
	}
	return raw
}

func sampleLandmarks() [numLandmarks][2]float64 {
	return [numLandmarks][2]float64{{90, 110}, {110, 110}, {100, 120}, {92, 135}, {108, 135}}
}

func TestParseYOLOv8FaceOutput(t *testing.T) {
	// origW=origH=inputSize ⇒ scale=1, no letterbox offset ⇒ coords pass through unchanged.
	want := faceDetection{bbox: [4]float64{80, 95, 120, 145}, score: 0.9, landmarks: sampleLandmarks()}
	low := faceDetection{bbox: [4]float64{0, 0, 10, 10}, score: 0.2} // below threshold
	raw := buildFaceOutput(3, []faceDetection{want, low, {}})

	got := parseYOLOv8FaceOutput(raw, 3, 640, 0.5, 640, 640)
	if len(got) != 1 {
		t.Fatalf("got %d detections, want 1", len(got))
	}
	d := got[0]
	if math.Abs(d.score-0.9) > 1e-6 {
		t.Errorf("score = %v, want 0.9", d.score)
	}
	if d.bbox != want.bbox {
		t.Errorf("bbox = %v, want %v", d.bbox, want.bbox)
	}
	if d.landmarks != want.landmarks {
		t.Errorf("landmarks = %v, want %v", d.landmarks, want.landmarks)
	}
}

func TestParseYOLOv8FaceOutputLetterbox(t *testing.T) {
	// Portrait source 320×640 into a 640 model input ⇒ scale=1, but centred ⇒ offsetX=160.
	// A model-input x of 320 (centre column) must map back to original x = 320-160 = 160.
	lm := [numLandmarks][2]float64{{320, 320}, {320, 320}, {320, 320}, {320, 320}, {320, 320}}
	d := faceDetection{bbox: [4]float64{300, 300, 340, 340}, score: 0.8, landmarks: lm}
	raw := buildFaceOutput(1, []faceDetection{d})

	got := parseYOLOv8FaceOutput(raw, 1, 640, 0.5, 320, 640)
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if x := got[0].landmarks[0][0]; math.Abs(x-160) > 1e-6 {
		t.Errorf("unletterboxed landmark x = %v, want 160", x)
	}
	if y := got[0].landmarks[0][1]; math.Abs(y-320) > 1e-6 {
		t.Errorf("landmark y = %v, want 320 (no vertical padding)", y)
	}
}

// buildFaceNMSOutput packs detections into the end-to-end-NMS layout [1, maxDet, nmsDetAttrs]
// (row-major per detection): x1,y1,x2,y2, score, class, 5×(x,y,vis).
func buildFaceNMSOutput(maxDet int, preds []faceDetection) []float32 {
	raw := make([]float32, maxDet*nmsDetAttrs)
	for i, p := range preds {
		base := i * nmsDetAttrs
		raw[base], raw[base+1], raw[base+2], raw[base+3] = float32(p.bbox[0]), float32(p.bbox[1]), float32(p.bbox[2]), float32(p.bbox[3])
		raw[base+4] = float32(p.score)
		raw[base+5] = 0 // class = face
		for k := 0; k < numLandmarks; k++ {
			kp := base + 6 + k*3
			raw[kp], raw[kp+1], raw[kp+2] = float32(p.landmarks[k][0]), float32(p.landmarks[k][1]), 1.0
		}
	}
	return raw
}

func TestParseYOLOv8FaceNMSOutput(t *testing.T) {
	want := faceDetection{bbox: [4]float64{80, 95, 120, 145}, score: 0.9, landmarks: sampleLandmarks()}
	low := faceDetection{bbox: [4]float64{0, 0, 10, 10}, score: 0.2} // below threshold
	// maxDet=3: slot 0 real, slot 1 below threshold, slot 2 zero-padding — both dropped.
	raw := buildFaceNMSOutput(3, []faceDetection{want, low})

	got := parseYOLOv8FaceNMSOutput(raw, 3, 640, 0.5, 640, 640)
	if len(got) != 1 {
		t.Fatalf("got %d detections, want 1 (padding + low-score dropped)", len(got))
	}
	d := got[0]
	if d.bbox != want.bbox {
		t.Errorf("bbox = %v, want %v", d.bbox, want.bbox)
	}
	if d.landmarks != want.landmarks {
		t.Errorf("landmarks = %v, want %v", d.landmarks, want.landmarks)
	}
	if math.Abs(d.score-0.9) > 1e-6 {
		t.Errorf("score = %v, want 0.9", d.score)
	}
}

func TestFaceNMS(t *testing.T) {
	a := faceDetection{bbox: [4]float64{0, 0, 100, 100}, score: 0.9, landmarks: sampleLandmarks()}
	b := faceDetection{bbox: [4]float64{10, 10, 105, 105}, score: 0.7}   // ~0.8 IoU with a → suppressed
	c := faceDetection{bbox: [4]float64{500, 500, 560, 560}, score: 0.6} // disjoint → kept

	kept := faceNMS([]faceDetection{b, a, c}, 0.45)
	if len(kept) != 2 {
		t.Fatalf("kept %d, want 2", len(kept))
	}
	if kept[0].score != 0.9 {
		t.Errorf("highest-score detection not first: %v", kept[0].score)
	}
	if kept[0].landmarks != a.landmarks {
		t.Errorf("landmarks did not ride along with the kept box")
	}
}

func TestSimilarityTransformRecoversKnownTransform(t *testing.T) {
	// Apply a known similarity (scale 2, 30°, translate (10,5)) to the template, then check
	// the estimator recovers exactly that affine.
	const deg = math.Pi / 6
	scale := 2.0
	a, b := scale*math.Cos(deg), scale*math.Sin(deg)
	tx, ty := 10.0, 5.0
	var to [numLandmarks][2]float64
	for i, p := range arcFaceTemplate {
		to[i] = [2]float64{a*p[0] - b*p[1] + tx, b*p[0] + a*p[1] + ty}
	}
	m := similarityTransform(arcFaceTemplate, to)
	for _, c := range []struct {
		got, want float64
		name      string
	}{{m[0][0], a, "a"}, {m[0][1], -b, "-b"}, {m[1][0], b, "b"}, {m[1][1], a, "a"}, {m[0][2], tx, "tx"}, {m[1][2], ty, "ty"}} {
		if math.Abs(c.got-c.want) > 1e-9 {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestAlignIdentityWhenLandmarksMatchTemplate(t *testing.T) {
	src := gradientImage(160, 160)
	// Landmarks already at the template ⇒ transform ≈ identity ⇒ aligned crop ≈ src top-left.
	aligned := alignTo112(src, arcFaceTemplate)
	for _, p := range [][2]int{{0, 0}, {50, 60}, {111, 111}} {
		want := src.RGBAAt(p[0], p[1])
		got := aligned.RGBAAt(p[0], p[1])
		if absI(int(got.R)-int(want.R)) > 1 || absI(int(got.G)-int(want.G)) > 1 || absI(int(got.B)-int(want.B)) > 1 {
			t.Errorf("at %v: aligned %v, want ≈ src %v", p, got, want)
		}
	}
}

func TestAlignIsDeterministic(t *testing.T) {
	src := gradientImage(200, 200)
	lm := [numLandmarks][2]float64{{60, 70}, {130, 72}, {95, 110}, {65, 150}, {128, 151}}
	first := alignTo112(src, lm)
	for i := 0; i < 16; i++ {
		again := alignTo112(src, lm)
		if !bytes.Equal(first.Pix, again.Pix) {
			t.Fatalf("alignment not deterministic on run %d", i)
		}
	}
}

func TestL2Normalize(t *testing.T) {
	v := l2Normalize([]float32{3, 4})
	if math.Abs(float64(v[0])-0.6) > 1e-6 || math.Abs(float64(v[1])-0.8) > 1e-6 {
		t.Errorf("normalized = %v, want [0.6 0.8]", v)
	}
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if math.Abs(sum-1) > 1e-6 {
		t.Errorf("‖v‖² = %v, want 1", sum)
	}
	if got := l2Normalize([]float32{0, 0}); got[0] != 0 || got[1] != 0 {
		t.Errorf("zero vector should pass through, got %v", got)
	}
}

func TestToFace(t *testing.T) {
	d := faceDetection{bbox: [4]float64{80, 95, 120, 145}, score: 0.9, landmarks: sampleLandmarks()}
	f := toFace(d)
	if f.BBox != [4]int{80, 95, 40, 50} {
		t.Errorf("BBox = %v, want [80 95 40 50] (x,y,w,h)", f.BBox)
	}
	if f.Landmarks[0] != [2]int{90, 110} {
		t.Errorf("Landmarks[0] = %v, want [90 110]", f.Landmarks[0])
	}
	if f.Score != 0.9 {
		t.Errorf("Score = %v, want 0.9", f.Score)
	}
}

// gradientImage builds a deterministic RGBA gradient for resampling tests.
func gradientImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: uint8((x + y) % 256), A: 255})
		}
	}
	return img
}

func absI(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
