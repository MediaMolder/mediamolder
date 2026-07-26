// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package face

import (
	"image"
	"math"
)

// No-reference quality metrics for an aligned face crop, both bounded to [0,1]. These are cheap,
// pure-Go measures computed on the same 112×112 aligned crop the embedder uses (see alignTo112) —
// deliberately NOT the full frame-quality proposal (docs/architecture/proposals/blur-detection.md),
// just a stable focus + light-level score good enough to rank a person's own faces.

// sharpnessRef sets the variance-of-Laplacian that maps to ~1.0. Sharpness is a log-scaled ratio, so
// the value only affects the [0,1] SPREAD, not the ORDER (log is monotonic in variance) — a downstream
// sort by sharpness is identical for any positive ref. Tuned for a crisp 112px face crop.
const sharpnessRef = 400.0

// clipLevel: pixels this close to full black / full white count as clipped (blown or crushed).
const clipLevel = 5

// cropMetrics computes (sharpness, exposure) for an aligned RGBA crop. A degenerate/empty crop yields
// zeros. Iterates the packed Pix buffer directly; no allocation beyond one luma plane.
func cropMetrics(aligned *image.RGBA) (sharpness, exposure float32) {
	b := aligned.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 3 || h < 3 {
		return 0, 0
	}
	// Luma plane (Rec.601). Also accumulate mean + clipped fraction for exposure.
	luma := make([]float64, w*h)
	var sum float64
	clipped := 0
	for y := 0; y < h; y++ {
		row := aligned.PixOffset(b.Min.X, b.Min.Y+y)
		for x := 0; x < w; x++ {
			i := row + x*4
			r := float64(aligned.Pix[i])
			g := float64(aligned.Pix[i+1])
			bl := float64(aligned.Pix[i+2])
			yv := 0.299*r + 0.587*g + 0.114*bl
			luma[y*w+x] = yv
			sum += yv
			if yv <= clipLevel || yv >= 255-clipLevel {
				clipped++
			}
		}
	}
	n := float64(w * h)

	// Sharpness: variance of the 4-neighbour Laplacian over interior pixels.
	var lSum, lSum2 float64
	cnt := 0.0
	for y := 1; y < h-1; y++ {
		for x := 1; x < w-1; x++ {
			c := luma[y*w+x]
			lap := 4*c - luma[y*w+x-1] - luma[y*w+x+1] - luma[(y-1)*w+x] - luma[(y+1)*w+x]
			lSum += lap
			lSum2 += lap * lap
			cnt++
		}
	}
	if cnt > 0 {
		varLap := lSum2/cnt - (lSum/cnt)*(lSum/cnt)
		if varLap < 0 {
			varLap = 0
		}
		s := math.Log1p(varLap) / math.Log1p(sharpnessRef)
		sharpness = float32(clamp01(s))
	}

	// Exposure: mid-gray-peaked mean luma, penalized by the clipped fraction.
	meanN := (sum / n) / 255.0 // [0,1]
	tone := 1 - 2*math.Abs(meanN-0.5)
	if tone < 0 {
		tone = 0
	}
	exposure = float32(clamp01(tone * (1 - float64(clipped)/n)))
	return sharpness, exposure
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// Metrics is the no-reference quality of one face crop.
type Metrics struct {
	Sharpness float32
	Exposure  float32
}

// AssessFace computes quality metrics for a face WITHOUT re-detecting or re-embedding it: it re-aligns
// the crop from the stored 5-point landmarks (the same alignment the embedder used) and measures it.
// Pure Go — no model dependency — so it drives a cheap backfill of faces detected before these metrics
// existed. `img` is the decoded source frame the landmarks index into.
func AssessFace(img image.Image, landmarks [numLandmarks][2]int) Metrics {
	var lm [numLandmarks][2]float64
	for i := range landmarks {
		lm[i][0] = float64(landmarks[i][0])
		lm[i][1] = float64(landmarks[i][1])
	}
	s, e := cropMetrics(alignTo112(img, lm))
	return Metrics{Sharpness: s, Exposure: e}
}
