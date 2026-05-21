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

// MeanPixelDistance returns the mean absolute per-pixel difference between
// two single-channel planes of equal length. Both slices must have the same
// non-zero length; returns 0 otherwise.
//
// Equivalent to:
//
//	numpy.sum(numpy.abs(a.astype(int) - b.astype(int))) / len(a)
//
// Used by ContentDetector to score H, S, V, and edge planes.
func MeanPixelDistance(a, b []byte) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var sum int64
	for i := range a {
		d := int(a[i]) - int(b[i])
		if d < 0 {
			d = -d
		}
		sum += int64(d)
	}
	return float64(sum) / float64(len(a))
}
