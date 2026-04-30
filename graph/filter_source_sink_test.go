// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package graph

import (
	"strings"
	"testing"
)

// Wave 7 #36a — KindFilterSource / KindFilterSink enum + Build validation.

func TestParseNodeKindFilterSourceSink(t *testing.T) {
	for _, tc := range []struct {
		s    string
		want NodeKind
	}{
		{"filter_source", KindFilterSource},
		{"filter_sink", KindFilterSink},
	} {
		got, err := parseNodeKind(tc.s)
		if err != nil {
			t.Errorf("parseNodeKind(%q): %v", tc.s, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseNodeKind(%q) = %v, want %v", tc.s, got, tc.want)
		}
		if got.String() != tc.s {
			t.Errorf("%v.String() = %q, want %q", tc.want, got.String(), tc.s)
		}
	}
}

func TestBuildClassifiesFilterSourceAsSource(t *testing.T) {
	def := &Def{
		Outputs: []OutputDef{{ID: "out"}},
		Nodes: []NodeDef{
			{ID: "color", Type: "filter_source", Filter: "color"},
		},
		Edges: []EdgeDef{
			{From: "color:default", To: "out:v", Type: "video"},
		},
	}
	g, err := Build(def)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(g.Sources) != 1 || g.Sources[0].ID != "color" {
		t.Errorf("Sources = %v, want [color]", nodeIDs(g.Sources))
	}
}

func TestBuildClassifiesFilterSinkAsSink(t *testing.T) {
	def := &Def{
		Inputs: []InputDef{{ID: "src"}},
		Nodes: []NodeDef{
			{ID: "drain", Type: "filter_sink", Filter: "nullsink"},
		},
		Edges: []EdgeDef{
			{From: "src:v:0", To: "drain:default", Type: "video"},
		},
	}
	g, err := Build(def)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(g.Sinks) != 1 || g.Sinks[0].ID != "drain" {
		t.Errorf("Sinks = %v, want [drain]", nodeIDs(g.Sinks))
	}
}

func TestBuildRejectsInboundEdgeIntoFilterSource(t *testing.T) {
	def := &Def{
		Inputs:  []InputDef{{ID: "src"}},
		Outputs: []OutputDef{{ID: "out"}},
		Nodes: []NodeDef{
			{ID: "color", Type: "filter_source", Filter: "color"},
		},
		Edges: []EdgeDef{
			{From: "src:v:0", To: "color:default", Type: "video"},
			{From: "color:default", To: "out:v", Type: "video"},
		},
	}
	_, err := Build(def)
	if err == nil {
		t.Fatal("Build: expected error rejecting inbound into filter_source")
	}
	if !strings.Contains(err.Error(), "filter_source") {
		t.Errorf("error = %q, want mention of filter_source", err.Error())
	}
}

func TestBuildRejectsOutboundEdgeFromFilterSink(t *testing.T) {
	def := &Def{
		Inputs:  []InputDef{{ID: "src"}},
		Outputs: []OutputDef{{ID: "out"}},
		Nodes: []NodeDef{
			{ID: "drain", Type: "filter_sink", Filter: "nullsink"},
		},
		Edges: []EdgeDef{
			{From: "src:v:0", To: "drain:default", Type: "video"},
			{From: "drain:default", To: "out:v", Type: "video"},
		},
	}
	_, err := Build(def)
	if err == nil {
		t.Fatal("Build: expected error rejecting outbound from filter_sink")
	}
	if !strings.Contains(err.Error(), "filter_sink") {
		t.Errorf("error = %q, want mention of filter_sink", err.Error())
	}
}
