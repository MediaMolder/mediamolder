//
//            PySceneDetect: Python-Based Video Scene Detector
//  -------------------------------------------------------------------
//     [  Site:   https://scenedetect.com                           ]
//     [  Docs:   https://scenedetect.com/docs/                     ]
//     [  Github: https://github.com/Breakthrough/PySceneDetect/    ]
//
// Copyright (C) 2018-2024 Brandon Castellano <http://www.bcastell.com>.
// PySceneDetect is licensed under the BSD 3-Clause License; see the
// included LICENSE file, or visit one of the above pages for details.
// License: https://github.com/Breakthrough/PySceneDetect/blob/main/LICENSE

// Package detectors provides scene detection algorithms ported from PySceneDetect.
package detectors

// Ported from scenedetect/detectors/content_detector.py.

import (
	"math"

	psd "github.com/MediaMolder/MediaMolder/PySceneDetect"
	imgmath "github.com/MediaMolder/MediaMolder/PySceneDetect/internal"
)

// ContentWeights holds the per-channel weights used when computing the frame
// score. Mirrors ContentDetector.Components (a NamedTuple) in content_detector.py.
type ContentWeights struct {
	DeltaHue   float64 // weight for the HSV hue channel        (default 1.0)
	DeltaSat   float64 // weight for the HSV saturation channel (default 1.0)
	DeltaLum   float64 // weight for the HSV value/luma channel (default 1.0)
	DeltaEdges float64 // weight for the Canny edge-map channel (default 0.0)
}

var (
	// DefaultContentWeights weights all HSV channels equally with no edge
	// component. Equivalent to Python's DEFAULT_COMPONENT_WEIGHTS.
	DefaultContentWeights = ContentWeights{DeltaHue: 1, DeltaSat: 1, DeltaLum: 1}

	// LumaOnlyWeights weights only the luma (HSV value) channel.
	// Equivalent to Python's LUMA_ONLY_WEIGHTS.
	LumaOnlyWeights = ContentWeights{DeltaLum: 1}
)

// ContentDetector detects fast cuts by computing a weighted frame score across
// HSV colour channels and an optional Canny-edge map.
//
// Ported from scenedetect/detectors/content_detector.py.
// Copyright (C) 2018-2024 Brandon Castellano.
type ContentDetector struct {
	threshold   float64
	weights     ContentWeights
	flashFilter psd.FlashFilter

	// resolvedKernel is 0 until the first ProcessFrame call when DeltaEdges != 0;
	// thereafter it holds the fixed dilation kernel size (matches Python's lazy
	// "if self._kernel is None" initialisation).
	resolvedKernel int

	// Per-frame state — valid after the first ProcessFrame call.
	lastH     []byte
	lastS     []byte
	lastLum   []byte
	lastEdges []byte // nil when DeltaEdges == 0
	hasLast   bool
	lastScore float64

	// Optional per-frame statistics recording.
	stats *psd.StatsManager
}

// NewContentDetector creates a ContentDetector with the given configuration.
//
//   - threshold: frame score that triggers a cut (Python default 27.0).
//   - minSceneLen: minimum scene length as a TimecodeLike (int frames,
//     float64 seconds, string like "0.6s", or FrameTimecode). Pass 0 to disable.
//   - weights: per-channel score weights; use DefaultContentWeights for typical use.
//   - kernelSize: explicit dilation kernel size; 0 = auto (EstimatedKernelSize).
//   - filterMode: FlashFilterModeMerge or FlashFilterModeSuppress.
func NewContentDetector(
	threshold float64,
	minSceneLen any,
	weights ContentWeights,
	kernelSize int,
	filterMode psd.FlashFilterMode,
) (*ContentDetector, error) {
	ff, err := psd.NewFlashFilter(filterMode, minSceneLen)
	if err != nil {
		return nil, err
	}
	return &ContentDetector{
		threshold:      threshold,
		weights:        weights,
		resolvedKernel: kernelSize, // 0 = auto; set on first edge-detection call
		flashFilter:    ff,
	}, nil
}

// SetStats attaches an optional StatsManager for per-frame metric recording.
// Must be called before the first ProcessFrame call.
func (d *ContentDetector) SetStats(s *psd.StatsManager) {
	d.stats = s
	if s != nil {
		s.RegisterMetrics(d.GetMetrics())
	}
}

// Score returns the frame score computed during the most recent ProcessFrame
// call. Returns 0 before the second frame has been processed.
func (d *ContentDetector) Score() float64 { return d.lastScore }

// ProcessFrame implements SceneDetector.
//
// On the first call the frame is stored and nil is returned (no score possible).
// On subsequent calls the weighted HSV delta score is computed, optionally
// recorded to the attached StatsManager, and passed through the FlashFilter.
func (d *ContentDetector) ProcessFrame(t psd.FrameTimecode, frame *psd.FrameData) ([]psd.FrameTimecode, error) {
	H, S, lum := imgmath.BGRToHSVPlanes(frame.BGR, frame.Width, frame.Height)

	needEdges := d.weights.DeltaEdges != 0
	var edges []byte
	if needEdges {
		// Cache the kernel size after the first resolution — matches Python's
		// "if self._kernel is None: kernel_size = …; self._kernel = …" pattern.
		if d.resolvedKernel == 0 {
			d.resolvedKernel = imgmath.EstimatedKernelSize(frame.Width, frame.Height)
		}
		e := imgmath.Canny(lum, frame.Width, frame.Height, 0, 0, true)
		edges = imgmath.Dilate(e, frame.Width, frame.Height, d.resolvedKernel)
	}

	if !d.hasLast {
		d.lastH, d.lastS, d.lastLum, d.lastEdges = H, S, lum, edges
		d.hasLast = true
		return nil, nil
	}

	dHue := imgmath.MeanPixelDistance(H, d.lastH)
	dSat := imgmath.MeanPixelDistance(S, d.lastS)
	dLum := imgmath.MeanPixelDistance(lum, d.lastLum)
	var dEdges float64
	if needEdges && edges != nil && d.lastEdges != nil {
		dEdges = imgmath.MeanPixelDistance(edges, d.lastEdges)
	}

	// Weighted score: sum(component * weight) / sum(|weight|)
	score := dHue*d.weights.DeltaHue + dSat*d.weights.DeltaSat +
		dLum*d.weights.DeltaLum + dEdges*d.weights.DeltaEdges
	absSum := math.Abs(d.weights.DeltaHue) + math.Abs(d.weights.DeltaSat) +
		math.Abs(d.weights.DeltaLum) + math.Abs(d.weights.DeltaEdges)
	if absSum > 0 {
		score /= absSum
	}
	d.lastScore = score

	if d.stats != nil {
		d.stats.SetMetrics(t.FrameNum(), map[string]float64{
			"content_val": score,
			"delta_hue":   dHue,
			"delta_sat":   dSat,
			"delta_lum":   dLum,
			"delta_edges": dEdges,
		})
	}

	d.lastH, d.lastS, d.lastLum, d.lastEdges = H, S, lum, edges

	return d.flashFilter.Filter(t, score >= d.threshold), nil
}

// PostProcess implements SceneDetector.
func (d *ContentDetector) PostProcess(_ psd.FrameTimecode) ([]psd.FrameTimecode, error) {
	return nil, nil
}

// GetMetrics implements SceneDetector.
// Returns the names of per-frame statistics written to the StatsManager.
func (d *ContentDetector) GetMetrics() []string {
	return []string{"content_val", "delta_hue", "delta_sat", "delta_lum", "delta_edges"}
}

// EventBufferLength implements SceneDetector.
func (d *ContentDetector) EventBufferLength() int64 {
	return d.flashFilter.MaxBehind()
}
