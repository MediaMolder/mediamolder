// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import "strings"

// PresetLadder represents the per-codec ordered list of preset names, from
// slowest (highest quality) to fastest (lowest quality / lowest CPU cost).
// Index 0 is the slowest preset; len(Names)-1 is the fastest. SVT-AV1
// presets are integers 0..13 with 0 being slowest; we store them as decimal
// strings so the same Step/IndexOf logic works for every codec.
type PresetLadder struct {
	Codec string
	Names []string
}

// LadderFor returns the preset ladder for the given encoder codec name.
// Returns (nil, false) when the codec is not part of the adaptive preset
// stepping feature (HW encoders, aac, mpegN, ...).
func LadderFor(codecName string) (PresetLadder, bool) {
	switch codecName {
	case "libx264":
		return PresetLadder{
			Codec: "libx264",
			Names: []string{
				"placebo", "veryslow", "slower", "slow",
				"medium",
				"fast", "faster", "veryfast", "superfast", "ultrafast",
			},
		}, true
	case "libx265":
		return PresetLadder{
			Codec: "libx265",
			Names: []string{
				"placebo", "veryslow", "slower", "slow",
				"medium",
				"fast", "faster", "veryfast", "superfast", "ultrafast",
			},
		}, true
	case "libsvtav1":
		return PresetLadder{
			Codec: "libsvtav1",
			Names: []string{
				"0", "1", "2", "3", "4", "5", "6",
				"7", "8", "9", "10", "11", "12", "13",
			},
		}, true
	}
	return PresetLadder{}, false
}

// IndexOf returns the position of name in the ladder, or -1 if not present.
// Comparison is case-insensitive for libx264 / libx265.
func (l PresetLadder) IndexOf(name string) int {
	if name == "" {
		return -1
	}
	name = strings.ToLower(name)
	for i, n := range l.Names {
		if strings.ToLower(n) == name {
			return i
		}
	}
	return -1
}

// Step returns the preset name n positions faster than current
// (n > 0 → faster, n < 0 → slower). The result is clamped at both
// ends of the ladder; clamped is true when the requested step was
// truncated.
//
// If current is not a recognised ladder entry the function returns the
// default ("medium" for x264/x265, "8" for SVT-AV1) with clamped=true so
// the caller can detect the substitution.
func (l PresetLadder) Step(current string, n int) (next string, clamped bool) {
	if len(l.Names) == 0 {
		return current, true
	}
	idx := l.IndexOf(current)
	if idx < 0 {
		return l.Default(), true
	}
	target := idx + n
	clamped = false
	if target < 0 {
		target = 0
		clamped = true
	}
	if target >= len(l.Names) {
		target = len(l.Names) - 1
		clamped = true
	}
	return l.Names[target], clamped
}

// Default returns the codec's default "medium" / preset 8.
func (l PresetLadder) Default() string {
	switch l.Codec {
	case "libsvtav1":
		return "8"
	default:
		return "medium"
	}
}

// IsFasterThan reports whether a is faster (higher ladder index) than b on
// this ladder. Returns false when either preset is not recognised.
func (l PresetLadder) IsFasterThan(a, b string) bool {
	ia, ib := l.IndexOf(a), l.IndexOf(b)
	if ia < 0 || ib < 0 {
		return false
	}
	return ia > ib
}
