// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: GPL-3.0-or-later

package job

import (
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
)

func TestValidateFilterAvailability_RejectsUnknownFilter(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.0",
		Inputs:        []Input{{ID: "in0", URL: "in.mp4"}},
		Outputs:       []Output{{ID: "out0", URL: "out.mp4", CodecVideo: "copy"}},
		Graph: GraphDef{
			Nodes: []NodeDef{{ID: "f0", Type: "filter", Filter: "no_such_filter_xyzzy"}},
			Edges: []EdgeDef{
				{From: "in0:v:0", To: "f0:in", Type: "video"},
				{From: "f0:out", To: "out0:v", Type: "video"},
			},
		},
	}
	err := validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "no_such_filter_xyzzy") {
		t.Fatalf("expected unknown-filter rejection, got %v", err)
	}
}

func TestValidateFilterAvailability_OptionalLibHint(t *testing.T) {
	if av.FindFilter("zscale") {
		t.Skip("zscale is built into this libavfilter; can't exercise the missing-lib hint")
	}
	cfg := &Config{
		SchemaVersion: "1.0",
		Inputs:        []Input{{ID: "in0", URL: "in.mp4"}},
		Outputs:       []Output{{ID: "out0", URL: "out.mp4", CodecVideo: "copy"}},
		Graph: GraphDef{
			Nodes: []NodeDef{{ID: "z0", Type: "filter", Filter: "zscale"}},
			Edges: []EdgeDef{
				{From: "in0:v:0", To: "z0:in", Type: "video"},
				{From: "z0:out", To: "out0:v", Type: "video"},
			},
		},
	}
	err := validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "libzimg") {
		t.Fatalf("expected --enable-libzimg hint, got %v", err)
	}
}

func TestValidateFilterAvailability_AllowsKnownFilter(t *testing.T) {
	if !av.FindFilter("scale") {
		t.Skip("scale not built; libavfilter build is broken")
	}
	cfg := &Config{
		SchemaVersion: "1.0",
		Inputs:        []Input{{ID: "in0", URL: "in.mp4"}},
		Outputs:       []Output{{ID: "out0", URL: "out.mp4", CodecVideo: "copy"}},
		Graph: GraphDef{
			Nodes: []NodeDef{{ID: "s0", Type: "filter", Filter: "scale", Params: map[string]any{"w": 640, "h": 360}}},
			Edges: []EdgeDef{
				{From: "in0:v:0", To: "s0:in", Type: "video"},
				{From: "s0:out", To: "out0:v", Type: "video"},
			},
		},
	}
	if err := validate(cfg); err != nil {
		t.Fatalf("expected scale to validate, got %v", err)
	}
}

// TestFilterAvailabilityCache verifies that builtInFilters() returns a
// non-empty set and that 'scale' (always compiled in) is present.
func TestFilterAvailabilityCache(t *testing.T) {
	set := builtInFilters()
	if len(set) == 0 {
		t.Fatal("builtInFilters returned empty set; av.ListFilters broken?")
	}
	if _, ok := set["scale"]; !ok {
		t.Error("builtInFilters: 'scale' not in cache; libavfilter build is broken")
	}
	// Calling again must return the same map (sync.Once).
	set2 := builtInFilters()
	if &set != &set2 && len(set) != len(set2) {
		t.Error("builtInFilters: second call returned different-sized map")
	}
}

// TestHardwareFilterHints_CUDA checks that CUDA filters that are absent get
// an actionable --enable-cuda-nvcc hint, and that scale_npp (which also
// requires libnpp) mentions libnpp specifically.
func TestHardwareFilterHints_CUDA(t *testing.T) {
	for _, tc := range []struct {
		filter   string
		wantHint string
	}{
		{"scale_cuda", "cuda-nvcc"},
		{"yadif_cuda", "cuda-nvcc"},
		{"overlay_cuda", "cuda-nvcc"},
		{"scale_npp", "libnpp"},
		{"transpose_npp", "libnpp"},
	} {
		if av.FindFilter(tc.filter) {
			continue // filter is present — skip hint check
		}
		err := filterAvailabilityError(tc.filter)
		if err == nil {
			t.Errorf("%s: expected unavailability error, got nil", tc.filter)
			continue
		}
		if !strings.Contains(err.Error(), tc.wantHint) {
			t.Errorf("%s: error %q does not mention %q", tc.filter, err.Error(), tc.wantHint)
		}
	}
}

// TestHardwareFilterHints_QSV checks Intel QSV filter hints.
func TestHardwareFilterHints_QSV(t *testing.T) {
	for _, filter := range []string{"scale_qsv", "deinterlace_qsv", "vpp_qsv", "overlay_qsv"} {
		if av.FindFilter(filter) {
			continue
		}
		err := filterAvailabilityError(filter)
		if err == nil {
			t.Errorf("%s: expected unavailability error, got nil", filter)
			continue
		}
		if !strings.Contains(err.Error(), "libmfx") {
			t.Errorf("%s: error %q does not mention libmfx", filter, err.Error())
		}
	}
}

// TestHardwareFilterHints_Vulkan checks Vulkan filter hints.
func TestHardwareFilterHints_Vulkan(t *testing.T) {
	for _, filter := range []string{"scale_vulkan", "overlay_vulkan", "transpose_vulkan"} {
		if av.FindFilter(filter) {
			continue
		}
		err := filterAvailabilityError(filter)
		if err == nil {
			t.Errorf("%s: expected unavailability error, got nil", filter)
			continue
		}
		if !strings.Contains(err.Error(), "vulkan") {
			t.Errorf("%s: error %q does not mention vulkan", filter, err.Error())
		}
	}
}

// TestHardwareFilterHints_OpenCL checks OpenCL filter hints.
func TestHardwareFilterHints_OpenCL(t *testing.T) {
	for _, filter := range []string{"tonemap_opencl", "scale_vulkan", "nlmeans_opencl", "unsharp_opencl"} {
		if av.FindFilter(filter) {
			continue
		}
		err := filterAvailabilityError(filter)
		if err == nil {
			t.Errorf("%s: expected unavailability error, got nil", filter)
			continue
		}
		// scale_vulkan mentions vulkan; others mention opencl
		if filter == "scale_vulkan" {
			if !strings.Contains(err.Error(), "vulkan") {
				t.Errorf("%s: error %q does not mention vulkan", filter, err.Error())
			}
		} else {
			if !strings.Contains(err.Error(), "opencl") {
				t.Errorf("%s: error %q does not mention opencl", filter, err.Error())
			}
		}
	}
}

// TestHardwareFilterHints_VAAPI checks VAAPI filter hints.
func TestHardwareFilterHints_VAAPI(t *testing.T) {
	for _, filter := range []string{"scale_vaapi", "deinterlace_vaapi", "overlay_vaapi", "procamp_vaapi"} {
		if av.FindFilter(filter) {
			continue
		}
		err := filterAvailabilityError(filter)
		if err == nil {
			t.Errorf("%s: expected unavailability error, got nil", filter)
			continue
		}
		if !strings.Contains(err.Error(), "vaapi") {
			t.Errorf("%s: error %q does not mention vaapi", filter, err.Error())
		}
	}
}

// TestScaleNppVsScaleCuda verifies the error messages differentiate NPP
// (needs libnpp) from plain CUDA (needs cuda-nvcc only).
func TestScaleNppVsScaleCuda(t *testing.T) {
	if av.FindFilter("scale_npp") || av.FindFilter("scale_cuda") {
		t.Skip("one or both CUDA scale filters are present; can't check missing hints")
	}
	nppErr := filterAvailabilityError("scale_npp")
	cudaErr := filterAvailabilityError("scale_cuda")
	if nppErr == nil || cudaErr == nil {
		t.Skip("filters available; skipping error message check")
	}
	if !strings.Contains(nppErr.Error(), "libnpp") {
		t.Errorf("scale_npp error should mention libnpp, got: %v", nppErr)
	}
	if strings.Contains(cudaErr.Error(), "libnpp") {
		t.Errorf("scale_cuda error should NOT mention libnpp (only cuda-nvcc), got: %v", cudaErr)
	}
}
