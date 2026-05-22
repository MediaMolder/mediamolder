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

// EstimatedKernelSize returns the morphological dilation kernel size for a
// frame of dimensions w×h.
//
// Ported from PySceneDetect ContentDetector._estimated_kernel_size():
//
//	round(4 + sqrt(w*h) / 192)
//
// The returned value k is used to construct a k×k rectangular kernel.
// Returns at least 1.
func EstimatedKernelSize(w, h int) int {
	k := int(math.Round(4.0 + math.Sqrt(float64(w*h))/192.0))
	if k < 1 {
		k = 1
	}
	if k%2 == 0 {
		k++ // Python: "if size % 2 == 0: size += 1"
	}
	return k
}

// Canny applies the Canny edge-detection algorithm to a grayscale (GRAY8)
// image. Returns a binary edge map where edge pixels are 255 and all others
// are 0. Equivalent to cv2.Canny(gray, lowThresh, highThresh).
//
// Steps: Sobel 3×3 gradient → gradient magnitude (L2) → non-maximum
// suppression → double-threshold hysteresis.
//
// If autoThreshold is true, lowThresh and highThresh are ignored and instead
// computed from the median pixel value (sigma = 0.33) as PySceneDetect does:
//
//	low  = max(0,   (1 - 0.33) * median)
//	high = min(255, (1 + 0.33) * median)
func Canny(gray []byte, w, h int, lowThresh, highThresh float64, autoThreshold bool) []byte {
	if autoThreshold {
		med := medianByte(gray)
		lowThresh = math.Max(0, (1.0-0.33)*float64(med))
		highThresh = math.Min(255, (1.0+0.33)*float64(med))
	}

	// Compute Sobel gradients.
	gx := make([]float64, w*h)
	gy := make([]float64, w*h)
	mag := make([]float64, w*h)
	for y := 1; y < h-1; y++ {
		for x := 1; x < w-1; x++ {
			// 3×3 Sobel (horizontal / vertical)
			p00 := float64(gray[(y-1)*w+(x-1)])
			p01 := float64(gray[(y-1)*w+x])
			p02 := float64(gray[(y-1)*w+(x+1)])
			p10 := float64(gray[y*w+(x-1)])
			p12 := float64(gray[y*w+(x+1)])
			p20 := float64(gray[(y+1)*w+(x-1)])
			p21 := float64(gray[(y+1)*w+x])
			p22 := float64(gray[(y+1)*w+(x+1)])

			dx := -p00 + p02 - 2*p10 + 2*p12 - p20 + p22
			dy := -p00 - 2*p01 - p02 + p20 + 2*p21 + p22

			gx[y*w+x] = dx
			gy[y*w+x] = dy
			mag[y*w+x] = math.Sqrt(dx*dx + dy*dy)
		}
	}

	// Non-maximum suppression.
	nms := make([]float64, w*h)
	for y := 1; y < h-1; y++ {
		for x := 1; x < w-1; x++ {
			idx := y*w + x
			m := mag[idx]
			angle := math.Atan2(gy[idx], gx[idx]) * 180 / math.Pi
			if angle < 0 {
				angle += 180
			}

			var q, r float64
			switch {
			case (0 <= angle && angle < 22.5) || (157.5 <= angle && angle <= 180):
				q = mag[y*w+(x+1)]
				r = mag[y*w+(x-1)]
			case 22.5 <= angle && angle < 67.5:
				q = mag[(y+1)*w+(x-1)]
				r = mag[(y-1)*w+(x+1)]
			case 67.5 <= angle && angle < 112.5:
				q = mag[(y+1)*w+x]
				r = mag[(y-1)*w+x]
			default: // 112.5–157.5
				q = mag[(y-1)*w+(x-1)]
				r = mag[(y+1)*w+(x+1)]
			}
			if m >= q && m >= r {
				nms[idx] = m
			}
		}
	}

	// Double-threshold classification.
	const strong byte = 255
	const weak byte = 128
	out := make([]byte, w*h)
	for i, m := range nms {
		if m >= highThresh {
			out[i] = strong
		} else if m >= lowThresh {
			out[i] = weak
		}
	}

	// Hysteresis: promote weak pixels connected to strong pixels (8-connectivity).
	for y := 1; y < h-1; y++ {
		for x := 1; x < w-1; x++ {
			if out[y*w+x] != weak {
				continue
			}
			if out[(y-1)*w+(x-1)] == strong || out[(y-1)*w+x] == strong ||
				out[(y-1)*w+(x+1)] == strong || out[y*w+(x-1)] == strong ||
				out[y*w+(x+1)] == strong || out[(y+1)*w+(x-1)] == strong ||
				out[(y+1)*w+x] == strong || out[(y+1)*w+(x+1)] == strong {
				out[y*w+x] = strong
			} else {
				out[y*w+x] = 0
			}
		}
	}

	return out
}

// Dilate applies morphological dilation to img with a kernelSize×kernelSize
// rectangular structuring element. Equivalent to:
//
//	cv2.dilate(img, numpy.ones((kernelSize, kernelSize), numpy.uint8))
//
// Each output pixel is the maximum of all input pixels within the window.
func Dilate(img []byte, w, h, kernelSize int) []byte {
	if kernelSize <= 1 {
		out := make([]byte, len(img))
		copy(out, img)
		return out
	}
	half := kernelSize / 2
	out := make([]byte, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var maxVal byte
			for ky := -half; ky <= half; ky++ {
				yy := y + ky
				if yy < 0 || yy >= h {
					continue
				}
				for kx := -half; kx <= half; kx++ {
					xx := x + kx
					if xx < 0 || xx >= w {
						continue
					}
					if v := img[yy*w+xx]; v > maxVal {
						maxVal = v
					}
				}
			}
			out[y*w+x] = maxVal
		}
	}
	return out
}

// medianByte returns the median value of a byte slice. Uses a histogram-based
// O(n) algorithm so it is non-destructive and suitable for large pixel planes.
func medianByte(data []byte) byte {
	var hist [256]int
	for _, v := range data {
		hist[v]++
	}
	mid := (len(data) + 1) / 2
	cum := 0
	for i, c := range hist {
		cum += c
		if cum >= mid {
			return byte(i)
		}
	}
	return 255
}
