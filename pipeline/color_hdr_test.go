// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: GPL-3.0-or-later

package pipeline

import (
	"strings"
	"testing"
)

func baseHDRConfig(out Output) *Config {
	return &Config{
		SchemaVersion: "1.0",
		Inputs:        []Input{{ID: "in0", URL: "in.mp4"}},
		Outputs:       []Output{out},
		Graph:         GraphDef{Edges: []EdgeDef{{From: "in0:v:0", To: "out0:v", Type: "video"}}},
	}
}

func validHDROutput() Output {
	return Output{
		ID:         "out0",
		URL:        "out.mp4",
		Format:     "mp4",
		CodecVideo: "libx265",
		Color:      &ColorMetadata{Primaries: "bt2020", Transfer: "smpte2084", Space: "bt2020nc", Range: "tv"},
		HDR: &HDRMetadata{
			MasteringDisplay: &MasteringDisplayMetadata{
				DisplayPrimariesRX: 35400, DisplayPrimariesRY: 14600,
				DisplayPrimariesGX: 8500, DisplayPrimariesGY: 39850,
				DisplayPrimariesBX: 6550, DisplayPrimariesBY: 2300,
				WhitePointX: 15635, WhitePointY: 16450,
				MinLuminance: 1, MaxLuminance: 10000000,
			},
			ContentLightLevel: &ContentLightLevelMetadata{MaxCLL: 1000, MaxFALL: 400},
		},
	}
}

func TestValidateHDR_Valid(t *testing.T) {
	cfg := baseHDRConfig(validHDROutput())
	if err := validate(cfg); err != nil {
		t.Fatalf("expected valid config, got %v", err)
	}
}

func TestValidateColor_RejectsUnknownEnum(t *testing.T) {
	out := Output{ID: "out0", URL: "out.mp4", Format: "mp4", CodecVideo: "libx265", Color: &ColorMetadata{Primaries: "bogus"}}
	err := validate(baseHDRConfig(out))
	if err == nil || !strings.Contains(err.Error(), "color.primaries") {
		t.Fatalf("expected color.primaries error, got %v", err)
	}
}

func TestValidateHDR_RejectsAudioOnly(t *testing.T) {
	o := validHDROutput()
	o.CodecVideo = ""
	err := validate(baseHDRConfig(o))
	if err == nil || !strings.Contains(err.Error(), "video stream") {
		t.Fatalf("expected video-stream error, got %v", err)
	}
}

func TestValidateHDR_RejectsNonHDRCodec(t *testing.T) {
	o := validHDROutput()
	o.CodecVideo = "mpeg4"
	err := validate(baseHDRConfig(o))
	if err == nil || !strings.Contains(err.Error(), "HDR-capable video codec") {
		t.Fatalf("expected codec error, got %v", err)
	}
}

func TestValidateHDR_RejectsNonHDRContainer(t *testing.T) {
	o := validHDROutput()
	o.Format = "avi"
	err := validate(baseHDRConfig(o))
	if err == nil || !strings.Contains(err.Error(), "container that carries") {
		t.Fatalf("expected container error, got %v", err)
	}
}

func TestValidateHDR_RejectsNonPQOrHLGTransfer(t *testing.T) {
	o := validHDROutput()
	o.Color.Transfer = "bt709"
	err := validate(baseHDRConfig(o))
	if err == nil || !strings.Contains(err.Error(), "smpte2084") {
		t.Fatalf("expected transfer error, got %v", err)
	}
}

func TestValidateHDR_RejectsBadLuminance(t *testing.T) {
	o := validHDROutput()
	o.HDR.MasteringDisplay.MinLuminance = 10000000
	o.HDR.MasteringDisplay.MaxLuminance = 1
	err := validate(baseHDRConfig(o))
	if err == nil || !strings.Contains(err.Error(), "min_luminance") {
		t.Fatalf("expected min<max error, got %v", err)
	}
}

func TestValidateHDR_RejectsCLLLessThanFALL(t *testing.T) {
	o := validHDROutput()
	o.HDR.ContentLightLevel.MaxCLL = 100
	o.HDR.ContentLightLevel.MaxFALL = 400
	err := validate(baseHDRConfig(o))
	if err == nil || !strings.Contains(err.Error(), "max_fall") {
		t.Fatalf("expected MaxFALL>MaxCLL error, got %v", err)
	}
}

func TestValidateHDR_RejectsPartialPrimaries(t *testing.T) {
	o := validHDROutput()
	o.HDR.MasteringDisplay.WhitePointX = 0 // remove WP
	err := validate(baseHDRConfig(o))
	if err == nil || !strings.Contains(err.Error(), "primaries + white_point") {
		t.Fatalf("expected partial-primaries error, got %v", err)
	}
}
