// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/pipeline"
)

func TestParseTeeSlaves(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []pipeline.TeeTarget
	}{
		{
			name: "two slaves with format",
			in:   "[f=mp4]out.mp4|[f=hls:hls_time=4]out.m3u8",
			want: []pipeline.TeeTarget{
				{URL: "out.mp4", Format: "mp4"},
				{URL: "out.m3u8", Format: "hls", Options: map[string]any{"hls_time": "4"}},
			},
		},
		{
			name: "select + onfail + bsfs",
			in:   "[select=v:onfail=ignore:bsfs=h264_mp4toannexb]out.ts",
			want: []pipeline.TeeTarget{
				{URL: "out.ts", Select: "v", OnFail: "ignore", BSFs: "h264_mp4toannexb"},
			},
		},
		{
			name: "use_fifo + fifo_options",
			in:   "[f=flv:use_fifo=1:fifo_options=queue_size=60]rtmp://x/live",
			want: []pipeline.TeeTarget{
				{URL: "rtmp://x/live", Format: "flv", UseFifo: true, FifoOptions: "queue_size=60"},
			},
		},
		{
			name: "bare URL",
			in:   "out.mp4",
			want: []pipeline.TeeTarget{{URL: "out.mp4"}},
		},
		{
			name: "escaped pipe in URL",
			in:   `[f=mp4]a\|b.mp4`,
			want: []pipeline.TeeTarget{{URL: "a|b.mp4", Format: "mp4"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseTeeSlaves(tc.in)
			if err != nil {
				t.Fatalf("parseTeeSlaves(%q): %v", tc.in, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestParseFTeePromotesOutput(t *testing.T) {
	args := []string{"-i", "in.mp4", "-c", "copy", "-f", "tee", "[f=mp4]a.mp4|[f=hls]b.m3u8"}
	cfg, err := ParseArgs(args)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(cfg.Outputs) != 1 {
		t.Fatalf("want 1 output, got %d", len(cfg.Outputs))
	}
	out := cfg.Outputs[0]
	if out.Kind != "tee" {
		t.Errorf("Kind = %q, want \"tee\"", out.Kind)
	}
	if out.Format != "" {
		t.Errorf("Format = %q, want empty (consumed by tee promotion)", out.Format)
	}
	if len(out.Targets) != 2 {
		t.Fatalf("want 2 targets, got %d", len(out.Targets))
	}
	if out.Targets[0].URL != "a.mp4" || out.Targets[0].Format != "mp4" {
		t.Errorf("target[0] = %+v", out.Targets[0])
	}
	if out.Targets[1].URL != "b.m3u8" || out.Targets[1].Format != "hls" {
		t.Errorf("target[1] = %+v", out.Targets[1])
	}
}

func TestParseTeeSlaveErrors(t *testing.T) {
	tests := []struct {
		in      string
		wantErr string
	}{
		{"", "empty"},
		{"[f=mp4", "unterminated"},
		{"[novalue]a.mp4", "missing `=`"},
		{"[f=mp4]", "missing url"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			_, err := parseTeeSlaves(tc.in)
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}
