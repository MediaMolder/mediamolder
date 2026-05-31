// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
)

// TestResolveInputTiming_FFmpegSemantics covers the conflict-resolution
// rules ported from fftools/ffmpeg_demux.c::ist_add_input_file().
func TestResolveInputTiming_FFmpegSemantics(t *testing.T) {
	cases := []struct {
		name        string
		opts        map[string]any
		wantStart   bool
		wantStartUS int64
		wantRecUS   int64
		wantWarn    bool
		wantErr     bool
	}{
		{
			name:      "empty",
			opts:      nil,
			wantStart: false, wantStartUS: av.NoPTSValue, wantRecUS: noLimitUS,
		},
		{
			name:      "ss only",
			opts:      map[string]any{"ss": "30"},
			wantStart: true, wantStartUS: 30_000_000, wantRecUS: noLimitUS,
		},
		{
			name:      "t only",
			opts:      map[string]any{"t": "5.5"},
			wantStart: false, wantStartUS: av.NoPTSValue, wantRecUS: 5_500_000,
		},
		{
			name:      "to only",
			opts:      map[string]any{"to": "00:01:30"},
			wantStart: false, wantStartUS: av.NoPTSValue, wantRecUS: 90_000_000,
		},
		{
			name:      "ss + to converts to recording_time",
			opts:      map[string]any{"ss": "10", "to": "25"},
			wantStart: true, wantStartUS: 10_000_000, wantRecUS: 15_000_000,
		},
		{
			// FFmpeg: -t and -to mutually exclusive, -t wins, warn.
			name:      "t and to together: t wins, warn emitted",
			opts:      map[string]any{"ss": "10", "t": "7", "to": "25"},
			wantStart: true, wantStartUS: 10_000_000, wantRecUS: 7_000_000,
			wantWarn: true,
		},
		{
			name:    "to <= ss is rejected",
			opts:    map[string]any{"ss": "30", "to": "20"},
			wantErr: true,
		},
		{
			name:    "bad time spec",
			opts:    map[string]any{"ss": "not-a-time"},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var warned bool
			got, err := resolveInputTiming(tc.opts, func(format string, args ...any) {
				warned = true
				if !strings.Contains(format, "-t and -to") {
					t.Fatalf("unexpected warning %q", format)
				}
			})
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (timing=%+v)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
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
			if warned != tc.wantWarn {
				t.Errorf("warned = %v, want %v", warned, tc.wantWarn)
			}
		})
	}
}

// TestStopTimestampUS_MirrorsRecordingTimeCheck verifies the absolute
// stop-PTS computation matches FFmpeg's `dts >= recording_time +
// start_time` check (with non-copy_ts start_time = 0). When -ss=450s
// and -t=10s on a container whose start_time is 0, the demux loop
// should stop at PTS = 460_000_000 us.
func TestStopTimestampUS_MirrorsRecordingTimeCheck(t *testing.T) {
	timing, err := resolveInputTiming(map[string]any{"ss": "450", "t": "10"}, nil)
	if err != nil {
		t.Fatalf("resolveInputTiming: %v", err)
	}
	// Container start_time = 0 (typical MP4).
	if got := timing.seekTimestampUS(0); got != 450_000_000 {
		t.Errorf("seekTimestampUS = %d, want 450_000_000", got)
	}
	if got := timing.stopTimestampUS(0); got != 460_000_000 {
		t.Errorf("stopTimestampUS = %d, want 460_000_000", got)
	}
	// Container start_time = NOPTS → ignored.
	if got := timing.seekTimestampUS(av.NoPTSValue); got != 450_000_000 {
		t.Errorf("seekTimestampUS(NOPTS) = %d, want 450_000_000", got)
	}
	// Container start_time = 1.5s → added to seek and stop.
	if got := timing.seekTimestampUS(1_500_000); got != 451_500_000 {
		t.Errorf("seekTimestampUS(1.5s) = %d, want 451_500_000", got)
	}
	if got := timing.stopTimestampUS(1_500_000); got != 461_500_000 {
		t.Errorf("stopTimestampUS(1.5s) = %d, want 461_500_000", got)
	}
}

// TestStopTimestampUS_NoLimit ensures no -t/-to means noLimitUS.
func TestStopTimestampUS_NoLimit(t *testing.T) {
	timing, err := resolveInputTiming(map[string]any{"ss": "5"}, nil)
	if err != nil {
		t.Fatalf("resolveInputTiming: %v", err)
	}
	if got := timing.stopTimestampUS(0); got != noLimitUS {
		t.Errorf("stopTimestampUS = %d, want noLimitUS", got)
	}
}
