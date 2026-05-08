// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

import (
	"bytes"
	"testing"
)

// TestFrameSideDataGenericRoundTrip exercises the type-agnostic side data
// API: attach two different types simultaneously, verify SideData reads each
// one back, AllSideData enumerates both, RemoveSideData drops one without
// touching the other, and unknown types return nil instead of panicking.
func TestFrameSideDataGenericRoundTrip(t *testing.T) {
	f, err := AllocFrame()
	if err != nil {
		t.Fatalf("AllocFrame: %v", err)
	}
	defer f.Close()

	if got := f.SideData(FrameSideDataSEIUnregistered); got != nil {
		t.Fatalf("fresh frame SideData(SEIUnregistered) = %v, want nil", got)
	}
	if got := f.AllSideData(); got != nil {
		t.Fatalf("fresh frame AllSideData = %v, want nil", got)
	}

	// 16-byte UUID + payload, the canonical SEI unregistered shape.
	uuid := []byte("0123456789abcdef")
	seiPayload := append(append([]byte{}, uuid...), []byte(`{"hello":"world"}`)...)
	if err := f.AddSideData(FrameSideDataSEIUnregistered, seiPayload); err != nil {
		t.Fatalf("AddSideData(SEIUnregistered): %v", err)
	}

	// A second, unrelated type so we can prove RemoveSideData is type-scoped.
	a53 := []byte{0xfc, 0x80, 0x80, 0xfc, 0x91, 0x12}
	if err := f.AddSideData(FrameSideDataA53CC, a53); err != nil {
		t.Fatalf("AddSideData(A53CC): %v", err)
	}

	got := f.SideData(FrameSideDataSEIUnregistered)
	if len(got) != 1 || !bytes.Equal(got[0], seiPayload) {
		t.Fatalf("SideData(SEIUnregistered) = %x, want %x", got, seiPayload)
	}
	gotA53 := f.SideData(FrameSideDataA53CC)
	if len(gotA53) != 1 || !bytes.Equal(gotA53[0], a53) {
		t.Fatalf("SideData(A53CC) = %x, want %x", gotA53, a53)
	}

	all := f.AllSideData()
	if len(all) != 2 {
		t.Fatalf("AllSideData len = %d, want 2 (got %+v)", len(all), all)
	}
	seenSEI, seenA53 := false, false
	for _, e := range all {
		switch e.Type {
		case FrameSideDataSEIUnregistered:
			if !bytes.Equal(e.Data, seiPayload) {
				t.Errorf("AllSideData SEI = %x, want %x", e.Data, seiPayload)
			}
			seenSEI = true
		case FrameSideDataA53CC:
			if !bytes.Equal(e.Data, a53) {
				t.Errorf("AllSideData A53 = %x, want %x", e.Data, a53)
			}
			seenA53 = true
		}
	}
	if !seenSEI || !seenA53 {
		t.Fatalf("AllSideData missing entries: seenSEI=%v seenA53=%v", seenSEI, seenA53)
	}

	// Remove only one type; the other must remain.
	f.RemoveSideData(FrameSideDataA53CC)
	if got := f.SideData(FrameSideDataA53CC); got != nil {
		t.Fatalf("after RemoveSideData(A53CC), SideData(A53CC) = %v, want nil", got)
	}
	if got := f.SideData(FrameSideDataSEIUnregistered); len(got) != 1 {
		t.Fatalf("RemoveSideData(A53CC) clobbered SEI side data: %v", got)
	}
}

// TestFrameSideDataRejectsBadInput pins down the validation behaviour of the
// public surface so future refactors don't accidentally start accepting
// nonsense (empty payloads, nil receiver, sub-UUID SEI Unregistered).
func TestFrameSideDataRejectsBadInput(t *testing.T) {
	f, err := AllocFrame()
	if err != nil {
		t.Fatalf("AllocFrame: %v", err)
	}
	defer f.Close()

	if err := f.AddSideData(FrameSideDataSEIUnregistered, nil); err == nil {
		t.Errorf("AddSideData(empty payload) returned nil, want EINVAL")
	}
	if err := f.AddSEIUnregisteredSideData([]byte("short")); err == nil {
		t.Errorf("AddSEIUnregisteredSideData(<16B) returned nil, want EINVAL")
	}

	var nilFrame *Frame
	if err := nilFrame.AddSideData(FrameSideDataSEIUnregistered, []byte("payload-bytes-16")); err == nil {
		t.Errorf("nil.AddSideData returned nil, want EINVAL")
	}
	if got := nilFrame.SideData(FrameSideDataSEIUnregistered); got != nil {
		t.Errorf("nil.SideData = %v, want nil", got)
	}
	if got := nilFrame.AllSideData(); got != nil {
		t.Errorf("nil.AllSideData = %v, want nil", got)
	}
	nilFrame.RemoveSideData(FrameSideDataSEIUnregistered) // must not panic.
}

// TestFrameSideDataS12MTimecodeRoundTrip exercises the typed S12M timecode
// helper, including the FFmpeg-defined wire layout (uint32 count + up to 3
// packed timecode words) and rejection of out-of-range counts.
func TestFrameSideDataS12MTimecodeRoundTrip(t *testing.T) {
	f, err := AllocFrame()
	if err != nil {
		t.Fatalf("AllocFrame: %v", err)
	}
	defer f.Close()

	if err := f.AddS12MTimecodes(); err == nil {
		t.Errorf("AddS12MTimecodes() with no args returned nil, want EINVAL")
	}
	if err := f.AddS12MTimecodes(1, 2, 3, 4); err == nil {
		t.Errorf("AddS12MTimecodes(>3) returned nil, want EINVAL")
	}

	want := []S12MTimecode{0xdeadbeef, 0xcafebabe}
	if err := f.AddS12MTimecodes(want...); err != nil {
		t.Fatalf("AddS12MTimecodes: %v", err)
	}

	got := f.S12MTimecodes()
	if len(got) != len(want) {
		t.Fatalf("S12MTimecodes len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("S12MTimecodes[%d] = %#x, want %#x", i, got[i], want[i])
		}
	}

	// The on-wire buffer must start with the count word in little-endian.
	raw := f.SideData(FrameSideDataS12MTimecode)
	if len(raw) != 1 || len(raw[0]) < 4 {
		t.Fatalf("raw S12M side data = %v, want 1 entry of >=4 bytes", raw)
	}
	header := uint32(raw[0][0]) | uint32(raw[0][1])<<8 | uint32(raw[0][2])<<16 | uint32(raw[0][3])<<24
	if header != uint32(len(want)) {
		t.Errorf("S12M count header = %d, want %d", header, len(want))
	}
}

// TestFrameSideDataTypeName guards against silently dropping the libavutil
// name lookup and documents which types we expose to callers.
func TestFrameSideDataTypeName(t *testing.T) {
	cases := []struct {
		typ  FrameSideDataType
		want string // a substring we expect FFmpeg's name to contain
	}{
		{FrameSideDataSEIUnregistered, "SEI"},
		{FrameSideDataA53CC, "A53"},
		{FrameSideDataS12MTimecode, "imecode"},
		{FrameSideDataMasteringDisplayMetadata, "Mastering"},
		{FrameSideDataContentLightLevel, "ight"},
	}
	for _, tc := range cases {
		got := tc.typ.Name()
		if got == "" {
			t.Errorf("FrameSideDataType(%d).Name() = \"\", want non-empty", tc.typ)
			continue
		}
		if !contains(got, tc.want) {
			t.Errorf("FrameSideDataType(%d).Name() = %q, want substring %q",
				tc.typ, got, tc.want)
		}
	}
}

func contains(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
