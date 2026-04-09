package graph

import (
	"testing"

	"pgregory.net/rapid"
)

// TestPropertyBuildNoPanic verifies that Build never panics on randomly
// generated graph topologies — it must either succeed or return an error.
func TestPropertyBuildNoPanic(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		numInputs := rapid.IntRange(0, 5).Draw(t, "numInputs")
		numOutputs := rapid.IntRange(0, 5).Draw(t, "numOutputs")
		numNodes := rapid.IntRange(0, 10).Draw(t, "numNodes")
		numEdges := rapid.IntRange(0, 15).Draw(t, "numEdges")

		def := &Def{}

		allIDs := make([]string, 0, numInputs+numOutputs+numNodes)
		for i := 0; i < numInputs; i++ {
			id := rapid.StringMatching(`^[a-z]{1,8}$`).Draw(t, "inputID")
			id = "i_" + id
			def.Inputs = append(def.Inputs, InputDef{ID: id})
			allIDs = append(allIDs, id)
		}
		for i := 0; i < numOutputs; i++ {
			id := rapid.StringMatching(`^[a-z]{1,8}$`).Draw(t, "outputID")
			id = "o_" + id
			def.Outputs = append(def.Outputs, OutputDef{ID: id})
			allIDs = append(allIDs, id)
		}

		nodeTypes := []string{"filter", "encoder", "source", "sink", "bogus"}
		for i := 0; i < numNodes; i++ {
			id := rapid.StringMatching(`^[a-z]{1,8}$`).Draw(t, "nodeID")
			id = "n_" + id
			nodeType := rapid.SampledFrom(nodeTypes).Draw(t, "nodeType")
			def.Nodes = append(def.Nodes, NodeDef{ID: id, Type: nodeType, Filter: "test"})
			allIDs = append(allIDs, id)
		}

		if len(allIDs) == 0 {
			allIDs = append(allIDs, "dummy")
		}

		portSuffixes := []string{"", ":default", ":v:0", ":a:0", ":s:0"}
		edgeTypes := []string{"video", "audio", "subtitle", "data", ""}

		for i := 0; i < numEdges; i++ {
			fromID := rapid.SampledFrom(allIDs).Draw(t, "edgeFrom")
			toID := rapid.SampledFrom(allIDs).Draw(t, "edgeTo")
			fromSuffix := rapid.SampledFrom(portSuffixes).Draw(t, "fromSuffix")
			toSuffix := rapid.SampledFrom(portSuffixes).Draw(t, "toSuffix")
			edgeType := rapid.SampledFrom(edgeTypes).Draw(t, "edgeType")
			def.Edges = append(def.Edges, EdgeDef{
				From: fromID + fromSuffix,
				To:   toID + toSuffix,
				Type: edgeType,
			})
		}

		// Must not panic.
		g, err := Build(def)
		if err != nil {
			return
		}

		// Invariants on a successful build.
		if len(g.Order) != len(g.Nodes) {
			t.Fatalf("topo order length %d != nodes %d", len(g.Order), len(g.Nodes))
		}
		// Verify topological ordering: for every edge, from appears before to.
		orderIdx := make(map[string]int, len(g.Order))
		for i, n := range g.Order {
			orderIdx[n.ID] = i
		}
		for _, e := range g.Edges {
			fi := orderIdx[e.From.ID]
			ti := orderIdx[e.To.ID]
			if fi >= ti {
				t.Fatalf("edge %s→%s violates topo order (%d >= %d)", e.From.ID, e.To.ID, fi, ti)
			}
		}
	})
}

// TestPropertyBuildCycleDetection verifies that Build correctly rejects
// graphs that contain cycles.
func TestPropertyBuildCycleDetection(t *testing.T) {
	// Build a graph with a forced cycle: A→B→C→A
	_, err := Build(&Def{
		Inputs:  []InputDef{{ID: "ext"}},
		Outputs: []OutputDef{{ID: "sink"}},
		Nodes: []NodeDef{
			{ID: "a", Type: "filter", Filter: "f"},
			{ID: "b", Type: "filter", Filter: "f"},
			{ID: "c", Type: "filter", Filter: "f"},
		},
		Edges: []EdgeDef{
			{From: "ext:v:0", To: "a:default", Type: "video"},
			{From: "a:default", To: "b:default", Type: "video"},
			{From: "b:default", To: "c:default", Type: "video"},
			{From: "c:default", To: "a:default", Type: "video"}, // cycle
			{From: "c:default", To: "sink:v", Type: "video"},
		},
	})
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}
}
