// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/graph"
)

// TestNormalizeConfig_DoesNotMutateInput verifies the contract that
// NormalizeConfig must not mutate the input *Config. Subsequent
// commits in the normalization-boundary effort move more lowering
// logic behind this boundary; the no-mutation invariant must hold.
func TestNormalizeConfig_DoesNotMutateInput(t *testing.T) {
	cfg := &Config{
		SchemaVersion:        "1.0",
		FilterComplexThreads: 4,
		Inputs:               []Input{{ID: "in0", URL: "in.mp4"}},
		Graph: GraphDef{
			Nodes: []NodeDef{{ID: "f", Type: "filter", Filter: "scale=1280:-2"}},
			Edges: []EdgeDef{
				{From: "in0:v:0", To: "f", Type: "video"},
				{From: "f", To: "out0", Type: "video"},
				{From: "in0:a:0", To: "out0", Type: "audio"},
			},
		},
		Outputs: []Output{{
			ID: "out0", URL: "out.mp4",
			CodecVideo: "libx264", CodecAudio: "aac",
			AudioSync:      1000,
			ForceKeyFrames: "expr:gte(t,n_forced*2)",
			Pass:           1, PassLogFile: "stats",
			SAR: "1:1", FPSMode: "cfr",
		}},
	}

	// Capture a deep-equal snapshot of the input by JSON round-trip.
	before, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal before: %v", err)
	}

	if _, _, err := NormalizeConfig(cfg); err != nil {
		t.Fatalf("NormalizeConfig: %v", err)
	}

	after, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal after: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("NormalizeConfig mutated input:\nbefore: %s\nafter:  %s", before, after)
	}
}

// TestNormalizeConfig_Deterministic verifies that the same input
// produces byte-identical normalized output across repeat calls.
// This is the gate that makes future snapshot tests safe to write.
func TestNormalizeConfig_Deterministic(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.0",
		Inputs:        []Input{{ID: "in0", URL: "in.mp4"}},
		Graph: GraphDef{
			Edges: []EdgeDef{
				{From: "in0:v:0", To: "out0", Type: "video"},
				{From: "in0:a:0", To: "out0", Type: "audio"},
			},
		},
		Outputs: []Output{{
			ID: "out0", URL: "out.mp4",
			CodecVideo:     "libx264",
			CodecAudio:     "aac",
			AudioSync:      1000,
			Pass:           1,
			PassLogFile:    "stats",
			ForceKeyFrames: "expr:gte(t,n_forced*2)",
			SAR:            "1:1",
			FPSMode:        "cfr",
		}},
	}

	a, _, err := NormalizeConfig(cfg)
	if err != nil {
		t.Fatalf("NormalizeConfig #1: %v", err)
	}
	b, _, err := NormalizeConfig(cfg)
	if err != nil {
		t.Fatalf("NormalizeConfig #2: %v", err)
	}
	if !reflect.DeepEqual(canonicalize(a), canonicalize(b)) {
		t.Errorf("NormalizeConfig is non-deterministic")
	}
}

// TestNormalizeConfig_LowersAllShorthand exercises every "authoring
// shorthand" row from docs/field-ownership.md and asserts that the
// normalized graph carries the corresponding node-local marker.
//
// This is the regression gate for the Milestone B sentinel-migration
// work: when the __* sentinels are replaced by NodeDef.Internal in a
// follow-up commit, this test must be updated to assert the typed
// fields instead of the string keys, but the *behaviour* (which
// shorthand reaches the encoder node) must not change.
func TestNormalizeConfig_LowersAllShorthand(t *testing.T) {
	cfg := &Config{
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
			ID:               "out0",
			URL:              "out.mp4",
			CodecVideo:       "libx264",
			CodecAudio:       "aac",
			FPSMode:          "cfr",
			AudioSync:        1000,
			Pass:             1,
			PassLogFile:      "stats",
			ForceKeyFrames:   "expr:gte(t,n_forced*2)",
			SAR:              "1:1",
			EncoderTimeBase:  "1/30000",
			FieldOrder:       "tt",
			InterlacedEncode: true,
		}},
	}

	def, _, err := NormalizeConfig(cfg)
	if err != nil {
		t.Fatalf("NormalizeConfig: %v", err)
	}

	// Locate the synthetic encoder node for the video stream.
	var videoEnc *graph.NodeDef
	var audioEnc *graph.NodeDef
	var asyncFilter *graph.NodeDef
	for i := range def.Nodes {
		n := &def.Nodes[i]
		switch {
		case strings.HasPrefix(n.ID, "__enc__out0_video"):
			videoEnc = n
		case strings.HasPrefix(n.ID, "__enc__out0_audio"):
			audioEnc = n
		case strings.HasPrefix(n.ID, "__async__"):
			asyncFilter = n
		}
	}
	if videoEnc == nil {
		t.Fatalf("synthetic video encoder not generated")
	}
	if audioEnc == nil {
		t.Fatalf("synthetic audio encoder not generated")
	}
	if asyncFilter == nil {
		t.Fatalf("audio_sync did not synthesize an aresample node")
	}

	// Filter-thread default propagated to user filter node.
	for _, n := range def.Nodes {
		if n.ID != "scale" {
			continue
		}
		if n.Internal.Filter == nil || n.Internal.Filter.Threads != 4 {
			t.Errorf("filter Internal.Filter.Threads = %+v, want 4", n.Internal.Filter)
		}
	}

	// Every shorthand row that should reach the video encoder, now
	// stamped on Internal.Encoder by NormalizeConfig (Milestone B).
	encInt := videoEnc.Internal.Encoder
	if encInt == nil {
		t.Fatalf("video encoder missing Internal.Encoder")
	}
	if encInt.FPSMode != "cfr" {
		t.Errorf("FPSMode = %q, want cfr", encInt.FPSMode)
	}
	if encInt.Pass != 1 {
		t.Errorf("Pass = %d, want 1", encInt.Pass)
	}
	if encInt.PassLogFile != "stats" {
		t.Errorf("PassLogFile = %q, want stats", encInt.PassLogFile)
	}
	if encInt.PassIndex != 0 {
		t.Errorf("PassIndex = %d, want 0", encInt.PassIndex)
	}
	if encInt.ForceKeyFrames != "expr:gte(t,n_forced*2)" {
		t.Errorf("ForceKeyFrames = %q", encInt.ForceKeyFrames)
	}
	if encInt.SAR != "1:1" {
		t.Errorf("SAR = %q, want 1:1", encInt.SAR)
	}
	if encInt.EncoderTimeBase != "1/30000" {
		t.Errorf("EncoderTimeBase = %q", encInt.EncoderTimeBase)
	}
	if encInt.FieldOrder != "tt" {
		t.Errorf("FieldOrder = %q, want tt", encInt.FieldOrder)
	}
	if !encInt.Interlaced {
		t.Errorf("Interlaced = false, want true")
	}
	// Generated provenance must be stamped on every synthetic node.
	if videoEnc.Internal.Generated == nil || videoEnc.Internal.Generated.By != "expandImplicitEncoders" {
		t.Errorf("video encoder Generated = %+v, want By=expandImplicitEncoders", videoEnc.Internal.Generated)
	}
	if asyncFilter.Internal.Generated == nil || asyncFilter.Internal.Generated.By != "spliceAudioSyncForOutputs" {
		t.Errorf("aresample Generated = %+v, want By=spliceAudioSyncForOutputs", asyncFilter.Internal.Generated)
	}

	// Audio encoder only carries codec; audio_sync lives on the
	// upstream aresample filter, not on the encoder.
	if got := audioEnc.Params["codec"]; got != "aac" {
		t.Errorf("audio encoder codec = %v, want aac", got)
	}
	if !strings.HasPrefix(asyncFilter.Filter, "aresample=async=1000") {
		t.Errorf("aresample filter = %q, want aresample=async=1000...", asyncFilter.Filter)
	}

	// Milestone B (B.5) regression: no __* sentinel keys must remain
	// in any node's Params. Internal carries the typed equivalents.
	for _, n := range def.Nodes {
		for k := range n.Params {
			if strings.HasPrefix(k, "__") {
				t.Errorf("node %q leaked sentinel param %q (should be on Internal)", n.ID, k)
			}
		}
	}
}

// TestNormalizeConfig_PerStreamEncoderOverride is the precedence
// regression: explicit Streams[i].Encoder must win over output-level
// CodecVideo / EncoderParamsVideo for the matching stream.
func TestNormalizeConfig_PerStreamEncoderOverride(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.0",
		Inputs:        []Input{{ID: "in0", URL: "in.mp4"}},
		Graph: GraphDef{
			Edges: []EdgeDef{
				{From: "in0:v:0", To: "out0", Type: "video"},
			},
		},
		Outputs: []Output{{
			ID: "out0", URL: "out.mp4",
			CodecVideo:         "libx264",
			EncoderParamsVideo: map[string]any{"b": "5M"},
			Streams: []StreamSpec{{
				Type:  "v",
				Index: 0,
				Encoder: &EncoderOverride{
					Codec:   "libx265",
					Options: map[string]any{"b": "2M"},
				},
			}},
		}},
	}
	def, _, err := NormalizeConfig(cfg)
	if err != nil {
		t.Fatalf("NormalizeConfig: %v", err)
	}
	for _, n := range def.Nodes {
		if !strings.HasPrefix(n.ID, "__enc__out0_video") {
			continue
		}
		if n.Params["codec"] != "libx265" {
			t.Errorf("override codec = %v, want libx265", n.Params["codec"])
		}
		if n.Params["b"] != "2M" {
			t.Errorf("override bitrate = %v, want 2M", n.Params["b"])
		}
		return
	}
	t.Fatalf("synthetic encoder not found in normalized graph")
}

// canonicalize returns a value suitable for reflect.DeepEqual that
// ignores map iteration order. We marshal to JSON with sorted keys.
func canonicalize(def *graph.Def) any {
	b, _ := json.Marshal(def)
	var v any
	_ = json.Unmarshal(b, &v)
	return v
}
