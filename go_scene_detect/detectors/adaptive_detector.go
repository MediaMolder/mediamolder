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

// Ported from scenedetect/detectors/adaptive_detector.py.

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	psd "github.com/MediaMolder/MediaMolder/go_scene_detect"
)

// bufferEntry holds a (timecode, content_val) pair for the AdaptiveDetector window.
type bufferEntry struct {
	timecode psd.FrameTimecode
	score    float64
}

// AdaptiveDetector detects cuts using a rolling-window ratio of adjacent frame
// content scores. Unlike ContentDetector's fixed threshold, the effective
// threshold adapts to the local motion level: a cut fires when the centre
// frame's score is significantly higher than its neighbours.
//
// Ported from scenedetect/detectors/adaptive_detector.py.
// Copyright (C) 2021 Brandon Castellano.
type AdaptiveDetector struct {
	// ContentDetector handles HSV conversion and scoring.
	// Initialised with threshold=255 so it never emits cuts directly;
	// AdaptiveDetector manages cut detection via the adaptive ratio.
	ContentDetector

	adaptiveThreshold float64 // default 3.0
	minContentVal     float64 // default 15.0
	windowWidth       int     // default 2; must be >= 1
	adaptiveRatioKey  string  // e.g. "adaptive_ratio (w=2)" or "adaptive_ratio_lum (w=2)"

	// Rolling window buffer — capped at 1 + 2*windowWidth entries.
	buffer []bufferEntry

	// Min-scene-length tracking — mirrors Python's self._last_cut.
	lastCut    psd.FrameTimecode
	hasLastCut bool

	// minSceneLenRaw is the original value from the constructor, kept for
	// lazy fps-dependent resolution on the first ProcessFrame call.
	minSceneLenRaw any
	minFrames      int64 // resolved frame count; 0 = disabled
	minFramesReady bool  // true once minFrames is finalised
}

// NewAdaptiveDetector creates an AdaptiveDetector.
//
//   - adaptiveThreshold: ratio that must be exceeded to trigger a cut (default 3.0).
//   - minSceneLen: minimum scene length as a TimecodeLike (int frames, float64
//     seconds, string like "0.6s", or FrameTimecode). Pass 0 to disable.
//   - windowWidth: number of frames before and after the target to average (default 2; min 1).
//   - minContentVal: minimum content_val the centre frame must meet (default 15.0).
//   - weights: per-channel weights forwarded to ContentDetector.
//   - lumaOnly: if true the stats key is suffixed with "_lum" (matches Python naming).
//   - kernelSize: dilation kernel size; 0 = auto (EstimatedKernelSize).
func NewAdaptiveDetector(
	adaptiveThreshold float64,
	minSceneLen any,
	windowWidth int,
	minContentVal float64,
	weights ContentWeights,
	lumaOnly bool,
	kernelSize int,
) (*AdaptiveDetector, error) {
	if windowWidth < 1 {
		return nil, fmt.Errorf("adaptive_detector: window_width must be >= 1, got %d", windowWidth)
	}

	// Parent uses threshold=255 (never fires) and minSceneLen=0 (disabled);
	// AdaptiveDetector manages cut detection itself.
	parent, err := NewContentDetector(255.0, 0, weights, kernelSize, psd.FlashFilterModeMerge)
	if err != nil {
		return nil, err
	}

	// Stats key: "adaptive_ratio (w=N)" or "adaptive_ratio_lum (w=N)".
	// Matches Python's ADAPTIVE_RATIO_KEY_TEMPLATE.format(luma_only=..., window_width=...).
	lumaOnlySuffix := ""
	if lumaOnly {
		lumaOnlySuffix = "_lum"
	}
	key := fmt.Sprintf("adaptive_ratio%s (w=%d)", lumaOnlySuffix, windowWidth)

	return &AdaptiveDetector{
		ContentDetector:   *parent,
		adaptiveThreshold: adaptiveThreshold,
		minContentVal:     minContentVal,
		windowWidth:       windowWidth,
		adaptiveRatioKey:  key,
		minSceneLenRaw:    minSceneLen,
	}, nil
}

// SetStats attaches a StatsManager and registers all metrics, including the
// adaptive_ratio key that is specific to this detector.
func (d *AdaptiveDetector) SetStats(s *psd.StatsManager) {
	d.ContentDetector.SetStats(s)
	if s != nil {
		s.RegisterMetrics([]string{d.adaptiveRatioKey})
	}
}

// ProcessFrame implements SceneDetector.
//
// Delegates HSV scoring to the embedded ContentDetector, buffers scores in a
// rolling window, and emits a cut when the centre frame's adaptive ratio and
// minimum content value exceed the configured thresholds.
func (d *AdaptiveDetector) ProcessFrame(t psd.FrameTimecode, frame *psd.FrameData) ([]psd.FrameTimecode, error) {
	// Resolve min_scene_len to a frame count on the first call once fps is known.
	if !d.minFramesReady {
		n, err := resolveMinSceneLen(d.minSceneLenRaw, t.FrameRate())
		if err != nil {
			return nil, fmt.Errorf("adaptive_detector: min_scene_len: %w", err)
		}
		d.minFrames = n
		d.minFramesReady = true
	}

	// Check whether the parent already has a previous frame (hadPrev=true means
	// this call will produce a valid content_val score).
	hadPrev := d.ContentDetector.hasLast

	// Delegate to ContentDetector to update HSV state and compute content_val.
	// The parent threshold is 255 so it never emits cuts.
	_, err := d.ContentDetector.ProcessFrame(t, frame)
	if err != nil {
		return nil, err
	}

	// No score yet (first frame — parent just stored the initial state).
	if !hadPrev {
		return nil, nil
	}

	score := d.ContentDetector.Score()

	// Initialise lastCut to the timecode of the first scored frame.
	// Mirrors Python's "if self._last_cut is None: self._last_cut = timecode".
	if !d.hasLastCut {
		d.lastCut = t
		d.hasLastCut = true
	}

	// Append to the rolling buffer and trim to the required window size.
	required := 1 + 2*d.windowWidth
	d.buffer = append(d.buffer, bufferEntry{timecode: t, score: score})
	if len(d.buffer) > required {
		d.buffer = d.buffer[len(d.buffer)-required:]
	}
	if len(d.buffer) < required {
		// Not enough frames yet to evaluate the full window.
		return nil, nil
	}

	// Centre frame of the window.
	centre := d.windowWidth
	targetTimecode := d.buffer[centre].timecode
	targetScore := d.buffer[centre].score

	// Average score of the 2*windowWidth non-centre entries.
	var sum float64
	for i, e := range d.buffer {
		if i != centre {
			sum += e.score
		}
	}
	avgWindowScore := sum / float64(2*d.windowWidth)

	// Compute adaptive_ratio:
	//   - zero average + high centre → max ratio (255)
	//   - zero average + low centre  → ratio 0
	//   - otherwise: min(centre / average, 255)
	var adaptiveRatio float64
	if math.Abs(avgWindowScore) < 0.00001 {
		if targetScore >= d.minContentVal {
			adaptiveRatio = 255.0
		}
	} else {
		adaptiveRatio = math.Min(targetScore/avgWindowScore, 255.0)
	}

	// Write stats if a StatsManager is attached.
	if d.ContentDetector.stats != nil {
		d.ContentDetector.stats.SetMetrics(targetTimecode.FrameNum(), map[string]float64{
			d.adaptiveRatioKey: adaptiveRatio,
		})
	}

	// Evaluate cut conditions:
	//   threshold_met: ratio >= adaptive_threshold AND score >= min_content_val
	//   min_length_met: frames since last cut >= min_scene_len
	//   (uses current timecode t, not target — matches Python behaviour)
	thresholdMet := adaptiveRatio >= d.adaptiveThreshold && targetScore >= d.minContentVal
	elapsed := t.FrameNum() - d.lastCut.FrameNum()
	minLengthMet := d.minFrames == 0 || elapsed >= d.minFrames

	if thresholdMet && minLengthMet {
		d.lastCut = targetTimecode
		return []psd.FrameTimecode{targetTimecode}, nil
	}
	return nil, nil
}

// PostProcess implements SceneDetector.
func (d *AdaptiveDetector) PostProcess(_ psd.FrameTimecode) ([]psd.FrameTimecode, error) {
	return nil, nil
}

// GetMetrics implements SceneDetector.
// Returns ContentDetector's five metrics plus the adaptive_ratio key.
func (d *AdaptiveDetector) GetMetrics() []string {
	return append(d.ContentDetector.GetMetrics(), d.adaptiveRatioKey)
}

// EventBufferLength implements SceneDetector.
// AdaptiveDetector may report a cut up to windowWidth frames behind the current frame.
func (d *AdaptiveDetector) EventBufferLength() int64 {
	return int64(d.windowWidth)
}

// resolveMinSceneLen converts a TimecodeLike value to a frame count given the
// stream's frame rate. Mirrors the parsing logic in goscenedetect.NewFlashFilter.
func resolveMinSceneLen(v any, fps float64) (int64, error) {
	switch n := v.(type) {
	case nil:
		return 0, nil
	case int:
		return int64(n), nil
	case int64:
		return n, nil
	case float64:
		return int64(math.Round(n * fps)), nil
	case string:
		s := strings.TrimSpace(n)
		if adaptIsDigitString(s) {
			frames, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid frame count %q: %w", s, err)
			}
			return frames, nil
		}
		secs, err := adaptParseTimecodeToSecs(s)
		if err != nil {
			return 0, err
		}
		return int64(math.Round(secs * fps)), nil
	case psd.FrameTimecode:
		return int64(math.Round(n.Seconds() * fps)), nil
	}
	return 0, fmt.Errorf("unsupported min_scene_len type %T", v)
}

func adaptIsDigitString(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func adaptParseTimecodeToSecs(s string) (float64, error) {
	if strings.HasSuffix(s, "s") {
		n, err := strconv.ParseFloat(s[:len(s)-1], 64)
		if err != nil {
			return 0, fmt.Errorf("invalid seconds value %q: %w", s, err)
		}
		return n, nil
	}
	if strings.Contains(s, ":") {
		parts := strings.Split(s, ":")
		var hrs, mins int
		var secs float64
		var err error
		switch len(parts) {
		case 2:
			mins, err = strconv.Atoi(parts[0])
			if err == nil {
				secs, err = strconv.ParseFloat(parts[1], 64)
			}
		case 3:
			hrs, err = strconv.Atoi(parts[0])
			if err == nil {
				mins, err = strconv.Atoi(parts[1])
			}
			if err == nil {
				secs, err = strconv.ParseFloat(parts[2], 64)
			}
		default:
			return 0, fmt.Errorf("invalid timecode %q: expected HH:MM:SS or MM:SS", s)
		}
		if err != nil {
			return 0, fmt.Errorf("invalid timecode %q: %w", s, err)
		}
		return float64(hrs)*3600 + float64(mins)*60 + secs, nil
	}
	return 0, fmt.Errorf("cannot parse timecode %q: expected HH:MM:SS, Ns, or frame count", s)
}
