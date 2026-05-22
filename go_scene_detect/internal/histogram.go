//
//            PySceneDetect: Python-Based Video Scene Detector
//  -------------------------------------------------------------------
//     [  Site:   https://scenedetect.com                           ]
//     [  Docs:   https://scenedetect.com/docs/                     ]
//     [  Github: https://github.com/Breakthrough/PySceneDetect/    ]
//
// Copyright (C) 2014-2024 Brandon Castellano <http://www.bcastell.com>.
// PySceneDetect is licensed under the BSD 3-Clause License; see the
// included LICENSE file, or visit one of the above pages for details.
// License: https://github.com/Breakthrough/PySceneDetect/blob/main/LICENSE

package imgmath

import "math"

// Calc builds a normalized 256-bin histogram of a GRAY8 pixel plane.
// The returned slice has length 256 and sums to 1.0 (unless data is empty).
//
// Equivalent to:
//
//	h = cv2.calcHist([channel], [0], None, [256], [0, 256])
//	h = cv2.normalize(h, h).flatten()
func Calc(gray []byte) []float64 {
	hist := make([]float64, 256)
	for _, v := range gray {
		hist[v]++
	}
	total := float64(len(gray))
	if total > 0 {
		for i := range hist {
			hist[i] /= total
		}
	}
	return hist
}

// Correlation computes the Pearson correlation coefficient between two
// normalized histograms. Returns a value in [–1, 1] where 1 means
// identical distributions.
//
// Equivalent to cv2.compareHist(h1, h2, cv2.HISTCMP_CORREL).
//
// Formula (OpenCV definition):
//
//	d(H1,H2) = Σ(H1ᵢ − H̄1)(H2ᵢ − H̄2) / sqrt(Σ(H1ᵢ − H̄1)² · Σ(H2ᵢ − H̄2)²)
//
// where H̄k = (1/N) Σ Hkᵢ.
func Correlation(h1, h2 []float64) float64 {
	n := len(h1)
	if n == 0 || len(h2) != n {
		return 0
	}

	var m1, m2 float64
	for i := 0; i < n; i++ {
		m1 += h1[i]
		m2 += h2[i]
	}
	fN := float64(n)
	m1 /= fN
	m2 /= fN

	var num, d1sq, d2sq float64
	for i := 0; i < n; i++ {
		a := h1[i] - m1
		b := h2[i] - m2
		num += a * b
		d1sq += a * a
		d2sq += b * b
	}

	denom := math.Sqrt(d1sq * d2sq)
	if denom == 0 {
		return 0
	}
	return num / denom
}
