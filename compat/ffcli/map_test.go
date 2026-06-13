// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

import (
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/job"
)

func TestParseMapArg(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want job.StreamSelect
	}{
		{
			name: "all video",
			in:   "0:v",
			want: job.StreamSelect{InputIndex: 0, Type: "video", All: true},
		},
		{
			name: "audio track 1",
			in:   "0:a:1",
			want: job.StreamSelect{InputIndex: 0, Type: "audio", Track: 1, All: false},
		},
		{
			name: "optional subtitle",
			in:   "0:s?",
			want: job.StreamSelect{InputIndex: 0, Type: "subtitle", All: true, Optional: true},
		},
		{
			name: "negate subtitle",
			in:   "-0:s",
			want: job.StreamSelect{InputIndex: 0, Type: "subtitle", All: true, Negate: true},
		},
		{
			name: "program + type",
			in:   "0:p:101:v",
			want: job.StreamSelect{InputIndex: 0, Type: "video", All: true, Program: 101},
		},
		{
			name: "program + type + idx",
			in:   "1:p:202:a:0",
			want: job.StreamSelect{InputIndex: 1, Type: "audio", Track: 0, All: false, Program: 202},
		},
		{
			name: "data type",
			in:   "0:d",
			want: job.StreamSelect{InputIndex: 0, Type: "data", All: true},
		},
		{
			name: "all attachments",
			in:   "0:t",
			want: job.StreamSelect{InputIndex: 0, Type: "attachment", All: true},
		},
		{
			name: "attachment track 0",
			in:   "0:t:0",
			want: job.StreamSelect{InputIndex: 0, Type: "attachment", Track: 0, All: false},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, err := parseMapArg(c.in)
			if err != nil {
				t.Fatalf("unexpected: %v", err)
			}
			if m.sel != c.want {
				t.Errorf("got %+v, want %+v", m.sel, c.want)
			}
		})
	}
}

func TestParseMapArg_Errors(t *testing.T) {
	cases := []struct {
		in     string
		errMsg string
	}{
		{"", "empty"},
		{"-0:s?", "mutually exclusive"},
		{"0", "bare"},
		{"0:p", "program id"},
		{"0:p:0:v", "program id"},
		{"0:p:5", "program with no stream-type"},
		{"0:x", "stream type"},
		{"0:v:-1", "stream index"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			_, err := parseMapArg(c.in)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", c.errMsg)
			}
			if !strings.Contains(err.Error(), c.errMsg) {
				t.Errorf("error %q does not contain %q", err.Error(), c.errMsg)
			}
		})
	}
}

func TestApplyMapSelectors_ReplacesDefaults(t *testing.T) {
	p := &parser{
		inputs: []job.Input{
			{ID: "input0", URL: "a.mp4", Streams: []job.StreamSelect{
				{InputIndex: 0, Type: "video", Track: 0},
				{InputIndex: 0, Type: "audio", Track: 0},
				{InputIndex: 0, Type: "subtitle", Track: 0},
			}},
		},
		mapSpecs: []parsedMap{
			{inputIdx: 0, sel: job.StreamSelect{InputIndex: 0, Type: "video", All: true}},
			{inputIdx: 0, sel: job.StreamSelect{InputIndex: 0, Type: "subtitle", All: true, Optional: true}},
		},
	}
	if err := p.applyMapSelectors(); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	got := p.inputs[0].Streams
	if len(got) != 2 {
		t.Fatalf("got %d streams, want 2: %+v", len(got), got)
	}
	if got[0].Type != "video" || !got[0].All {
		t.Errorf("first stream wrong: %+v", got[0])
	}
	if got[1].Type != "subtitle" || !got[1].Optional {
		t.Errorf("second stream wrong: %+v", got[1])
	}
}

func TestApplyMapSelectors_Noop(t *testing.T) {
	original := []job.StreamSelect{{InputIndex: 0, Type: "video", Track: 0}}
	p := &parser{
		inputs: []job.Input{{ID: "input0", URL: "a.mp4", Streams: original}},
	}
	if err := p.applyMapSelectors(); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(p.inputs[0].Streams) != 1 {
		t.Errorf("defaults should be preserved when no -map present, got %+v", p.inputs[0].Streams)
	}
}

func TestApplyMapSelectors_OutOfRangeInput(t *testing.T) {
	p := &parser{
		inputs: []job.Input{{ID: "input0", URL: "a.mp4"}},
		mapSpecs: []parsedMap{
			{inputIdx: 5, sel: job.StreamSelect{InputIndex: 5, Type: "video", All: true}},
		},
	}
	if err := p.applyMapSelectors(); err == nil {
		t.Fatal("expected out-of-range error")
	}
}
