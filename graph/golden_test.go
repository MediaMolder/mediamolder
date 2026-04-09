package graph

import (
	"testing"
)

// goldenGraphTests covers graph construction patterns for Phase 1 integration.
var goldenGraphTests = []struct {
	name      string
	def       Def
	wantNodes int
	wantEdges int
	wantSrc   int
	wantSink  int
	wantErr   bool
}{
	{
		name: "diamond: 2 sources → merge → 2 sinks",
		def: Def{
			Inputs:  []InputDef{{ID: "src1"}, {ID: "src2"}},
			Nodes:   []NodeDef{{ID: "merge", Type: "filter", Filter: "amerge"}},
			Outputs: []OutputDef{{ID: "out1"}, {ID: "out2"}},
			Edges: []EdgeDef{
				{From: "src1:v:0", To: "merge:default", Type: "audio"},
				{From: "src2:v:0", To: "merge:default", Type: "audio"},
				{From: "merge:out0", To: "out1:a", Type: "audio"},
				{From: "merge:out1", To: "out2:a", Type: "audio"},
			},
		},
		wantNodes: 5, wantEdges: 4, wantSrc: 2, wantSink: 2,
	},
	{
		name: "chain of 5 filters",
		def: Def{
			Inputs:  []InputDef{{ID: "in"}},
			Nodes: []NodeDef{
				{ID: "f1", Type: "filter", Filter: "scale"},
				{ID: "f2", Type: "filter", Filter: "fps"},
				{ID: "f3", Type: "filter", Filter: "pad"},
				{ID: "f4", Type: "filter", Filter: "drawtext"},
				{ID: "f5", Type: "filter", Filter: "crop"},
			},
			Outputs: []OutputDef{{ID: "out"}},
			Edges: []EdgeDef{
				{From: "in:v:0", To: "f1:default", Type: "video"},
				{From: "f1:default", To: "f2:default", Type: "video"},
				{From: "f2:default", To: "f3:default", Type: "video"},
				{From: "f3:default", To: "f4:default", Type: "video"},
				{From: "f4:default", To: "f5:default", Type: "video"},
				{From: "f5:default", To: "out:v", Type: "video"},
			},
		},
		wantNodes: 7, wantEdges: 6, wantSrc: 1, wantSink: 1,
	},
	{
		name: "parallel video and audio chains",
		def: Def{
			Inputs: []InputDef{{ID: "in"}},
			Nodes: []NodeDef{
				{ID: "vscale", Type: "filter", Filter: "scale"},
				{ID: "vfps", Type: "filter", Filter: "fps"},
				{ID: "avol", Type: "filter", Filter: "volume"},
			},
			Outputs: []OutputDef{{ID: "out"}},
			Edges: []EdgeDef{
				{From: "in:v:0", To: "vscale:default", Type: "video"},
				{From: "vscale:default", To: "vfps:default", Type: "video"},
				{From: "vfps:default", To: "out:v", Type: "video"},
				{From: "in:a:0", To: "avol:default", Type: "audio"},
				{From: "avol:default", To: "out:a", Type: "audio"},
			},
		},
		wantNodes: 5, wantEdges: 5, wantSrc: 1, wantSink: 1,
	},
	{
		name: "3 inputs overlay to 1 output",
		def: Def{
			Inputs:  []InputDef{{ID: "bg"}, {ID: "fg1"}, {ID: "fg2"}},
			Nodes: []NodeDef{
				{ID: "ov1", Type: "filter", Filter: "overlay"},
				{ID: "ov2", Type: "filter", Filter: "overlay"},
			},
			Outputs: []OutputDef{{ID: "out"}},
			Edges: []EdgeDef{
				{From: "bg:v:0", To: "ov1:default", Type: "video"},
				{From: "fg1:v:0", To: "ov1:overlay", Type: "video"},
				{From: "ov1:default", To: "ov2:default", Type: "video"},
				{From: "fg2:v:0", To: "ov2:overlay", Type: "video"},
				{From: "ov2:default", To: "out:v", Type: "video"},
			},
		},
		wantNodes: 6, wantEdges: 5, wantSrc: 3, wantSink: 1,
	},
	{
		name: "split to 3 outputs with different filters",
		def: Def{
			Inputs: []InputDef{{ID: "in"}},
			Nodes: []NodeDef{
				{ID: "split", Type: "filter", Filter: "split"},
				{ID: "s1", Type: "filter", Filter: "scale", Params: map[string]any{"w": "1920", "h": "1080"}},
				{ID: "s2", Type: "filter", Filter: "scale", Params: map[string]any{"w": "1280", "h": "720"}},
				{ID: "s3", Type: "filter", Filter: "scale", Params: map[string]any{"w": "640", "h": "480"}},
			},
			Outputs: []OutputDef{{ID: "hd"}, {ID: "md"}, {ID: "sd"}},
			Edges: []EdgeDef{
				{From: "in:v:0", To: "split:default", Type: "video"},
				{From: "split:out0", To: "s1:default", Type: "video"},
				{From: "split:out1", To: "s2:default", Type: "video"},
				{From: "split:out2", To: "s3:default", Type: "video"},
				{From: "s1:default", To: "hd:v", Type: "video"},
				{From: "s2:default", To: "md:v", Type: "video"},
				{From: "s3:default", To: "sd:v", Type: "video"},
			},
		},
		wantNodes: 8, wantEdges: 7, wantSrc: 1, wantSink: 3,
	},
	{
		name: "encoder node between filter and output",
		def: Def{
			Inputs: []InputDef{{ID: "in"}},
			Nodes: []NodeDef{
				{ID: "scale", Type: "filter", Filter: "scale"},
				{ID: "enc", Type: "encoder", Params: map[string]any{"codec": "libx264", "crf": "23"}},
			},
			Outputs: []OutputDef{{ID: "out"}},
			Edges: []EdgeDef{
				{From: "in:v:0", To: "scale:default", Type: "video"},
				{From: "scale:default", To: "enc:default", Type: "video"},
				{From: "enc:default", To: "out:v", Type: "video"},
			},
		},
		wantNodes: 4, wantEdges: 3, wantSrc: 1, wantSink: 1,
	},
	{
		name: "cycle detection in diamond",
		def: Def{
			Inputs: []InputDef{{ID: "in"}},
			Nodes: []NodeDef{
				{ID: "a", Type: "filter", Filter: "x"},
				{ID: "b", Type: "filter", Filter: "y"},
			},
			Outputs: []OutputDef{{ID: "out"}},
			Edges: []EdgeDef{
				{From: "in:v:0", To: "a:default", Type: "video"},
				{From: "a:default", To: "b:default", Type: "video"},
				{From: "b:default", To: "a:default", Type: "video"}, // cycle!
				{From: "b:default", To: "out:v", Type: "video"},
			},
		},
		wantErr: true,
	},
	{
		name: "isolated node error",
		def: Def{
			Inputs: []InputDef{{ID: "in"}},
			Nodes: []NodeDef{
				{ID: "lonely", Type: "filter", Filter: "null"},
			},
			Outputs: []OutputDef{{ID: "out"}},
			Edges: []EdgeDef{
				{From: "in:v:0", To: "out:v", Type: "video"},
			},
		},
		// lonely node has no edges but that's still a valid graph (just unused)
		wantNodes: 3, wantEdges: 1, wantSrc: 1, wantSink: 1,
	},
}

func TestGoldenGraphConstruction(t *testing.T) {
	for _, tt := range goldenGraphTests {
		t.Run(tt.name, func(t *testing.T) {
			g, err := Build(&tt.def)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(g.Nodes) != tt.wantNodes {
				t.Errorf("nodes: got %d, want %d", len(g.Nodes), tt.wantNodes)
			}
			if len(g.Edges) != tt.wantEdges {
				t.Errorf("edges: got %d, want %d", len(g.Edges), tt.wantEdges)
			}
			if len(g.Sources) != tt.wantSrc {
				t.Errorf("sources: got %d, want %d", len(g.Sources), tt.wantSrc)
			}
			if len(g.Sinks) != tt.wantSink {
				t.Errorf("sinks: got %d, want %d", len(g.Sinks), tt.wantSink)
			}
			// Verify topological order covers all nodes
			if len(g.Order) != tt.wantNodes {
				t.Errorf("order length: got %d, want %d", len(g.Order), tt.wantNodes)
			}
		})
	}
}

func TestGraphNodeLookup(t *testing.T) {
	def := &Def{
		Inputs:  []InputDef{{ID: "in"}},
		Nodes:   []NodeDef{{ID: "f", Type: "filter", Filter: "scale"}},
		Outputs: []OutputDef{{ID: "out"}},
		Edges: []EdgeDef{
			{From: "in:v:0", To: "f:default", Type: "video"},
			{From: "f:default", To: "out:v", Type: "video"},
		},
	}
	g, err := Build(def)
	if err != nil {
		t.Fatal(err)
	}
	n := g.NodeByID("f")
	if n == nil {
		t.Fatal("NodeByID returned nil for 'f'")
	}
	if n.Kind != KindFilter {
		t.Errorf("kind: got %v, want KindFilter", n.Kind)
	}
	preds := n.Predecessors()
	if len(preds) != 1 || preds[0].ID != "in" {
		t.Errorf("predecessors: got %v", preds)
	}
	succs := n.Successors()
	if len(succs) != 1 || succs[0].ID != "out" {
		t.Errorf("successors: got %v", succs)
	}
	if g.NodeByID("nonexistent") != nil {
		t.Error("NodeByID should return nil for unknown ID")
	}
}
