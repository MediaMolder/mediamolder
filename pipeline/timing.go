// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"fmt"

	"github.com/MediaMolder/MediaMolder/av"
)

// inputTiming captures the per-input trim parameters in the same form
// FFmpeg's fftools/ffmpeg_demux.c carries them: an optional start time
// (`-ss`) and a recording duration (`-t`), both in AV_TIME_BASE units
// (microseconds). The `recording_time == noLimitUS` sentinel matches
// FFmpeg's `INT64_MAX` "no limit" convention.
//
// resolveInputTiming is a direct port of the conflict-resolution logic
// in `fftools/ffmpeg_demux.c::ist_add_input_file()`:
//
//   - `-t` and `-to` are mutually exclusive; `-t` wins (with a warning).
//   - When only `-to` is set, recording_time = stop_time - max(ss, 0).
//   - `-to` <= `-ss` is rejected.
//
// Mirrors FFmpeg so that any command line that worked under
// `ffmpeg -ss A -t B -to C -i ...` produces the same demux window
// under MediaMolder.
type inputTiming struct {
	// haveStart indicates whether `-ss` was set. When false, startUS is
	// the FFmpeg sentinel AV_NOPTS_VALUE (mirrors `start_time` in
	// fftools/ffmpeg_demux.c).
	haveStart bool
	startUS   int64

	// recordingUS is the duration after which the demuxer should stop
	// emitting packets. noLimitUS means "no limit" (== INT64_MAX in
	// FFmpeg).
	recordingUS int64
}

// noLimitUS mirrors FFmpeg's INT64_MAX recording_time sentinel.
const noLimitUS int64 = 1<<63 - 1

// resolveInputTiming parses the per-input timing entries (`ss`, `t`,
// `to`) from a pipeline.Input.Options map and applies the same conflict
// resolution as fftools/ffmpeg_demux.c. Unknown / missing keys are
// treated as unset (FFmpeg defaults: start_time = AV_NOPTS_VALUE,
// recording_time = INT64_MAX, stop_time = INT64_MAX). All values use
// the same FFmpeg time-spec grammar as the CLI, parsed via
// av.ParseTime → av_parse_time().
//
// Logs a warning to the supplied logger (may be nil) when `-t` and
// `-to` are both set, matching FFmpeg's user-visible behaviour.
func resolveInputTiming(opts map[string]any, warn func(format string, args ...any)) (inputTiming, error) {
	startUS := int64(av.NoPTSValue)
	recordingUS := noLimitUS
	stopUS := noLimitUS
	haveT := false
	haveTo := false
	haveSS := false

	getStr := func(k string) (string, bool) {
		v, ok := opts[k]
		if !ok {
			return "", false
		}
		s, ok := v.(string)
		if !ok {
			return fmt.Sprintf("%v", v), true
		}
		return s, true
	}

	if s, ok := getStr("ss"); ok {
		// FFmpeg's CLI parses every OPT_TYPE_TIME value (including -ss
		// and -to) with av_parse_time(..., 1) — see
		// fftools/cmdutils.c::write_option(). Match that, so bare
		// seconds ("30") are accepted.
		us, err := av.ParseTime(s, true)
		if err != nil {
			return inputTiming{}, fmt.Errorf("ss: %w", err)
		}
		startUS = us
		haveSS = true
	}
	if s, ok := getStr("t"); ok {
		us, err := av.ParseTime(s, true)
		if err != nil {
			return inputTiming{}, fmt.Errorf("t: %w", err)
		}
		recordingUS = us
		haveT = true
	}
	if s, ok := getStr("to"); ok {
		us, err := av.ParseTime(s, true)
		if err != nil {
			return inputTiming{}, fmt.Errorf("to: %w", err)
		}
		stopUS = us
		haveTo = true
	}

	// Mirror fftools/ffmpeg_demux.c: -t and -to are mutually exclusive,
	// -t wins (with warning).
	if haveT && haveTo {
		if warn != nil {
			warn("-t and -to cannot be used together; using -t.")
		}
		haveTo = false
		stopUS = noLimitUS
	}

	if haveTo && !haveT {
		start := int64(0)
		if haveSS && startUS > 0 {
			start = startUS
		}
		if stopUS <= start {
			return inputTiming{}, fmt.Errorf("to (%d us) must be greater than ss (%d us)", stopUS, start)
		}
		recordingUS = stopUS - start
	}

	return inputTiming{
		haveStart:   haveSS,
		startUS:     startUS,
		recordingUS: recordingUS,
	}, nil
}

// seekTimestampUS computes the seek target passed to
// avformat_seek_file, mirroring the timestamp computation in
// fftools/ffmpeg_demux.c::ist_add_input_file():
//
//	timestamp = (start_time == AV_NOPTS_VALUE) ? 0 : start_time;
//	if (!o->seek_timestamp && ic->start_time != AV_NOPTS_VALUE)
//	    timestamp += ic->start_time;
//
// `containerStartUS` is the AVFormatContext.start_time value reported
// by libavformat after `avformat_find_stream_info` (== AV_NOPTS_VALUE
// when unknown). Returns the absolute timestamp to seek to in
// AV_TIME_BASE units.
func (t inputTiming) seekTimestampUS(containerStartUS int64) int64 {
	ts := int64(0)
	if t.haveStart {
		ts = t.startUS
	}
	if containerStartUS != av.NoPTSValue {
		ts += containerStartUS
	}
	return ts
}

// outputTiming captures per-output-file trim parameters in the same
// form FFmpeg's fftools/ffmpeg_mux_init.c carries them on the
// `OutputFile` struct. The semantics are intentionally parallel to
// inputTiming, but the enforcement happens on the muxer side
// (`ffmpeg_mux.c::of_streamcopy` / `ffmpeg_enc.c::check_recording_time`)
// rather than on the demuxer side: packets whose PTS is below
// `startUS` are dropped before muxing, packets whose PTS reaches
// `startUS + recordingUS` cause the muxer to stop. The same `-t` /
// `-to` conflict resolution applies (`-t` wins with a warning).
type outputTiming struct {
	haveStart   bool
	startUS     int64
	recordingUS int64
}

// resolveOutputTiming parses the output-side timing entries (`ss`,
// `t`, `to`) from a pipeline.Output.Options map. Mirrors
// resolveInputTiming so that command lines like
// `ffmpeg -i in -ss 5 -t 10 out.mp4` (output-side trim) resolve to
// the same windowing logic FFmpeg applies in
// `fftools/ffmpeg_mux_init.c`.
func resolveOutputTiming(opts map[string]any, warn func(format string, args ...any)) (outputTiming, error) {
	t, err := resolveInputTiming(opts, warn)
	if err != nil {
		return outputTiming{}, err
	}
	return outputTiming(t), nil
}

// stopTimestampUS returns the absolute packet PTS at or after which the
// muxer should stop accepting packets. Mirrors
// `fftools/ffmpeg_mux.c::of_streamcopy`'s
// `dts >= of->recording_time + start_time` check. `copyTS` corresponds
// to FFmpeg's global `-copyts` flag: when false the runtime has
// already shifted demuxed packets back to a 0-anchored timeline, so
// the comparison is `pts_ts >= recordingUS`; when true the original
// timestamps are preserved end-to-end so the comparison anchors at
// `startUS + recordingUS` (or just `recordingUS` when no start is set).
// Returns noLimitUS when no `-t` / `-to` is in effect.
func (t outputTiming) stopTimestampUS(copyTS bool) int64 {
	if t.recordingUS == noLimitUS {
		return noLimitUS
	}
	if !copyTS {
		return t.recordingUS
	}
	if t.haveStart && t.startUS != int64(av.NoPTSValue) {
		return t.startUS + t.recordingUS
	}
	return t.recordingUS
}

// startTimestampUS returns the absolute packet PTS below which the
// muxer should drop packets. Mirrors the
// `dts < of->start_time` drop in `fftools/ffmpeg_mux.c::of_streamcopy`.
// Returns NoPTSValue when no `-ss` is set on the output.
func (t outputTiming) startTimestampUS() int64 {
	if !t.haveStart {
		return int64(av.NoPTSValue)
	}
	return t.startUS
}

// stopTimestampUS returns the absolute packet PTS at or after which the
// demuxer should stop emitting, in AV_TIME_BASE units. Mirrors the
// recording_time check in fftools/ffmpeg_demux.c::input_packet_process():
// stop when `dts >= recording_time + start_time`, with the FFmpeg
// default of `start_time = 0` (we don't expose `-copy_ts`).
//
// Returns noLimitUS when no `-t` / `-to` is in effect.
func (t inputTiming) stopTimestampUS(containerStartUS int64) int64 {
	if t.recordingUS == noLimitUS {
		return noLimitUS
	}
	// In non-copy_ts mode FFmpeg shifts every packet PTS by
	// `ts_offset = -timestamp` (the seek target). After that shift, a
	// packet's effective timestamp is `original_pts - timestamp`, and
	// the stop condition is `effective_dts >= recording_time`. We
	// don't apply the ts_offset to packets — instead we compare
	// against the equivalent absolute threshold:
	//   original_pts >= timestamp + recording_time
	return t.seekTimestampUS(containerStartUS) + t.recordingUS
}
