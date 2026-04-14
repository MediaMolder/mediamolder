// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package graph

import (
	"strings"
	"testing"
)

func simpleDef() *Def {
	return &Def{
		Inputs:  []InputDef{{ID: "src"}},
		Outputs: []OutputDef{{ID: "out"}},
		Nodes: []NodeDef{
			{ID: "scale", Type: "filter", Filter: "scale", Params: map[string]any{"w": 1280, "h": 720}},
		},
		Edges: []EdgeDef{
			{From: "src:v:0", To: "scale:default", Type: "video"},
			{From: "scale:default", To: "out:v", Type: "video"},
		},
	}
}

func TestBuildSimpleLinear(t *testing.T) {
	g, err := Build(simpleDef())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(g.Nodes) != 3 {
		t.Fatalf("len(Nodes) = %d, want 3", len(g.Nodes))
	}
	if len(g.Edges) != 2 {
		t.Fatalf("len(Edges) = %d, want 2", len(g.Edges))
	}
	if len(g.Sources) != 1 || g.Sources[0].ID != "src" {
		t.Errorf("Sources = %v, want [src]", nodeIDs(g.Sources))
	}
	if len(g.Sinks) != 1 || g.Sinks[0].ID != "out" {
		t.Errorf("Sinks = %v, want [out]", nodeIDs(g.Sinks))
	}
	order := nodeIDs(g.Order)
	srcIdx := indexOf(order, "src")
	scaleIdx := indexOf(order, "scale")
	outIdx := indexOf(order, "out")
	if srcIdx >= scaleIdx || scaleIdx >= outIdx {
		t.Errorf("Order = %v; expected src < scale < out", order)
	}
	scale := g.NodeByID("scale")
	if len(scale.Inbound) != 1 {
		t.Fatalf("scale.Inbound = %d, want 1", len(scale.Inbound))
	}
	if scale.Inbound[0].From.ID != "src" {
		t.Errorf("scale inbound from %q, want src", scale.Inbound[0].From.ID)
	}
	if scale.Inbound[0].FromPort != "v:0" {
		t.Errorf("scale inbound fromPort %q, want v:0", scale.Inbound[0].FromPort)
	}
	if scale.Outbound[0].To.ID != "out" {
		t.Errorf("scale outbound to %q, want out", scale.Outbound[0].To.ID)
	}
}

func TestBuildPassthrough(t *testing.T) {
	def := &Def{
		Inputs:  []InputDef{{ID: "in"}},
		Outputs: []OutputDef{{ID: "out"}},
		Edges: []EdgeDef{
			{From: "in:v:0", To: "out:v", Type: "video"},
		},
	}
	g, err := Build(def)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(g.Nodes) != 2 {
		t.Fatalf("len(Nodes) = %d, want 2", len(g.Nodes))
	}
	if len(g.Order) != 2 {
		t.Fatalf("len(Order) = %d, want 2", len(g.Order))
	}
}

func TestBuildMultiInput(t *testing.T) {
	def := &Def{
		Inputs:  []InputDef{{ID: "bg"}, {ID: "fg"}},
		Outputs: []OutputDef{{ID: "out"}},
		Nodes: []NodeDef{
			{ID: "overlay", Type: "filter", Filter: "overlay", Params: map[string]any{"x": 10, "y": 10}},
		},
		Edges: []EdgeDef{
			{From: "bg:v:0", To: "overlay:0", Type: "video"},
			{From: "fg:v:0", To: "overlay:1", Type: "video"},
			{From: "overlay:default", To: "out:v", Type: "video"},
		},
	}
	g, err := Build(def)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(g.Sources) != 2 {
		t.Errorf("len(Sources) = %d, want 2", len(g.Sources))
	}
	overlay := g.NodeByID("overlay")
	if len(overlay.Inbound) != 2 {
		t.Errorf("overlay.Inbound = %d, want 2", len(overlay.Inbound))
	}
	preds := overlay.Predecessors()
	if len(preds) != 2 {
		t.Errorf("overlay predecessors = %d, want 2", len(preds))
	}
}

func TestBuildMultiOutput(t *testing.T) {
	def := &Def{
		Inputs:  []InputDef{{ID: "src"}},
		Outputs: []OutputDef{{ID: "hls"}, {ID: "dash"}},
		Nodes: []NodeDef{
			{ID: "split", Type: "filter", Filter: "split"},
		},
		Edges: []EdgeDef{
			{From: "src:v:0", To: "split:default", Type: "video"},
			{From: "split:0", To: "hls:v", Type: "video"},
			{From: "split:1", To: "dash:v", Type: "video"},
		},
	}
	g, err := Build(def)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(g.Sinks) != 2 {
		t.Errorf("len(Sinks) = %d, want 2", len(g.Sinks))
	}
	split := g.NodeByID("split")
	if len(split.Outbound) != 2 {
		t.Errorf("split.Outbound = %d, want 2", len(split.Outbound))
	}
	succs := split.Successors()
	if len(succs) != 2 {
		t.Errorf("split successors = %d, want 2", len(succs))
	}
}

func TestBuildComplex(t *testing.T) {
	def := &Def{
		Inputs:  []InputDef{{ID: "bg"}, {ID: "fg"}},
		Outputs: []OutputDef{{ID: "hd"}, {ID: "sd"}},
		Nodes: []NodeDef{
			{ID: "overlay", Type: "filter", Filter: "overlay"},
			{ID: "split", Type: "filter", Filter: "split"},
			{ID: "scale_hd", Type: "filter", Filter: "scale", Params: map[string]any{"w": 1920, "h": 1080}},
			{ID: "scale_sd", Type: "filter", Filter: "scale", Params: map[string]any{"w": 640, "h": 360}},
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
	if len(g.Nodes) != 8 {
		t.Fatalf("len(Nodes) = %d, want 8", len(g.Nodes))
	}
	if len(g.Edges) != 7 {
		t.Fatalf("len(Edges) = %d, want 7", len(g.Edges))
	}
	order := nodeIDs(g.Order)
	assertBefore(t, order, "bg", "overlay")
	assertBefore(t, order, "fg", "overlay")
	assertBefore(t, order, "overlay", "split")
	assertBefore(t, order, "split", "scale_hd")
	assertBefore(t, order, "split", "scale_sd")
	assertBefore(t, order, "scale_hd", "hd")
	assertBefore(t, order, "scale_sd", "sd")
}

func TestBuildAudioVideoMixed(t *testing.T) {
	def := &Def{
		Inputs:  []InputDef{{ID: "src"}},
		Outputs: []OutputDef{{ID: "out"}},
		Nodes: []NodeDef{
			{ID: "scale", Type: "filter", Filter: "scale"},
			{ID: "aresample", Type: "filter", Filter: "aresample"},
		},
		Edges: []EdgeDef{
			{From: "src:v:0", To: "scale:default", Type: "video"},
			{From: "src:a:0", To: "aresample:default", Type: "audio"},
			{From: "scale:default", To: "out:v", Type: "video"},
			{From: "aresample:default", To: "out:a", Type: "audio"},
		},
	}
	g, err := Build(def)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(g.Edges) != 4 {
		t.Fatalf("len(Edges) = %d, want 4", len(g.Edges))
	}
	for _, e := range g.Edges {
		n := e.From.ID
		if n == "scale" || (n == "src" && strings.HasPrefix(e.FromPort, "v")) {
			if e.Type != PortVideo {
				t.Errorf("edge from %s:%s type = %s, want video", n, e.FromPort, e.Type)
			}
		}
	}
}

func TestBuildCycleDetected(t *testing.T) {
	def := &Def{
		Inputs:  []InputDef{{ID: "src"}},
		Outputs: []OutputDef{{ID: "out"}},
		Nodes: []NodeDef{
			{ID: "a", Type: "filter", Filter: "null"},
			{ID: "b", Type: "filter", Filter: "null"},
		},
		Edges: []EdgeDef{
			{From: "src:v:0", To: "a:default", Type: "video"},
			{From: "a:default", To: "b:default", Type: "video"},
			{From: "b:default", To: "a:default", Type: "video"},
			{From: "b:default", To: "out:v", Type: "video"},
		},
	}
	_, err := Build(def)
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error %q doesn't mention cycle", err.Error())
	}
}

func TestBuildSelfLoop(t *testing.T) {
	def := &Def{
		Inputs:  []InputDef{{ID: "src"}},
		Outputs: []OutputDef{{ID: "out"}},
		Nodes: []NodeDef{
			{ID: "loop", Type: "filter", Filter: "null"},
		},
		Edges: []EdgeDef{
			{From: "src:v:0", To: "loop:default", Type: "video"},
			{From: "loop:default", To: "loop:in", Type: "video"},
		},
	}
	_, err := Build(def)
	if err == nil {
		t.Fatal("expected self-loop error, got nil")
	}
	if !strings.Contains(err.Error(), "self-loop") {
		t.Errorf("error %q doesn't mention self-loop", err.Error())
	}
}

func TestBuildUnknownNode(t *testing.T) {
	def := &Def{
		Inputs:  []InputDef{{ID: "src"}},
		Outputs: []OutputDef{{ID: "out"}},
		Edges: []EdgeDef{
			{From: "src:v:0", To: "ghost:default", Type: "video"},
		},
	}
	_, err := Build(def)
	if err == nil {
		t.Fatal("expected unknown node error")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error %q doesn't mention unknown node", err.Error())
	}
}

func TestBuildDuplicateNodeID(t *testing.T) {
	def := &Def{
		Inputs:  []InputDef{{ID: "src"}},
		Outputs: []OutputDef{{ID: "src"}},
	}
	_, err := Build(def)
	if err == nil {
		t.Fatal("expected duplicate id error")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error %q doesn't mention duplicate", err.Error())
	}
}

func TestBuildEmptyRef(t *testing.T) {
	def := &Def{
		Inputs:  []InputDef{{ID: "src"}},
		Outputs: []OutputDef{{ID: "out"}},
		Edges: []EdgeDef{
			{From: "", To: "out:v", Type: "video"},
		},
	}
	_, err := Build(def)
	if err == nil {
		t.Fatal("expected empty reference error")
	}
}

func TestBuildInvalidNodeType(t *testing.T) {
	def := &Def{
		Inputs:  []InputDef{{ID: "src"}},
		Outputs: []OutputDef{{ID: "out"}},
		Nodes: []NodeDef{
			{ID: "bad", Type: "unknown_type"},
		},
	}
	_, err := Build(def)
	if err == nil {
		t.Fatal("expected unknown type error")
	}
}

func TestBuildNoEdges(t *testing.T) {
	def := &Def{
		Inputs:  []InputDef{{ID: "src"}},
		Outputs: []OutputDef{{ID: "out"}},
	}
	g, err := Build(def)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(g.Order) != 2 {
		t.Errorf("len(Order) = %d, want 2", len(g.Order))
	}
}

func TestBuildEncoderNode(t *testing.T) {
	def := &Def{
		Inputs:  []InputDef{{ID: "src"}},
		Outputs: []OutputDef{{ID: "out"}},
		Nodes: []NodeDef{
			{ID: "enc", Type: "encoder", Params: map[string]any{"codec": "libx264"}},
		},
		Edges: []EdgeDef{
			{From: "src:v:0", To: "enc:default", Type: "video"},
			{From: "enc:default", To: "out:v", Type: "video"},
		},
	}
	g, err := Build(def)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	enc := g.NodeByID("enc")
	if enc.Kind != KindEncoder {
		t.Errorf("enc.Kind = %v, want KindEncoder", enc.Kind)
	}
}

func TestNodeKindString(t *testing.T) {
	tests := []struct {
		kind NodeKind
		want string
	}{
		{KindSource, "source"},
		{KindFilter, "filter"},
		{KindEncoder, "encoder"},
		{KindSink, "sink"},
		{NodeKind(99), "NodeKind(99)"},
	}
	for _, tt := range tests {
		if got := tt.kind.String(); got != tt.want {
			t.Errorf("%d.String() = %q, want %q", int(tt.kind), got, tt.want)
		}
	}
}

func TestParseRef(t *testing.T) {
	tests := []struct {
		ref      string
		wantNode string
		wantPort string
		wantErr  bool
	}{
		{"src", "src", "default", false},
		{"src:v", "src", "v", false},
		{"src:v:0", "src", "v:0", false},
		{"", "", "", true},
		{"src:", "", "", true},
		{"src:v:", "", "", true},
	}
	for _, tt := range tests {
		nodeID, port, err := parseRef(tt.ref)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseRef(%q): expected error", tt.ref)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseRef(%q): %v", tt.ref, err)
			continue
		}
		if nodeID != tt.wantNode || port != tt.wantPort {
			t.Errorf("parseRef(%q) = (%q, %q), want (%q, %q)",
				tt.ref, nodeID, port, tt.wantNode, tt.wantPort)
		}
	}
}

func nodeIDs(nodes []*Node) []string {
	ids := make([]string, len(nodes))
	for i, n := range nodes {
		ids[i] = n.ID
	}
	return ids
}

func indexOf(s []string, val string) int {
	for i, v := range s {
		if v == val {
			return i
		}
	}
	return -1
}

func assertBefore(t *testing.T, order []string, a, b string) {
	t.Helper()
	ai := indexOf(order, a)
	bi := indexOf(order, b)
	if ai < 0 {
		t.Errorf("%q not found in order %v", a, order)
		return
	}
	if bi < 0 {
		t.Errorf("%q not found in order %v", b, order)
		return
	}
	if ai >= bi {
		t.Errorf("expected %q before %q in order %v", a, b, order)
	}
}
