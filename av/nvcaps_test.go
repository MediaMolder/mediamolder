// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

import (
	"testing"
)

// TestSMGE covers boundary conditions of the compute-capability comparator.
func TestSMGE(t *testing.T) {
	cases := []struct {
		major, minor, reqMaj, reqMin int
		want                         bool
	}{
		// equal
		{8, 9, 8, 9, true},
		{7, 5, 7, 5, true},
		// major strictly greater
		{9, 0, 8, 9, true},
		{8, 0, 7, 5, true},
		// minor strictly greater (same major)
		{8, 9, 8, 6, true},
		{7, 5, 7, 0, true},
		// major strictly less
		{7, 5, 8, 0, false},
		{5, 0, 6, 0, false},
		// same major, minor less
		{8, 6, 8, 9, false},
		{7, 0, 7, 5, false},
	}
	for _, tc := range cases {
		got := smGE(tc.major, tc.minor, tc.reqMaj, tc.reqMin)
		if got != tc.want {
			t.Errorf("smGE(%d,%d, %d,%d) = %v; want %v",
				tc.major, tc.minor, tc.reqMaj, tc.reqMin, got, tc.want)
		}
	}
}

// TestNvidiaArchName verifies representative SM→architecture mappings.
func TestNvidiaArchName(t *testing.T) {
	cases := []struct {
		major, minor int
		want         string
	}{
		{10, 0, "Blackwell"},
		{9, 0, "Hopper"},
		{8, 9, "Ada Lovelace"},
		{8, 6, "Ampere"},
		{8, 0, "Ampere"},
		{7, 5, "Turing"},
		{7, 0, "Volta"},
		{6, 1, "Pascal"},
		{6, 0, "Pascal"},
		{5, 2, "Maxwell"},
		{5, 0, "Maxwell"},
		{3, 7, "Kepler"},
		{3, 0, "Kepler"},
		{2, 0, "Fermi"},
		{1, 0, ""},
	}
	for _, tc := range cases {
		got := nvidiaArchName(tc.major, tc.minor)
		if got != tc.want {
			t.Errorf("nvidiaArchName(%d,%d) = %q; want %q", tc.major, tc.minor, got, tc.want)
		}
	}
}

// buildCodecList is a helper that builds an HWCodecInfo slice from name:role pairs.
func buildCodecList(pairs ...string) []HWCodecInfo {
	out := make([]HWCodecInfo, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, HWCodecInfo{Name: pairs[i], Role: pairs[i+1]})
	}
	return out
}

// hasCodec reports whether a name appears in the codec list.
func hasCodec(list []HWCodecInfo, name string) bool {
	for _, c := range list {
		if c.Name == name {
			return true
		}
	}
	return false
}

// noteFor returns the Note for the named codec, or "" if absent.
func noteFor(list []HWCodecInfo, name string) string {
	for _, c := range list {
		if c.Name == name {
			return c.Note
		}
	}
	return ""
}

// TestFilterNVIDIACodecs_AdaLovelace verifies a full Ada Lovelace (SM 8.9)
// GPU: all codecs present, no limitations.
func TestFilterNVIDIACodecs_AdaLovelace(t *testing.T) {
	all := buildCodecList(
		"h264_nvenc", "encode",
		"hevc_nvenc", "encode",
		"av1_nvenc", "encode",
		"h264_cuvid", "decode",
		"hevc_cuvid", "decode",
		"av1_cuvid", "decode",
		"vp9_cuvid", "decode",
	)
	got := FilterNVIDIACodecs(8, 9, all)
	for _, name := range []string{"h264_nvenc", "hevc_nvenc", "av1_nvenc",
		"h264_cuvid", "hevc_cuvid", "av1_cuvid", "vp9_cuvid"} {
		if !hasCodec(got, name) {
			t.Errorf("Ada Lovelace: expected %s to be present", name)
		}
		if note := noteFor(got, name); note != "" {
			t.Errorf("Ada Lovelace: expected no note for %s, got %q", name, note)
		}
	}
}

// TestFilterNVIDIACodecs_Ampere verifies Ampere (SM 8.6): AV1 decode present,
// AV1 encode absent.
func TestFilterNVIDIACodecs_Ampere(t *testing.T) {
	all := buildCodecList(
		"h264_nvenc", "encode",
		"hevc_nvenc", "encode",
		"av1_nvenc", "encode", // should be removed
		"h264_cuvid", "decode",
		"hevc_cuvid", "decode",
		"av1_cuvid", "decode", // should be present
		"vp9_cuvid", "decode",
	)
	got := FilterNVIDIACodecs(8, 6, all)
	if hasCodec(got, "av1_nvenc") {
		t.Error("Ampere: av1_nvenc should be absent (requires Ada Lovelace SM 8.9+)")
	}
	if !hasCodec(got, "av1_cuvid") {
		t.Error("Ampere: av1_cuvid should be present (requires Ampere SM 8.0+)")
	}
}

// TestFilterNVIDIACodecs_Turing verifies Turing (SM 7.5): AV1 absent, HEVC
// notes cleared, VP9 decode present.
func TestFilterNVIDIACodecs_Turing(t *testing.T) {
	all := buildCodecList(
		"hevc_nvenc", "encode",
		"hevc_cuvid", "decode",
		"av1_cuvid", "decode", // should be removed
		"av1_nvenc", "encode", // should be removed
		"vp9_cuvid", "decode",
	)
	got := FilterNVIDIACodecs(7, 5, all)
	if hasCodec(got, "av1_cuvid") {
		t.Error("Turing: av1_cuvid should be absent (requires Ampere SM 8.0+)")
	}
	if hasCodec(got, "av1_nvenc") {
		t.Error("Turing: av1_nvenc should be absent (requires Ada SM 8.9+)")
	}
	if n := noteFor(got, "hevc_nvenc"); n != "" {
		t.Errorf("Turing: hevc_nvenc should have no note, got %q", n)
	}
	if n := noteFor(got, "hevc_cuvid"); n != "" {
		t.Errorf("Turing: hevc_cuvid should have no note, got %q", n)
	}
	if !hasCodec(got, "vp9_cuvid") {
		t.Error("Turing: vp9_cuvid should be present")
	}
}

// TestFilterNVIDIACodecs_Pascal verifies Pascal (SM 6.0): HEVC notes present,
// AV1 absent, VP9 decode present.
func TestFilterNVIDIACodecs_Pascal(t *testing.T) {
	all := buildCodecList(
		"hevc_nvenc", "encode",
		"hevc_cuvid", "decode",
		"av1_cuvid", "decode",
		"vp9_cuvid", "decode",
	)
	got := FilterNVIDIACodecs(6, 0, all)
	if hasCodec(got, "av1_cuvid") {
		t.Error("Pascal: av1_cuvid should be absent")
	}
	if n := noteFor(got, "hevc_nvenc"); n == "" {
		t.Error("Pascal: hevc_nvenc should have a 4:2:2 limitation note")
	}
	if n := noteFor(got, "hevc_cuvid"); n == "" {
		t.Error("Pascal: hevc_cuvid should have a 4:2:2 limitation note")
	}
	if !hasCodec(got, "vp9_cuvid") {
		t.Error("Pascal: vp9_cuvid should be present")
	}
}

// TestFilterNVIDIACodecs_Maxwell verifies Maxwell (SM 5.0): VP9 decode absent.
func TestFilterNVIDIACodecs_Maxwell(t *testing.T) {
	all := buildCodecList(
		"h264_nvenc", "encode",
		"hevc_nvenc", "encode",
		"vp9_cuvid", "decode", // requires Pascal SM 6.0
		"hevc_cuvid", "decode",
	)
	got := FilterNVIDIACodecs(5, 0, all)
	if hasCodec(got, "vp9_cuvid") {
		t.Error("Maxwell: vp9_cuvid should be absent (requires Pascal SM 6.0+)")
	}
	if !hasCodec(got, "hevc_nvenc") {
		t.Error("Maxwell: hevc_nvenc should be present")
	}
}

// TestFilterNVIDIACodecs_Kepler verifies Kepler (SM 3.0): HEVC encode absent.
func TestFilterNVIDIACodecs_Kepler(t *testing.T) {
	all := buildCodecList(
		"h264_nvenc", "encode",
		"hevc_nvenc", "encode", // requires Maxwell SM 5.0
		"vp9_cuvid", "decode",
	)
	got := FilterNVIDIACodecs(3, 0, all)
	if hasCodec(got, "hevc_nvenc") {
		t.Error("Kepler: hevc_nvenc should be absent (requires Maxwell SM 5.0+)")
	}
	if hasCodec(got, "vp9_cuvid") {
		t.Error("Kepler: vp9_cuvid should be absent (requires Pascal SM 6.0+)")
	}
	if !hasCodec(got, "h264_nvenc") {
		t.Error("Kepler: h264_nvenc should be present")
	}
}

// TestFilterNVIDIACodecs_UnknownCodec verifies that codecs not in nvCaps are
// passed through without filtering (forward-compatibility guarantee).
func TestFilterNVIDIACodecs_UnknownCodec(t *testing.T) {
	all := buildCodecList("future_codec_cuvid", "decode")
	got := FilterNVIDIACodecs(3, 0, all) // even Kepler
	if !hasCodec(got, "future_codec_cuvid") {
		t.Error("unknown codec should pass through unfiltered")
	}
}
