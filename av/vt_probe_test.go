// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build darwin

package av

import (
	"testing"
)

// TestQueryVTCapabilities verifies that QueryVTCapabilities runs without
// panicking and that any reported extra codecs are structurally valid.
// On non-Apple-Silicon hosts the result may simply be empty.
func TestQueryVTCapabilities(t *testing.T) {
	caps := QueryVTCapabilities()

	for i, c := range caps.ExtraEncoders {
		if c.Name == "" {
			t.Errorf("ExtraEncoders[%d].Name is empty", i)
		}
		if c.Role != "encode" {
			t.Errorf("ExtraEncoders[%d].Role = %q, want \"encode\"", i, c.Role)
		}
		if c.MediaType == "" {
			t.Errorf("ExtraEncoders[%d].MediaType is empty", i)
		}
		t.Logf("VT extra encoder: %s (%s)", c.Name, c.MediaType)
	}

	for i, c := range caps.ExtraDecoders {
		if c.Name == "" {
			t.Errorf("ExtraDecoders[%d].Name is empty", i)
		}
		if c.Role != "decode" {
			t.Errorf("ExtraDecoders[%d].Role = %q, want \"decode\"", i, c.Role)
		}
		if c.MediaType == "" {
			t.Errorf("ExtraDecoders[%d].MediaType is empty", i)
		}
		t.Logf("VT extra decoder: %s (%s)", c.Name, c.MediaType)
	}
}

// TestParseUint32 exercises the decimal uint32 parser.
func TestParseUint32(t *testing.T) {
	cases := []struct {
		in      string
		want    uint32
		wantErr bool
	}{
		{"0", 0, false},
		{"1", 1, false},
		{"4294967295", 0xFFFFFFFF, false},
		{"1635148593", 0x61766331, false}, // 'avc1' as decimal
		{"4294967296", 0, true},           // overflow
		{"abc", 0, true},
		{"", 0, true},
	}
	for _, tc := range cases {
		var got uint32
		_, err := parseUint32(tc.in, &got)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseUint32(%q): expected error, got nil (val=%d)", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseUint32(%q): unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseUint32(%q) = 0x%x, want 0x%x", tc.in, got, tc.want)
		}
	}
}

// TestMergeVTCodecs verifies that mergeVTCodecs deduplicates correctly.
func TestMergeVTCodecs(t *testing.T) {
	base := []HWCodecInfo{
		{Name: "h264_videotoolbox", Role: "encode", MediaType: "video"},
		{Name: "hevc_videotoolbox", Role: "encode", MediaType: "video"},
	}
	vt := VTPlatformCapabilities{
		ExtraEncoders: []HWCodecInfo{
			// duplicate — should be skipped
			{Name: "h264_videotoolbox", Role: "encode", MediaType: "video"},
			// new
			{Name: "prores_raw_vt", Role: "encode", MediaType: "video"},
		},
		ExtraDecoders: []HWCodecInfo{
			{Name: "prores_raw_vt", Role: "decode", MediaType: "video"},
		},
	}
	got := mergeVTCodecs(base, vt)
	if len(got) != 4 {
		t.Fatalf("mergeVTCodecs: got %d entries, want 4: %v", len(got), got)
	}
	names := make(map[string]bool)
	for _, c := range got {
		names[c.Name+":"+c.Role] = true
	}
	if !names["prores_raw_vt:encode"] {
		t.Error("prores_raw_vt:encode missing from merged result")
	}
	if !names["prores_raw_vt:decode"] {
		t.Error("prores_raw_vt:decode missing from merged result")
	}
	if names["h264_videotoolbox:encode"] && len(got) > 4 {
		t.Error("duplicate h264_videotoolbox:encode should have been skipped")
	}
}
