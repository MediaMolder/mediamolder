// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

import (
	"encoding/json"
	"strings"
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

// TestParseStreamCopyEmitsEmptyNodes is a regression test for a bug
// where stream-copy commands (`-c copy`) produced a Config whose
// Graph.Nodes was a nil Go slice, marshalling to JSON `null`. The
// frontend then crashed in `materializeImplicitEncoders` calling
// `.map()` on null. The parser must always emit a non-nil (possibly
// empty) Nodes / Edges slice so the JSON payload contains `[]`.
func TestParseStreamCopyEmitsEmptyNodes(t *testing.T) {
	cfg, err := Parse("ffmpeg -i in.mp4 -c copy out.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Graph.Nodes == nil {
		t.Fatal("Graph.Nodes is nil; expected non-nil empty slice")
	}
	if len(cfg.Graph.Nodes) != 0 {
		t.Errorf("Graph.Nodes: got %d nodes, want 0", len(cfg.Graph.Nodes))
	}
	b, err := json.Marshal(cfg.Graph)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"nodes":null`) {
		t.Errorf("graph JSON contains \"nodes\":null; got %s", b)
	}
	if !strings.Contains(string(b), `"nodes":[]`) {
		t.Errorf("graph JSON missing \"nodes\":[]; got %s", b)
	}
}

// TestParseTimingFlagsAttachToOutput is a regression test for `-t`,
// `-ss`, and `-to` being silently dropped (dumped into the parser's
// globalOpts catch-all bucket and never round-tripped). They must be
// attached to the next file specifier on the command line — the
// output, in this case — as entries in `Output.Options`.
func TestParseTimingFlagsAttachToOutput(t *testing.T) {
	cfg, err := Parse("ffmpeg -i in.mp4 -c copy -t 30 -ss 5 out.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Outputs) != 1 {
		t.Fatalf("outputs: got %d, want 1", len(cfg.Outputs))
	}
	opts := cfg.Outputs[0].Options
	if opts == nil {
		t.Fatal("Outputs[0].Options is nil; expected -t / -ss to populate it")
	}
	if opts["t"] != "30" {
		t.Errorf("Options[t]: got %v, want \"30\"", opts["t"])
	}
	if opts["ss"] != "5" {
		t.Errorf("Options[ss]: got %v, want \"5\"", opts["ss"])
	}
}

// TestParseTimingFlagsAttachToInput verifies that `-t`/`-ss`/`-to`
// placed *before* `-i` are attached to the input rather than the
// output, matching FFmpeg's positional semantics.
func TestParseTimingFlagsAttachToInput(t *testing.T) {
	cfg, err := Parse("ffmpeg -ss 10 -t 30 -i in.mp4 -c copy out.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Inputs) != 1 {
		t.Fatalf("inputs: got %d, want 1", len(cfg.Inputs))
	}
	opts := cfg.Inputs[0].Options
	if opts == nil {
		t.Fatal("Inputs[0].Options is nil; expected -ss / -t to populate it")
	}
	if opts["ss"] != "10" {
		t.Errorf("Options[ss]: got %v, want \"10\"", opts["ss"])
	}
	if opts["t"] != "30" {
		t.Errorf("Options[t]: got %v, want \"30\"", opts["t"])
	}
	// Output must NOT inherit the input's timing options.
	if cfg.Outputs[0].Options != nil {
		t.Errorf("Outputs[0].Options leaked: got %v, want nil", cfg.Outputs[0].Options)
	}
}
