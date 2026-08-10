// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package face

// Facial expression: continuous blendshape coefficients for a detected face, from which two
// headline scores are derived — Smile and EyesOpen, both in [0,1]. A dense-landmark model
// (478 3-D points + a face-presence score, on a 256×256 eye-aligned face crop) feeds a small
// blendshape model that regresses 52 ARKit-compatible coefficients. Callers rank, gate, or
// search with the scores; the full coefficient vector is exposed for downstream uses.
//
// This file is buildable without the with_onnx tag: the crop geometry, tensor layout, and
// score derivation are pure, so they unit-test everywhere. Inference lives in
// expression_onnx.go (with_onnx) with a stub in expression_stub.go.

import (
	"errors"
	"image"
	"math"
)

// ErrNoFace is returned by AssessFaceExpression when the landmark model's face-presence
// score falls below exprPresenceThresh for the assessed region — the crop does not contain
// a face the model is confident about (heavy occlusion, a false-positive detection, an
// unreadable crop), so no expression verdict should be recorded.
var ErrNoFace = errors.New("face: no confident face in the assessed region")

const (
	// BlendshapeCount is the length of the blendshape coefficient vector.
	BlendshapeCount = 52

	exprMeshLandmarks = 478                   // dense-landmark model output points
	exprMeshValues    = exprMeshLandmarks * 3 // flattened (x, y, z) per point
	exprSubsetLen     = 146                   // landmark subset the blendshape model consumes

	// exprContext sizes the square crop the landmark model sees: side = max(w,h) of the
	// face bbox × this. Matches the upstream pipeline's ROI expansion (1.5× ≈ 25% margin
	// around the face), which the model was trained against.
	exprContext = 1.5

	// exprPresenceThresh gates on the landmark model's face-presence confidence (after
	// sigmoid; the upstream pipeline's default detection-confidence threshold). Below it,
	// AssessFaceExpression returns ErrNoFace rather than hallucinated coefficients.
	exprPresenceThresh = 0.5
)

// Blendshape coefficient indices (the model's output order; see BlendshapeNames for all 52).
const (
	BlendshapeEyeBlinkLeft    = 9
	BlendshapeEyeBlinkRight   = 10
	BlendshapeMouthSmileLeft  = 44
	BlendshapeMouthSmileRight = 45
)

// BlendshapeNames is the model's output order for the 52 coefficients — the ARKit-compatible
// facial expression set (index 0 is the neutral-face residual).
var BlendshapeNames = [BlendshapeCount]string{
	"_neutral", "browDownLeft", "browDownRight", "browInnerUp", "browOuterUpLeft",
	"browOuterUpRight", "cheekPuff", "cheekSquintLeft", "cheekSquintRight", "eyeBlinkLeft",
	"eyeBlinkRight", "eyeLookDownLeft", "eyeLookDownRight", "eyeLookInLeft", "eyeLookInRight",
	"eyeLookOutLeft", "eyeLookOutRight", "eyeLookUpLeft", "eyeLookUpRight", "eyeSquintLeft",
	"eyeSquintRight", "eyeWideLeft", "eyeWideRight", "jawForward", "jawLeft",
	"jawOpen", "jawRight", "mouthClose", "mouthDimpleLeft", "mouthDimpleRight",
	"mouthFrownLeft", "mouthFrownRight", "mouthFunnel", "mouthLeft", "mouthLowerDownLeft",
	"mouthLowerDownRight", "mouthPressLeft", "mouthPressRight", "mouthPucker", "mouthRight",
	"mouthRollLower", "mouthRollUpper", "mouthShrugLower", "mouthShrugUpper", "mouthSmileLeft",
	"mouthSmileRight", "mouthStretchLeft", "mouthStretchRight", "mouthUpperUpLeft", "mouthUpperUpRight",
	"noseSneerLeft", "noseSneerRight",
}

// blendshapeLandmarkSubset lists, in tensor-row order, the indices of the 478 dense
// landmarks the blendshape model consumes (the upstream pipeline's fixed subset; the tail
// 468–477 are the iris points).
var blendshapeLandmarkSubset = [exprSubsetLen]int{
	0, 1, 4, 5, 6, 7, 8, 10, 13, 14, 17, 21, 33, 37, 39,
	40, 46, 52, 53, 54, 55, 58, 61, 63, 65, 66, 67, 70, 78, 80,
	81, 82, 84, 87, 88, 91, 93, 95, 103, 105, 107, 109, 127, 132, 133,
	136, 144, 145, 146, 148, 149, 150, 152, 153, 154, 155, 157, 158, 159, 160,
	161, 162, 163, 168, 172, 173, 176, 178, 181, 185, 191, 195, 197, 234, 246,
	249, 251, 263, 267, 269, 270, 276, 282, 283, 284, 285, 288, 291, 293, 295,
	296, 297, 300, 308, 310, 311, 312, 314, 317, 318, 321, 323, 324, 332, 334,
	336, 338, 356, 361, 362, 365, 373, 374, 375, 377, 378, 379, 380, 381, 382,
	384, 385, 386, 387, 388, 389, 390, 397, 398, 400, 402, 405, 409, 415, 454,
	466, 468, 469, 470, 471, 472, 473, 474, 475, 476, 477,
}

// Expression is the facial-expression assessment of one face.
type Expression struct {
	// Smile is the mean of the two mouth-smile coefficients, [0,1].
	Smile float32
	// EyesOpen is 1 − the mean of the two eye-blink coefficients, [0,1]:
	// 1 = both eyes open, 0 = both shut.
	EyesOpen float32
	// Presence is the landmark model's face-presence confidence, [0,1] (post-sigmoid).
	// Always at or above the assessment threshold on a successful call.
	Presence float32
	// Blendshapes is the full coefficient vector, indexed per BlendshapeNames.
	Blendshapes [BlendshapeCount]float32
}

// exprCropGeom computes the square, eye-aligned sampling window for the landmark model:
// centred on the face bbox, side exprContext × max(w,h), rotated so the eye line runs
// horizontal (the alignment the model is trained on). Landmarks may be absent or degenerate
// (label-only faces, extreme profiles) — the window then stays axis-aligned; a bad bbox
// yields ok=false.
func exprCropGeom(bbox [4]int, lm [numLandmarks][2]int) (cx, cy, side, cosA, sinA float64, ok bool) {
	x, y, w, h := bbox[0], bbox[1], bbox[2], bbox[3]
	if w <= 0 || h <= 0 {
		return 0, 0, 0, 0, 0, false
	}
	cx = float64(x) + float64(w)/2
	cy = float64(y) + float64(h)/2
	side = exprContext * math.Max(float64(w), float64(h))
	cosA, sinA = 1, 0

	// Eye line from detector keypoints 0 (left eye) and 1 (right eye). Degenerate spacing
	// (collapsed or implausibly small against the bbox — includes all-zero landmarks)
	// keeps the axis-aligned window.
	ex := float64(lm[1][0] - lm[0][0])
	ey := float64(lm[1][1] - lm[0][1])
	if dist := math.Hypot(ex, ey); dist >= 0.1*float64(w) {
		cosA, sinA = ex/dist, ey/dist
	}
	return cx, cy, side, cosA, sinA, true
}

// exprSampleCrop samples the rotated square window (exprCropGeom's output) into a size×size
// RGBA crop, nearest-neighbour, black outside the image bounds (the same padding the model's
// native pipeline uses). Output +x runs along the eye line, so the face comes out upright.
// Deterministic: pure integer/float math on At() reads.
func exprSampleCrop(img image.Image, cx, cy, side float64, cosA, sinA float64, size int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	b := img.Bounds()
	step := side / float64(size)
	half := float64(size) / 2
	for oy := 0; oy < size; oy++ {
		vy := (float64(oy) + 0.5 - half) * step
		for ox := 0; ox < size; ox++ {
			vx := (float64(ox) + 0.5 - half) * step
			sx := int(math.Floor(cx + vx*cosA - vy*sinA))
			sy := int(math.Floor(cy + vx*sinA + vy*cosA))
			if (image.Point{sx, sy}).In(b) {
				r, g, bl, _ := img.At(sx, sy).RGBA()
				i := dst.PixOffset(ox, oy)
				dst.Pix[i] = uint8(r >> 8)
				dst.Pix[i+1] = uint8(g >> 8)
				dst.Pix[i+2] = uint8(bl >> 8)
				dst.Pix[i+3] = 0xff
			} else {
				dst.Pix[dst.PixOffset(ox, oy)+3] = 0xff // black, opaque
			}
		}
	}
	return dst
}

// meshSubsetPoints fills dst ([exprSubsetLen×2], row-major) with the (x, y) of the blendshape
// landmark subset out of a flattened 478×(x,y,z) mesh output. Coordinates stay in the mesh's
// own crop-pixel space: the blendshape model normalises scale, rotation and translation
// internally, so no re-projection into source coordinates is needed (or wanted — it would
// only add float noise).
func meshSubsetPoints(mesh []float32, dst []float32) {
	for i, li := range blendshapeLandmarkSubset {
		dst[2*i] = mesh[3*li]
		dst[2*i+1] = mesh[3*li+1]
	}
}

// expressionFromCoeffs derives the headline scores from the raw coefficient vector.
func expressionFromCoeffs(coeffs []float32, presence float32) Expression {
	e := Expression{Presence: presence}
	copy(e.Blendshapes[:], coeffs)
	e.Smile = clamp01f((e.Blendshapes[BlendshapeMouthSmileLeft] + e.Blendshapes[BlendshapeMouthSmileRight]) / 2)
	e.EyesOpen = clamp01f(1 - (e.Blendshapes[BlendshapeEyeBlinkLeft]+e.Blendshapes[BlendshapeEyeBlinkRight])/2)
	return e
}

func clamp01f(v float32) float32 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// sigmoid maps the landmark model's raw presence logit to a [0,1] confidence.
func sigmoid(x float32) float32 {
	return float32(1 / (1 + math.Exp(-float64(x))))
}
