//
//            PySceneDetect: Python-Based Video Scene Detector
//  -------------------------------------------------------------------
//     [  Site:   https://scenedetect.com                           ]
//     [  Docs:   https://scenedetect.com/docs/                     ]
//     [  Github: https://github.com/Breakthrough/PySceneDetect/    ]
//
// Copyright (C) 2018 Brandon Castellano <http://www.bcastell.com>.
// PySceneDetect is licensed under the BSD 3-Clause License; see the
// included LICENSE file, or visit one of the above pages for details.
// License: https://github.com/Breakthrough/PySceneDetect/blob/main/LICENSE

package detectors

// Ported from scenedetect/detectors/threshold_detector.py.

import (
	"fmt"
	"math"

	psd "github.com/MediaMolder/MediaMolder/PySceneDetect"
)

// thresholdValueKey is the per-frame stat written to StatsManager.
// Matches Python's ThresholdDetector.THRESHOLD_VALUE_KEY = "average_rgb".
const thresholdValueKey = "average_rgb"

// ThresholdMethod controls whether ThresholdDetector looks for fades to black
// (FLOOR, the default) or fades to white (CEILING).
type ThresholdMethod int

const (
	// ThresholdMethodFloor detects a fade-out when average brightness falls
	// below the threshold (fade-to-black). This is the default.
	ThresholdMethodFloor ThresholdMethod = 0

	// ThresholdMethodCeiling detects a fade-out when average brightness rises
	// above the threshold (fade-to-white / flash detection).
	ThresholdMethodCeiling ThresholdMethod = 1
)

// ThresholdDetector detects cuts based on average frame brightness crossing a
// threshold. It finds fade-in / fade-out transitions: a cut is emitted each time
// the brightness transitions from a "fade-out" state back to a "fade-in" state.
// The cut timecode is positioned between the two states using fade_bias.
//
// Ported from scenedetect/detectors/threshold_detector.py.
// Copyright (C) 2018 Brandon Castellano.
type ThresholdDetector struct {
	threshold     float64
	method        ThresholdMethod
	fadeBias      float64
	addFinalScene bool

	// min-scene-len: raw value for lazy fps-dependent resolution.
	minSceneLenRaw any
	minFrames      int64
	minFramesReady bool

	stats *psd.StatsManager

	// State machine fields — mirror Python's instance variables.
	processedFrame  bool
	lastSceneCut    psd.FrameTimecode
	hasLastSceneCut bool
	lastFadeType    string // "in" or "out"; empty = not yet set
	lastFadeFrame   psd.FrameTimecode

	// lastAvgRGB is the average_rgb value computed for the most recent frame.
	lastAvgRGB float64
}

// NewThresholdDetector creates a ThresholdDetector.
//
//   - threshold: average brightness threshold in [0, 255] (default 12).
//   - minSceneLen: minimum scene length as a TimecodeLike; pass 0 to disable.
//   - fadeBias: [-1.0, 1.0]; -1 places the cut at the fade-out frame,
//     0 at the midpoint, and +1 at the fade-in frame.
//   - addFinalScene: if true, PostProcess emits a cut when the video ends on
//     a fade-out (only effective when using the detector directly or via
//     SceneManager; the processors.Processor wrapper cannot return this cut).
//   - method: ThresholdMethodFloor (detect fade-to-black) or
//     ThresholdMethodCeiling (detect fade-to-white).
func NewThresholdDetector(
	threshold float64,
	minSceneLen any,
	fadeBias float64,
	addFinalScene bool,
	method ThresholdMethod,
) (*ThresholdDetector, error) {
	if fadeBias < -1.0 || fadeBias > 1.0 {
		return nil, fmt.Errorf("threshold_detector: fade_bias must be in [-1.0, 1.0], got %g", fadeBias)
	}
	return &ThresholdDetector{
		threshold:      threshold,
		method:         method,
		fadeBias:       fadeBias,
		addFinalScene:  addFinalScene,
		minSceneLenRaw: minSceneLen,
	}, nil
}

// SetStats attaches a StatsManager and registers the "average_rgb" metric key.
func (d *ThresholdDetector) SetStats(s *psd.StatsManager) {
	d.stats = s
	if s != nil {
		s.RegisterMetrics([]string{thresholdValueKey})
	}
}

// isFadeOut returns true when frameAvg represents a "fade out" for the
// configured method.
//
//   - FLOOR:   fade-out when avg < threshold  (dim frame → going to black)
//   - CEILING: fade-out when avg ≥ threshold  (bright frame → going to white)
func (d *ThresholdDetector) isFadeOut(avg float64) bool {
	if d.method == ThresholdMethodFloor {
		return avg < d.threshold
	}
	return avg >= d.threshold
}

// ProcessFrame implements SceneDetector.
//
// Computes the mean of all B, G, R pixel values, tracks fade-in/fade-out state
// transitions, and emits a cut timecode when a fade-in follows a fade-out and
// the minimum scene length has elapsed.
func (d *ThresholdDetector) ProcessFrame(t psd.FrameTimecode, frame *psd.FrameData) ([]psd.FrameTimecode, error) {
	// Resolve min_scene_len on first call once fps is known.
	if !d.minFramesReady {
		n, err := resolveMinSceneLen(d.minSceneLenRaw, t.FrameRate())
		if err != nil {
			return nil, fmt.Errorf("threshold_detector: min_scene_len: %w", err)
		}
		d.minFrames = n
		d.minFramesReady = true
	}

	// Python: "if self.last_scene_cut is None: self.last_scene_cut = timecode"
	// Runs at the very top of process_frame on every call until set.
	if !d.hasLastSceneCut {
		d.lastSceneCut = t
		d.hasLastSceneCut = true
	}

	frameAvg := computeFrameAvg(frame)
	d.lastAvgRGB = frameAvg
	if d.stats != nil {
		d.stats.SetMetrics(t.FrameNum(), map[string]float64{thresholdValueKey: frameAvg})
	}

	var cuts []psd.FrameTimecode

	if d.processedFrame {
		if d.lastFadeType == "in" && d.isFadeOut(frameAvg) {
			// Transitioned out of a bright scene — wait for the next fade-in.
			d.lastFadeType = "out"
			d.lastFadeFrame = t

		} else if d.lastFadeType == "out" && !d.isFadeOut(frameAvg) {
			// Transitioned back into a bright scene.
			elapsed := t.FrameNum() - d.lastSceneCut.FrameNum()
			if d.minFrames == 0 || elapsed >= d.minFrames {
				// Position the cut between the fade-out and fade-in endpoints using
				// fade_bias. Matches Python's frame-number arithmetic exactly:
				//   split = f_out + round(duration * (1 + bias) / 2)
				// bias=-1 → split = f_out (cut at start of dark period)
				// bias= 0 → split = midpoint
				// bias=+1 → split = timecode (cut at first bright frame)
				fOutFrameNum := d.lastFadeFrame.FrameNum()
				durationFrames := t.FrameNum() - fOutFrameNum
				splitFrameNum := fOutFrameNum + int64(math.Round(float64(durationFrames)*(1.0+d.fadeBias)/2.0))
				splitTC, err := psd.NewFrameTimecode(splitFrameNum, t.FrameRate())
				if err != nil {
					return nil, fmt.Errorf("threshold_detector: split timecode: %w", err)
				}
				cuts = append(cuts, splitTC)
				d.lastSceneCut = t
			}
			// Transition to "in" regardless of whether min_scene_len was met —
			// matches Python behaviour: the next fade-out/in cycle is always evaluated.
			d.lastFadeType = "in"
			d.lastFadeFrame = t
		}
	} else {
		// First frame: initialise the fade state.
		// Note: the brightness check here always uses FLOOR semantics, matching the
		// Python reference implementation exactly (an intentional simplification for
		// the initial state; subsequent transitions use the configured method).
		d.lastFadeFrame = t
		if frameAvg < d.threshold {
			d.lastFadeType = "out"
		} else {
			d.lastFadeType = "in"
		}
		d.processedFrame = true
	}

	return cuts, nil
}

// PostProcess implements SceneDetector.
//
// If addFinalScene is true and the video ended on a fade-out with sufficient
// elapsed time, emits a cut at the fade-out frame. Mirrors Python's
// ThresholdDetector.post_process().
//
// Note: this cut cannot be surfaced through the processors.Processor wrapper
// (Close() returns only error). Use the detector directly or via SceneManager.
func (d *ThresholdDetector) PostProcess(t psd.FrameTimecode) ([]psd.FrameTimecode, error) {
	if !d.addFinalScene || !d.processedFrame || d.lastFadeType != "out" {
		return nil, nil
	}

	// Python: "elapsed = timecode if self.last_scene_cut is None else timecode - self.last_scene_cut"
	var elapsed int64
	if !d.hasLastSceneCut {
		elapsed = t.FrameNum()
	} else {
		elapsed = t.FrameNum() - d.lastSceneCut.FrameNum()
	}

	if d.minFrames == 0 || elapsed >= d.minFrames {
		return []psd.FrameTimecode{d.lastFadeFrame}, nil
	}
	return nil, nil
}

// LastAvgRGB returns the average_rgb value computed for the most recent frame.
// Returns 0 before any frame has been processed.
func (d *ThresholdDetector) LastAvgRGB() float64 {
	return d.lastAvgRGB
}

// GetMetrics implements SceneDetector.
func (d *ThresholdDetector) GetMetrics() []string {
	return []string{thresholdValueKey}
}

// EventBufferLength implements SceneDetector.
// ThresholdDetector always reports cuts at or before the current frame.
func (d *ThresholdDetector) EventBufferLength() int64 {
	return 0
}

// computeFrameAvg returns the mean of all B, G, R byte values in the frame.
// Equivalent to numpy.mean(frame_img) on a uint8 BGR ndarray.
func computeFrameAvg(frame *psd.FrameData) float64 {
	if len(frame.BGR) == 0 {
		return 0.0
	}
	var sum uint64
	for _, b := range frame.BGR {
		sum += uint64(b)
	}
	return float64(sum) / float64(len(frame.BGR))
}
