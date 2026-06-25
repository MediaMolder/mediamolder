// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

// Package face provides native face analysis — detection (YOLOv8-face, a 5-keypoint
// landmark variant), similarity-transform alignment, and SFace embedding — behind a small,
// stable API: [Capable] and [Analyze]. It is the single boundary that contains the optional
// ML model dependency: the real pipeline is compiled only under the `with_onnx` build tag
// (see analyze_onnx.go); the default build links a stub (analyze_stub.go) so callers compile
// and run with no model dependency.
//
// The detector model (YOLOv8-face) is AGPL-3.0; it is loaded at runtime as data behind this
// API, swappable for a permissive model (YuNet, MIT) with no change to this package's
// contract or any downstream code. SFace (the embedder) is Apache-2.0.
//
// This file holds the pure-Go, dependency-free core (output parsing, NMS, alignment) so it
// compiles and is unit-tested in every build, ONNX or not.
package face

import (
	"errors"
	"image"
	"image/color"
	"math"
	"math/cmplx"
)

// ErrUnsupported is returned by [Analyze] when this build has no face models — either the
// default `!with_onnx` build, or a `with_onnx` build whose models are absent/unverified.
var ErrUnsupported = errors.New("face: analysis not available in this build")

// Face is one detected face: its location, the 5 keypoints used for alignment, the detector
// confidence, and an L2-normalised SFace embedding for clustering/recognition. The field
// shapes are a stable contract for downstream consumers (BBox is x, y, w, h in source
// pixels; Landmarks are eyes, nose, mouth corners).
type Face struct {
	BBox      [4]int    // x, y, w, h in source pixels
	Landmarks [5][2]int // left eye, right eye, nose, left mouth, right mouth
	Score     float32   // detector confidence, 0..1
	Embedding []float32 // 128-d, L2-normalised (SFace); nil until embedded
}

// Options tunes a single [AnalyzeImageOpts] call. The zero value is valid:
// thresholds <= 0 fall back to the package defaults (0.5 confidence, 0.45 NMS IoU)
// and Embed is off (detect+align only — the faster path).
type Options struct {
	ConfThresh float64 // detector confidence threshold; <= 0 ⇒ default
	IoUThresh  float64 // NMS IoU threshold (raw exports only); <= 0 ⇒ default
	Embed      bool    // run the SFace embedding step; false skips it
}

// Record is a time-stamped detected face: a [Face] plus its position in a media
// stream. It is the JSON serialisation unit shared by the `face-detect` CLI and the
// `face_detect` pipeline processor, and is mirrored by FaceRecord in the GUI frontend.
type Record struct {
	Frame     uint64    `json:"frame"`
	PTS       int64     `json:"pts"`
	Time      float64   `json:"t,omitempty"` // seconds; omitted when the time base is unknown
	BBox      [4]int    `json:"bbox"`        // x, y, w, h in source pixels
	Landmarks [5][2]int `json:"landmarks"`   // left eye, right eye, nose, left/right mouth
	Score     float32   `json:"score"`
	Label     string    `json:"label,omitempty"`     // "" until gallery matching assigns a name
	Embedding []float32 `json:"embedding,omitempty"` // 128-d; present only when embedded
}

// ToRecord wraps a Face with its stream position (frame index, PTS, and the PTS in
// seconds — pass 0 for t when the time base is unknown, e.g. inside a processor).
func (f Face) ToRecord(frame uint64, pts int64, t float64) Record {
	return Record{
		Frame:     frame,
		PTS:       pts,
		Time:      t,
		BBox:      f.BBox,
		Landmarks: f.Landmarks,
		Score:     f.Score,
		Embedding: f.Embedding,
	}
}

const (
	numLandmarks = 5   // YOLOv8-face emits 5 keypoints per face
	alignSize    = 112 // SFace input is 112×112

	// nmsDetAttrs is the per-detection width of an end-to-end-NMS YOLOv8-face export:
	// x1,y1,x2,y2, score, class + 5×(x,y,visibility) = 21.
	nmsDetAttrs = 6 + numLandmarks*3
)

// arcFaceTemplate is the canonical 5-point destination for alignment in a 112×112 crop
// (the InsightFace/ArcFace reference, shared by SFace). Order matches the detector's
// keypoint order: left eye, right eye, nose, left mouth corner, right mouth corner.
var arcFaceTemplate = [numLandmarks][2]float64{
	{38.2946, 51.6963},
	{73.5318, 51.5014},
	{56.0252, 71.7366},
	{41.5493, 92.3655},
	{70.7299, 92.2041},
}

// faceDetection is an intermediate detection in original-frame pixel coordinates: a corner
// bounding box, the face score, and the 5 landmarks.
type faceDetection struct {
	bbox      [4]float64 // x1, y1, x2, y2
	score     float64
	landmarks [numLandmarks][2]float64
}

// parseYOLOv8FaceOutput decodes raw YOLOv8-face output into detections with keypoints,
// reversing the letterbox applied at preprocessing.
//
// YOLOv8 pose/face output has shape [1, A, numPreds] in transposed (column-major) layout,
// so the value for prediction i, attribute a is raw[a*numPreds+i]. A = 4 + 1 + 5*kptDim:
// attrs 0–3 are (cx, cy, w, h) in model-input pixels, attr 4 is the face score, and the
// remaining 5*kptDim are the keypoints (kptDim is 2 for (x,y) or 3 for (x,y,visibility) —
// derived from A so both layouts work). Coordinates are mapped back to original-frame
// pixels via the same letterbox parameters processors.Letterbox uses (scale =
// min(inputSize/origW, inputSize/origH), centred).
func parseYOLOv8FaceOutput(raw []float32, numPreds, inputSize int, confThresh float64, origW, origH int) []faceDetection {
	if numPreds <= 0 || origW <= 0 || origH <= 0 {
		return nil
	}
	attrs := len(raw) / numPreds
	if attrs < 5+numLandmarks*2 {
		return nil
	}
	kptDim := (attrs - 5) / numLandmarks

	unX, unY, ok := unletterbox(inputSize, origW, origH)
	if !ok {
		return nil
	}

	var dets []faceDetection
	for i := 0; i < numPreds; i++ {
		score := float64(raw[4*numPreds+i])
		if score < confThresh {
			continue
		}
		cx := float64(raw[0*numPreds+i])
		cy := float64(raw[1*numPreds+i])
		w := float64(raw[2*numPreds+i])
		h := float64(raw[3*numPreds+i])

		d := faceDetection{
			bbox:  [4]float64{unX(cx - w/2), unY(cy - h/2), unX(cx + w/2), unY(cy + h/2)},
			score: score,
		}
		for k := 0; k < numLandmarks; k++ {
			base := 5 + k*kptDim
			d.landmarks[k][0] = unX(float64(raw[base*numPreds+i]))
			d.landmarks[k][1] = unY(float64(raw[(base+1)*numPreds+i]))
		}
		dets = append(dets, d)
	}
	return dets
}

// parseYOLOv8FaceNMSOutput decodes an end-to-end-NMS YOLOv8-face export (Ultralytics
// export(nms=True), task=pose, kpt_shape=[5,3]). Output shape is [1, maxDet, nmsDetAttrs] in
// row-major (per-detection) layout — boxes are already NMS-filtered and padded to maxDet with
// zero-confidence rows. Each row is [x1,y1,x2,y2, score, class, 5×(kx,ky,kvis)] in model-input
// pixels; coordinates are reverse-letterboxed to original-frame pixels. No further NMS needed.
func parseYOLOv8FaceNMSOutput(raw []float32, maxDet, inputSize int, confThresh float64, origW, origH int) []faceDetection {
	if maxDet <= 0 || len(raw) < maxDet*nmsDetAttrs {
		return nil
	}
	unX, unY, ok := unletterbox(inputSize, origW, origH)
	if !ok {
		return nil
	}
	var dets []faceDetection
	for i := 0; i < maxDet; i++ {
		base := i * nmsDetAttrs
		score := float64(raw[base+4])
		if score < confThresh {
			continue // padded/empty slot or below threshold
		}
		d := faceDetection{
			bbox:  [4]float64{unX(float64(raw[base])), unY(float64(raw[base+1])), unX(float64(raw[base+2])), unY(float64(raw[base+3]))},
			score: score,
		}
		for k := 0; k < numLandmarks; k++ {
			kp := base + 6 + k*3 // skip x1,y1,x2,y2,score,class
			d.landmarks[k][0] = unX(float64(raw[kp]))
			d.landmarks[k][1] = unY(float64(raw[kp+1]))
		}
		dets = append(dets, d)
	}
	return dets
}

// letterbox resizes src into a targetW×targetH RGBA, preserving aspect ratio and centring the
// scaled image on a black background (nearest-neighbour). It is the forward transform whose
// inverse [unletterbox] reverses; the geometry (scale, centred integer offset, rounding) must
// stay bit-identical to that inverse, so this is a self-contained copy rather than a borrowed
// dependency — it keeps the face package a leaf (no processors import, hence no import cycle
// for the face_detect processor).
func letterbox(src image.Image, targetW, targetH int) *image.RGBA {
	srcB := src.Bounds()
	origW := srcB.Dx()
	origH := srcB.Dy()
	if origW == 0 || origH == 0 {
		return image.NewRGBA(image.Rect(0, 0, targetW, targetH))
	}

	scale := math.Min(float64(targetW)/float64(origW), float64(targetH)/float64(origH))
	newW := int(math.Round(float64(origW) * scale))
	newH := int(math.Round(float64(origH) * scale))
	if newW < 1 {
		newW = 1
	}
	if newH < 1 {
		newH = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
	// dst is already zeroed (black) by NewRGBA.

	offsetX := (targetW - newW) / 2
	offsetY := (targetH - newH) / 2

	// Nearest-neighbour resize + place at offset.
	for dy := 0; dy < newH; dy++ {
		srcY := srcB.Min.Y + int(float64(dy)/scale+0.5)
		if srcY >= srcB.Max.Y {
			srcY = srcB.Max.Y - 1
		}
		for dx := 0; dx < newW; dx++ {
			srcX := srcB.Min.X + int(float64(dx)/scale+0.5)
			if srcX >= srcB.Max.X {
				srcX = srcB.Max.X - 1
			}
			r, g, b, a := src.At(srcX, srcY).RGBA()
			dst.SetRGBA(offsetX+dx, offsetY+dy, color.RGBA{
				R: uint8(r >> 8),
				G: uint8(g >> 8),
				B: uint8(b >> 8),
				A: uint8(a >> 8),
			})
		}
	}
	return dst
}

// unletterbox returns functions mapping model-input coordinates (after a centred letterbox to
// inputSize²) back to original-frame pixels, clamped to the frame; ok is false for a
// degenerate size. Shared by the raw and NMS-embedded parsers.
func unletterbox(inputSize, origW, origH int) (unX, unY func(float64) float64, ok bool) {
	if inputSize <= 0 || origW <= 0 || origH <= 0 {
		return nil, nil, false
	}
	scale := math.Min(float64(inputSize)/float64(origW), float64(inputSize)/float64(origH))
	if scale <= 0 {
		return nil, nil, false
	}
	offsetX := (float64(inputSize) - math.Round(float64(origW)*scale)) / 2.0
	offsetY := (float64(inputSize) - math.Round(float64(origH)*scale)) / 2.0
	unX = func(x float64) float64 { return math.Max(0, math.Min((x-offsetX)/scale, float64(origW))) }
	unY = func(y float64) float64 { return math.Max(0, math.Min((y-offsetY)/scale, float64(origH))) }
	return unX, unY, true
}

// faceNMS performs greedy non-maximum suppression by descending score, suppressing boxes
// that overlap a kept box by more than iouThresh. Landmarks ride along with their box.
func faceNMS(dets []faceDetection, iouThresh float64) []faceDetection {
	if len(dets) == 0 {
		return nil
	}
	order := make([]int, len(dets))
	for i := range order {
		order[i] = i
	}
	// Stable insertion sort by score desc — small N, and keeps ties deterministic.
	for i := 1; i < len(order); i++ {
		for j := i; j > 0 && dets[order[j]].score > dets[order[j-1]].score; j-- {
			order[j], order[j-1] = order[j-1], order[j]
		}
	}

	var kept []faceDetection
	for _, idx := range order {
		d := dets[idx]
		suppressed := false
		for _, k := range kept {
			if iou(d.bbox, k.bbox) > iouThresh {
				suppressed = true
				break
			}
		}
		if !suppressed {
			kept = append(kept, d)
		}
	}
	return kept
}

// iou is the intersection-over-union of two [x1,y1,x2,y2] boxes.
func iou(a, b [4]float64) float64 {
	ix1, iy1 := math.Max(a[0], b[0]), math.Max(a[1], b[1])
	ix2, iy2 := math.Min(a[2], b[2]), math.Min(a[3], b[3])
	iw, ih := math.Max(0, ix2-ix1), math.Max(0, iy2-iy1)
	inter := iw * ih
	union := (a[2]-a[0])*(a[3]-a[1]) + (b[2]-b[0])*(b[3]-b[1]) - inter
	if union <= 0 {
		return 0
	}
	return inter / union
}

// similarityTransform estimates the least-squares 2-D similarity (uniform scale + rotation +
// translation, no shear) mapping the `from` points onto the `to` points, returned as a 2×3
// affine matrix m such that [x',y'] = m·[x,y,1]. This is the closed-form Horn/Umeyama
// solution expressed with complex numbers: with points as complex p,q, the multiplier
// c = Σ conj(p'_i)·q'_i / Σ|p'_i|² (primes = mean-centred) is scale·e^{iθ}, and the
// translation is mean_q − c·mean_p.
func similarityTransform(from, to [numLandmarks][2]float64) [2][3]float64 {
	var mp, mq complex128
	for i := 0; i < numLandmarks; i++ {
		mp += complex(from[i][0], from[i][1])
		mq += complex(to[i][0], to[i][1])
	}
	mp /= numLandmarks
	mq /= numLandmarks

	var num, den complex128
	for i := 0; i < numLandmarks; i++ {
		p := complex(from[i][0], from[i][1]) - mp
		q := complex(to[i][0], to[i][1]) - mq
		num += cmplx.Conj(p) * q
		den += complex(real(p)*real(p)+imag(p)*imag(p), 0)
	}
	var c complex128
	if den != 0 {
		c = num / den
	}
	t := mq - c*mp
	a, b := real(c), imag(c) // [[a,-b],[b,a]]
	return [2][3]float64{
		{a, -b, real(t)},
		{b, a, imag(t)},
	}
}

// alignTo112 warps the face out of src into a 112×112 RGBA crop so its landmarks land on the
// canonical template. It estimates the template→landmarks transform and, for each output
// pixel, bilinearly samples the corresponding source location — a deterministic,
// reproducible resampling (float ops only, edge-clamped).
func alignTo112(src image.Image, lm [numLandmarks][2]float64) *image.RGBA {
	m := similarityTransform(arcFaceTemplate, lm) // output(template space) → source pixels
	out := image.NewRGBA(image.Rect(0, 0, alignSize, alignSize))
	for oy := 0; oy < alignSize; oy++ {
		fy := float64(oy)
		for ox := 0; ox < alignSize; ox++ {
			fx := float64(ox)
			sx := m[0][0]*fx + m[0][1]*fy + m[0][2]
			sy := m[1][0]*fx + m[1][1]*fy + m[1][2]
			out.SetRGBA(ox, oy, bilinearSample(src, sx, sy))
		}
	}
	return out
}

// bilinearSample reads src at fractional (x,y) with bilinear interpolation, clamping to the
// image edges so out-of-bounds samples replicate the border rather than wrap or panic.
func bilinearSample(src image.Image, x, y float64) color.RGBA {
	b := src.Bounds()
	x0 := int(math.Floor(x))
	y0 := int(math.Floor(y))
	dx := x - float64(x0)
	dy := y - float64(y0)

	at := func(ix, iy int) (float64, float64, float64, float64) {
		ix = clamp(ix, b.Min.X, b.Max.X-1)
		iy = clamp(iy, b.Min.Y, b.Max.Y-1)
		r, g, bl, a := src.At(ix, iy).RGBA() // 16-bit per channel
		return float64(r >> 8), float64(g >> 8), float64(bl >> 8), float64(a >> 8)
	}
	r00, g00, b00, a00 := at(x0, y0)
	r10, g10, b10, a10 := at(x0+1, y0)
	r01, g01, b01, a01 := at(x0, y0+1)
	r11, g11, b11, a11 := at(x0+1, y0+1)

	lerp := func(c00, c10, c01, c11 float64) uint8 {
		top := c00 + (c10-c00)*dx
		bot := c01 + (c11-c01)*dx
		return uint8(math.Round(top + (bot-top)*dy))
	}
	return color.RGBA{
		R: lerp(r00, r10, r01, r11),
		G: lerp(g00, g10, g01, g11),
		B: lerp(b00, b10, b01, b11),
		A: lerp(a00, a10, a01, a11),
	}
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// toFace converts an internal detection (corner box + float landmarks, sans embedding) into
// the public Face (origin+size box, integer landmarks).
func toFace(d faceDetection) Face {
	x1, y1 := int(math.Round(d.bbox[0])), int(math.Round(d.bbox[1]))
	x2, y2 := int(math.Round(d.bbox[2])), int(math.Round(d.bbox[3]))
	f := Face{
		BBox:  [4]int{x1, y1, x2 - x1, y2 - y1},
		Score: float32(d.score),
	}
	for k := 0; k < numLandmarks; k++ {
		f.Landmarks[k] = [2]int{int(math.Round(d.landmarks[k][0])), int(math.Round(d.landmarks[k][1]))}
	}
	return f
}

// l2Normalize scales v to unit L2 norm in place and returns it (a no-op for a zero vector).
func l2Normalize(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return v
	}
	inv := float32(1.0 / math.Sqrt(sum))
	for i := range v {
		v[i] *= inv
	}
	return v
}
