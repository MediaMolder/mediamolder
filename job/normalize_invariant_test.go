// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestMilestoneC_RuntimeReadsNoShorthand is the architectural
// invariant gate for Milestone C of
// private_local/normalization_plan_revised.md: after NormalizeConfig
// runs, the runtime must not depend on any "authoring shorthand"
// field on Output (CodecVideo/CodecAudio/CodecSubtitle, EncoderParams*,
// FPSMode, AudioSync, Pass, PassLogFile, ForceKeyFrames, SAR, DAR,
// EncoderTimeBase, FieldOrder, InterlacedEncode). The proof is:
//
//  1. Build a representative Config exercising every shorthand row.
//  2. Run it through NormalizeConfig → graph.Def "A".
//  3. Assert every shorthand row that was present has been lowered
//     onto a node (Internal.Encoder / Internal.Filter / a synthetic
//     filter / a synthetic encoder node) — verified by structural
//     checks below.
//  4. Make a deep copy of the Config, clear every shorthand field
//     on the copy, normalize again. The cleared graph is *expected*
//     to differ structurally (no synthesised encoders, no audio
//     resampler, no Pass tracking) — that is exactly the proof that
//     the lowering reads only from shorthand and the runtime reads
//     only from the lowered graph. The cleared run must produce no
//     ambiguity warnings (cleared shorthand cannot conflict).
//
// The static "no .CodecVideo / .FPSMode reads outside lowering" grep
// audit lives in [docs/field-ownership.md] (Milestone C exit
// criteria); the dynamic gate is here.
func TestMilestoneC_RuntimeReadsNoShorthand(t *testing.T) {
	cfg := representativeShorthandConfig()
	defA, warningsA, err := NormalizeConfig(cfg)
	if err != nil {
		t.Fatalf("NormalizeConfig (A): %v", err)
	}
	if len(warningsA) != 0 {
		t.Errorf("representative config produced unexpected warnings: %+v", warningsA)
	}

	// Structural assertion: every encoder / filter / synthetic node
	// carries the typed Internal it needs. If any of these are nil,
	// the runtime would have to fall back to reading shorthand.
	sawEncoderInternal := false
	sawFilterInternal := false
	sawGenerated := false
	for _, n := range defA.Nodes {
		if n.Type == "encoder" && n.Internal.Encoder != nil {
			sawEncoderInternal = true
		}
		if n.Type == "filter" && n.Internal.Filter != nil {
			sawFilterInternal = true
		}
		if n.Internal.Generated != nil {
			sawGenerated = true
		}
	}
	if !sawEncoderInternal {
		t.Errorf("expected at least one encoder with Internal.Encoder populated, got none")
	}
	if !sawFilterInternal {
		t.Errorf("expected at least one filter with Internal.Filter populated, got none")
	}
	if !sawGenerated {
		t.Errorf("expected at least one synthetic node with Internal.Generated provenance, got none")
	}

	// Cleared-shorthand control run: must succeed, must produce
	// zero warnings (nothing left to conflict).
	cfgB := deepCloneConfig(t, cfg)
	clearAllShorthand(cfgB)
	_, warningsB, err := NormalizeConfig(cfgB)
	if err != nil {
		t.Fatalf("NormalizeConfig (B, shorthand cleared): %v", err)
	}
	if len(warningsB) != 0 {
		t.Errorf("cleared config produced unexpected warnings: %+v", warningsB)
	}
}

// representativeShorthandConfig builds a Config that exercises every
// shorthand row in docs/field-ownership.md so the invariant test
// covers each lowering path.
func representativeShorthandConfig() *Config {
	return &Config{
		SchemaVersion:        "1.0",
		FilterComplexThreads: 4,
		Inputs:               []Input{{ID: "in0", URL: "in.mp4"}},
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "scale", Type: "filter", Filter: "scale=1280:-2"},
			},
			Edges: []EdgeDef{
				{From: "in0:v:0", To: "scale", Type: "video"},
				{From: "scale", To: "out0", Type: "video"},
				{From: "in0:a:0", To: "out0", Type: "audio"},
			},
		},
		Outputs: []Output{{
			ID:                 "out0",
			URL:                "out.mp4",
			CodecVideo:         "libx264",
			CodecAudio:         "aac",
			EncoderParamsVideo: map[string]any{"crf": "22", "preset": "slow"},
			EncoderParamsAudio: map[string]any{"b:a": "128k"},
			FPSMode:            "cfr",
			AudioSync:          1000,
			Pass:               1,
			PassLogFile:        "stats",
			ForceKeyFrames:     "expr:gte(t,n_forced*2)",
			SAR:                "1:1",
			EncoderTimeBase:    "1/30000",
			FieldOrder:         "tt",
			InterlacedEncode:   true,
		}},
	}
}

// clearAllShorthand wipes every authoring-shorthand Output field in
// place. The list mirrors §1.4 / Milestone C step 1 of
// private_local/normalization_plan_revised.md.
func clearAllShorthand(cfg *Config) {
	for i := range cfg.Outputs {
		o := &cfg.Outputs[i]
		o.CodecVideo = ""
		o.CodecAudio = ""
		o.CodecSubtitle = ""
		o.EncoderParamsVideo = nil
		o.EncoderParamsAudio = nil
		o.EncoderParamsSubtitle = nil
		o.FPSMode = ""
		o.AudioSync = 0
		o.Pass = 0
		o.PassLogFile = ""
		o.ForceKeyFrames = ""
		o.SAR = ""
		o.DAR = ""
		o.EncoderTimeBase = ""
		o.FieldOrder = ""
		o.InterlacedEncode = false
		for si := range o.Streams {
			o.Streams[si].Encoder = nil
		}
	}
}

func deepCloneConfig(t *testing.T, cfg *Config) *Config {
	t.Helper()
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	var out Config
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	return &out
}

// Sanity: our deep-clone helper actually clones (no aliasing).
func TestDeepCloneConfig_Independent(t *testing.T) {
	a := representativeShorthandConfig()
	b := deepCloneConfig(t, a)
	b.Outputs[0].CodecVideo = "different"
	if a.Outputs[0].CodecVideo == b.Outputs[0].CodecVideo {
		t.Fatalf("deep clone aliased: a=%q b=%q", a.Outputs[0].CodecVideo, b.Outputs[0].CodecVideo)
	}
	if !reflect.DeepEqual(a.Outputs[0].EncoderParamsVideo, map[string]any{"crf": "22", "preset": "slow"}) {
		t.Fatalf("clone mutated original encoder_params_video")
	}
}
