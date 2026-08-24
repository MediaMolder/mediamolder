// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package cbs

import (
	"bytes"
	"testing"
)

func TestExtractRBSPNoEscape(t *testing.T) {
	src := []byte{0x67, 0x42, 0x00, 0x1e, 0xab}
	rbsp, epb, consumed := extractRBSP(src)
	if !bytes.Equal(rbsp, src) || consumed != len(src) || len(epb) != 0 {
		t.Fatalf("got rbsp=%x epb=%v consumed=%d", rbsp, epb, consumed)
	}
}

func TestExtractRBSPEscapes(t *testing.T) {
	cases := []struct {
		src  []byte
		want []byte
		epb  []int
	}{
		// 00 00 03 00 → 00 00 00
		{[]byte{0x11, 0x00, 0x00, 0x03, 0x00, 0x22}, []byte{0x11, 0x00, 0x00, 0x00, 0x22}, []int{2}},
		// 00 00 03 03 → 00 00 03
		{[]byte{0x00, 0x00, 0x03, 0x03, 0x7f}, []byte{0x00, 0x00, 0x03, 0x7f}, []int{1}},
		// two escapes
		{[]byte{0x00, 0x00, 0x03, 0x01, 0x00, 0x00, 0x03, 0x02},
			[]byte{0x00, 0x00, 0x01, 0x00, 0x00, 0x02}, []int{1, 4}},
	}
	for _, c := range cases {
		rbsp, epb, consumed := extractRBSP(c.src)
		if !bytes.Equal(rbsp, c.want) {
			t.Errorf("extractRBSP(%x): rbsp %x, want %x", c.src, rbsp, c.want)
		}
		if consumed != len(c.src) {
			t.Errorf("extractRBSP(%x): consumed %d, want %d", c.src, consumed, len(c.src))
		}
		if len(epb) != len(c.epb) {
			t.Errorf("extractRBSP(%x): epb %v, want %v", c.src, epb, c.epb)
			continue
		}
		for i := range epb {
			if epb[i] != c.epb[i] {
				t.Errorf("extractRBSP(%x): epb %v, want %v", c.src, epb, c.epb)
				break
			}
		}
	}
}

func TestExtractRBSPStopsAtStartCode(t *testing.T) {
	src := []byte{0x41, 0x42, 0x00, 0x00, 0x01, 0x99, 0x98}
	rbsp, _, consumed := extractRBSP(src)
	if !bytes.Equal(rbsp, []byte{0x41, 0x42}) {
		t.Fatalf("rbsp: %x", rbsp)
	}
	if consumed != 2 {
		t.Fatalf("consumed: %d", consumed)
	}
}

func TestPacketSplitAnnexB(t *testing.T) {
	// zero_byte + start code + SPS header byte, then 3-byte start code +
	// non-IDR slice header byte.
	buf := []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0xAA, 0xBB,
		0x00, 0x00, 0x01, 0x41, 0xCC,
	}
	nals, err := h2645PacketSplit(buf, CodecH264, 0, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(nals) != 2 {
		t.Fatalf("got %d NALs, want 2", len(nals))
	}
	if nals[0].typ != 7 || nals[0].offset != 4 || nals[0].prefix != 4 {
		t.Errorf("nal 0: %+v", nals[0])
	}
	if nals[1].typ != 1 || nals[1].offset != 10 || nals[1].prefix != 3 {
		t.Errorf("nal 1: %+v", nals[1])
	}
	if nals[0].refIDC != 3 {
		t.Errorf("nal 0 ref_idc: %d", nals[0].refIDC)
	}
}

func TestPacketSplitNALFF(t *testing.T) {
	buf := []byte{
		0x00, 0x00, 0x00, 0x03, 0x67, 0xAA, 0xBB, // 4-byte length = 3
		0x00, 0x00, 0x00, 0x02, 0x41, 0xCC, // length = 2
	}
	nals, err := h2645PacketSplit(buf, CodecH264, 4, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(nals) != 2 {
		t.Fatalf("got %d NALs, want 2", len(nals))
	}
	if nals[0].typ != 7 || nals[0].offset != 4 || nals[0].raw != 3 {
		t.Errorf("nal 0: %+v", nals[0])
	}
	if nals[1].typ != 1 || nals[1].offset != 11 || nals[1].raw != 2 {
		t.Errorf("nal 1: %+v", nals[1])
	}
}

func TestPacketSplitNALFFBadSize(t *testing.T) {
	buf := []byte{0x00, 0x00, 0x00, 0xFF, 0x67, 0xAA}
	_, err := h2645PacketSplit(buf, CodecH264, 4, true, nil)
	if err == nil {
		t.Fatal("expected invalid NAL size error")
	}
}

func TestPacketSplitNoStartCode(t *testing.T) {
	buf := []byte{0x11, 0x22, 0x33, 0x44, 0x55}
	_, err := h2645PacketSplit(buf, CodecH264, 0, false, nil)
	if err == nil {
		t.Fatal("expected no-start-code error")
	}
}

func TestPacketSplitForbiddenBit(t *testing.T) {
	buf := []byte{0x00, 0x00, 0x01, 0x80, 0x11, 0x22} // forbidden_zero_bit set
	nals, err := h2645PacketSplit(buf, CodecH264, 0, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(nals) != 1 || nals[0].skip != SkipBadNALHeader {
		t.Fatalf("got %+v", nals)
	}
}

func TestHEVCLayer63Dropped(t *testing.T) {
	// nuh_layer_id == 63: forbidden(0) type(32=VPS:100000) layer(111111) tid+1(001)
	// bits: 0 100000 111111 001 → bytes 0x41 0xF9
	buf := []byte{0x00, 0x00, 0x01, 0x41, 0xF9, 0x11, 0x22}
	nals, err := h2645PacketSplit(buf, CodecH265, 0, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(nals) != 1 || nals[0].skip != SkipLayerID63 {
		t.Fatalf("got %+v", nals)
	}
}

func TestAVCCExtradata(t *testing.T) {
	// Minimal avcC: version 1, profile/compat/level, lengthSizeMinusOne=3,
	// 1 SPS of 4 bytes, 1 PPS of 2 bytes, no trailing bytes.
	xd := []byte{
		1, 0x64, 0x00, 0x1e, 0xff,
		0xe1, 0x00, 0x04, 0x67, 0x64, 0x00, 0x1e,
		0x01, 0x00, 0x02, 0x68, 0xee,
	}
	h := newH264Context(nil)
	frag := &Fragment{Data: xd}
	if err := h.splitFragment(frag, true); err != nil {
		t.Fatal(err)
	}
	if h.nalLengthSize != 4 || !h.mp4 {
		t.Fatalf("nalLengthSize=%d mp4=%v", h.nalLengthSize, h.mp4)
	}
	if len(frag.Units) != 2 {
		t.Fatalf("units: %d", len(frag.Units))
	}
	if frag.Units[0].Type != 7 || frag.Units[1].Type != 8 {
		t.Fatalf("types: %d %d", frag.Units[0].Type, frag.Units[1].Type)
	}
	if frag.Units[0].Offset != 8 || frag.Units[0].PrefixSize != 2 {
		t.Fatalf("sps offset/prefix: %d/%d", frag.Units[0].Offset, frag.Units[0].PrefixSize)
	}
}

func TestAVCCBadVersion(t *testing.T) {
	h := newH264Context(nil)
	frag := &Fragment{Data: []byte{2, 0, 0, 0, 0xff, 0xe0, 0}}
	if err := h.splitFragment(frag, true); err == nil {
		t.Fatal("expected invalid AVCC version error")
	}
}

func TestMissingPPSErrorIsolation(t *testing.T) {
	// A slice NAL referencing a PPS that was never seen: the unit gets an
	// error, parsing continues (no panic escapes).
	buf := []byte{0x00, 0x00, 0x01, 0x41, 0x9A, 0x00, 0x08, 0xBF}
	h := newH264Context(nil)
	frag, err := h.ReadPacket(buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(frag.Units) != 1 {
		t.Fatalf("units: %d", len(frag.Units))
	}
	if frag.Units[0].Err == nil {
		t.Fatal("expected a unit error for missing PPS")
	}
	if frag.Units[0].Decomposed {
		t.Fatal("unit must not be marked decomposed")
	}
}
