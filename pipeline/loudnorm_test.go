// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/graph"
)

func TestValidateLoudnormPassRange(t *testing.T) {
	cases := []struct {
		name    string
		pass    int
		wantErr bool
	}{
		{"zero ok", 0, false},
		{"one ok", 1, false},
		{"two ok", 2, false},
		{"negative rejected", -1, true},
		{"three rejected", 3, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{
				SchemaVersion: "1.0",
				Inputs:        []Input{{ID: "in0", URL: "in.mp4"}},
				Outputs:       []Output{{ID: "out0", URL: "out.mp4", LoudnormPass: tc.pass}},
				Graph:         GraphDef{Edges: []EdgeDef{{From: "in0:a:0", To: "out0:a", Type: "audio"}}},
			}
			err := validate(cfg)
			if tc.wantErr && err == nil {
				t.Fatalf("expected validation error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
		})
	}
}

func TestValidateLoudnormStatsRequiresPass(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.0",
		Inputs:        []Input{{ID: "in0", URL: "in.mp4"}},
		Outputs:       []Output{{ID: "out0", URL: "out.mp4", LoudnormStatsFile: "x"}},
		Graph:         GraphDef{Edges: []EdgeDef{{From: "in0:a:0", To: "out0:a", Type: "audio"}}},
	}
	err := validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "loudnorm_statsfile is only valid") {
		t.Fatalf("expected loudnorm_statsfile gating error, got %v", err)
	}
}

func TestValidateLoudnormConflictingPasses(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.0",
		Inputs:        []Input{{ID: "in0", URL: "in.mp4"}},
		Outputs: []Output{
			{ID: "a", URL: "a.mp4", LoudnormPass: 1},
			{ID: "b", URL: "b.mp4", LoudnormPass: 2},
		},
		Graph: GraphDef{Edges: []EdgeDef{
			{From: "in0:a:0", To: "a:a", Type: "audio"},
			{From: "in0:a:0", To: "b:a", Type: "audio"},
		}},
	}
	err := validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "conflicting loudnorm_pass") {
		t.Fatalf("expected conflicting loudnorm_pass error, got %v", err)
	}
}

func TestApplyLoudnormShuttlePass1Injects(t *testing.T) {
	cfg := &Config{
		Outputs: []Output{{ID: "out0", LoudnormPass: 1, LoudnormStatsFile: "/tmp/foo"}},
	}
	def := &graph.Def{
		Nodes: []graph.NodeDef{
			{ID: "loudnorm0", Type: "filter", Filter: "loudnorm", Params: map[string]any{"I": -16}},
		},
	}
	if err := applyLoudnormShuttle(cfg, def); err != nil {
		t.Fatalf("applyLoudnormShuttle: %v", err)
	}
	p := def.Nodes[0].Params
	if p["print_format"] != "json" {
		t.Errorf("print_format = %v, want json", p["print_format"])
	}
	if got := p["stats_file"]; got != "/tmp/foo-0.json" {
		t.Errorf("stats_file = %v, want /tmp/foo-0.json", got)
	}
	fi := def.Nodes[0].Internal.Filter
	if fi == nil || fi.LoudnormPass != 1 {
		t.Errorf("Internal.Filter.LoudnormPass = %+v, want 1", fi)
	}
}

func TestApplyLoudnormShuttleDefaultPrefix(t *testing.T) {
	cfg := &Config{Outputs: []Output{{ID: "out0", LoudnormPass: 1}}}
	def := &graph.Def{
		Nodes: []graph.NodeDef{
			{ID: "loudnorm0", Type: "filter", Filter: "loudnorm"},
			{ID: "loudnorm1", Type: "filter", Filter: "loudnorm"},
		},
	}
	if err := applyLoudnormShuttle(cfg, def); err != nil {
		t.Fatalf("applyLoudnormShuttle: %v", err)
	}
	if got := def.Nodes[0].Params["stats_file"]; got != "mm-loudnorm-0.json" {
		t.Errorf("node 0 stats_file = %v", got)
	}
	if got := def.Nodes[1].Params["stats_file"]; got != "mm-loudnorm-1.json" {
		t.Errorf("node 1 stats_file = %v", got)
	}
}

func TestApplyLoudnormShuttleNoOpWhenZero(t *testing.T) {
	cfg := &Config{Outputs: []Output{{ID: "out0"}}}
	def := &graph.Def{
		Nodes: []graph.NodeDef{
			{ID: "loudnorm0", Type: "filter", Filter: "loudnorm", Params: map[string]any{"I": -16}},
		},
	}
	if err := applyLoudnormShuttle(cfg, def); err != nil {
		t.Fatalf("applyLoudnormShuttle: %v", err)
	}
	if def.Nodes[0].Internal.Filter != nil && def.Nodes[0].Internal.Filter.LoudnormPass != 0 {
		t.Errorf("expected no LoudnormPass marker on no-op, got %d", def.Nodes[0].Internal.Filter.LoudnormPass)
	}
	if _, ok := def.Nodes[0].Params["stats_file"]; ok {
		t.Errorf("expected no stats_file injection on no-op")
	}
}

func TestLoadLoudnormMeasurements(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "loudnorm.json")
	// Mirror the literal `"%.2f"` string-encoded layout from
	// libavfilter/af_loudnorm.c::uninit (lines 877-885).
	body := `{
		"input_i": "-23.45",
		"input_tp": "-2.10",
		"input_lra": "8.20",
		"input_thresh": "-33.40",
		"output_i": "-16.10",
		"output_tp": "-1.51",
		"output_lra": "5.30",
		"output_thresh": "-26.10",
		"normalization_type": "dynamic",
		"target_offset": "0.05"
	}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := loadLoudnormMeasurements(path)
	if err != nil {
		t.Fatalf("loadLoudnormMeasurements: %v", err)
	}
	want := map[string]float64{
		"measured_I":      -23.45,
		"measured_TP":     -2.10,
		"measured_LRA":    8.20,
		"measured_thresh": -33.40,
		"offset":          0.05,
	}
	for k, v := range want {
		gv, ok := got[k].(float64)
		if !ok {
			t.Errorf("%s: not float64 (%T)", k, got[k])
			continue
		}
		if gv != v {
			t.Errorf("%s = %v, want %v", k, gv, v)
		}
	}
}

func TestLoadLoudnormMeasurementsErrors(t *testing.T) {
	if _, err := loadLoudnormMeasurements("/no/such/file"); err == nil {
		t.Errorf("expected error for missing file")
	}
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadLoudnormMeasurements(bad); err == nil {
		t.Errorf("expected parse error")
	}
	nonNumeric := filepath.Join(dir, "nan.json")
	if err := os.WriteFile(nonNumeric, []byte(`{"input_i":"abc","input_tp":"0","input_lra":"0","input_thresh":"0","target_offset":"0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadLoudnormMeasurements(nonNumeric); err == nil {
		t.Errorf("expected float-parse error")
	}
}

func TestBuildFilterSpecStripsReservedKeys(t *testing.T) {
	// `__` prefixed keys remain reserved at the buildFilterSpec layer
	// as a defensive guard even though NormalizeConfig (Milestone B)
	// no longer writes any. The reserved-prefix check must keep
	// stripping them so user-authored Params with such keys (e.g.
	// from a hand-edited GUI export) can never reach libavfilter.
	spec := buildFilterSpec(NodeDef{
		Filter: "loudnorm",
		Params: map[string]any{
			"I":                -16,
			"__loudnorm_pass":  2,
			"__loudnorm_stats": "/tmp/foo.json",
			"measured_I":       -23.45,
		},
	})
	if strings.Contains(spec, "__loudnorm") {
		t.Errorf("filter spec leaked reserved key: %s", spec)
	}
	if !strings.Contains(spec, "I=-16") || !strings.Contains(spec, "measured_I=-23.45") {
		t.Errorf("filter spec missing expected params: %s", spec)
	}
}
