// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"bytes"
	"testing"

	"github.com/MediaMolder/MediaMolder/cbs"
)

func TestSplitAnnexBAndReframe(t *testing.T) {
	// 4-byte start code + SPS stub, 3-byte start code + slice stub with a
	// trailing zero (trailing_zero_8bits), then an SEI.
	in := []byte{
		0, 0, 0, 1, 0x67, 0xAA, 0xBB,
		0, 0, 1, 0x65, 0x88, 0x00,
		0, 0, 1, 0x06, 0x05, 0x01,
	}
	nals := splitAnnexB(in)
	if len(nals) != 3 {
		t.Fatalf("got %d NALs, want 3: %x", len(nals), nals)
	}
	if !bytes.Equal(nals[1], []byte{0x65, 0x88}) {
		t.Errorf("slice NAL = %x, want 6588 (trailing zero stripped)", nals[1])
	}
	lp, err := annexBToLengthPrefixed(in, 4)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{
		0, 0, 0, 3, 0x67, 0xAA, 0xBB,
		0, 0, 0, 2, 0x65, 0x88,
		0, 0, 0, 3, 0x06, 0x05, 0x01,
	}
	if !bytes.Equal(lp, want) {
		t.Errorf("length-prefixed = %x, want %x", lp, want)
	}
	back, err := lengthPrefixedNALs(lp, 4)
	if err != nil || len(back) != 3 {
		t.Fatalf("lengthPrefixedNALs: %v (%d)", err, len(back))
	}
	annexB, err := joinNALs(back, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(annexB, []byte{0, 0, 0, 1, 0x67, 0xAA, 0xBB, 0, 0, 0, 1, 0x65, 0x88, 0, 0, 0, 1, 0x06, 0x05, 0x01}) {
		t.Errorf("joinNALs(annexb) = %x", annexB)
	}
	if _, err := lengthPrefixedNALs([]byte{0, 0, 0, 9, 1}, 4); err == nil {
		t.Errorf("truncated NALFF accepted")
	}
	// A NAL that does not fit a short length prefix is refused, never wrapped.
	big := make([]byte, 300)
	big[0] = 0x65
	if _, err := joinNALs([][]byte{big}, 1); err == nil {
		t.Errorf("300-byte NAL accepted with a 1-byte prefix")
	}
	if out, err := joinNALs([][]byte{big}, 2); err != nil || len(out) != 302 || out[0] != 0x01 || out[1] != 0x2c {
		t.Errorf("2-byte prefix: %v, %d bytes, prefix %x", err, len(out), out[:2])
	}
	if _, err := joinNALs([][]byte{make([]byte, 1<<16)}, 2); err == nil {
		t.Errorf("65536-byte NAL accepted with a 2-byte prefix")
	}
}

func TestAvcCWithParamSets(t *testing.T) {
	sps0 := []byte{0x67, 0x64, 0x00, 0x1e, 0xac}
	pps0 := []byte{0x68, 0xeb, 0xe3, 0xcb}
	tail := []byte{0xfd, 0xf8, 0xf8, 0x00} // High-profile extension bytes
	avcC := append([]byte{1, 0x64, 0, 0x1e, 0xff, 0xe1, 0, byte(len(sps0))}, sps0...)
	avcC = append(avcC, 1, 0, byte(len(pps0)))
	avcC = append(avcC, pps0...)
	avcC = append(avcC, tail...)

	if n := isomNALLengthSize(cbs.CodecH264, avcC); n != 4 {
		t.Errorf("lengthSize = %d, want 4", n)
	}
	if n := isomNALLengthSize(cbs.CodecH264, []byte{0, 0, 0, 1, 0x67}); n != 0 {
		t.Errorf("Annex B lengthSize = %d, want 0", n)
	}
	sps1 := []byte{0x67, 0x64, 0x00, 0x1e, 0xac, 0x11}
	pps1 := []byte{0x68, 0xee, 0x3c, 0xb0}
	merged, err := avcCWithParamSets(avcC, [][]byte{sps1}, [][]byte{pps1})
	if err != nil {
		t.Fatal(err)
	}
	sps, pps, gotTail, err := avcCParamSets(merged)
	if err != nil {
		t.Fatal(err)
	}
	if len(sps) != 2 || !bytes.Equal(sps[0], sps0) || !bytes.Equal(sps[1], sps1) {
		t.Errorf("merged SPS = %x", sps)
	}
	if len(pps) != 2 || !bytes.Equal(pps[0], pps0) || !bytes.Equal(pps[1], pps1) {
		t.Errorf("merged PPS = %x", pps)
	}
	if !bytes.Equal(gotTail, tail) {
		t.Errorf("tail = %x, want %x", gotTail, tail)
	}
	if !bytes.Equal(merged[:5], avcC[:5]) || merged[5] != 0xE2 {
		t.Errorf("header = %x", merged[:6])
	}
}

func TestHvcCParamSets(t *testing.T) {
	vps := []byte{0x40, 0x01, 0x0c}
	sps := []byte{0x42, 0x01, 0x01}
	pps := []byte{0x44, 0x01, 0xc0}
	hvcC := make([]byte, 22)
	hvcC[0] = 1
	hvcC[21] = 0x0f // lengthSizeMinusOne = 3
	hvcC = append(hvcC, 3)
	for _, a := range []struct {
		typ byte
		nal []byte
	}{{32, vps}, {33, sps}, {34, pps}} {
		hvcC = append(hvcC, 0x80|a.typ, 0, 1, 0, byte(len(a.nal)))
		hvcC = append(hvcC, a.nal...)
	}
	if n := isomNALLengthSize(cbs.CodecH265, hvcC); n != 4 {
		t.Errorf("lengthSize = %d, want 4", n)
	}
	nals, err := hvcCParamSets(hvcC)
	if err != nil {
		t.Fatal(err)
	}
	if len(nals) != 3 || !bytes.Equal(nals[0], vps) || !bytes.Equal(nals[1], sps) || !bytes.Equal(nals[2], pps) {
		t.Errorf("nals = %x", nals)
	}
	for _, n := range nals {
		if !nalIsParamSet(cbs.CodecH265, n) {
			t.Errorf("%x not recognised as a parameter set", n)
		}
	}
}

func TestHevcCRAToBLAAndPrepend(t *testing.T) {
	cra := []byte{hevcNALCRA << 1, 0x01, 0xaf, 0x2e}
	sei := []byte{39 << 1, 0x01, 0x05, 0x00}
	pkt, _ := joinNALs([][]byte{sei, cra}, 4)
	if packetFirstVCLType(cbs.CodecH265, pkt, 4) != hevcNALCRA {
		t.Fatalf("first VCL type = %d", packetFirstVCLType(cbs.CodecH265, pkt, 4))
	}
	if !hevcCRAToBLA(pkt, 4) {
		t.Fatalf("no CRA rewritten")
	}
	nals, _ := lengthPrefixedNALs(pkt, 4)
	if hevcNALType(nals[1]) != hevcNALBLANLP || nals[1][1] != 0x01 {
		t.Errorf("rewritten NAL header = %x", nals[1][:2])
	}
	if hevcCRAToBLA(pkt, 4) {
		t.Errorf("second rewrite changed something")
	}
	if packetHasParamSets(cbs.CodecH265, pkt, 4) {
		t.Errorf("packet without parameter sets reported as having them")
	}
	ps := [][]byte{{0x40, 0x01, 0x0c}, {0x42, 0x01, 0x01}, {0x44, 0x01, 0xc0}}
	with, err := prependNALs(cbs.CodecH265, pkt, 4, ps)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := lengthPrefixedNALs(with, 4)
	if len(got) != 5 || !bytes.Equal(got[0], ps[0]) || !bytes.Equal(got[3], sei) || !bytes.Equal(got[4], nals[1]) {
		t.Errorf("prepended packet NALs = %x", got)
	}
	if !packetHasParamSets(cbs.CodecH265, with, 4) {
		t.Errorf("parameter sets not detected after prepend")
	}
	// An AUD stays first.
	aud := []byte{hevcNALAUD << 1, 0x01, 0x50}
	audPkt, _ := joinNALs([][]byte{aud, cra}, 4)
	withAUD, _ := prependNALs(cbs.CodecH265, audPkt, 4, ps[:1])
	got, _ = lengthPrefixedNALs(withAUD, 4)
	if len(got) != 3 || !bytes.Equal(got[0], aud) || !bytes.Equal(got[1], ps[0]) {
		t.Errorf("AUD ordering = %x", got)
	}
}

func TestEscapeRBSPAndBitOps(t *testing.T) {
	rbsp := []byte{0x65, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x03, 0xff, 0x00, 0x00}
	esc := escapeRBSP(rbsp)
	want := []byte{0x65, 0x00, 0x00, 0x03, 0x01, 0x00, 0x00, 0x03, 0x00, 0x03, 0xff, 0x00, 0x00}
	if !bytes.Equal(esc, want) {
		t.Errorf("escapeRBSP = %x, want %x", esc, want)
	}
	for v, n := range map[uint32]int{0: 1, 1: 3, 2: 3, 3: 5, 6: 5, 7: 7, 14: 7, 15: 9} {
		if got := ueLen(v); got != n {
			t.Errorf("ueLen(%d) = %d, want %d", v, got, n)
		}
	}
	buf := []byte{0xff, 0x00, 0xff}
	setBits(buf, 5, 6, 0x2a) // 101010 across the byte boundary
	if got := getBits(buf, 5, 6); got != 0x2a {
		t.Errorf("getBits after setBits = %#x", got)
	}
	if buf[0] != 0xfd || buf[1] != 0x40 || buf[2] != 0xff {
		t.Errorf("buf = %x", buf)
	}
}

func TestMergeX264Params(t *testing.T) {
	got := mergeX264Params(map[string]string{"crf": "18", "x264-params": "aq-mode=2"}, []string{"sps-id=1", "bframes=1"})
	if got["x264-params"] != "aq-mode=2:sps-id=1:bframes=1" || got["crf"] != "18" {
		t.Errorf("merged = %v", got)
	}
	if mergeX264Params(nil, []string{"sps-id=3"})["x264-params"] != "sps-id=3" {
		t.Errorf("empty base merge failed")
	}
}
