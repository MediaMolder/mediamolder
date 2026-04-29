// Copyright (C) 2026 Thomas Vaughan
//
// SPDX-License-Identifier: GPL-3.0-or-later

package ffcli

import (
	"reflect"
	"testing"
)

func TestParseHLSFlags(t *testing.T) {
	cmd := `ffmpeg -i in.mp4 -c:v libx264 -hls_time 4 -hls_init_time 1 -hls_list_size 6 ` +
		`-hls_playlist_type vod -hls_segment_type fmp4 -hls_segment_filename seg_%03d.m4s ` +
		`-hls_fmp4_init_filename init.mp4 -start_number 10 -master_pl_name master.m3u8 ` +
		`-var_stream_map "v:0,a:0 v:1,a:0" -hls_flags delete_segments+independent_segments ` +
		`-f hls out.m3u8`
	cfg, err := Parse(cmd)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(cfg.Outputs) != 1 || cfg.Outputs[0].HLS == nil {
		t.Fatalf("HLS field not populated: %+v", cfg.Outputs)
	}
	h := cfg.Outputs[0].HLS
	if h.Time != 4 || h.InitTime != 1 || h.ListSize != 6 {
		t.Errorf("numeric fields: %+v", h)
	}
	if h.PlaylistType != "vod" || h.SegmentType != "fmp4" {
		t.Errorf("enum fields: %+v", h)
	}
	if h.SegmentFilename != "seg_%03d.m4s" || h.FMP4InitFilename != "init.mp4" {
		t.Errorf("filename fields: %+v", h)
	}
	if h.StartNumber != 10 || h.MasterPlName != "master.m3u8" || h.VarStreamMap != "v:0,a:0 v:1,a:0" {
		t.Errorf("ABR fields: %+v", h)
	}
	if !reflect.DeepEqual(h.Flags, []string{"delete_segments", "independent_segments"}) {
		t.Errorf("flags: %v", h.Flags)
	}
}

func TestParseDASHFlags(t *testing.T) {
	cmd := `ffmpeg -i in.mp4 -c:v libx264 -seg_duration 5 -frag_duration 1 -window_size 10 ` +
		`-extra_window_size 3 -init_seg_name init-$RepresentationID$.m4s ` +
		`-media_seg_name chunk-$RepresentationID$-$Number$.m4s -single_file 1 ` +
		`-use_template 1 -use_timeline 0 -streaming 1 ` +
		`-adaptation_sets "id=0,streams=v id=1,streams=a" -hls_playlist 1 -ldash 1 ` +
		`-dash_flags global_sidx -f dash out.mpd`
	cfg, err := Parse(cmd)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(cfg.Outputs) != 1 || cfg.Outputs[0].DASH == nil {
		t.Fatalf("DASH field not populated: %+v", cfg.Outputs)
	}
	d := cfg.Outputs[0].DASH
	if d.SegDuration != 5 || d.FragDuration != 1 || d.WindowSize != 10 || d.ExtraWindowSize != 3 {
		t.Errorf("numeric fields: %+v", d)
	}
	if d.UseTemplate == nil || !*d.UseTemplate {
		t.Errorf("UseTemplate not set true: %+v", d.UseTemplate)
	}
	if d.UseTimeline == nil || *d.UseTimeline {
		t.Errorf("UseTimeline not set false: %+v", d.UseTimeline)
	}
	if !d.SingleFile || !d.Streaming || !d.HLSPlaylist || !d.LDash {
		t.Errorf("bool fields: %+v", d)
	}
	if d.AdaptationSets != "id=0,streams=v id=1,streams=a" {
		t.Errorf("adaptation_sets: %q", d.AdaptationSets)
	}
	if !reflect.DeepEqual(d.Flags, []string{"global_sidx"}) {
		t.Errorf("flags: %v", d.Flags)
	}
}

func TestParseHLSDASHRejectInvalid(t *testing.T) {
	cases := []string{
		`ffmpeg -i in.mp4 -hls_time abc -f hls out.m3u8`,
		`ffmpeg -i in.mp4 -hls_list_size -1 -f hls out.m3u8`,
		`ffmpeg -i in.mp4 -seg_duration nope -f dash out.mpd`,
		`ffmpeg -i in.mp4 -use_template maybe -f dash out.mpd`,
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			if _, err := Parse(c); err == nil {
				t.Fatal("expected parse error")
			}
		})
	}
}
