// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

import (
	"testing"

	"github.com/MediaMolder/MediaMolder/pipeline"
)

func TestParseColorFlags(t *testing.T) {
	cfg, err := ParseArgs([]string{
		"-i", "in.mp4",
		"-c:v", "libx265",
		"-color_primaries", "bt2020",
		"-color_trc", "smpte2084",
		"-colorspace", "bt2020nc",
		"-color_range", "tv",
		"-chroma_sample_location", "topleft",
		"-f", "mp4", "out.mp4",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(cfg.Outputs) != 1 || cfg.Outputs[0].Color == nil {
		t.Fatalf("color not attached to output: %+v", cfg.Outputs)
	}
	c := cfg.Outputs[0].Color
	want := pipeline.ColorMetadata{
		Range: "tv", Primaries: "bt2020", Transfer: "smpte2084",
		Space: "bt2020nc", ChromaLocation: "topleft",
	}
	if *c != want {
		t.Fatalf("color mismatch: got %+v want %+v", *c, want)
	}
}

func TestParseMasteringDisplayCanonicalRec2020(t *testing.T) {
	// Canonical Rec.2020 + D65 + 1000-nit mastering display string.
	md, err := parseMasteringDisplay("G(8500,39850)B(6550,2300)R(35400,14600)WP(15635,16450)L(10000000,1)")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if md.DisplayPrimariesGX != 8500 || md.DisplayPrimariesGY != 39850 {
		t.Errorf("G mismatch: %+v", md)
	}
	if md.DisplayPrimariesBX != 6550 || md.DisplayPrimariesBY != 2300 {
		t.Errorf("B mismatch: %+v", md)
	}
	if md.DisplayPrimariesRX != 35400 || md.DisplayPrimariesRY != 14600 {
		t.Errorf("R mismatch: %+v", md)
	}
	if md.WhitePointX != 15635 || md.WhitePointY != 16450 {
		t.Errorf("WP mismatch: %+v", md)
	}
	if md.MaxLuminance != 10000000 || md.MinLuminance != 1 {
		t.Errorf("L mismatch: %+v", md)
	}
}

func TestParseMasteringDisplayErrors(t *testing.T) {
	for _, s := range []string{
		"",
		"G(1,2)B(3,4)",                           // missing R, WP
		"G(1,2)B(3,4)R(5,6)WP(7,8)X(9,10)",       // unknown tag
		"G(1)B(3,4)R(5,6)WP(7,8)L(9,10)",         // bad arity
		"G(a,b)B(3,4)R(5,6)WP(7,8)L(9,10)",       // non-int
		"G(1,2)B(3,4)R(5,6)WP(7,8)L(9,10)G(1,2)", // duplicate
	} {
		if _, err := parseMasteringDisplay(s); err == nil {
			t.Errorf("expected error for %q", s)
		}
	}
}

func TestParseContentLightLevel(t *testing.T) {
	cll, err := parseContentLightLevel("1000,400")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cll.MaxCLL != 1000 || cll.MaxFALL != 400 {
		t.Fatalf("got %+v", cll)
	}
	cll, err = parseContentLightLevel("1500|600")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cll.MaxCLL != 1500 || cll.MaxFALL != 600 {
		t.Fatalf("got %+v", cll)
	}
	for _, s := range []string{"", "1000", "abc,400", "1000,xyz", "1,2,3"} {
		if _, err := parseContentLightLevel(s); err == nil {
			t.Errorf("expected error for %q", s)
		}
	}
}

func TestParseHDRFlagsEndToEnd(t *testing.T) {
	cfg, err := ParseArgs([]string{
		"-i", "in.mp4",
		"-c:v", "libx265",
		"-color_trc", "smpte2084",
		"-mastering_display_metadata",
		"G(8500,39850)B(6550,2300)R(35400,14600)WP(15635,16450)L(10000000,1)",
		"-content_light_level", "1000,400",
		"-f", "mp4", "out.mp4",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out := cfg.Outputs[0]
	if out.HDR == nil || out.HDR.MasteringDisplay == nil || out.HDR.ContentLightLevel == nil {
		t.Fatalf("hdr not fully attached: %+v", out.HDR)
	}
	if out.HDR.ContentLightLevel.MaxCLL != 1000 {
		t.Errorf("MaxCLL: %+v", out.HDR.ContentLightLevel)
	}
	if out.HDR.MasteringDisplay.DisplayPrimariesRX != 35400 {
		t.Errorf("R primaries: %+v", out.HDR.MasteringDisplay)
	}
	if out.Color == nil || out.Color.Transfer != "smpte2084" {
		t.Errorf("color.transfer: %+v", out.Color)
	}
}

func TestParseDoViFlags(t *testing.T) {
	cfg, err := ParseArgs([]string{
		"-i", "in.mp4",
		"-c:v", "libx265",
		"-dovi_profile", "8",
		"-dovi_level", "9",
		"-dovi_bl_compatibility_id", "1",
		"-dovi_el_present", "0",
		"-dovi_rpu_present", "true",
		"-f", "mp4", "out.mp4",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out := cfg.Outputs[0]
	if out.HDR == nil || out.HDR.DoVi == nil {
		t.Fatalf("dovi not attached: %+v", out.HDR)
	}
	dv := out.HDR.DoVi
	if dv.Profile != 8 || dv.Level != 9 || dv.BLCompatibilityID != 1 {
		t.Fatalf("dovi mismatch: %+v", dv)
	}
	if dv.RPUPresent == nil || !*dv.RPUPresent {
		t.Fatalf("rpu_present: %+v", dv.RPUPresent)
	}
	if dv.ELPresent {
		t.Fatalf("el_present: want false, got true")
	}
}

func TestParseDoViRejectsBadProfile(t *testing.T) {
	// Parser accepts any uint8; profile-set validation lives in
	// pipeline.validate. Verify the value lands intact so the
	// pipeline validator can reject it downstream.
	cfg, err := ParseArgs([]string{
		"-i", "in.mp4", "-c:v", "libx265",
		"-dovi_profile", "6",
		"-f", "mp4", "out.mp4",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Outputs[0].HDR == nil || cfg.Outputs[0].HDR.DoVi == nil ||
		cfg.Outputs[0].HDR.DoVi.Profile != 6 {
		t.Fatalf("profile=6 not retained for downstream validator: %+v", cfg.Outputs[0].HDR)
	}
}
