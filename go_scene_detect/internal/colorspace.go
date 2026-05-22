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

// Package imgmath provides image processing primitives used by PySceneDetect
// detectors: colorspace conversion, pixel distance, DCT, edge detection,
// frame resize, and histogram comparison.
package imgmath

import "math"

// BGRToHSVPlanes converts a packed BGR24 image to three separate byte planes.
// Matches cv2.cvtColor(frame, cv2.COLOR_BGR2HSV) then cv2.split():
//   - H: 0–179 (hue/2, OpenCV convention)
//   - S: 0–255 (saturation)
//   - V: 0–255 (value = max channel)
//
// Input bgr must have length w*h*3 with each pixel stored as [B, G, R].
func BGRToHSVPlanes(bgr []byte, w, h int) (H, S, V []byte) {
	n := w * h
	H = make([]byte, n)
	S = make([]byte, n)
	V = make([]byte, n)
	for i := 0; i < n; i++ {
		b := float64(bgr[i*3])
		g := float64(bgr[i*3+1])
		r := float64(bgr[i*3+2])

		vMax := b
		if g > vMax {
			vMax = g
		}
		if r > vMax {
			vMax = r
		}
		vMin := b
		if g < vMin {
			vMin = g
		}
		if r < vMin {
			vMin = r
		}
		delta := vMax - vMin

		V[i] = byte(math.Round(vMax))

		if vMax == 0 {
			S[i] = 0
		} else {
			S[i] = byte(math.Round(delta / vMax * 255))
		}

		var hDeg float64
		if delta != 0 {
			switch vMax {
			case r:
				hDeg = 60 * math.Mod((g-b)/delta, 6)
			case g:
				hDeg = 60 * ((b-r)/delta + 2)
			default: // vMax == b
				hDeg = 60 * ((r-g)/delta + 4)
			}
			if hDeg < 0 {
				hDeg += 360
			}
		}
		H[i] = byte(math.Round(hDeg / 2)) // 0–179
	}
	return
}

// BGRToLuma returns the BT.601 luma (Y) channel for each pixel in a BGR24 image.
// Matches cv2.cvtColor(frame, cv2.COLOR_BGR2YUV) then cv2.split() for channel 0.
// Output has length w*h.
func BGRToLuma(bgr []byte, w, h int) []byte {
	n := w * h
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		b := float64(bgr[i*3])
		g := float64(bgr[i*3+1])
		r := float64(bgr[i*3+2])
		y := 0.114*b + 0.587*g + 0.299*r
		out[i] = byte(math.Round(y))
	}
	return out
}
