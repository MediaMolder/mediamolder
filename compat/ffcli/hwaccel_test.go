// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

import "testing"

func TestParseHWAccel(t *testing.T) {
	cfg, err := Parse("ffmpeg -hwaccel cuda -hwaccel_device 0 -i input.mp4 -c:v h264_nvenc output.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GlobalOptions.HardwareAccel != "cuda" {
		t.Errorf("hw_accel: got %q, want %q", cfg.GlobalOptions.HardwareAccel, "cuda")
	}
	if cfg.GlobalOptions.HardwareDevice != "0" {
		t.Errorf("hw_device: got %q, want %q", cfg.GlobalOptions.HardwareDevice, "0")
	}
	if cfg.Outputs[0].CodecVideo != "h264_nvenc" {
		t.Errorf("codec_video: got %q, want %q", cfg.Outputs[0].CodecVideo, "h264_nvenc")
	}
}

func TestParseHWAccelVAAPI(t *testing.T) {
	cfg, err := Parse("ffmpeg -hwaccel vaapi -hwaccel_device /dev/dri/renderD128 -hwaccel_output_format vaapi -i input.mp4 -c:v h264_vaapi output.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GlobalOptions.HardwareAccel != "vaapi" {
		t.Errorf("hw_accel: got %q, want %q", cfg.GlobalOptions.HardwareAccel, "vaapi")
	}
	if cfg.GlobalOptions.HardwareDevice != "/dev/dri/renderD128" {
		t.Errorf("hw_device: got %q", cfg.GlobalOptions.HardwareDevice)
	}
}

func TestParseBSFVideo(t *testing.T) {
	cfg, err := Parse("ffmpeg -i input.mp4 -c:v copy -bsf:v h264_mp4toannexb -f mpegts output.ts")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Outputs[0].BSFVideo != "h264_mp4toannexb" {
		t.Errorf("bsf_video: got %q, want %q", cfg.Outputs[0].BSFVideo, "h264_mp4toannexb")
	}
	if cfg.Outputs[0].Format != "mpegts" {
		t.Errorf("format: got %q, want %q", cfg.Outputs[0].Format, "mpegts")
	}
}

func TestParseBSFAudio(t *testing.T) {
	cfg, err := Parse("ffmpeg -i input.ts -c:a copy -bsf:a aac_adtstoasc output.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Outputs[0].BSFAudio != "aac_adtstoasc" {
		t.Errorf("bsf_audio: got %q, want %q", cfg.Outputs[0].BSFAudio, "aac_adtstoasc")
	}
}

func TestParseSubtitleCodec(t *testing.T) {
	cfg, err := Parse("ffmpeg -i input.mkv -c:v copy -c:a copy -c:s srt output.mkv")
	if err != nil {
		t.Fatal(err)
	}
	// Should have subtitle edge.
	foundSub := false
	for _, e := range cfg.Graph.Edges {
		if e.Type == "subtitle" {
			foundSub = true
		}
	}
	if !foundSub {
		t.Error("expected subtitle edge in graph")
	}
}

func TestParseSubtitleDisable(t *testing.T) {
	cfg, err := Parse("ffmpeg -sn -i input.mkv -c:v copy output.mp4")
	if err != nil {
		t.Fatal(err)
	}
	// No subtitle edge should exist.
	for _, e := range cfg.Graph.Edges {
		if e.Type == "subtitle" {
			t.Error("unexpected subtitle edge when -sn is specified")
		}
	}
	// Input should not have subtitle stream.
	for _, s := range cfg.Inputs[0].Streams {
		if s.Type == "subtitle" {
			t.Error("input should not have subtitle stream with -sn")
		}
	}
}

func TestParseHWAccelMissing(t *testing.T) {
	_, err := Parse("ffmpeg -hwaccel")
	if err == nil {
		t.Fatal("expected error for -hwaccel without argument")
	}
}

func TestParseBSFMissing(t *testing.T) {
	_, err := Parse("ffmpeg -i input.mp4 -bsf:v")
	if err == nil {
		t.Fatal("expected error for -bsf:v without argument")
	}
}

func TestParseHWAccelQSV(t *testing.T) {
	cfg, err := Parse("ffmpeg -hwaccel qsv -i input.mp4 -c:v h264_qsv output.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GlobalOptions.HardwareAccel != "qsv" {
		t.Errorf("hw_accel: got %q, want %q", cfg.GlobalOptions.HardwareAccel, "qsv")
	}
}

func TestParseCombinedHWAndBSF(t *testing.T) {
	cfg, err := Parse("ffmpeg -hwaccel cuda -i input.mp4 -c:v h264_nvenc -bsf:v h264_mp4toannexb -f mpegts output.ts")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GlobalOptions.HardwareAccel != "cuda" {
		t.Errorf("hw_accel: got %q", cfg.GlobalOptions.HardwareAccel)
	}
	if cfg.Outputs[0].BSFVideo != "h264_mp4toannexb" {
		t.Errorf("bsf_video: got %q", cfg.Outputs[0].BSFVideo)
	}
	if cfg.Outputs[0].CodecVideo != "h264_nvenc" {
		t.Errorf("codec_video: got %q", cfg.Outputs[0].CodecVideo)
	}
}

func TestParseSubtitleWithFilters(t *testing.T) {
	cfg, err := Parse("ffmpeg -i input.mkv -c:v libx264 -c:s copy -vf scale=1280:720 output.mkv")
	if err != nil {
		t.Fatal(err)
	}
	// Should have video edges (through filter), audio edges, and subtitle edge.
	typeCount := map[string]int{}
	for _, e := range cfg.Graph.Edges {
		typeCount[e.Type]++
	}
	if typeCount["video"] < 2 {
		t.Errorf("expected at least 2 video edges (through filter), got %d", typeCount["video"])
	}
	if typeCount["subtitle"] != 1 {
		t.Errorf("expected 1 subtitle edge, got %d", typeCount["subtitle"])
	}
}
