// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
)

// TestResolveOutputTiming_FFmpegSemantics covers the conflict resolution
// rules ported from fftools/ffmpeg_mux_init.c::of_open() handling of
// -ss/-t/-to on the output side. Mirrors the input-side test so that
// `ffmpeg -i in -ss 5 -t 10 out.mp4` and `-ss 5 -to 25` resolve to the
// same windowing logic FFmpeg applies in the muxer.
func TestResolveOutputTiming_FFmpegSemantics(t *testing.T) {
	cases := []struct {
		name        string
		opts        map[string]any
		wantStart   bool
		wantStartUS int64
		wantRecUS   int64
	}{
		{name: "empty", opts: nil, wantStart: false, wantStartUS: av.NoPTSValue, wantRecUS: noLimitUS},
		{name: "ss only", opts: map[string]any{"ss": "5"}, wantStart: true, wantStartUS: 5_000_000, wantRecUS: noLimitUS},
		{name: "t only", opts: map[string]any{"t": "10"}, wantStart: false, wantStartUS: av.NoPTSValue, wantRecUS: 10_000_000},
		{name: "ss + to", opts: map[string]any{"ss": "5", "to": "20"}, wantStart: true, wantStartUS: 5_000_000, wantRecUS: 15_000_000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveOutputTiming(tc.opts, nil)
			if err != nil {
				t.Fatalf("resolveOutputTiming: %v", err)
			}
			if got.haveStart != tc.wantStart {
				t.Errorf("haveStart = %v, want %v", got.haveStart, tc.wantStart)
			}
			if got.startUS != tc.wantStartUS {
				t.Errorf("startUS = %d, want %d", got.startUS, tc.wantStartUS)
			}
			if got.recordingUS != tc.wantRecUS {
				t.Errorf("recordingUS = %d, want %d", got.recordingUS, tc.wantRecUS)
			}
		})
	}
}

// TestOutputTiming_StopAndStartSemantics verifies that the stop helper
// mirrors FFmpeg's `of_streamcopy` stop condition:
//
//	int64_t start_time = (of->start_time == AV_NOPTS_VALUE) ? 0 : of->start_time;
//	if (recording_time != INT64_MAX && dts >= recording_time + start_time)
//
// When output-side `-ss 5 -t 10` is set the stop anchors at
// `startUS + recordingUS` regardless of copyTS. Without an output-side
// `-ss` (haveStart=false) the stop reduces to `recordingUS`.
func TestOutputTiming_StopAndStartSemantics(t *testing.T) {
	timing, err := resolveOutputTiming(map[string]any{"ss": "5", "t": "10"}, nil)
	if err != nil {
		t.Fatalf("resolveOutputTiming: %v", err)
	}
	if got := timing.startTimestampUS(); got != 5_000_000 {
		t.Errorf("startTimestampUS = %d, want 5_000_000", got)
	}
	// With output-side -ss, stop anchors at startUS + recordingUS for both copyTS values.
	if got := timing.stopTimestampUS(false); got != 15_000_000 {
		t.Errorf("stopTimestampUS(copyTS=false) = %d, want 15_000_000", got)
	}
	if got := timing.stopTimestampUS(true); got != 15_000_000 {
		t.Errorf("stopTimestampUS(copyTS=true) = %d, want 15_000_000", got)
	}
	// No -t/-to → no limit regardless of copyTS.
	none, _ := resolveOutputTiming(map[string]any{"ss": "5"}, nil)
	if got := none.stopTimestampUS(false); got != noLimitUS {
		t.Errorf("stopTimestampUS no-recording = %d, want noLimitUS", got)
	}
	if got := none.stopTimestampUS(true); got != noLimitUS {
		t.Errorf("stopTimestampUS no-recording (copyTS) = %d, want noLimitUS", got)
	}
}

// TestValidateMaxFileSizeRejectsNegative ensures the validator catches
// a negative MaxFileSize value with a useful error.
func TestValidateMaxFileSizeRejectsNegative(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.0",
		Inputs: []Input{
			{ID: "in", URL: "x.mp4", Streams: []StreamSelect{{Type: "video"}}},
		},
		Outputs: []Output{{ID: "out", URL: "y.mp4", MaxFileSize: -1}},
	}
	if err := validate(cfg); err == nil ||
		!strings.Contains(err.Error(), "max_file_size") {
		t.Fatalf("validate err = %v, want one containing \"max_file_size\"", err)
	}
}
