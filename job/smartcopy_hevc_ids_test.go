// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"bytes"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/cbs"
)

func TestBitBufUE(t *testing.T) {
	var w bitBuf
	for _, v := range []uint32{0, 1, 2, 3, 7, 8, 15, 63} {
		w.ue(v)
	}
	// Decode the codes back independently: count leading zeros, then read
	// that many bits plus one.
	pos := 0
	for _, want := range []uint32{0, 1, 2, 3, 7, 8, 15, 63} {
		zeros := 0
		for getBits(w.b, pos, 1) == 0 {
			zeros++
			pos++
		}
		v := getBits(w.b, pos, zeros+1) - 1
		pos += zeros + 1
		if v != want {
			t.Fatalf("ue round-trip: got %d, want %d", v, want)
		}
	}
}

func TestHvcCWithParamSets(t *testing.T) {
	vps := []byte{0x40, 0x01, 0x0c}
	sps := []byte{0x42, 0x01, 0x01}
	pps := []byte{0x44, 0x01, 0xc0}
	hvcC := make([]byte, 22)
	hvcC[0] = 1
	hvcC[21] = 0x0f
	hvcC = append(hvcC, 2)
	hvcC = append(hvcC, 0xa0, 0, 1, 0, byte(len(vps)))
	hvcC = append(hvcC, vps...)
	hvcC = append(hvcC, 0xa1, 0, 1, 0, byte(len(sps)))
	hvcC = append(hvcC, sps...)
	out, err := hvcCWithParamSets(hvcC, [][]byte{{0x40, 0x01, 0x1c}, {0x42, 0x01, 0x11}, pps})
	if err != nil {
		t.Fatal(err)
	}
	if out[22] != 3 {
		t.Fatalf("arrays = %d, want 3 (VPS, SPS, new PPS)", out[22])
	}
	nals, err := hvcCParamSets(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(nals) != 5 || !bytes.Equal(nals[0], vps) || !bytes.Equal(nals[1], []byte{0x40, 0x01, 0x1c}) || !bytes.Equal(nals[4], pps) {
		t.Errorf("merged nals = %x", nals)
	}
	if isomNALLengthSize(cbs.CodecH265, out) != 4 {
		t.Errorf("length size changed")
	}
}

// TestHEVCRenumberRoundTrip renumbers real x265 parameter sets and a real
// slice, then parses the result: the ids must read back as assigned, the
// slice must still parse against the renumbered sets, and its slice data
// must be byte-identical.
func TestHEVCRenumberRoundTrip(t *testing.T) {
	if !av.FindEncoder("libx265") {
		t.Skip("libx265 not available")
	}
	enc, err := av.OpenEncoder(av.EncoderOptions{
		CodecName: "libx265", Width: 64, Height: 64, PixFmt: 0,
		FrameRate: [2]int{25, 1}, TimeBase: [2]int{1, 25}, GOPSize: 12, GlobalHeader: true,
		ExtraOpts: map[string]string{"preset": "ultrafast", "x265-params": "log-level=error"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	var orig [][]byte
	for _, n := range splitAnnexB(enc.ExtraData()) {
		if nalIsParamSet(cbs.CodecH265, n) {
			orig = append(orig, n)
		}
	}
	if len(orig) != 3 {
		t.Fatalf("x265 extradata has %d parameter sets, want 3", len(orig))
	}
	ids := hevcIDs{vps: 3, sps: 5, pps: 9}
	ren, err := hevcRenumberParamSets(orig, ids)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := cbs.New(cbs.CodecH265, nil)
	frag, err := c.ReadExtradata(joinNALsAnnexB(ren))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for i := range frag.Units {
		if frag.Units[i].Err != nil {
			t.Fatalf("renumbered unit %d: %v", i, frag.Units[i].Err)
		}
		switch ps := frag.Units[i].Content.(type) {
		case *cbs.H265RawVPS:
			seen["vps"] = int(ps.VpsVideoParameterSetID)
		case *cbs.H265RawSPS:
			seen["sps"] = int(ps.SpsSeqParameterSetID)
			if int(ps.SpsVideoParameterSetID) != ids.vps {
				t.Errorf("SPS refers to VPS %d, want %d", ps.SpsVideoParameterSetID, ids.vps)
			}
		case *cbs.H265RawPPS:
			seen["pps"] = int(ps.PpsPicParameterSetID)
			if int(ps.PpsSeqParameterSetID) != ids.sps {
				t.Errorf("PPS refers to SPS %d, want %d", ps.PpsSeqParameterSetID, ids.sps)
			}
		}
	}
	if seen["vps"] != ids.vps || seen["sps"] != ids.sps || seen["pps"] != ids.pps {
		t.Fatalf("renumbered ids %v, want %+v", seen, ids)
	}

	// One encoded frame → slices referring to PPS 0; rewrite to PPS 9.
	fr := av.NewTestFrame(t, 64, 64, 0)
	defer fr.Close()
	av.FillTestFrameYChecker(fr)
	fr.SetPTS(0)
	if err := enc.SendFrame(fr); err != nil {
		t.Fatal(err)
	}
	if err := enc.Flush(); err != nil && !av.IsEOF(err) && !av.IsEAgain(err) {
		t.Fatal(err)
	}
	pkt, _ := av.AllocPacket()
	defer pkt.Close()
	if err := enc.ReceivePacket(pkt); err != nil {
		t.Fatalf("ReceivePacket: %v", err)
	}
	r, err := newHEVCSliceRenumberer(orig, ids.pps)
	if err != nil {
		t.Fatal(err)
	}
	nals, err := r.rewritePacket(pkt.Data())
	if err != nil {
		t.Fatal(err)
	}
	c2, _ := cbs.New(cbs.CodecH265, nil)
	if _, err := c2.ReadExtradata(joinNALsAnnexB(orig)); err != nil {
		t.Fatal(err)
	}
	origFrag, _ := c2.ReadPacket(pkt.Data())
	after, err := c.ReadPacket(joinNALsAnnexB(nals))
	if err != nil {
		t.Fatal(err)
	}
	slices := 0
	for i := range after.Units {
		sl, ok := after.Units[i].Content.(*cbs.H265RawSlice)
		if !ok {
			continue
		}
		if after.Units[i].Err != nil {
			t.Fatalf("rewritten slice: %v", after.Units[i].Err)
		}
		if int(sl.Header.SlicePicParameterSetID) != ids.pps {
			t.Errorf("rewritten slice refers to PPS %d, want %d", sl.Header.SlicePicParameterSetID, ids.pps)
		}
		osl, ok := origFrag.Units[i].Content.(*cbs.H265RawSlice)
		if !ok || !bytes.Equal(osl.Data, sl.Data) || sl.DataBitStart != 0 {
			t.Errorf("slice %d data changed by the rewrite", i)
		}
		slices++
	}
	if slices == 0 {
		t.Fatal("no slices in the encoded packet")
	}
}

func TestLastOneBit(t *testing.T) {
	for _, tc := range []struct {
		buf  []byte
		want int
	}{
		{[]byte{0x80}, 0}, {[]byte{0x84}, 5}, {[]byte{0x01}, 7}, {[]byte{0xff, 0x00}, 7}, {[]byte{0x00, 0x10}, 11}, {[]byte{0, 0}, -1},
	} {
		if got := lastOneBit(tc.buf); got != tc.want {
			t.Errorf("lastOneBit(%x) = %d, want %d", tc.buf, got, tc.want)
		}
	}
}
