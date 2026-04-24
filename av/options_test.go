// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

import (
	"strings"
	"testing"
)

func TestEncoderOptionsByName_Generic(t *testing.T) {
	// "rawvideo" is always built into libavcodec, so this test is robust
	// across FFmpeg builds.
	info, err := EncoderOptionsByName("rawvideo")
	if err != nil {
		t.Fatalf("EncoderOptionsByName(rawvideo): %v", err)
	}
	if info.Name != "rawvideo" {
		t.Errorf("name = %q, want rawvideo", info.Name)
	}
	if len(info.Options) == 0 {
		t.Fatal("expected generic AVCodecContext options, got none")
	}
	// Generic AVCodecContext exposes "b" (bit_rate) and "g" (gop_size).
	hasBitRate := false
	hasGop := false
	for _, o := range info.Options {
		if o.Name == "b" {
			hasBitRate = true
		}
		if o.Name == "g" {
			hasGop = true
		}
	}
	if !hasBitRate {
		t.Error("expected to find bit-rate option 'b' in generic options")
	}
	if !hasGop {
		t.Error("expected to find gop_size option 'g' in generic options")
	}
}

func TestEncoderOptionsByName_Unknown(t *testing.T) {
	_, err := EncoderOptionsByName("definitely_not_a_codec_xyz")
	if err == nil {
		t.Fatal("expected error for unknown encoder")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v, want 'not found'", err)
	}
}

func TestEncoderOptionsByName_PrivateAndConstants(t *testing.T) {
	// libx264 is the canonical example with rich private options + enums.
	// Skip when the build doesn't link libx264.
	info, err := EncoderOptionsByName("libx264")
	if err != nil {
		t.Skipf("libx264 not available in this build: %v", err)
	}
	var preset *EncoderOption
	for i := range info.Options {
		if info.Options[i].Name == "preset" && info.Options[i].IsPrivate {
			preset = &info.Options[i]
			break
		}
	}
	if preset == nil {
		t.Fatal("expected libx264 to expose private 'preset' option")
	}
	if preset.Type != OptTypeString {
		t.Errorf("preset.Type = %q, want string", preset.Type)
	}
}
