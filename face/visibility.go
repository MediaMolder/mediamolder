// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package face

// Face-region visibility: how much of a detected face is actually visible, versus covered
// by something in front of it (a hand, a glass, another person's shoulder). A portrait
// multiclass-segmentation model labels each pixel of a head-context crop as background /
// hair / body-skin / face-skin / clothes / other; inside the face's own box, anything that
// is NOT face-skin or hair is coverage — a hand in front of a mouth reads as body-skin,
// a raised cup as other/background, regardless of what the occluder is. The result is a
// single occlusion fraction in [0,1]; callers rank or gate with it.
//
// This file is buildable without the with_onnx tag: the geometry and mask math are pure,
// so they unit-test everywhere. Inference lives in visibility_onnx.go (with_onnx) with a
// stub in visibility_stub.go.

import "image"

// Multiclass-segmentation class indices (the model's output channel order).
const (
	visClassBackground = 0
	visClassHair       = 1
	visClassBodySkin   = 2
	visClassFaceSkin   = 3
	visClassClothes    = 4
	visClassOther      = 5 // accessories etc.

	// visNumClasses is the model's output channel count.
	visNumClasses = 6
)

const (
	// visContextX/YTop/YBottom expand the face bbox to the head-context crop the model
	// sees: a person-segmentation model wants surroundings, not a tight face box. In bbox
	// widths/heights per side.
	visContextX       = 0.50
	visContextYTop    = 0.75
	visContextYBottom = 0.75

	// visMeasureShrink shrinks the face bbox before measuring, so the corners of a tight
	// detector box (which legitimately contain background) don't read as coverage.
	visMeasureShrink = 0.10

	// visExpectedCover calibrates "fully visible": even a clean frontal face's tight bbox
	// is not 100% face-skin+hair (jaw corners, ears, backdrop slivers). Coverage at or
	// above this fraction reads as occlusion 0; below it, occlusion grows linearly to 1.
	visExpectedCover = 0.85
)

// headContextRect returns the crop the segmentation model runs on: bbox expanded by the
// visContext margins, clamped to the image bounds. Empty when the bbox itself is empty.
func headContextRect(bbox [4]int, bounds image.Rectangle) image.Rectangle {
	x, y, w, h := bbox[0], bbox[1], bbox[2], bbox[3]
	if w <= 0 || h <= 0 {
		return image.Rectangle{}
	}
	r := image.Rect(
		x-int(float64(w)*visContextX),
		y-int(float64(h)*visContextYTop),
		x+w+int(float64(w)*visContextX),
		y+h+int(float64(h)*visContextYBottom),
	)
	return r.Intersect(bounds)
}

// measureRect returns the region of the face bbox that visibility is measured over — the
// bbox shrunk by visMeasureShrink per side, in the same coordinate space as the bbox.
func measureRect(bbox [4]int) image.Rectangle {
	x, y, w, h := bbox[0], bbox[1], bbox[2], bbox[3]
	dx := int(float64(w) * visMeasureShrink)
	dy := int(float64(h) * visMeasureShrink)
	return image.Rect(x+dx, y+dy, x+w-dx, y+h-dy)
}

// letterboxGeom reproduces letterbox()'s geometry — scale and centred offsets — so mask
// coordinates map back to source pixels. Must stay bit-identical to letterbox (and its
// inverse in the processors package); the rounding here mirrors it exactly.
func letterboxGeom(srcW, srcH, targetW, targetH int) (scale float64, offX, offY int) {
	if srcW == 0 || srcH == 0 {
		return 0, 0, 0
	}
	sw := float64(targetW) / float64(srcW)
	if sh := float64(targetH) / float64(srcH); sh < sw {
		sw = sh
	}
	newW := int(float64(srcW)*sw + 0.5)
	newH := int(float64(srcH)*sw + 0.5)
	if newW < 1 {
		newW = 1
	}
	if newH < 1 {
		newH = 1
	}
	return sw, (targetW - newW) / 2, (targetH - newH) / 2
}

// occlusionFromMask computes the occlusion fraction from a class mask. mask is a row-major
// maskW×maskH class-id plane; measure is the region to score IN MASK COORDINATES. Pixels
// labelled face-skin or hair count as visible; occlusion is the shortfall against
// visExpectedCover, clamped to [0,1]. A degenerate measure region yields 0 (no evidence of
// coverage is not evidence of coverage).
func occlusionFromMask(mask []uint8, maskW, maskH int, measure image.Rectangle) float64 {
	measure = measure.Intersect(image.Rect(0, 0, maskW, maskH))
	total := measure.Dx() * measure.Dy()
	if total <= 0 {
		return 0
	}
	visible := 0
	for y := measure.Min.Y; y < measure.Max.Y; y++ {
		row := y * maskW
		for x := measure.Min.X; x < measure.Max.X; x++ {
			switch mask[row+x] {
			case visClassFaceSkin, visClassHair:
				visible++
			}
		}
	}
	cover := float64(visible) / float64(total)
	occ := 1 - cover/visExpectedCover
	if occ < 0 {
		return 0
	}
	if occ > 1 {
		return 1
	}
	return occ
}
