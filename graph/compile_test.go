// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package graph

import (
	"testing"
)

// ---------- Stage grouping tests ----------

func TestCompileLinearStages(t *testing.T) {
	// src → scale → out: three stages (depths 0, 1, 2).
	g, err := Build(simpleDef())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	plan, err := Compile(g)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(plan.Stages) != 3 {
		t.Fatalf("len(Stages) = %d, want 3", len(plan.Stages))
	}
	// Stage 0: src
	assertStageNodes(t, plan.Stages[0], "src")
	// Stage 1: scale
	assertStageNodes(t, plan.Stages[1], "scale")
	// Stage 2: out
	assertStageNodes(t, plan.Stages[2], "out")

	if len(plan.Warnings) != 0 {
		t.Errorf("expected no warnings, got %v", plan.Warnings)
	}
}

func TestCompileParallelStages(t *testing.T) {
	// Two sources feed separate filters that merge at a sink.
	//
	//   bg → scale_bg ─┐
	//                   ├→ out
	//   fg → scale_fg ─┘
	def := &Def{
		Inputs:  []InputDef{{ID: "bg"}, {ID: "fg"}},
		Outputs: []OutputDef{{ID: "out"}},
		Nodes: []NodeDef{
			{ID: "scale_bg", Type: "filter", Filter: "scale"},
			{ID: "scale_fg", Type: "filter", Filter: "scale"},
		},
		Edges: []EdgeDef{
			{From: "bg:v:0", To: "scale_bg:default", Type: "video"},
			{From: "fg:v:0", To: "scale_fg:default", Type: "video"},
			{From: "scale_bg:default", To: "out:v:0", Type: "video"},
			{From: "scale_fg:default", To: "out:v:1", Type: "video"},
		},
	}
	g, err := Build(def)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	plan, err := Compile(g)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	if len(plan.Stages) != 3 {
		t.Fatalf("len(Stages) = %d, want 3", len(plan.Stages))
	}
	// Stage 0: both sources (parallel).
	assertStageNodes(t, plan.Stages[0], "bg", "fg")
	// Stage 1: both filters (parallel — neither depends on the other).
	assertStageNodes(t, plan.Stages[1], "scale_bg", "scale_fg")
	// Stage 2: sink.
	assertStageNodes(t, plan.Stages[2], "out")
}

func TestCompileDiamondStages(t *testing.T) {
	// Diamond: src → split → {scale_hd, scale_sd} → merge → out
	//
	//          split ──→ scale_hd ──┐
	//   src ──┤                     ├──→ out
	//          split ──→ scale_sd ──┘
	def := &Def{
		Inputs:  []InputDef{{ID: "src"}},
		Outputs: []OutputDef{{ID: "out"}},
		Nodes: []NodeDef{
			{ID: "split", Type: "filter", Filter: "split"},
			{ID: "scale_hd", Type: "filter", Filter: "scale"},
			{ID: "scale_sd", Type: "filter", Filter: "scale"},
		},
		Edges: []EdgeDef{
			{From: "src:v:0", To: "split:default", Type: "video"},
			{From: "split:0", To: "scale_hd:default", Type: "video"},
			{From: "split:1", To: "scale_sd:default", Type: "video"},
			{From: "scale_hd:default", To: "out:v:0", Type: "video"},
			{From: "scale_sd:default", To: "out:v:1", Type: "video"},
		},
	}
	g, err := Build(def)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	plan, err := Compile(g)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	// src(0) → split(1) → {scale_hd, scale_sd}(2) → out(3)
	if len(plan.Stages) != 4 {
		t.Fatalf("len(Stages) = %d, want 4", len(plan.Stages))
	}
	assertStageNodes(t, plan.Stages[0], "src")
	assertStageNodes(t, plan.Stages[1], "split")
	assertStageNodes(t, plan.Stages[2], "scale_hd", "scale_sd")
	assertStageNodes(t, plan.Stages[3], "out")
}

// ---------- Dead-branch detection tests ----------

func TestCompileDeadBranch(t *testing.T) {
	// "orphan" filter is connected to src but not to any output.
	//
	//   src ──→ scale ──→ out
	//    └────→ orphan           (dead branch)
	def := &Def{
		Inputs:  []InputDef{{ID: "src"}},
		Outputs: []OutputDef{{ID: "out"}},
		Nodes: []NodeDef{
			{ID: "scale", Type: "filter", Filter: "scale"},
			{ID: "orphan", Type: "filter", Filter: "null"},
		},
		Edges: []EdgeDef{
			{From: "src:v:0", To: "scale:default", Type: "video"},
			{From: "scale:default", To: "out:v", Type: "video"},
			{From: "src:v:0", To: "orphan:default", Type: "video"},
		},
	}
	g, err := Build(def)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	plan, err := Compile(g)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	warnings := filterWarnings(plan.Warnings, WarnDeadNode)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 dead-node warning, got %d: %v", len(warnings), plan.Warnings)
	}
	if warnings[0].NodeID != "orphan" {
		t.Errorf("dead-node warning for %q, want orphan", warnings[0].NodeID)
	}
}

func TestCompileDeadChain(t *testing.T) {
	// A chain of nodes (a → b → c) not connected to any sink.
	//
	//   src ──→ out     (direct passthrough)
	//    └────→ a → b → c       (dead chain)
	def := &Def{
		Inputs:  []InputDef{{ID: "src"}},
		Outputs: []OutputDef{{ID: "out"}},
		Nodes: []NodeDef{
			{ID: "a", Type: "filter", Filter: "null"},
			{ID: "b", Type: "filter", Filter: "null"},
			{ID: "c", Type: "filter", Filter: "null"},
		},
		Edges: []EdgeDef{
			{From: "src:v:0", To: "out:v", Type: "video"},
			{From: "src:v:0", To: "a:default", Type: "video"},
			{From: "a:default", To: "b:default", Type: "video"},
			{From: "b:default", To: "c:default", Type: "video"},
		},
	}
	g, err := Build(def)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	plan, err := Compile(g)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	warnings := filterWarnings(plan.Warnings, WarnDeadNode)
	if len(warnings) != 3 {
		t.Fatalf("expected 3 dead-node warnings, got %d: %v", len(warnings), warnings)
	}
	ids := make(map[string]bool)
	for _, w := range warnings {
		ids[w.NodeID] = true
	}
	for _, id := range []string{"a", "b", "c"} {
		if !ids[id] {
			t.Errorf("missing dead-node warning for %q", id)
		}
	}
}

func TestCompileNoDeadBranch(t *testing.T) {
	// All nodes are live — no warnings expected.
	g, err := Build(simpleDef())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	plan, err := Compile(g)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	warnings := filterWarnings(plan.Warnings, WarnDeadNode)
	if len(warnings) != 0 {
		t.Errorf("expected no dead-node warnings, got %v", warnings)
	}
}

// ---------- Disconnected source tests ----------

func TestCompileDisconnectedSource(t *testing.T) {
	// "unused" source has no outbound edges.
	def := &Def{
		Inputs:  []InputDef{{ID: "src"}, {ID: "unused"}},
		Outputs: []OutputDef{{ID: "out"}},
		Edges: []EdgeDef{
			{From: "src:v:0", To: "out:v", Type: "video"},
		},
	}
	g, err := Build(def)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	plan, err := Compile(g)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	disconnected := filterWarnings(plan.Warnings, WarnDisconnectedSource)
	if len(disconnected) != 1 {
		t.Fatalf("expected 1 disconnected-source warning, got %d", len(disconnected))
	}
	if disconnected[0].NodeID != "unused" {
		t.Errorf("warning for %q, want unused", disconnected[0].NodeID)
	}

	// "unused" should also be flagged as dead (no path to sink).
	dead := filterWarnings(plan.Warnings, WarnDeadNode)
	if len(dead) != 1 {
		t.Fatalf("expected 1 dead-node warning, got %d", len(dead))
	}
}

// ---------- Error cases ----------

func TestCompileNilGraph(t *testing.T) {
	_, err := Compile(nil)
	if err == nil {
		t.Fatal("expected error for nil graph")
	}
}

func TestCompileEmptyGraph(t *testing.T) {
	g := &Graph{Nodes: make(map[string]*Node)}
	_, err := Compile(g)
	if err == nil {
		t.Fatal("expected error for empty graph")
	}
}

// ---------- Plan structure tests ----------

func TestCompileStageDeterminism(t *testing.T) {
	// Run Compile multiple times and verify stages are identical.
	g, err := Build(simpleDef())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	var prev []string
	for i := 0; i < 20; i++ {
		plan, err := Compile(g)
		if err != nil {
			t.Fatalf("Compile[%d]: %v", i, err)
		}
		var ids []string
		for _, stage := range plan.Stages {
			for _, n := range stage.Nodes {
				ids = append(ids, n.ID)
			}
		}
		if prev != nil {
			for j := range ids {
				if ids[j] != prev[j] {
					t.Fatalf("non-deterministic at iteration %d: %v vs %v", i, ids, prev)
				}
			}
		}
		prev = ids
	}
}

func TestCompileComplexGraph(t *testing.T) {
	// The full complex example from graph_test.go:
	//   bg ──→ overlay ──→ split ──→ scale_hd ──→ hd
	//   fg ──┘                └────→ scale_sd ──→ sd
	def := &Def{
		Inputs:  []InputDef{{ID: "bg"}, {ID: "fg"}},
		Outputs: []OutputDef{{ID: "hd"}, {ID: "sd"}},
		Nodes: []NodeDef{
			{ID: "overlay", Type: "filter", Filter: "overlay"},
			{ID: "split", Type: "filter", Filter: "split"},
			{ID: "scale_hd", Type: "filter", Filter: "scale"},
			{ID: "scale_sd", Type: "filter", Filter: "scale"},
		},
		Edges: []EdgeDef{
			{From: "bg:v:0", To: "overlay:0", Type: "video"},
			{From: "fg:v:0", To: "overlay:1", Type: "video"},
			{From: "overlay:default", To: "split:default", Type: "video"},
			{From: "split:0", To: "scale_hd:default", Type: "video"},
			{From: "split:1", To: "scale_sd:default", Type: "video"},
			{From: "scale_hd:default", To: "hd:v", Type: "video"},
			{From: "scale_sd:default", To: "sd:v", Type: "video"},
		},
	}
	g, err := Build(def)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	plan, err := Compile(g)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	// Depths: bg=0, fg=0, overlay=1, split=2, scale_hd=3, scale_sd=3, hd=4, sd=4
	if len(plan.Stages) != 5 {
		t.Fatalf("len(Stages) = %d, want 5", len(plan.Stages))
	}
	assertStageNodes(t, plan.Stages[0], "bg", "fg")
	assertStageNodes(t, plan.Stages[1], "overlay")
	assertStageNodes(t, plan.Stages[2], "split")
	assertStageNodes(t, plan.Stages[3], "scale_hd", "scale_sd")
	assertStageNodes(t, plan.Stages[4], "hd", "sd")

	if len(plan.Warnings) != 0 {
		t.Errorf("expected no warnings for fully connected graph, got %v", plan.Warnings)
	}
}

// ---------- test helpers ----------

func assertStageNodes(t *testing.T, stage Stage, expectedIDs ...string) {
	t.Helper()
	if len(stage.Nodes) != len(expectedIDs) {
		t.Errorf("stage %d: got %d nodes %v, want %d %v",
			stage.Depth, len(stage.Nodes), stageNodeIDs(stage), len(expectedIDs), expectedIDs)
		return
	}
	got := stageNodeIDs(stage)
	for i, want := range expectedIDs {
		if got[i] != want {
			t.Errorf("stage %d[%d] = %q, want %q (full: %v)", stage.Depth, i, got[i], want, got)
		}
	}
}

func stageNodeIDs(s Stage) []string {
	ids := make([]string, len(s.Nodes))
	for i, n := range s.Nodes {
		ids[i] = n.ID
	}
	return ids
}

func filterWarnings(ws []Warning, code WarningCode) []Warning {
	var out []Warning
	for _, w := range ws {
		if w.Code == code {
			out = append(out, w)
		}
	}
	return out
}
