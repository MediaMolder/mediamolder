//
//            PySceneDetect: Python-Based Video Scene Detector
//  -------------------------------------------------------------------
//     [  Site:   https://scenedetect.com                           ]
//     [  Docs:   https://scenedetect.com/docs/                     ]
//     [  Github: https://github.com/Breakthrough/PySceneDetect/    ]
//
// Copyright (C) 2025 Brandon Castellano <http://www.bcastell.com>.
// PySceneDetect is licensed under the BSD 3-Clause License; see the
// included LICENSE file, or visit one of the above pages for details.
// License: https://github.com/Breakthrough/PySceneDetect/blob/main/LICENSE
//

package goscenedetect

// Ported from scenedetect/common.py.

// Scene represents the start and end timecodes of a detected scene.
// Equivalent to a (start, end) FrameTimecode pair in the Python API.
type Scene struct {
	Start FrameTimecode
	End   FrameTimecode
}

// SceneList is an ordered list of detected scenes.
// Equivalent to Python's SceneList type alias.
type SceneList []Scene

// CutList is an ordered list of FrameTimecodes where scene cuts occur.
// Each timecode marks the first frame of a new shot.
// Equivalent to Python's CutList type alias.
type CutList []FrameTimecode

// CropRegion specifies a rectangular sub-region of a frame as (X0, Y0, X1, Y1).
// Coordinates are relative to the source frame before any downscaling.
// Equivalent to Python's CropRegion type alias.
type CropRegion [4]int

// Interpolation specifies the resampling method used when downscaling frames.
// Values correspond to OpenCV INTER_* constants; the Go port maps them to
// libswscale SWS_* flags in internal/downscale.go.
// Ported from scenedetect/common.py, class Interpolation.
type Interpolation int

const (
	// InterpolationNearest uses nearest-neighbour resampling.
	// Equivalent to cv2.INTER_NEAREST.
	InterpolationNearest Interpolation = iota
	// InterpolationLinear uses bilinear resampling. Default for downscaling.
	// Equivalent to cv2.INTER_LINEAR.
	InterpolationLinear
	// InterpolationCubic uses bicubic resampling.
	// Equivalent to cv2.INTER_CUBIC.
	InterpolationCubic
	// InterpolationArea uses pixel-area relation resampling. Best for downscaling
	// without moiré artefacts. Equivalent to cv2.INTER_AREA.
	InterpolationArea
	// InterpolationLanczos4 uses Lanczos resampling over an 8×8 neighbourhood.
	// Equivalent to cv2.INTER_LANCZOS4.
	InterpolationLanczos4
)

// FrameData holds decoded video frame data in BGR24 format.
// This is the Go equivalent of the numpy ndarray (BGR image) passed to
// Python scene detectors.
//
// Pixel at column x, row y:
//
//	BGR[3*(y*Width+x) : 3*(y*Width+x)+3]  →  [B, G, R]
type FrameData struct {
	Width  int
	Height int
	BGR    []byte
}

// DefaultMinWidth is the minimum frame width used when computing the automatic
// downscale factor. Frames narrower than this are not downscaled.
// Equivalent to Python's DEFAULT_MIN_WIDTH.
const DefaultMinWidth = 256

// ComputeDownscaleFactor returns the factor by which to divide frame dimensions
// so the effective width is at least DefaultMinWidth pixels.
// The result is always >= 1 (1 means no downscaling).
//
// Ported from scenedetect/scene_manager.py, compute_downscale_factor().
func ComputeDownscaleFactor(frameWidth int) float64 {
	if frameWidth <= 0 || frameWidth < DefaultMinWidth {
		return 1
	}
	return float64(frameWidth) / float64(DefaultMinWidth)
}
