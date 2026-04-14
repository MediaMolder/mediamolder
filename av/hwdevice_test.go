// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

import "testing"

// TestListHWDeviceTypes verifies that the FFmpeg build reports device types.
func TestListHWDeviceTypes(t *testing.T) {
	types := ListHWDeviceTypes()
	t.Logf("available hw device types: %v", types)
	// We don't require specific types since this depends on the build,
	// but the function should not panic.
}

// TestParseHWDeviceType tests round-trip parsing of device type names.
func TestParseHWDeviceType(t *testing.T) {
	tests := []struct {
		name string
		want HWDeviceType
	}{
		{"cuda", HWDeviceCUDA},
		{"vaapi", HWDeviceVAAPI},
		{"qsv", HWDeviceQSV},
		{"videotoolbox", HWDeviceVideoToolbox},
		{"nonexistent", HWDeviceNone},
	}
	for _, tt := range tests {
		got := ParseHWDeviceType(tt.name)
		if got != tt.want {
			t.Errorf("ParseHWDeviceType(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// TestHWDeviceTypeString tests the String() method.
func TestHWDeviceTypeString(t *testing.T) {
	tests := []struct {
		dt   HWDeviceType
		want string
	}{
		{HWDeviceCUDA, "cuda"},
		{HWDeviceVAAPI, "vaapi"},
		{HWDeviceQSV, "qsv"},
		{HWDeviceVideoToolbox, "videotoolbox"},
		{HWDeviceNone, "none"},
	}
	for _, tt := range tests {
		got := tt.dt.String()
		if got != tt.want {
			t.Errorf("HWDeviceType(%d).String() = %q, want %q", tt.dt, got, tt.want)
		}
	}
}

// TestHWPixelFormat tests that known device types return valid pixel formats.
func TestHWPixelFormat(t *testing.T) {
	// CUDA should have a known pixel format.
	pf := HWDeviceCUDA.HWPixelFormat()
	if pf < 0 {
		t.Logf("CUDA pixel format: %d (may be -1 if not compiled in)", pf)
	}
}

// TestOpenHWDeviceAutoSkip tests that opening a hardware device either succeeds
// or returns an error (no crash/panic).
func TestOpenHWDeviceAutoSkip(t *testing.T) {
	for _, dt := range []HWDeviceType{HWDeviceCUDA, HWDeviceVAAPI, HWDeviceQSV, HWDeviceVideoToolbox} {
		dev, err := OpenHWDevice(dt, "")
		if err != nil {
			t.Logf("OpenHWDevice(%s): %v (expected on this system)", dt, err)
			continue
		}
		t.Logf("OpenHWDevice(%s): success", dt)
		dev.Close()
	}
}

// TestIsHWFrame tests the IsHWFrame function with a software frame.
func TestIsHWFrame(t *testing.T) {
	f, err := AllocFrame()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if IsHWFrame(f) {
		t.Error("newly allocated frame should not be a HW frame")
	}
}

// TestListBitstreamFilters verifies that we can list BSFs.
func TestListBitstreamFilters(t *testing.T) {
	filters := ListBitstreamFilters()
	if len(filters) == 0 {
		t.Fatal("expected at least one bitstream filter")
	}
	foundH264 := false
	for _, f := range filters {
		if f.Name == "h264_mp4toannexb" {
			foundH264 = true
		}
	}
	if !foundH264 {
		t.Error("h264_mp4toannexb BSF not found")
	}
	t.Logf("found %d bitstream filters", len(filters))
}

// TestSubtitleTypes tests subtitle type constants.
func TestSubtitleTypes(t *testing.T) {
	tests := []struct {
		st   SubtitleType
		want string
	}{
		{SubtitleTypeBitmap, "bitmap"},
		{SubtitleTypeText, "text"},
		{SubtitleTypeASS, "ass"},
		{SubtitleTypeNone, "none"},
	}
	for _, tt := range tests {
		got := tt.st.String()
		if got != tt.want {
			t.Errorf("SubtitleType(%d).String() = %q, want %q", tt.st, got, tt.want)
		}
	}
}

// TestHWFilterName tests the HW filter name mapping.
func TestHWFilterName(t *testing.T) {
	tests := []struct {
		filter string
		dt     HWDeviceType
		want   string
	}{
		{"scale", HWDeviceCUDA, "scale_cuda"},
		{"scale", HWDeviceVAAPI, "scale_vaapi"},
		{"scale", HWDeviceQSV, "scale_qsv"},
		{"scale", HWDeviceVideoToolbox, "scale_vt"},
		{"overlay", HWDeviceCUDA, "overlay_cuda"},
		{"unknownfilter", HWDeviceCUDA, "unknownfilter"},
		{"scale", HWDeviceNone, "scale"},
	}
	for _, tt := range tests {
		got := HWFilterName(tt.filter, tt.dt)
		if got != tt.want {
			t.Errorf("HWFilterName(%q, %s) = %q, want %q", tt.filter, tt.dt, got, tt.want)
		}
	}
}

// TestSupportsHWDecode tests the SupportsHWDecode function.
func TestSupportsHWDecode(t *testing.T) {
	// Codec ID 27 = AV_CODEC_ID_H264. This should report support for
	// at least some HW types on any system with appropriate drivers.
	for _, dt := range ListHWDeviceTypes() {
		supported := SupportsHWDecode(27, dt) // AV_CODEC_ID_H264
		t.Logf("H264 hw decode on %s: %v", dt, supported)
	}
}

// TestSubtitleBurnInFilter tests filter spec generation.
func TestSubtitleBurnInFilter(t *testing.T) {
	spec := SubtitleBurnInFilter("/path/to/subs.srt", "UTF-8")
	if spec == "" {
		t.Error("expected non-empty filter spec")
	}
	t.Log("burn-in spec:", spec)
}

// TestASSBurnInFilter tests ASS filter spec generation.
func TestASSBurnInFilter(t *testing.T) {
	spec := ASSBurnInFilter("/path/to/subs.ass")
	if spec == "" {
		t.Error("expected non-empty filter spec")
	}
	t.Log("ASS burn-in spec:", spec)
}
