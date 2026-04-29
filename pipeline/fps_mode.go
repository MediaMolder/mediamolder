// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

// fpsRewriter implements the per-frame renumber / drop / duplicate
// policy selected by `Output.FPSMode`. It mirrors the relevant
// behaviour of FFmpeg's `fftools/ffmpeg_enc.c::do_video_out` for the
// `passthrough`, `vfr`, `cfr` and `drop` modes.
//
// The rewriter operates on integer PTS values expressed in the
// encoder's time_base. The caller computes `frameDurTB` once
// (frameDurTB = timebase.den * framerate.den /
//
//	(timebase.num * framerate.num), rounded to the nearest
//
// integer) and feeds frames in monotonic arrival order. For every
// arriving frame the rewriter returns:
//
//   - emit:   number of frames the caller should emit on this iteration
//     (1 in `vfr`/`drop`/`passthrough`; 0..N in `cfr` where N>1
//     means duplicate the current frame to fill a forward gap).
//   - pts:    the PTS to assign to the first emitted frame; subsequent
//     duplicates are at pts + i*frameDurTB.
//   - drop:   true when the frame should not be emitted at all (CFR
//     "too soon" or VFR/drop "non-monotonic").
//
// emit==0 is shorthand for drop==true; both are returned so callers
// don't need to convert.
type fpsRewriter struct {
	mode        string // "", "passthrough", "vfr", "cfr", "drop"
	frameDurTB  int64  // 1/framerate expressed in encoder time_base units
	nextPTS     int64  // next CFR target PTS (mode=="cfr")
	lastEmitted int64  // last PTS actually emitted (mode=="vfr"/"drop")
	primed      bool
}

// newFPSRewriter returns a rewriter configured for `mode`. `frameDurTB`
// must be > 0 for `cfr` and `drop`; `vfr` / `passthrough` ignore it.
// An unknown mode degrades to passthrough so that misspelt JSON does
// not silently corrupt PTS values mid-encode (`validate()` already
// rejects this at parse time, but defence in depth is cheap here).
func newFPSRewriter(mode string, frameDurTB int64) *fpsRewriter {
	switch mode {
	case "", "passthrough", "vfr", "cfr", "drop":
	default:
		mode = ""
	}
	return &fpsRewriter{mode: mode, frameDurTB: frameDurTB}
}

// rewrite returns (emitCount, basePTS, drop). When emitCount > 1 the
// caller emits emitCount frames at PTS basePTS, basePTS+frameDurTB,
// basePTS+2*frameDurTB, ... (CFR duplication into a forward gap).
func (r *fpsRewriter) rewrite(framePTS int64) (emit int, basePTS int64, drop bool) {
	switch r.mode {
	case "", "passthrough":
		return 1, framePTS, false

	case "vfr":
		if r.primed && framePTS <= r.lastEmitted {
			return 0, 0, true
		}
		r.lastEmitted = framePTS
		r.primed = true
		return 1, framePTS, false

	case "drop":
		// Like vfr, but also drops "duplicates" that arrive within half a
		// frame duration of the previous emission. This is what FFmpeg's
		// `-fps_mode drop` does for sources that emit redundant frames
		// (e.g. some MJPEG webcams).
		if r.primed {
			if r.frameDurTB > 0 {
				if framePTS-r.lastEmitted < r.frameDurTB/2 {
					return 0, 0, true
				}
			} else if framePTS <= r.lastEmitted {
				return 0, 0, true
			}
		}
		r.lastEmitted = framePTS
		r.primed = true
		return 1, framePTS, false

	case "cfr":
		if r.frameDurTB <= 0 {
			// Cannot enforce CFR without a frame duration; degrade to
			// passthrough rather than spinning or zero-emitting forever.
			return 1, framePTS, false
		}
		if !r.primed {
			r.nextPTS = framePTS
			r.primed = true
		}
		// Frame arrived more than half a duration before the next CFR
		// slot \u2014 drop it (it would either go backwards or land on the
		// previous slot we already filled).
		if framePTS+r.frameDurTB/2 < r.nextPTS {
			return 0, 0, true
		}
		// How many CFR slots does this frame fill? At least 1; +1 for
		// every full duration the frame is "ahead" of the next slot.
		gap := framePTS - r.nextPTS
		emit = 1
		if gap >= r.frameDurTB {
			emit += int(gap / r.frameDurTB)
		}
		basePTS = r.nextPTS
		r.nextPTS += int64(emit) * r.frameDurTB
		return emit, basePTS, false
	}
	return 1, framePTS, false
}

// computeFrameDurationTB returns the integer frame duration expressed
// in `tb` units for a stream whose framerate is `fr`. Returns 0 when
// any input is non-positive (callers degrade to passthrough). The
// formula is `tb.den / fr.num * fr.den / tb.num`, computed with
// integer arithmetic that avoids overflow for typical broadcast values
// (tb up to 1/90000, framerate up to 240/1).
func computeFrameDurationTB(fr [2]int, tb [2]int) int64 {
	if fr[0] <= 0 || fr[1] <= 0 || tb[0] <= 0 || tb[1] <= 0 {
		return 0
	}
	// dur = (1/fr) / tb = (fr.den * tb.den) / (fr.num * tb.num)
	num := int64(fr[1]) * int64(tb[1])
	den := int64(fr[0]) * int64(tb[0])
	if den <= 0 {
		return 0
	}
	return num / den
}
