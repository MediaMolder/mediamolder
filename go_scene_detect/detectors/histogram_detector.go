//
//            PySceneDetect: Python-Based Video Scene Detector
//  -------------------------------------------------------------------
//     [  Site:   https://scenedetect.com                           ]
//     [  Docs:   https://scenedetect.com/docs/                     ]
//     [  Github: https://github.com/Breakthrough/PySceneDetect/    ]
//
// Copyright (C) 2021 Brandon Castellano <http://www.bcastell.com>.
// PySceneDetect is licensed under the BSD 3-Clause License; see the
// included LICENSE file, or visit one of the above pages for details.
// License: https://github.com/Breakthrough/PySceneDetect/blob/main/LICENSE

package detectors

// Ported from scenedetect/detectors/histogram_detector.py.

import (
	"fmt"
	"math"

	imgmath "github.com/MediaMolder/MediaMolder/go_scene_detect/internal"

	psd "github.com/MediaMolder/MediaMolder/go_scene_detect"
)

// HistogramDetector detects scene cuts by comparing per-frame luminance
// histograms using the Pearson correlation coefficient.  Mirrors
// HistogramDetector in histogram_detector.py.
//
// Algorithm:
//  1. Convert frame to BT.601 luma (Y channel).
//  2. Build a normalised `bins`-bin histogram of the luma values.
//  3. Compute Pearson correlation between consecutive histograms.
//  4. Fire a cut when correlation ≤ internalThreshold (= 1 − threshold_input)
//     and min_scene_len is satisfied.
//
// The user-facing `threshold` is the maximum tolerated *difference* between
// frames (default 0.05).  Internally it is stored inverted:
//
//	internalThreshold = max(0, min(1, 1 − threshold))
//
// so that threshold=0.05 means "cut when correlation ≤ 0.95".
type HistogramDetector struct {
	threshold float64 // internal: max(0, min(1, 1−threshold_input))
	bins      int     // number of histogram bins (default 256)
	metricKey string  // e.g. "hist_diff [bins=256]"

	minSceneLenRaw any
	minFrames      int64
	minFramesReady bool

	stats *psd.StatsManager

	// State
	lastHist     []float64 // normalised histogram of the previous frame; nil = first frame
	lastCut      psd.FrameTimecode
	hasLastCut   bool
	lastHistDiff float64 // most recently computed Pearson correlation
}

// NewHistogramDetector constructs a HistogramDetector.
//
//   - threshold:   maximum tolerated frame-to-frame difference in [0,1]
//     (default 0.05).  Internally stored as 1−threshold.
//   - bins:        number of histogram bins (default 256).
//   - minSceneLen: minimum scene length; accepts int frame count or "HH:MM:SS"
//     timecode (default 15).
func NewHistogramDetector(threshold float64, bins int, minSceneLen any) (*HistogramDetector, error) {
	if bins < 1 {
		return nil, fmt.Errorf("histogram_detector: bins must be ≥ 1, got %d", bins)
	}
	internal := math.Max(0.0, math.Min(1.0, 1.0-threshold))
	return &HistogramDetector{
		threshold:      internal,
		bins:           bins,
		metricKey:      fmt.Sprintf("hist_diff [bins=%d]", bins),
		minSceneLenRaw: minSceneLen,
	}, nil
}

// SetStats attaches a StatsManager for per-frame metric recording.
func (d *HistogramDetector) SetStats(s *psd.StatsManager) { d.stats = s }

// GetMetrics returns the metric keys written by this detector.
func (d *HistogramDetector) GetMetrics() []string { return []string{d.metricKey} }

// EventBufferLength returns 0; HistogramDetector emits cuts immediately.
func (d *HistogramDetector) EventBufferLength() int64 { return 0 }

// LastHistDiff returns the Pearson correlation computed for the most recently
// processed frame pair.  Returns 0 before the second frame.
func (d *HistogramDetector) LastHistDiff() float64 { return d.lastHistDiff }

// ProcessFrame implements psd.SceneDetector.
func (d *HistogramDetector) ProcessFrame(t psd.FrameTimecode, frame *psd.FrameData) ([]psd.FrameTimecode, error) {
	if !d.minFramesReady {
		n, err := resolveMinSceneLen(d.minSceneLenRaw, t.FrameRate())
		if err != nil {
			return nil, fmt.Errorf("histogram_detector: min_scene_len: %w", err)
		}
		d.minFrames = n
		d.minFramesReady = true
	}

	if !d.hasLastCut {
		d.lastCut = t
		d.hasLastCut = true
	}

	luma := imgmath.BGRToLuma(frame.BGR, frame.Width, frame.Height)
	hist := histogramN(luma, d.bins)

	var cuts []psd.FrameTimecode

	if d.lastHist != nil {
		corr := imgmath.Correlation(d.lastHist, hist)
		d.lastHistDiff = corr

		if d.stats != nil {
			d.stats.SetMetrics(t.FrameNum(), map[string]float64{d.metricKey: corr})
		}

		elapsed := t.FrameNum() - d.lastCut.FrameNum()
		if corr <= d.threshold && (d.minFrames == 0 || elapsed >= d.minFrames) {
			cuts = append(cuts, t)
			d.lastCut = t
		}
	}

	d.lastHist = hist
	return cuts, nil
}

// PostProcess implements psd.SceneDetector.  HistogramDetector has no deferred cuts.
func (d *HistogramDetector) PostProcess(_ psd.FrameTimecode) ([]psd.FrameTimecode, error) {
	return nil, nil
}

// histogramN builds a normalised bins-bin histogram of luma values.
// Equivalent to cv2.calcHist([y], [0], None, [bins], [0, 256]) followed by
// cv2.normalize(hist, hist) (L1-normalised; scale-invariant under Pearson
// correlation so equivalent to OpenCV's L2 normalisation for that metric).
func histogramN(luma []byte, bins int) []float64 {
	hist := make([]float64, bins)
	for _, v := range luma {
		bin := int(v) * bins / 256
		if bin >= bins {
			bin = bins - 1
		}
		hist[bin]++
	}
	n := float64(len(luma))
	if n > 0 {
		for i := range hist {
			hist[i] /= n
		}
	}
	return hist
}
