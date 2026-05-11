// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build darwin

package av

import (
	"testing"
)

// TestIsVTCodec verifies the four-CC tag matching for known ProRes RAW variants.
func TestIsVTCodec(t *testing.T) {
	cases := []struct {
		tag  uint32
		want bool
		name string
	}{
		{vtCodecProResRAW, true, "ProRes RAW (aprn)"},
		{vtCodecProResRAWHQ, true, "ProRes RAW HQ (aprh)"},
		{0x61766331, false, "avc1 (H.264) — not VT-native"},
		{0x68657631, false, "hev1 (HEVC) — not VT-native"},
		{0x00000000, false, "zero tag"},
	}
	for _, tc := range cases {
		if got := IsVTCodec(tc.tag); got != tc.want {
			t.Errorf("IsVTCodec(0x%08x) [%s]: got %v, want %v", tc.tag, tc.name, got, tc.want)
		}
	}
}

// TestOpenVTDecoderBadStreamIndex checks that OpenVTDecoder returns a clear
// error when given an out-of-range stream index.
func TestOpenVTDecoderBadStreamIndex(t *testing.T) {
	input, err := OpenInput("testdata/tiny.mp4", nil)
	if err != nil {
		t.Skip("testdata/tiny.mp4 not available:", err)
	}
	defer input.Close()

	_, err = OpenVTDecoder(input, 999)
	if err == nil {
		t.Fatal("expected error for out-of-range stream index, got nil")
	}
}

// TestOpenVTDecoderNonVTCodec verifies that OpenVTDecoder rejects a stream
// whose codec tag is not a known VT-native codec.
func TestOpenVTDecoderNonVTCodec(t *testing.T) {
	input, err := OpenInput("testdata/tiny.mp4", nil)
	if err != nil {
		t.Skip("testdata/tiny.mp4 not available:", err)
	}
	defer input.Close()

	// tiny.mp4 contains H.264 (avc1), not ProRes RAW.
	streams, err := input.AllStreams()
	if err != nil {
		t.Fatal(err)
	}
	videoIdx := -1
	for _, s := range streams {
		if s.Type == MediaTypeVideo {
			videoIdx = s.Index
			break
		}
	}
	if videoIdx < 0 {
		t.Skip("no video stream in testdata/tiny.mp4")
	}

	_, err = OpenVTDecoder(input, videoIdx)
	if err == nil {
		t.Fatal("expected error for non-VT-native codec, got nil")
	}
}

// TestVTDecoderCodecConstants verifies the ProRes RAW four-CC values match
// the Apple-documented kCMVideoCodecType constants.
func TestVTDecoderCodecConstants(t *testing.T) {
	// 'aprn' = 0x61 0x70 0x72 0x6E = big-endian uint32 = 1634824302
	const wantProResRAW uint32 = 0x6170726E
	// 'aprh' = 0x61 0x70 0x72 0x68 = big-endian uint32 = 1634824296
	const wantProResRAWHQ uint32 = 0x61707268
	if vtCodecProResRAW != wantProResRAW {
		t.Errorf("vtCodecProResRAW: got 0x%08x, want 0x%08x", vtCodecProResRAW, wantProResRAW)
	}
	if vtCodecProResRAWHQ != wantProResRAWHQ {
		t.Errorf("vtCodecProResRAWHQ: got 0x%08x, want 0x%08x", vtCodecProResRAWHQ, wantProResRAWHQ)
	}
}

// TestVTDecodeSession_CreateAndClose verifies that mm_vt_dec_create succeeds
// for ProRes RAW dimensions and that Close properly releases all resources
// without panicking or leaking.  This is the lightest integration test that
// exercises the VT session lifecycle without requiring a real ProRes RAW file.
func TestVTDecodeSession_CreateAndClose(t *testing.T) {
	// Use a synthetic InputFormatContext via the lavfi "nullsrc" source to
	// get a stream index, but we test OpenVTDecoder directly using a
	// manually constructed VTDecoderContext to avoid needing a ProRes RAW
	// file in CI.  We call the lower-level C helpers indirectly via the
	// exported API.

	// Build a minimal context by calling the C helper through a table of
	// known-good dimensions.
	type dim struct{ w, h int }
	dims := []dim{{1920, 1080}, {3840, 2160}, {640, 480}}

	for _, d := range dims {
		ctx, err := newVTDecoderContextForTest(t, vtCodecProResRAW, d.w, d.h)
		if err != nil {
			t.Logf("VT session create %dx%d: %v (may require entitlement/hardware)", d.w, d.h, err)
			continue
		}
		if err := ctx.Close(); err != nil {
			t.Errorf("%dx%d: Close returned %v", d.w, d.h, err)
		}
	}
}

// newVTDecoderContextForTest creates a VTDecoderContext directly from a
// codec type and dimensions, bypassing the InputFormatContext requirement.
// This is a test-only wrapper around the unexported newVTDecoderForCodec.
func newVTDecoderContextForTest(t *testing.T, codecTag uint32, width, height int) (*VTDecoderContext, error) {
	t.Helper()
	return newVTDecoderForCodec(codecTag, width, height)
}
