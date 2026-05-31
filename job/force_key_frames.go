// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/MediaMolder/MediaMolder/av"
)

// forceKeyFramesKind identifies one of the three FFmpeg-faithful
// argument grammars `-force_key_frames` accepts (mirrors the
// `if (strncmp(arg, "expr:", 5) == 0)` / `else if !strcmp(arg,
// "source")` / else-time-list dispatch in
// fftools/ffmpeg_mux_init.c::new_stream_video::3232).
type forceKeyFramesKind int

const (
	forceKeyFramesNone forceKeyFramesKind = iota
	forceKeyFramesTimeList
	forceKeyFramesExpr
	forceKeyFramesSource
)

// forceKeyFramesSpec is the parsed, immutable representation of
// `Output.ForceKeyFrames`. Built once at config time by
// parseForceKeyFrames; per-encoder state lives in
// forceKeyFramesMatcher.
type forceKeyFramesSpec struct {
	kind forceKeyFramesKind
	// times is the sorted (ascending) list of forced-keyframe times in
	// seconds. Populated for forceKeyFramesTimeList.
	times []float64
	// expr is the raw expression body (without the "expr:" prefix).
	// Populated for forceKeyFramesExpr.
	expr string
}

// forceKeyFramesExprVars enumerates the variable names libavutil's
// `av_expr_parse` must accept for `-force_key_frames "expr:..."`.
// Order is fixed (parallel to the values slice passed to Eval).
// Mirrors ffmpeg.h:557-561.
var forceKeyFramesExprVars = []string{
	"n",             // current video frame number (0-based)
	"n_forced",      // number of frames already forced
	"prev_forced_n", // n at the most recent forced frame
	"prev_forced_t", // t (seconds) at the most recent forced frame
	"t",             // current frame time in seconds
}

// parseForceKeyFrames validates and parses an FFmpeg `-force_key_frames`
// spec. Empty string returns (nil, nil) and disables forcing.
//
// Supported grammars:
//   - `expr:EXPR` — libavutil expression evaluated per video frame; the
//     frame is marked AV_PICTURE_TYPE_I when EXPR > 0. Vars: n,
//     n_forced, prev_forced_n, prev_forced_t, t (see
//     forceKeyFramesExprVars).
//   - `source` — copy keyframes from the source: the matcher fires when
//     the upstream frame carries AV_FRAME_FLAG_KEY, matching FFmpeg's
//     `forced_kf_apply` check for `frame->flags & AV_FRAME_FLAG_KEY`.
//   - comma-separated time list — float seconds, e.g. `3,7.5,10.25`.
//     The FFmpeg HH:MM:SS form `av_parse_time` accepts is deferred
//     until a corpus job needs it.
//
// Out of scope (rare; deferred): `source_no_drop` (deprecated
// libavformat alias), `chapters[+offset]` (requires chapter table
// access from the muxer side).
func parseForceKeyFrames(spec string) (*forceKeyFramesSpec, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil
	}
	if rest, ok := strings.CutPrefix(spec, "expr:"); ok {
		expr := strings.TrimSpace(rest)
		if expr == "" {
			return nil, fmt.Errorf("force_key_frames: empty expression after `expr:`")
		}
		// Parse-time validation: build and discard a compiled
		// expression so a malformed spec is rejected at config
		// load, not at first frame.
		pe, err := av.ParseExpression(expr, forceKeyFramesExprVars)
		if err != nil {
			return nil, fmt.Errorf("force_key_frames: invalid expression %q: %w", expr, err)
		}
		pe.Close()
		return &forceKeyFramesSpec{kind: forceKeyFramesExpr, expr: expr}, nil
	}
	if spec == "source" {
		return &forceKeyFramesSpec{kind: forceKeyFramesSource}, nil
	}
	parts := strings.Split(spec, ",")
	times := make([]float64, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return nil, fmt.Errorf("force_key_frames: empty entry in time list %q", spec)
		}
		v, err := strconv.ParseFloat(p, 64)
		if err != nil {
			return nil, fmt.Errorf("force_key_frames: invalid time %q in list: %w", p, err)
		}
		if v < 0 {
			return nil, fmt.Errorf("force_key_frames: negative time %v in list", v)
		}
		times = append(times, v)
	}
	sort.Float64s(times)
	return &forceKeyFramesSpec{kind: forceKeyFramesTimeList, times: times}, nil
}

// forceKeyFramesMatcher carries the per-encoder per-frame evaluation
// state for a forceKeyFramesSpec. Built once per encoder by
// newForceKeyFramesMatcher; shouldForce is called per frame.
type forceKeyFramesMatcher struct {
	spec *forceKeyFramesSpec
	expr *av.ParsedExpression

	// Time-base of the encoder, used to convert frame.PTS() to
	// seconds (t = pts * num / den). Mirrors ffmpeg_enc.c:749.
	tbNum int
	tbDen int

	// Per-frame counters tracked across frames. Mirror ffmpeg.h:557-561.
	n           int64
	nForced     int64
	prevForcedN int64
	prevForcedT float64
	timeListIdx int
}

// newForceKeyFramesMatcher builds a per-encoder matcher. The encoder's
// time-base is captured so Eval can compute t in seconds. Returns
// (nil, nil) when spec is nil.
func newForceKeyFramesMatcher(spec *forceKeyFramesSpec, tbNum, tbDen int) (*forceKeyFramesMatcher, error) {
	if spec == nil {
		return nil, nil
	}
	m := &forceKeyFramesMatcher{spec: spec, tbNum: tbNum, tbDen: tbDen}
	if spec.kind == forceKeyFramesExpr {
		pe, err := av.ParseExpression(spec.expr, forceKeyFramesExprVars)
		if err != nil {
			return nil, fmt.Errorf("force_key_frames matcher: %w", err)
		}
		m.expr = pe
	}
	return m, nil
}

// Close releases any compiled expression. Safe on nil receiver.
func (m *forceKeyFramesMatcher) Close() {
	if m == nil {
		return
	}
	m.expr.Close()
	m.expr = nil
}

// shouldForce returns true when the frame should be encoded as an
// AV_PICTURE_TYPE_I keyframe. Caller must invoke it exactly once per
// frame in PTS order; the matcher's internal counters advance on every
// call. sourceKeyFrame is the upstream frame's AV_FRAME_FLAG_KEY state
// (only consulted for `source` kind).
func (m *forceKeyFramesMatcher) shouldForce(pts int64, sourceKeyFrame bool) bool {
	if m == nil || m.spec == nil {
		return false
	}
	t := 0.0
	if m.tbDen > 0 {
		t = float64(pts) * float64(m.tbNum) / float64(m.tbDen)
	}
	defer func() { m.n++ }()
	switch m.spec.kind {
	case forceKeyFramesTimeList:
		// Fire when the next pending time has been reached or
		// passed. Mirrors the av_compare_ts(frame->pts,
		// frame->time_base, kf->pts[index], AV_TIME_BASE_Q) >= 0
		// loop in fftools/ffmpeg_enc.c::forced_kf_apply (line 744).
		if m.timeListIdx >= len(m.spec.times) {
			return false
		}
		if t+1e-9 >= m.spec.times[m.timeListIdx] {
			m.timeListIdx++
			m.recordForced(t)
			return true
		}
		return false
	case forceKeyFramesExpr:
		v := m.expr.Eval([]float64{
			float64(m.n),
			float64(m.nForced),
			float64(m.prevForcedN),
			m.prevForcedT,
			t,
		})
		if v > 0 {
			m.recordForced(t)
			return true
		}
		return false
	case forceKeyFramesSource:
		if sourceKeyFrame {
			m.recordForced(t)
			return true
		}
		return false
	}
	return false
}

func (m *forceKeyFramesMatcher) recordForced(t float64) {
	m.nForced++
	m.prevForcedN = m.n
	m.prevForcedT = t
}
