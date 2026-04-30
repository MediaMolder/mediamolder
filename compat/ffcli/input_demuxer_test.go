// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

import (
	"testing"
)

func TestParse_RawVideoInputTypedFields(t *testing.T) {
	cfg, err := Parse(`ffmpeg -f rawvideo -pix_fmt yuv420p -s 320x240 -framerate 30 -i in.yuv out.mp4`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(cfg.Inputs) != 1 {
		t.Fatalf("want 1 input, got %d", len(cfg.Inputs))
	}
	in := cfg.Inputs[0]
	if in.Kind != "raw" {
		t.Errorf("kind = %q, want \"raw\"", in.Kind)
	}
	if in.Format != "rawvideo" {
		t.Errorf("format = %q, want \"rawvideo\"", in.Format)
	}
	if in.PixelFormat != "yuv420p" {
		t.Errorf("pixel_format = %q", in.PixelFormat)
	}
	if in.VideoSize != "320x240" {
		t.Errorf("video_size = %q", in.VideoSize)
	}
	if in.FrameRate != 30 {
		t.Errorf("framerate = %g", in.FrameRate)
	}
	if len(in.Options) != 0 {
		t.Errorf("Options should be drained: %#v", in.Options)
	}
}

func TestParse_RawAudioInputTypedFields(t *testing.T) {
	cfg, err := Parse(`ffmpeg -f s16le -ar 48000 -ac 2 -i in.pcm out.wav`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	in := cfg.Inputs[0]
	if in.Kind != "raw" || in.Format != "s16le" {
		t.Errorf("kind/format = %q/%q", in.Kind, in.Format)
	}
	if in.SampleRate != 48000 {
		t.Errorf("sample_rate = %d", in.SampleRate)
	}
	if in.Channels != 2 {
		t.Errorf("channels = %d", in.Channels)
	}
}

func TestParse_ForceFormatInput(t *testing.T) {
	cfg, err := Parse(`ffmpeg -f mpegts -i udp://239.0.0.1:1234 out.mp4`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	in := cfg.Inputs[0]
	if in.Format != "mpegts" {
		t.Errorf("format = %q", in.Format)
	}
	if in.Kind != "" {
		t.Errorf("kind should remain empty for non-raw force-format, got %q", in.Kind)
	}
}

func TestParse_ProtocolWhitelistAndQueue(t *testing.T) {
	cfg, err := Parse(`ffmpeg -protocol_whitelist file,http,https -thread_queue_size 1024 -i playlist.m3u8 out.mp4`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	in := cfg.Inputs[0]
	if got := in.ProtocolWhitelist; len(got) != 3 || got[0] != "file" || got[1] != "http" || got[2] != "https" {
		t.Errorf("protocol_whitelist = %#v", got)
	}
	if in.ThreadQueueSize != 1024 {
		t.Errorf("thread_queue_size = %d", in.ThreadQueueSize)
	}
}

func TestParse_AccurateSeekAndSeekTimestamp(t *testing.T) {
	cfg, err := Parse(`ffmpeg -noaccurate_seek -seek_timestamp -ss 5 -i in.ts out.mp4`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	in := cfg.Inputs[0]
	if in.AccurateSeek == nil || *in.AccurateSeek {
		t.Errorf("accurate_seek = %v, want false", in.AccurateSeek)
	}
	if !in.SeekTimestamp {
		t.Errorf("seek_timestamp not set")
	}
	if _, ok := in.Options["ss"]; !ok {
		t.Errorf("ss should remain in options for runtime: %#v", in.Options)
	}
}

func TestParse_PatternType(t *testing.T) {
	cfg, err := Parse(`ffmpeg -pattern_type glob -framerate 25 -i frames/*.png out.mp4`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	in := cfg.Inputs[0]
	if in.PatternType != "glob" {
		t.Errorf("pattern_type = %q", in.PatternType)
	}
	if in.FrameRate != 25 {
		t.Errorf("framerate = %g", in.FrameRate)
	}
}

func TestParse_OutputFlagsStillRouted(t *testing.T) {
	// -pix_fmt / -ar / -ac / -r between -i and the output URL must
	// still land on the encoder opts (output-side meaning), not on
	// the input.
	cfg, err := Parse(`ffmpeg -i in.mp4 -pix_fmt yuv420p -ar 44100 -ac 2 -r 30 out.mp4`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Inputs[0].PixelFormat != "" {
		t.Errorf("input pix should not be set: %q", cfg.Inputs[0].PixelFormat)
	}
	if cfg.Inputs[0].SampleRate != 0 {
		t.Errorf("input sample_rate should not be set: %d", cfg.Inputs[0].SampleRate)
	}
	out := cfg.Outputs[0]
	if v, ok := out.EncoderParamsVideo["pix_fmt"]; !ok || v != "yuv420p" {
		t.Errorf("pix_fmt missing on encoder: %#v", out.EncoderParamsVideo)
	}
	if v, ok := out.EncoderParamsAudio["ar"]; !ok || v != "44100" {
		t.Errorf("ar missing on encoder: %#v", out.EncoderParamsAudio)
	}
}
