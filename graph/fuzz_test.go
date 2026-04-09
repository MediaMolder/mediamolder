package graph

import (
	"testing"
)

// FuzzBuildGraph exercises the graph builder with arbitrary edge definitions.
// Invariant: Build must never panic, regardless of input.
func FuzzBuildGraph(f *testing.F) {
	// Seed corpus.
	f.Add("src", "out", "scale", "filter", "src:v:0", "scale:default", "scale:default", "out:v", "video", "video")
	f.Add("in", "out", "", "", "in:v:0", "out:v", "", "", "video", "")
	f.Add("a", "b", "f", "filter", "a:v:0", "f:default", "f:default", "b:v", "audio", "audio")

	f.Fuzz(func(t *testing.T, inputID, outputID, nodeID, nodeType, e1From, e1To, e2From, e2To, e1Type, e2Type string) {
		def := &Def{
			Inputs:  []InputDef{{ID: inputID}},
			Outputs: []OutputDef{{ID: outputID}},
		}
		if nodeID != "" {
			def.Nodes = []NodeDef{{ID: nodeID, Type: nodeType, Filter: "test"}}
		}
		if e1From != "" && e1To != "" {
			def.Edges = append(def.Edges, EdgeDef{From: e1From, To: e1To, Type: e1Type})
		}
		if e2From != "" && e2To != "" {
			def.Edges = append(def.Edges, EdgeDef{From: e2From, To: e2To, Type: e2Type})
		}

		// Must not panic. Errors are expected and fine.
		g, err := Build(def)
		if err != nil {
			return
		}

		// If build succeeded, verify invariants.
		if len(g.Order) != len(g.Nodes) {
			t.Errorf("topological order length %d != node count %d", len(g.Order), len(g.Nodes))
		}
		for _, n := range g.Sources {
			if n.Kind != KindSource {
				t.Errorf("source %q has kind %s", n.ID, n.Kind)
			}
		}
		for _, n := range g.Sinks {
			if n.Kind != KindSink {
				t.Errorf("sink %q has kind %s", n.ID, n.Kind)
			}
		}
	})
}
