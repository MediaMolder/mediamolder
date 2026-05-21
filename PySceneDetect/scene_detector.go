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

package pyscenedetect

// Ported from scenedetect/detector.py.

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// SceneDetector is the interface all scene detection algorithms must implement.
//
// Each call to ProcessFrame advances the detector's state machine by one frame.
// Cuts may be reported up to EventBufferLength frames before the current
// timecode; the caller must buffer frames accordingly.
//
// Ported from scenedetect/detector.py, class SceneDetector.
type SceneDetector interface {
	// ProcessFrame advances the detector by one frame.
	// Returns zero or more timecodes where scene cuts were detected.
	// Returned timecodes may be as early as EventBufferLength frames before t.
	ProcessFrame(t FrameTimecode, frame *FrameData) ([]FrameTimecode, error)

	// PostProcess is called once after the last frame has been processed.
	// Returns any remaining buffered cut timecodes.
	PostProcess(t FrameTimecode) ([]FrameTimecode, error)

	// GetMetrics returns the names of per-frame statistics this detector writes
	// to the StatsManager (if one is attached).
	GetMetrics() []string

	// EventBufferLength returns the maximum number of frames by which a
	// reported cut may lag behind the current ProcessFrame call.
	// For most detectors this is 0; AdaptiveDetector uses window_width.
	EventBufferLength() int64
}

// FlashFilterMode controls how FlashFilter enforces the minimum scene length.
// Ported from scenedetect/detector.py, FlashFilter.Mode.
type FlashFilterMode int

const (
	// FlashFilterModeMerge merges rapid-fire cuts so only the last frame of the
	// above-threshold region is emitted once the below-threshold region is long
	// enough. Preserves cuts when the scene length is genuinely met.
	FlashFilterModeMerge FlashFilterMode = iota

	// FlashFilterModeSuppress suppresses a cut entirely until min_scene_len
	// frames have elapsed since the previous cut.
	FlashFilterModeSuppress
)

// FlashFilter enforces a minimum scene length, filtering out cuts that occur
// too close together (e.g. camera flashes, stroboscopic effects).
//
// Ported from scenedetect/detector.py, class FlashFilter.
//
// The length parameter to NewFlashFilter accepts any TimecodeLike value:
//   - int / int64  — frame count
//   - float64      — seconds
//   - string       — timecode (HH:MM:SS or seconds) or digit-only frame count
//   - FrameTimecode — uses the timecode's seconds value
type FlashFilter struct {
	mode FlashFilterMode

	// minFrames holds the resolved minimum-scene-length in frames.
	// Before the first frame is processed it may be 0 if the length was given
	// in seconds; resolution happens on the first Filter() call.
	minFrames int64

	// minSecs holds the length in seconds when the constructor received a
	// float/string/FrameTimecode value.  hasMinSecs distinguishes "0 frames
	// (disabled)" from "0.0 seconds (disabled)".
	minSecs    float64
	hasMinSecs bool // true when length was specified in seconds

	// State machine.
	lastAbove    FrameTimecode
	hasLastAbove bool

	mergeEnabled   bool
	mergeTriggered bool
	mergeStart     FrameTimecode // valid only when mergeTriggered == true
}

// NewFlashFilter creates a FlashFilter.
//
// mode selects MERGE or SUPPRESS behaviour.
// length is a TimecodeLike: int, int64, float64 (seconds), string, or FrameTimecode.
// Pass length = 0 (or 0.0) to disable the filter.
//
// Ported from scenedetect/detector.py, FlashFilter.__init__.
func NewFlashFilter(mode FlashFilterMode, length any) (FlashFilter, error) {
	f := FlashFilter{mode: mode}
	switch v := length.(type) {
	case int:
		f.minFrames = int64(v)
	case int64:
		f.minFrames = v
	case float64:
		f.minSecs = v
		f.hasMinSecs = true
	case string:
		s := strings.TrimSpace(v)
		// Digit-only strings: treat as frame count. Any other string: parse as
		// timecode and store as seconds (using fps=100 placeholder, same as Python).
		if isDigitString(s) {
			n, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				return FlashFilter{}, fmt.Errorf("pyscenedetect: FlashFilter: invalid frame count %q: %w", s, err)
			}
			f.minFrames = n
		} else {
			secs, err := parseTimecodeToSecs(s, 100.0)
			if err != nil {
				return FlashFilter{}, fmt.Errorf("pyscenedetect: FlashFilter: invalid length %q: %w", s, err)
			}
			f.minSecs = secs
			f.hasMinSecs = true
		}
	case FrameTimecode:
		f.minSecs = v.Seconds()
		f.hasMinSecs = true
	default:
		return FlashFilter{}, fmt.Errorf("pyscenedetect: FlashFilter: unsupported length type %T", length)
	}
	return f, nil
}

// isDisabled reports whether the filter is effectively disabled (length = 0).
func (f *FlashFilter) isDisabled() bool {
	if f.hasMinSecs {
		return f.minSecs <= 0
	}
	return f.minFrames <= 0
}

// MaxBehind returns the maximum number of frames by which a reported cut may
// lag behind the currently processed frame.  Used as EventBufferLength.
//
// Ported from scenedetect/detector.py, FlashFilter.max_behind.
func (f *FlashFilter) MaxBehind() int64 {
	if f.mode == FlashFilterModeSuppress {
		return 0
	}
	if f.minFrames > 0 {
		return f.minFrames
	}
	if f.hasMinSecs {
		// Before fps is known, estimate using 240 fps (same as Python).
		return int64(math.Ceil(f.minSecs * 240.0))
	}
	return 0
}

// Filter processes one frame and returns any cut timecodes produced.
// aboveThreshold should be true when the detector's score exceeds its threshold.
//
// Ported from scenedetect/detector.py, FlashFilter.filter.
func (f *FlashFilter) Filter(t FrameTimecode, aboveThreshold bool) []FrameTimecode {
	if f.isDisabled() {
		if aboveThreshold {
			return []FrameTimecode{t}
		}
		return nil
	}

	// Initialise lastAbove on the very first frame.
	if !f.hasLastAbove {
		f.lastAbove = t
		f.hasLastAbove = true
	}

	// Resolve minFrames from seconds on first call once the fps is known.
	if f.minFrames == 0 && f.hasMinSecs && f.minSecs > 0 {
		fps := t.FrameRate()
		if fps > 0 {
			f.minFrames = int64(math.Round(f.minSecs * fps))
		}
	}

	switch f.mode {
	case FlashFilterModeMerge:
		return f.filterMerge(t, aboveThreshold)
	case FlashFilterModeSuppress:
		return f.filterSuppress(t, aboveThreshold)
	default:
		panic(fmt.Sprintf("pyscenedetect: FlashFilter: unhandled mode %d", f.mode))
	}
}

// filterSuppress implements SUPPRESS mode.
// A cut is emitted only when the frame is above threshold AND at least
// minFrames have elapsed since the previous emitted cut.
//
// Ported from scenedetect/detector.py, FlashFilter._filter_suppress.
func (f *FlashFilter) filterSuppress(t FrameTimecode, aboveThreshold bool) []FrameTimecode {
	elapsed := t.FrameNum() - f.lastAbove.FrameNum()
	minLengthMet := elapsed >= f.minFrames

	if !(aboveThreshold && minLengthMet) {
		return nil
	}
	f.lastAbove = t
	return []FrameTimecode{t}
}

// filterMerge implements MERGE mode.
//
// When a cut is found too close to the previous one, the filter enters a
// "merge triggered" state and defers emitting until a below-threshold region
// of sufficient length confirms the scene actually ended.
//
// Ported from scenedetect/detector.py, FlashFilter._filter_merge.
func (f *FlashFilter) filterMerge(t FrameTimecode, aboveThreshold bool) []FrameTimecode {
	// Compute elapsed BEFORE updating lastAbove.
	elapsed := t.FrameNum() - f.lastAbove.FrameNum()
	minLengthMet := elapsed >= f.minFrames

	// Always advance lastAbove to the most recent above-threshold frame.
	if aboveThreshold {
		f.lastAbove = t
	}

	if f.mergeTriggered {
		// We're in the merge-buffering state.  Wait until:
		//  (a) the current frame is below threshold — confirmed scene end, AND
		//  (b) enough frames have passed since lastAbove (min_length_met), AND
		//  (c) the merge region itself is long enough.
		mergeElapsed := f.lastAbove.FrameNum() - f.mergeStart.FrameNum()
		if minLengthMet && !aboveThreshold && mergeElapsed >= f.minFrames {
			f.mergeTriggered = false
			return []FrameTimecode{f.lastAbove}
		}
		return nil
	}

	if !aboveThreshold {
		return nil
	}

	// Frame is above threshold.
	if minLengthMet {
		// Scene is long enough: emit cut and allow future merging.
		f.mergeEnabled = true
		return []FrameTimecode{t}
	}

	// Not enough frames since the last cut: begin merging if a previous cut
	// exists (mergeEnabled is set after the first emitted cut).
	if f.mergeEnabled {
		f.mergeTriggered = true
		f.mergeStart = t
	}
	return nil
}

// isDigitString reports whether s consists entirely of ASCII digit characters.
func isDigitString(s string) bool {
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
