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

// DCT2D computes the 2-D separable type-II DCT (orthonormal normalisation)
// of a row-major float32 image of size w×h. Returns a new []float32 slice.
//
// The result matches cv2.dct() for float32 input, which is used by
// HashDetector to produce a perceptual hash of each frame.
//
// Algorithm: apply the 1-D DCT along every row, then along every column.
// Time complexity: O(w·h·(w+h)) — acceptable for the small images used
// by HashDetector (typically 32×32 or smaller).
func DCT2D(data []float32, w, h int) []float32 {
	if len(data) != w*h || w <= 0 || h <= 0 {
		return nil
	}

	// Work in float64 for accuracy, same as cv2.dct internally.
	tmp := make([]float64, w*h)
	for i, v := range data {
		tmp[i] = float64(v)
	}

	row := make([]float64, w)
	// Row-wise DCT.
	for y := 0; y < h; y++ {
		copy(row, tmp[y*w:(y+1)*w])
		dct1D(row)
		copy(tmp[y*w:(y+1)*w], row)
	}

	col := make([]float64, h)
	// Column-wise DCT.
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			col[y] = tmp[y*w+x]
		}
		dct1D(col)
		for y := 0; y < h; y++ {
			tmp[y*w+x] = col[y]
		}
	}

	out := make([]float32, w*h)
	for i, v := range tmp {
		out[i] = float32(v)
	}
	return out
}

// dct1D applies the 1-D orthonormal type-II DCT to x in place.
//
// X[k] = w(k) · Σ_{n=0}^{N-1} x[n] · cos(π(2n+1)k / 2N)
//
// where w(0) = 1/√N and w(k) = √(2/N) for k > 0.
// This matches the OpenCV / scipy.fft.dct(x, type=2, norm='ortho') convention.
func dct1D(x []float64) {
	n := len(x)
	if n == 0 {
		return
	}
	out := make([]float64, n)
	fN := float64(n)
	w0 := 1.0 / math.Sqrt(fN)
	wk := math.Sqrt(2.0 / fN)
	for k := 0; k < n; k++ {
		var sum float64
		for i := 0; i < n; i++ {
			sum += x[i] * math.Cos(math.Pi*float64(2*i+1)*float64(k)/(2*fN))
		}
		if k == 0 {
			out[k] = w0 * sum
		} else {
			out[k] = wk * sum
		}
	}
	copy(x, out)
}
