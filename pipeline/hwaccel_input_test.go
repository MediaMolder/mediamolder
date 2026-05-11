// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"strings"
	"testing"
)

// baseHWConfig is a minimal valid config template for hwaccel input tests.
const baseHWConfig = `{
  "schema_version": "1.0",
  "inputs": [
    {
      "id": "src",
      "url": "input.mp4",
      %s
      "streams": [{"input_index": 0, "type": "video", "track": 0}]
    }
  ],
  "graph": {"nodes": [], "edges": [{"from": "src:v:0", "to": "out:v", "type": "video"}]},
  "outputs": [{"id": "out", "url": "output.mp4", "codec_video": "copy"}]
}`

func hwConfig(fields string) []byte {
	return []byte(strings.ReplaceAll(baseHWConfig, "%s", fields))
}

// TestValidate_AcceptsHWAccelAlone verifies that setting hwaccel without
// hwaccel_device or hwaccel_output_format is valid.
func TestValidate_AcceptsHWAccelAlone(t *testing.T) {
	cfg := hwConfig(`"hwaccel": "cuda",`)
	if _, err := ParseConfig(cfg); err != nil {
		t.Fatalf("expected valid, got: %v", err)
	}
}

// TestValidate_AcceptsHWAccelNone verifies that hwaccel="none" is accepted.
func TestValidate_AcceptsHWAccelNone(t *testing.T) {
	cfg := hwConfig(`"hwaccel": "none",`)
	if _, err := ParseConfig(cfg); err != nil {
		t.Fatalf("expected valid, got: %v", err)
	}
}

// TestValidate_RejectsHWAccelDeviceWithoutHWAccel verifies that
// hwaccel_device cannot be set without hwaccel.
func TestValidate_RejectsHWAccelDeviceWithoutHWAccel(t *testing.T) {
	cfg := hwConfig(`"hwaccel_device": "mydev",`)
	_, err := ParseConfig(cfg)
	if err == nil {
		t.Fatal("expected error for hwaccel_device without hwaccel, got nil")
	}
	if !strings.Contains(err.Error(), "hwaccel_device requires hwaccel") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestValidate_RejectsHWAccelOutputFormatWithoutHWAccel verifies that
// hwaccel_output_format cannot be set without hwaccel.
func TestValidate_RejectsHWAccelOutputFormatWithoutHWAccel(t *testing.T) {
	cfg := hwConfig(`"hwaccel_output_format": "nv12",`)
	_, err := ParseConfig(cfg)
	if err == nil {
		t.Fatal("expected error for hwaccel_output_format without hwaccel, got nil")
	}
	if !strings.Contains(err.Error(), "hwaccel_output_format requires hwaccel") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestValidate_RejectsHWAccelDeviceNotDeclared verifies that
// hwaccel_device must reference a declared hardware_devices entry.
func TestValidate_RejectsHWAccelDeviceNotDeclared(t *testing.T) {
	cfg := `{
  "schema_version": "1.0",
  "inputs": [{
    "id": "src", "url": "input.mp4",
    "hwaccel": "cuda",
    "hwaccel_device": "undeclared_device",
    "streams": [{"input_index": 0, "type": "video", "track": 0}]
  }],
  "graph": {"nodes": [], "edges": [{"from": "src:v:0", "to": "out:v", "type": "video"}]},
  "outputs": [{"id": "out", "url": "output.mp4", "codec_video": "copy"}]
}`
	_, err := ParseConfig([]byte(cfg))
	if err == nil {
		t.Fatal("expected error for undeclared hwaccel_device, got nil")
	}
	if !strings.Contains(err.Error(), "does not match any hardware_devices entry") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestValidate_AcceptsHWAccelWithMatchingDevice verifies that a
// hwaccel_device referencing a declared hardware_devices entry is valid.
func TestValidate_AcceptsHWAccelWithMatchingDevice(t *testing.T) {
	cfg := `{
  "schema_version": "1.0",
  "hardware_devices": [{"name": "mydev", "type": "cuda", "device": "0"}],
  "inputs": [{
    "id": "src", "url": "input.mp4",
    "hwaccel": "cuda",
    "hwaccel_device": "mydev",
    "streams": [{"input_index": 0, "type": "video", "track": 0}]
  }],
  "graph": {"nodes": [], "edges": [{"from": "src:v:0", "to": "out:v", "type": "video"}]},
  "outputs": [{"id": "out", "url": "output.mp4", "codec_video": "copy"}]
}`
	if _, err := ParseConfig([]byte(cfg)); err != nil {
		t.Fatalf("expected valid, got: %v", err)
	}
}

// TestValidate_AcceptsHWAccelWithOutputFormat verifies that
// hwaccel_output_format is accepted when hwaccel is set.
func TestValidate_AcceptsHWAccelWithOutputFormat(t *testing.T) {
	cfg := hwConfig(`"hwaccel": "cuda", "hwaccel_output_format": "cuda",`)
	if _, err := ParseConfig(cfg); err != nil {
		t.Fatalf("expected valid, got: %v", err)
	}
}

// TestIsSwPixFmtName covers the helper used by the source handler to
// decide whether to enable AutoTransfer on the hw decoder.
func TestIsSwPixFmtName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"", false},
		{"cuda", false},
		{"vaapi", false},
		{"qsv", false},
		{"videotoolbox", false},
		{"d3d11va", false},
		{"dxva2", false},
		{"opencl", false},
		{"vulkan", false},
		{"nv12", true},
		{"yuv420p", true},
		{"p010le", true},
		{"NV12", true}, // case-insensitive
		{"CUDA", false},
	}
	for _, tc := range cases {
		got := isSwPixFmtName(tc.name)
		if got != tc.want {
			t.Errorf("isSwPixFmtName(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
