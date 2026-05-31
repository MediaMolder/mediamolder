// Copyright (C) 2026 Thomas Vaughan
//
// SPDX-License-Identifier: GPL-3.0-or-later

package job

import (
	"strings"
	"testing"
)

func baseHLSConfig(out Output) *Config {
	return &Config{
		SchemaVersion: "1.0",
		Inputs:        []Input{{ID: "in0", URL: "in.mp4"}},
		Outputs:       []Output{out},
		Graph:         GraphDef{Edges: []EdgeDef{{From: "in0:v:0", To: "out0:v", Type: "video"}}},
	}
}

func TestValidateHLS_RejectsWrongFormat(t *testing.T) {
	cfg := baseHLSConfig(Output{
		ID:     "out0",
		URL:    "out.mp4",
		Format: "mp4",
		HLS:    &HLSOptions{Time: 4},
	})
	err := validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "hls options only valid") {
		t.Fatalf("expected format-mismatch error, got %v", err)
	}
}

func TestValidateHLS_RejectsBadEnums(t *testing.T) {
	cases := []struct {
		name string
		hls  HLSOptions
		want string
	}{
		{"playlist_type", HLSOptions{PlaylistType: "live"}, "hls.playlist_type"},
		{"segment_type", HLSOptions{SegmentType: "ts"}, "hls.segment_type"},
		{"var_stream_map without master", HLSOptions{VarStreamMap: "v:0,a:0"}, "var_stream_map requires"},
		{"negative time", HLSOptions{Time: -1}, "hls.time"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := baseHLSConfig(Output{ID: "out0", URL: "out.m3u8", Format: "hls", HLS: &tc.hls})
			err := validate(cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestValidateHLS_Accepts(t *testing.T) {
	cfg := baseHLSConfig(Output{
		ID: "out0", URL: "out.m3u8", Format: "hls",
		HLS: &HLSOptions{
			Time: 4, PlaylistType: "vod", SegmentType: "fmp4",
			MasterPlName: "master.m3u8", VarStreamMap: "v:0,a:0",
			Flags: []string{"independent_segments", "program_date_time"},
		},
	})
	if err := validate(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateDASH_RejectsWrongFormat(t *testing.T) {
	cfg := baseHLSConfig(Output{ID: "out0", URL: "out.mp4", Format: "mp4", DASH: &DASHOptions{SegDuration: 4}})
	err := validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "dash options only valid") {
		t.Fatalf("expected format-mismatch error, got %v", err)
	}
}

func TestValidateDASH_Accepts(t *testing.T) {
	tr := true
	cfg := baseHLSConfig(Output{
		ID: "out0", URL: "out.mpd", Format: "dash",
		DASH: &DASHOptions{
			SegDuration: 4, FragDuration: 2,
			UseTemplate: &tr, UseTimeline: &tr,
			AdaptationSets: "id=0,streams=v id=1,streams=a",
			HLSPlaylist:    true,
		},
	})
	if err := validate(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildMuxerOptions_HLS(t *testing.T) {
	out := &Output{
		Format: "hls",
		HLS: &HLSOptions{
			Time: 4, InitTime: 1, ListSize: 6, PlaylistType: "vod",
			SegmentType: "fmp4", SegmentFilename: "seg_%03d.m4s",
			FMP4InitFilename: "init.mp4", StartNumber: 10,
			MasterPlName: "master.m3u8", VarStreamMap: "v:0,a:0 v:1,a:0",
			Flags: []string{"delete_segments", "independent_segments"},
		},
	}
	got := buildMuxerOptions(out)
	want := map[string]string{
		"hls_time":               "4",
		"hls_init_time":          "1",
		"hls_list_size":          "6",
		"hls_playlist_type":      "vod",
		"hls_segment_type":       "fmp4",
		"hls_segment_filename":   "seg_%03d.m4s",
		"hls_fmp4_init_filename": "init.mp4",
		"start_number":           "10",
		"master_pl_name":         "master.m3u8",
		"var_stream_map":         "v:0,a:0 v:1,a:0",
		"hls_flags":              "delete_segments+independent_segments",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("key %q: got %q want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("dict size mismatch: got %d (%v) want %d", len(got), got, len(want))
	}
}

func TestBuildMuxerOptions_DASH(t *testing.T) {
	tr, fl := true, false
	out := &Output{
		Format: "dash",
		DASH: &DASHOptions{
			SegDuration: 5, FragDuration: 1, WindowSize: 10, ExtraWindowSize: 3,
			InitSegName: "init-$RepresentationID$.m4s", MediaSegName: "chunk-$RepresentationID$-$Number%05d$.m4s",
			SingleFile: true, UseTemplate: &tr, UseTimeline: &fl, Streaming: true,
			AdaptationSets: "id=0,streams=v id=1,streams=a", HLSPlaylist: true, LDash: true,
			Flags: []string{"global_sidx"},
		},
	}
	got := buildMuxerOptions(out)
	if got["seg_duration"] != "5" || got["use_template"] != "1" || got["use_timeline"] != "0" {
		t.Errorf("typed bool/duration mapping wrong: %v", got)
	}
	if got["hls_playlist"] != "1" || got["ldash"] != "1" || got["streaming"] != "1" {
		t.Errorf("dash boolean flags wrong: %v", got)
	}
	if got["dash_flags"] != "global_sidx" {
		t.Errorf("dash_flags joined wrong: %q", got["dash_flags"])
	}
}

func TestBuildMuxerOptions_TypedFieldOverridesOptionsBag(t *testing.T) {
	out := &Output{
		Format:  "hls",
		Options: map[string]any{"hls_time": "9", "hls_segment_type": "mpegts"},
		HLS:     &HLSOptions{Time: 4, SegmentType: "fmp4"},
	}
	got := buildMuxerOptions(out)
	if got["hls_time"] != "4" {
		t.Errorf("typed Time should override Options: got %q", got["hls_time"])
	}
	if got["hls_segment_type"] != "fmp4" {
		t.Errorf("typed SegmentType should override Options: got %q", got["hls_segment_type"])
	}
}

func TestBuildMuxerOptions_StripsTimingKeys(t *testing.T) {
	out := &Output{Options: map[string]any{"ss": "10", "t": "30", "to": "40", "movflags": "+faststart"}}
	got := buildMuxerOptions(out)
	for _, k := range []string{"ss", "t", "to"} {
		if _, ok := got[k]; ok {
			t.Errorf("timing key %q must be stripped before reaching the muxer dict", k)
		}
	}
	if got["movflags"] != "+faststart" {
		t.Errorf("non-timing Options entries must pass through: %v", got)
	}
}

func TestBuildMuxerOptions_NilReturnsNil(t *testing.T) {
	if got := buildMuxerOptions(nil); got != nil {
		t.Errorf("nil out should yield nil dict, got %v", got)
	}
	if got := buildMuxerOptions(&Output{}); got != nil {
		t.Errorf("empty out should yield nil dict, got %v", got)
	}
}
