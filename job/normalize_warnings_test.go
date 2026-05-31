// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"strings"
	"testing"
)

// Milestone C: NormalizeConfig must surface
// `compat.output_encoder_shorthand_ignored` warnings whenever an
// Output carries authoring shorthand (codec_video, fps_mode, ...)
// alongside an explicit encoder/copy node that already satisfies
// the same edge. The explicit node wins; the shorthand is dropped;
// the warning lets the user (and the GUI) see the conflict.

func TestNormalize_AmbiguityWarn_VideoCodecShorthand(t *testing.T) {
	cfg := &Config{
		Inputs: []Input{{ID: "in0", URL: "in.mp4"}},
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "enc0", Type: "encoder", Params: map[string]any{"codec": "libx264", "crf": "22"}},
			},
			Edges: []EdgeDef{
				{From: "in0:v:0", To: "enc0", Type: "video"},
				{From: "enc0", To: "out0", Type: "video"},
			},
		},
		Outputs: []Output{{
			ID:                 "out0",
			URL:                "out.mp4",
			CodecVideo:         "libx265",                   // ignored
			FPSMode:            "cfr",                       // ignored
			ForceKeyFrames:     "expr:gte(t,n_forced*2)",    // ignored
			EncoderParamsVideo: map[string]any{"crf": "20"}, // ignored
			Pass:               1,                           // ignored
			PassLogFile:        "stats",                     // ignored
		}},
	}
	_, warnings, err := NormalizeConfig(cfg)
	if err != nil {
		t.Fatalf("NormalizeConfig: %v", err)
	}
	got := codeSet(warnings)
	if got["compat.output_encoder_shorthand_ignored"] < 6 {
		t.Errorf("expected >=6 ambiguity warnings (codec, fps_mode, force_key_frames, encoder_params, pass, passlogfile), got %d: %+v",
			got["compat.output_encoder_shorthand_ignored"], warnings)
	}
	for _, w := range warnings {
		if !strings.HasPrefix(w.Path, "outputs[0].") {
			t.Errorf("warning Path = %q, want outputs[0].*", w.Path)
		}
	}
}

func TestNormalize_AmbiguityWarn_AudioAndSubtitle(t *testing.T) {
	cfg := &Config{
		Inputs: []Input{{ID: "in0", URL: "in.mp4"}},
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "aenc", Type: "encoder", Params: map[string]any{"codec": "libopus"}},
				{ID: "senc", Type: "encoder", Params: map[string]any{"codec": "ass"}},
			},
			Edges: []EdgeDef{
				{From: "in0:a:0", To: "aenc", Type: "audio"},
				{From: "aenc", To: "out0", Type: "audio"},
				{From: "in0:s:0", To: "senc", Type: "subtitle"},
				{From: "senc", To: "out0", Type: "subtitle"},
			},
		},
		Outputs: []Output{{
			ID:                    "out0",
			URL:                   "out.mkv",
			CodecAudio:            "aac",                         // ignored
			EncoderParamsAudio:    map[string]any{"b:a": "128k"}, // ignored
			CodecSubtitle:         "mov_text",                    // ignored
			EncoderParamsSubtitle: map[string]any{"x": "1"},      // ignored
		}},
	}
	_, warnings, err := NormalizeConfig(cfg)
	if err != nil {
		t.Fatalf("NormalizeConfig: %v", err)
	}
	if n := len(warnings); n != 4 {
		t.Errorf("expected 4 warnings (audio codec/params, subtitle codec/params), got %d: %+v", n, warnings)
	}
}

func TestNormalize_AmbiguityWarn_PerStreamEncoderOverride(t *testing.T) {
	cfg := &Config{
		Inputs: []Input{{ID: "in0", URL: "in.mp4"}},
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "enc0", Type: "encoder", Params: map[string]any{"codec": "libx264"}},
			},
			Edges: []EdgeDef{
				{From: "in0:v:0", To: "enc0", Type: "video"},
				{From: "enc0", To: "out0", Type: "video"},
			},
		},
		Outputs: []Output{{
			ID:  "out0",
			URL: "out.mp4",
			Streams: []StreamSpec{{
				Type:    "v",
				Index:   0,
				Encoder: &EncoderOverride{Codec: "libx265"},
			}},
		}},
	}
	_, warnings, err := NormalizeConfig(cfg)
	if err != nil {
		t.Fatalf("NormalizeConfig: %v", err)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w.Path, "streams[0].encoder") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected per-stream encoder ambiguity warning, got %+v", warnings)
	}
}

// Implicit encoders (no explicit graph encoder node) must NOT trigger
// the warning — the shorthand is the only source of truth there.
func TestNormalize_NoAmbiguityWarn_ImplicitOnly(t *testing.T) {
	cfg := &Config{
		Inputs: []Input{{ID: "in0", URL: "in.mp4"}},
		Graph: GraphDef{
			Nodes: []NodeDef{},
			Edges: []EdgeDef{{From: "in0:v:0", To: "out0", Type: "video"}},
		},
		Outputs: []Output{{
			ID:         "out0",
			URL:        "out.mp4",
			CodecVideo: "libx264",
			FPSMode:    "cfr",
		}},
	}
	_, warnings, err := NormalizeConfig(cfg)
	if err != nil {
		t.Fatalf("NormalizeConfig: %v", err)
	}
	for _, w := range warnings {
		if w.Code == "compat.output_encoder_shorthand_ignored" {
			t.Errorf("unexpected ambiguity warning for implicit-only graph: %+v", w)
		}
	}
}

// Stream-copy node also satisfies the encoder slot; shorthand on the
// matching Output is silently ignored, so we must warn.
func TestNormalize_AmbiguityWarn_CopyNode(t *testing.T) {
	cfg := &Config{
		Inputs: []Input{{ID: "in0", URL: "in.mp4"}},
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "cpy", Type: "copy"},
			},
			Edges: []EdgeDef{
				{From: "in0:v:0", To: "cpy", Type: "video"},
				{From: "cpy", To: "out0", Type: "video"},
			},
		},
		Outputs: []Output{{
			ID:         "out0",
			URL:        "out.mp4",
			CodecVideo: "libx264",
		}},
	}
	_, warnings, err := NormalizeConfig(cfg)
	if err != nil {
		t.Fatalf("NormalizeConfig: %v", err)
	}
	if len(warnings) == 0 {
		t.Errorf("expected ambiguity warning for explicit copy node + shorthand, got none")
	}
}

func codeSet(ws []NormalizeWarning) map[string]int {
	out := make(map[string]int)
	for _, w := range ws {
		out[w.Code]++
	}
	return out
}
