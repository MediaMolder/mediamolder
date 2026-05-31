// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"testing"

	"github.com/MediaMolder/MediaMolder/graph"
)

// ---------- helpers ----------

func cudaConfig(filterName string) *Config {
	return &Config{
		HardwareDevices: []HardwareDevice{
			{Name: "gpu0", Type: "cuda"},
		},
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "src", Type: "source"},
				{ID: "f", Type: "filter", Filter: filterName, Device: "gpu0", AutoMapHW: true},
				{ID: "enc", Type: "encoder"},
			},
			Edges: []EdgeDef{
				{From: "src", To: "f", Type: "video"},
				{From: "f", To: "enc", Type: "video"},
			},
		},
	}
}

func cudaDef(filterName string) *graph.Def {
	return &graph.Def{
		Inputs:  []graph.InputDef{{ID: "src"}},
		Outputs: []graph.OutputDef{{ID: "enc"}},
		Nodes: []graph.NodeDef{
			{ID: "src", Type: "source"},
			{ID: "f", Type: "filter", Filter: filterName, Device: "gpu0", AutoMapHW: true},
			{ID: "enc", Type: "encoder"},
		},
		Edges: []graph.EdgeDef{
			{From: "src", To: "f", Type: "video"},
			{From: "f", To: "enc", Type: "video"},
		},
	}
}

// nodeByID returns the NodeDef with the given ID, or nil.
func nodeByID(def *graph.Def, id string) *graph.NodeDef {
	for i := range def.Nodes {
		if def.Nodes[i].ID == id {
			return &def.Nodes[i]
		}
	}
	return nil
}

// ---------- promotion tests ----------

func TestExpandHWFilterMappings_PromotesScaleToCUDA(t *testing.T) {
	cfg := cudaConfig("scale")
	def := cudaDef("scale")
	expandHWFilterMappings(cfg, def)
	n := nodeByID(def, "f")
	if n == nil {
		t.Fatal("node 'f' missing after expansion")
	}
	if n.Filter != "scale_cuda" {
		t.Errorf("Filter = %q, want %q", n.Filter, "scale_cuda")
	}
}

func TestExpandHWFilterMappings_PromotesYadifToCUDA(t *testing.T) {
	cfg := cudaConfig("yadif")
	def := cudaDef("yadif")
	expandHWFilterMappings(cfg, def)
	n := nodeByID(def, "f")
	if n.Filter != "yadif_cuda" {
		t.Errorf("Filter = %q, want %q", n.Filter, "yadif_cuda")
	}
}

func TestExpandHWFilterMappings_NoOpWhenAutoMapHWFalse(t *testing.T) {
	cfg := &Config{
		HardwareDevices: []HardwareDevice{{Name: "gpu0", Type: "cuda"}},
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "f", Type: "filter", Filter: "scale", Device: "gpu0", AutoMapHW: false},
			},
		},
	}
	def := &graph.Def{
		Nodes: []graph.NodeDef{
			{ID: "f", Type: "filter", Filter: "scale", Device: "gpu0", AutoMapHW: false},
		},
	}
	expandHWFilterMappings(cfg, def)
	n := nodeByID(def, "f")
	if n.Filter != "scale" {
		t.Errorf("Filter = %q, want unchanged %q", n.Filter, "scale")
	}
}

func TestExpandHWFilterMappings_NoOpWhenNoHWDevices(t *testing.T) {
	cfg := &Config{} // no HardwareDevices
	def := &graph.Def{
		Nodes: []graph.NodeDef{
			{ID: "f", Type: "filter", Filter: "scale", Device: "gpu0", AutoMapHW: true},
		},
	}
	expandHWFilterMappings(cfg, def)
	n := nodeByID(def, "f")
	if n.Filter != "scale" {
		t.Errorf("Filter = %q, want unchanged %q", n.Filter, "scale")
	}
}

func TestExpandHWFilterMappings_NoOpWhenFilterNotInTable(t *testing.T) {
	cfg := cudaConfig("null")
	def := cudaDef("null")
	expandHWFilterMappings(cfg, def)
	n := nodeByID(def, "f")
	if n.Filter != "null" {
		t.Errorf("Filter = %q, want unchanged %q", n.Filter, "null")
	}
}

func TestExpandHWFilterMappings_NoOpWhenDeviceTypeNotMapped(t *testing.T) {
	// "thumbnail" has no videotoolbox alternative; filter should be unchanged.
	cfg := &Config{
		HardwareDevices: []HardwareDevice{{Name: "vt0", Type: "videotoolbox"}},
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "f", Type: "filter", Filter: "thumbnail", Device: "vt0", AutoMapHW: true},
			},
		},
	}
	def := &graph.Def{
		Nodes: []graph.NodeDef{
			{ID: "f", Type: "filter", Filter: "thumbnail", Device: "vt0", AutoMapHW: true},
		},
	}
	expandHWFilterMappings(cfg, def)
	n := nodeByID(def, "f")
	if n.Filter != "thumbnail" {
		t.Errorf("Filter = %q, want unchanged %q", n.Filter, "thumbnail")
	}
}

// ---------- hwupload / hwdownload insertion tests ----------

func TestExpandHWFilterMappings_InsertsHwupload(t *testing.T) {
	// CPU source → scale (AutoMapHW, cuda) → encoder.
	// Expect: hwupload node between src and scale.
	cfg := cudaConfig("scale")
	def := cudaDef("scale")
	expandHWFilterMappings(cfg, def)

	// The hwupload node id contains "__hwup__f_src" by convention.
	found := false
	for _, n := range def.Nodes {
		if n.Filter == "hwupload" {
			found = true
			if n.Device != "gpu0" {
				t.Errorf("hwupload.Device = %q, want %q", n.Device, "gpu0")
			}
		}
	}
	if !found {
		t.Error("hwupload node was not inserted for CPU→GPU edge")
	}
}

func TestExpandHWFilterMappings_InsertsHwdownload(t *testing.T) {
	// scale (AutoMapHW, cuda) → CPU encoder.
	// Expect: hwdownload node between scale and enc.
	cfg := cudaConfig("scale")
	def := cudaDef("scale")
	expandHWFilterMappings(cfg, def)

	found := false
	for _, n := range def.Nodes {
		if n.Filter == "hwdownload" {
			found = true
			if n.Device != "" {
				t.Errorf("hwdownload.Device = %q, want empty", n.Device)
			}
		}
	}
	if !found {
		t.Error("hwdownload node was not inserted for GPU→CPU edge")
	}
}

func TestExpandHWFilterMappings_NoHwuploadWhenSameDevice(t *testing.T) {
	// Source node on same cuda device → scale with AutoMapHW.
	// No hwupload should be inserted.
	cfg := &Config{
		HardwareDevices: []HardwareDevice{{Name: "gpu0", Type: "cuda"}},
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "src", Type: "source", Device: "gpu0"},
				{ID: "f", Type: "filter", Filter: "scale", Device: "gpu0", AutoMapHW: true},
			},
		},
	}
	def := &graph.Def{
		Nodes: []graph.NodeDef{
			{ID: "src", Type: "source", Device: "gpu0"},
			{ID: "f", Type: "filter", Filter: "scale", Device: "gpu0", AutoMapHW: true},
		},
		Edges: []graph.EdgeDef{
			{From: "src", To: "f", Type: "video"},
		},
	}
	before := len(def.Nodes)
	expandHWFilterMappings(cfg, def)
	if len(def.Nodes) != before {
		t.Errorf("inserted %d extra nodes, want 0 (same-device source)", len(def.Nodes)-before)
	}
}

func TestExpandHWFilterMappings_AudioEdgeNotConverted(t *testing.T) {
	// Audio edge from CPU source should not get hwupload.
	cfg := &Config{
		HardwareDevices: []HardwareDevice{{Name: "gpu0", Type: "cuda"}},
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "f", Type: "filter", Filter: "scale", Device: "gpu0", AutoMapHW: true},
			},
		},
	}
	def := &graph.Def{
		Nodes: []graph.NodeDef{
			{ID: "src", Type: "source"},
			{ID: "f", Type: "filter", Filter: "scale", Device: "gpu0", AutoMapHW: true},
		},
		Edges: []graph.EdgeDef{
			{From: "src", To: "f", Type: "audio"}, // audio edge
		},
	}
	before := len(def.Nodes)
	expandHWFilterMappings(cfg, def)
	if len(def.Nodes) != before {
		t.Errorf("inserted %d extra nodes for audio edge, want 0", len(def.Nodes)-before)
	}
}

// ---------- validation tests ----------

func TestValidate_RejectsAutoMapHWWithoutDevice(t *testing.T) {
	cfg := &Config{
		Inputs: []Input{{ID: "src"}},
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "f", Type: "filter", Filter: "scale", AutoMapHW: true}, // no Device
			},
			Edges: []EdgeDef{
				{From: "src", To: "f", Type: "video"},
				{From: "f", To: "out", Type: "video"},
			},
		},
		Outputs: []Output{{ID: "out", URL: "out.mp4"}},
	}
	if err := validate(cfg); err == nil {
		t.Error("validate() returned nil; want error for AutoMapHW without Device")
	}
}

func TestValidate_RejectsAutoMapHWOnEncoder(t *testing.T) {
	cfg := &Config{
		HardwareDevices: []HardwareDevice{{Name: "gpu0", Type: "cuda"}},
		Inputs:          []Input{{ID: "src"}},
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "enc", Type: "encoder", Device: "gpu0", AutoMapHW: true}, // not a filter node
			},
			Edges: []EdgeDef{
				{From: "src", To: "enc", Type: "video"},
				{From: "enc", To: "out", Type: "video"},
			},
		},
		Outputs: []Output{{ID: "out", URL: "out.mp4"}},
	}
	if err := validate(cfg); err == nil {
		t.Error("validate() returned nil; want error for AutoMapHW on encoder node")
	}
}

func TestValidate_RejectsAutoMapHWUnsupportedPairing(t *testing.T) {
	// "thumbnail" has no videotoolbox alternative; validate should reject.
	cfg := &Config{
		HardwareDevices: []HardwareDevice{{Name: "vt0", Type: "videotoolbox"}},
		Inputs:          []Input{{ID: "src"}},
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "f", Type: "filter", Filter: "thumbnail", Device: "vt0", AutoMapHW: true},
			},
			Edges: []EdgeDef{
				{From: "src", To: "f", Type: "video"},
				{From: "f", To: "out", Type: "video"},
			},
		},
		Outputs: []Output{{ID: "out", URL: "out.mp4"}},
	}
	if err := validate(cfg); err == nil {
		t.Error("validate() returned nil; want error for unsupported filter/device pairing")
	}
}

func TestValidate_AcceptsAutoMapHWValidPairing(t *testing.T) {
	cfg := &Config{
		SchemaVersion:   "1.0",
		HardwareDevices: []HardwareDevice{{Name: "gpu0", Type: "cuda"}},
		Inputs:          []Input{{ID: "src", URL: "input.mp4"}},
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "f", Type: "filter", Filter: "scale", Device: "gpu0", AutoMapHW: true},
				{ID: "enc", Type: "encoder"},
			},
			Edges: []EdgeDef{
				{From: "src", To: "f", Type: "video"},
				{From: "f", To: "enc", Type: "video"},
				{From: "enc", To: "out", Type: "video"},
			},
		},
		Outputs: []Output{{ID: "out", URL: "out.mp4"}},
	}
	if err := validate(cfg); err != nil {
		t.Errorf("validate() unexpected error: %v", err)
	}
}

// ---------- exported table test ----------

func TestHWFilterAlts_NonEmpty(t *testing.T) {
	alts := HWFilterAlts()
	if len(alts) == 0 {
		t.Fatal("HWFilterAlts() returned empty map")
	}
	scaleCUDA, ok := alts["scale"]["cuda"]
	if !ok || scaleCUDA != "scale_cuda" {
		t.Errorf(`alts["scale"]["cuda"] = %q, want "scale_cuda"`, scaleCUDA)
	}
}

func TestHWFilterAlts_ReturnsCopy(t *testing.T) {
	a := HWFilterAlts()
	a["__mutate_test__"] = map[string]string{"x": "y"}
	b := HWFilterAlts()
	if _, ok := b["__mutate_test__"]; ok {
		t.Error("HWFilterAlts() returned a shared reference; mutation leaked")
	}
}
