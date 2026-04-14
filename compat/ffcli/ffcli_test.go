// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

import (
	"testing"
)

func TestParseSimpleTranscode(t *testing.T) {
	cfg, err := Parse("ffmpeg -i input.mp4 -c:v libx264 -c:a aac output.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Inputs) != 1 {
		t.Fatalf("inputs: got %d, want 1", len(cfg.Inputs))
	}
	if cfg.Inputs[0].URL != "input.mp4" {
		t.Errorf("input URL: got %q, want %q", cfg.Inputs[0].URL, "input.mp4")
	}
	if len(cfg.Outputs) != 1 {
		t.Fatalf("outputs: got %d, want 1", len(cfg.Outputs))
	}
	if cfg.Outputs[0].URL != "output.mp4" {
		t.Errorf("output URL: got %q, want %q", cfg.Outputs[0].URL, "output.mp4")
	}
	if cfg.Outputs[0].CodecVideo != "libx264" {
		t.Errorf("codec_video: got %q, want %q", cfg.Outputs[0].CodecVideo, "libx264")
	}
	if cfg.Outputs[0].CodecAudio != "aac" {
		t.Errorf("codec_audio: got %q, want %q", cfg.Outputs[0].CodecAudio, "aac")
	}
	// Should have video + audio edges
	if len(cfg.Graph.Edges) != 2 {
		t.Errorf("edges: got %d, want 2", len(cfg.Graph.Edges))
	}
}

func TestParseWithFilter(t *testing.T) {
	cfg, err := Parse("ffmpeg -i in.mp4 -vf scale=1280:720 -c:v libx264 out.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Graph.Nodes) != 1 {
		t.Fatalf("nodes: got %d, want 1", len(cfg.Graph.Nodes))
	}
	n := cfg.Graph.Nodes[0]
	if n.Filter != "scale" {
		t.Errorf("filter: got %q, want %q", n.Filter, "scale")
	}
	if n.Params["_pos0"] != "1280" {
		t.Errorf("param _pos0: got %v, want %q", n.Params["_pos0"], "1280")
	}
	// 3 edges: input->filter, filter->output (video), input->output (audio)
	if len(cfg.Graph.Edges) != 3 {
		t.Errorf("edges: got %d, want 3", len(cfg.Graph.Edges))
	}
}

func TestParseWithAudioCodec(t *testing.T) {
	cfg, err := Parse("ffmpeg -i in.mp4 -acodec mp3 out.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Outputs[0].CodecAudio != "mp3" {
		t.Errorf("codec_audio: got %q, want %q", cfg.Outputs[0].CodecAudio, "mp3")
	}
}

func TestParseNoInput(t *testing.T) {
	_, err := Parse("ffmpeg -c:v libx264 output.mp4")
	if err == nil {
		t.Fatal("expected error for no input")
	}
}

func TestParseNoOutput(t *testing.T) {
	_, err := Parse("ffmpeg -i input.mp4 -c:v libx264")
	if err == nil {
		t.Fatal("expected error for no output")
	}
}

func TestParseFilterChain(t *testing.T) {
	cfg, err := Parse("ffmpeg -i in.mp4 -vf scale=1280:720,drawtext=text=hello -c:v libx264 out.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Graph.Nodes) != 2 {
		t.Fatalf("nodes: got %d, want 2", len(cfg.Graph.Nodes))
	}
	if cfg.Graph.Nodes[0].Filter != "scale" {
		t.Errorf("node 0 filter: got %q, want %q", cfg.Graph.Nodes[0].Filter, "scale")
	}
	if cfg.Graph.Nodes[1].Filter != "drawtext" {
		t.Errorf("node 1 filter: got %q, want %q", cfg.Graph.Nodes[1].Filter, "drawtext")
	}
}

func TestParseVideoOnly(t *testing.T) {
	cfg, err := Parse("ffmpeg -i in.mp4 -an -c:v libx264 out.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Outputs[0].CodecAudio != "" {
		t.Errorf("codec_audio should be empty, got %q", cfg.Outputs[0].CodecAudio)
	}
	// Only video edge
	if len(cfg.Graph.Edges) != 1 {
		t.Errorf("edges: got %d, want 1", len(cfg.Graph.Edges))
	}
}

func TestTokenize(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"ffmpeg -i input.mp4 output.mp4", 4},
		{`ffmpeg -i "my file.mp4" output.mp4`, 4},
		{`ffmpeg -i 'my file.mp4' output.mp4`, 4},
		{"  ffmpeg   -i  in.mp4  out.mp4  ", 4},
	}
	for _, tt := range tests {
		got := tokenize(tt.input)
		if len(got) != tt.want {
			t.Errorf("tokenize(%q): got %d tokens %v, want %d", tt.input, len(got), got, tt.want)
		}
	}
}
