//
//            PySceneDetect: Python-Based Video Scene Detector
//  -------------------------------------------------------------------
//     [  Site:   https://scenedetect.com                           ]
//     [  Docs:   https://scenedetect.com/docs/                     ]
//     [  Github: https://github.com/Breakthrough/PySceneDetect/    ]
//
// Copyright (C) 2025 Brandon Castellano <http://www.bcastell.com>.
// PySceneDetect is licensed under the BSD 3-Clause License; see
// License: https://github.com/Breakthrough/PySceneDetect/blob/main/LICENSE
//

package pyscenedetect

// Ported from scenedetect/common.py, class FrameTimecode.

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// FrameTimecode is a frame-accurate video timestamp paired with a frame rate.
//
// It stores a zero-based frame index and the video's frames-per-second rate,
// enabling conversion between frame numbers, seconds, and HH:MM:SS.nnn strings.
//
// Arithmetic operations clamp at zero — negative results are treated as frame 0,
// matching PySceneDetect's Python behaviour.
//
// Ported from scenedetect/common.py, class FrameTimecode.
type FrameTimecode struct {
	frameNum  int64
	frameRate float64 // frames per second; must be > 0
}

// NewFrameTimecode creates a FrameTimecode from a zero-based frame number and
// a frame rate. frameNum is clamped to 0 if negative.
func NewFrameTimecode(frameNum int64, fps float64) (FrameTimecode, error) {
	if fps <= 0 {
		return FrameTimecode{}, fmt.Errorf("pyscenedetect: fps must be > 0, got %g", fps)
	}
	if frameNum < 0 {
		frameNum = 0
	}
	return FrameTimecode{frameNum: frameNum, frameRate: fps}, nil
}

// FrameTimecodeFromSeconds creates a FrameTimecode from a duration in seconds,
// rounding to the nearest frame boundary. secs is clamped to 0 if negative.
func FrameTimecodeFromSeconds(secs float64, fps float64) (FrameTimecode, error) {
	if fps <= 0 {
		return FrameTimecode{}, fmt.Errorf("pyscenedetect: fps must be > 0, got %g", fps)
	}
	if secs < 0 {
		secs = 0
	}
	return FrameTimecode{frameNum: int64(math.Round(secs * fps)), frameRate: fps}, nil
}

// ParseFrameTimecode parses a timecode-like string into a FrameTimecode.
//
// Accepted formats:
//   - "HH:MM:SS" or "HH:MM:SS.nnn"  — standard timecode
//   - "MM:SS"    or "MM:SS.nnn"      — timecode without hours
//   - "1234"                         — exact frame number (integer digits only)
//   - "1234.5" or "1234.5s"          — seconds (decimal or with trailing 's')
//
// Matches PySceneDetect's FrameTimecode.__init__ parsing behaviour.
func ParseFrameTimecode(s string, fps float64) (FrameTimecode, error) {
	if fps <= 0 {
		return FrameTimecode{}, fmt.Errorf("pyscenedetect: fps must be > 0, got %g", fps)
	}
	secs, err := parseTimecodeToSecs(s, fps)
	if err != nil {
		return FrameTimecode{}, err
	}
	return FrameTimecodeFromSeconds(secs, fps)
}

// FrameNum returns the zero-based frame index.
func (t FrameTimecode) FrameNum() int64 { return t.frameNum }

// FrameRate returns the frames-per-second rate.
func (t FrameTimecode) FrameRate() float64 { return t.frameRate }

// Seconds returns the presentation time in seconds.
func (t FrameTimecode) Seconds() float64 {
	if t.frameRate == 0 {
		return 0
	}
	return float64(t.frameNum) / t.frameRate
}

// Timecode returns the presentation time as "HH:MM:SS.nnn" with millisecond
// precision, matching PySceneDetect's FrameTimecode.get_timecode() output.
func (t FrameTimecode) Timecode() string {
	// Snap to nearest frame boundary then convert (matches Python nearest_frame=True).
	secs := float64(t.frameNum) / t.frameRate

	hrs := int(secs / 3600)
	secs -= float64(hrs) * 3600
	mins := int(secs / 60)
	secs -= float64(mins) * 60

	// Round to 3 decimal places, then guard against a 60.000 result after rounding.
	rounded := math.Round(secs*1000) / 1000
	if rounded >= 60.0 {
		rounded = 0
		mins++
		if mins >= 60 {
			mins = 0
			hrs++
		}
	}
	return fmt.Sprintf("%02d:%02d:%06.3f", hrs, mins, rounded)
}

// String implements fmt.Stringer, returning the same value as Timecode().
func (t FrameTimecode) String() string { return t.Timecode() }

// AddFrames returns a new FrameTimecode advanced by delta frames.
// The result is clamped at frame 0 (cannot go negative).
func (t FrameTimecode) AddFrames(delta int64) FrameTimecode {
	n := t.frameNum + delta
	if n < 0 {
		n = 0
	}
	return FrameTimecode{frameNum: n, frameRate: t.frameRate}
}

// SubTimecode returns the elapsed time from `from` to `t` as a FrameTimecode.
// The result is clamped at 0 (no negative durations).
//
// This is the Go equivalent of the Python expression `t - from`.
func (t FrameTimecode) SubTimecode(from FrameTimecode) FrameTimecode {
	n := t.frameNum - from.frameNum
	if n < 0 {
		n = 0
	}
	return FrameTimecode{frameNum: n, frameRate: t.frameRate}
}

// ElapsedFrames returns the number of frames elapsed since `from`.
// Returns 0 if t is before from.
func (t FrameTimecode) ElapsedFrames(from FrameTimecode) int64 {
	n := t.frameNum - from.frameNum
	if n < 0 {
		return 0
	}
	return n
}

// Equal reports whether t and other represent the same frame number.
func (t FrameTimecode) Equal(other FrameTimecode) bool {
	return t.frameNum == other.frameNum
}

// Less reports whether t occurs strictly before other.
func (t FrameTimecode) Less(other FrameTimecode) bool {
	return t.frameNum < other.frameNum
}

// LessEq reports whether t occurs at or before other.
func (t FrameTimecode) LessEq(other FrameTimecode) bool {
	return t.frameNum <= other.frameNum
}

// Greater reports whether t occurs strictly after other.
func (t FrameTimecode) Greater(other FrameTimecode) bool {
	return t.frameNum > other.frameNum
}

// GreaterEq reports whether t occurs at or after other.
func (t FrameTimecode) GreaterEq(other FrameTimecode) bool {
	return t.frameNum >= other.frameNum
}

// parseTimecodeToSecs converts a timecode-like string to seconds.
//
// Handles:
//   - "HH:MM:SS[.nnn]" or "MM:SS[.nnn]" — standard timecode
//   - "1234"                             — integer frame count (converted via fps)
//   - "1234.5" or "1234.5s"             — seconds
func parseTimecodeToSecs(s string, fps float64) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("pyscenedetect: empty timecode string")
	}

	// Trailing 's': treat as seconds value.
	if strings.HasSuffix(s, "s") {
		num, err := strconv.ParseFloat(s[:len(s)-1], 64)
		if err != nil {
			return 0, fmt.Errorf("pyscenedetect: invalid seconds value %q: %w", s, err)
		}
		if num < 0 {
			return 0, fmt.Errorf("pyscenedetect: timecode must be >= 0, got %q", s)
		}
		return num, nil
	}

	// Contains ':': "HH:MM:SS[.nnn]" or "MM:SS[.nnn]".
	if strings.Contains(s, ":") {
		parts := strings.Split(s, ":")
		if len(parts) < 2 || len(parts) > 3 {
			return 0, fmt.Errorf("pyscenedetect: invalid timecode %q (wrong number of ':' separators)", s)
		}
		var hrs, mins int
		var secs float64
		var err error
		if len(parts) == 3 {
			hrs, err = strconv.Atoi(parts[0])
			if err != nil {
				return 0, fmt.Errorf("pyscenedetect: invalid hours in %q: %w", s, err)
			}
			mins, err = strconv.Atoi(parts[1])
			if err != nil {
				return 0, fmt.Errorf("pyscenedetect: invalid minutes in %q: %w", s, err)
			}
			secs, err = strconv.ParseFloat(parts[2], 64)
		} else {
			mins, err = strconv.Atoi(parts[0])
			if err != nil {
				return 0, fmt.Errorf("pyscenedetect: invalid minutes in %q: %w", s, err)
			}
			secs, err = strconv.ParseFloat(parts[1], 64)
		}
		if err != nil {
			return 0, fmt.Errorf("pyscenedetect: invalid seconds in %q: %w", s, err)
		}
		if hrs < 0 || mins < 0 || secs < 0 || mins >= 60 || secs >= 60 {
			return 0, fmt.Errorf("pyscenedetect: timecode %q out of valid range", s)
		}
		return float64(hrs)*3600 + float64(mins)*60 + secs, nil
	}

	// Pure digits: interpret as a frame count, convert to seconds via fps.
	// Matches Python: `isinstance(length, str) and length.strip().isdigit()` → frames.
	allDigits := true
	for _, c := range s {
		if c < '0' || c > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("pyscenedetect: invalid frame number %q: %w", s, err)
		}
		if fps <= 0 {
			return 0, fmt.Errorf("pyscenedetect: fps required to convert frame number %q to seconds", s)
		}
		return float64(n) / fps, nil
	}

	// Otherwise: decimal seconds without 's' suffix.
	num, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("pyscenedetect: cannot parse timecode %q: %w", s, err)
	}
	if num < 0 {
		return 0, fmt.Errorf("pyscenedetect: timecode must be >= 0, got %q", s)
	}
	return num, nil
}
