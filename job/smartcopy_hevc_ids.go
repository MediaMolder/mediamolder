// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"encoding/binary"
	"fmt"

	"github.com/MediaMolder/MediaMolder/cbs"
)

// HEVC parameter-set renumbering for smartcopy.
//
// x265 has no way to choose its VPS/SPS/PPS ids, so a re-encoded boundary
// run would reuse the source's ids and the output hvcC could carry only one
// of the two sets. Rewriting the ids in the boundary parameter sets and in
// every re-encoded slice header lets both live in one hvcC under hvc1, as
// libx264's sps-id does for H.264. The fields are Exp-Golomb (except the
// 4-bit VPS id), so the rest of the header shifts: parameter sets end in
// rbsp_trailing_bits, which are re-padded, and slice segment headers end
// in byte_alignment() before the byte-aligned slice data, which is
// re-padded so the data stays aligned.

// hevcIDs are the ids given to a boundary run's parameter sets.
type hevcIDs struct {
	vps, sps, pps int
}

// bitBuf is an MSB-first bit writer.
type bitBuf struct {
	b []byte
	n int // bits written
}

func (w *bitBuf) put(v uint32, width int) {
	for i := width - 1; i >= 0; i-- {
		if w.n%8 == 0 {
			w.b = append(w.b, 0)
		}
		if (v>>uint(i))&1 != 0 {
			w.b[w.n/8] |= 0x80 >> uint(w.n%8)
		}
		w.n++
	}
}

func (w *bitBuf) ue(v uint32) {
	n := ueLen(v)
	w.put(v+1, n) // leading zeros come from the width
}

// copyBits appends src bits [from, to).
func (w *bitBuf) copyBits(src []byte, from, to int) {
	for i := from; i < to; i++ {
		w.put(getBits(src, i, 1), 1)
	}
}

// bytes returns the written bits padded with zeros to a byte boundary.
func (w *bitBuf) bytes() []byte { return w.b }

// lastOneBit returns the position (MSB-first) of the last 1 bit in buf,
// or -1: for a parameter set that is the rbsp_stop_one_bit.
func lastOneBit(buf []byte) int {
	for i := len(buf) - 1; i >= 0; i-- {
		if buf[i] != 0 {
			for b := 0; b <= 7; b++ { // least significant bit = latest position
				if buf[i]&(1<<uint(b)) != 0 {
					return i*8 + (7 - b)
				}
			}
		}
	}
	return -1
}

// tracerRecorder collects traced elements per unit.
type tracerRecorder struct {
	unit  int
	elems map[int][]cbs.Element
}

func (t *tracerRecorder) Header(string) {}
func (t *tracerRecorder) Element(e cbs.Element) {
	if t.elems == nil {
		t.elems = map[int][]cbs.Element{}
	}
	t.elems[t.unit] = append(t.elems[t.unit], e)
}
func (t *tracerRecorder) Diag(cbs.Level, string) {}
func (t *tracerRecorder) BeginUnit(i int)        { t.unit = i }
func (t *tracerRecorder) EndUnit(int)            {}

func (t *tracerRecorder) find(unit int, name string) (cbs.Element, bool) {
	for _, e := range t.elems[unit] {
		if e.Name == name {
			return e, true
		}
	}
	return cbs.Element{}, false
}

// hevcUsedIDs returns the VPS, SPS and PPS ids declared by extradata (hvcC
// or Annex B).
func hevcUsedIDs(extradata []byte) (vps, sps, pps map[int]bool, err error) {
	c, err := cbs.New(cbs.CodecH265, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	frag, err := c.ReadExtradata(extradata)
	if err != nil {
		return nil, nil, nil, err
	}
	vps, sps, pps = map[int]bool{}, map[int]bool{}, map[int]bool{}
	for i := range frag.Units {
		switch c := frag.Units[i].Content.(type) {
		case *cbs.H265RawVPS:
			vps[int(c.VpsVideoParameterSetID)] = true
		case *cbs.H265RawSPS:
			sps[int(c.SpsSeqParameterSetID)] = true
		case *cbs.H265RawPPS:
			pps[int(c.PpsPicParameterSetID)] = true
		}
	}
	return vps, sps, pps, nil
}

// freeHEVCIDs picks the lowest ids not used by the source.
func freeHEVCIDs(extradata []byte) (hevcIDs, error) {
	vps, sps, pps, err := hevcUsedIDs(extradata)
	if err != nil {
		return hevcIDs{}, err
	}
	pick := func(used map[int]bool, max int) int {
		for id := 0; id < max; id++ {
			if !used[id] {
				return id
			}
		}
		return -1
	}
	ids := hevcIDs{vps: pick(vps, 16), sps: pick(sps, 16), pps: pick(pps, 64)}
	if ids.vps < 0 || ids.sps < 0 || ids.pps < 0 {
		return hevcIDs{}, fmt.Errorf("source hvcC leaves no free VPS/SPS/PPS id")
	}
	return ids, nil
}

// hevcRenumberParamSets rewrites the ids of the VPS/SPS/PPS NAL units in
// nals (Annex B payloads from the encoder's extradata) to ids and returns
// the rewritten units in the same order. Other NAL types pass through.
func hevcRenumberParamSets(nals [][]byte, ids hevcIDs) ([][]byte, error) {
	rec := &tracerRecorder{}
	c, err := cbs.New(cbs.CodecH265, rec)
	if err != nil {
		return nil, err
	}
	frag, err := c.ReadExtradata(joinNALsAnnexB(nals))
	if err != nil {
		return nil, fmt.Errorf("parse boundary parameter sets: %w", err)
	}
	if len(frag.Units) != len(nals) {
		return nil, fmt.Errorf("parse boundary parameter sets: %d units for %d NALs", len(frag.Units), len(nals))
	}
	out := make([][]byte, len(nals))
	for i := range frag.Units {
		u := &frag.Units[i]
		if u.Err != nil {
			return nil, fmt.Errorf("parse boundary parameter set %d: %w", i, u.Err)
		}
		var w bitBuf
		switch u.Content.(type) {
		case *cbs.H265RawVPS:
			rbsp := append([]byte(nil), u.RBSP...)
			setBits(rbsp, 16, 4, uint32(ids.vps))
			out[i] = escapeRBSP(rbsp)
			continue
		case *cbs.H265RawSPS:
			e, ok := rec.find(i, "sps_seq_parameter_set_id")
			if !ok {
				return nil, fmt.Errorf("SPS: sps_seq_parameter_set_id not traced")
			}
			stop := lastOneBit(u.RBSP)
			w.copyBits(u.RBSP, 0, 16)
			w.put(uint32(ids.vps), 4)
			w.copyBits(u.RBSP, 20, e.Position)
			w.ue(uint32(ids.sps))
			w.copyBits(u.RBSP, e.Position+e.Length, stop+1)
		case *cbs.H265RawPPS:
			pps := u.Content.(*cbs.H265RawPPS)
			pos := 16
			oldLen := ueLen(uint32(pps.PpsPicParameterSetID)) + ueLen(uint32(pps.PpsSeqParameterSetID))
			stop := lastOneBit(u.RBSP)
			w.copyBits(u.RBSP, 0, pos)
			w.ue(uint32(ids.pps))
			w.ue(uint32(ids.sps))
			w.copyBits(u.RBSP, pos+oldLen, stop+1)
		default:
			out[i] = nals[i]
			continue
		}
		out[i] = escapeRBSP(w.bytes())
	}
	return out, nil
}

// hevcSliceRenumberer rewrites slice_pic_parameter_set_id in re-encoded
// slice NAL units. Its cbs context holds the encoder's original parameter
// sets so the slice headers parse.
type hevcSliceRenumberer struct {
	codec cbs.Codec
	pps   int
}

func newHEVCSliceRenumberer(encParamSets [][]byte, ppsID int) (*hevcSliceRenumberer, error) {
	c, err := cbs.New(cbs.CodecH265, nil)
	if err != nil {
		return nil, err
	}
	if _, err := c.ReadExtradata(joinNALsAnnexB(encParamSets)); err != nil {
		return nil, fmt.Errorf("parse boundary parameter sets: %w", err)
	}
	return &hevcSliceRenumberer{codec: c, pps: ppsID}, nil
}

// rewritePacket renumbers every slice NAL of an Annex B packet and returns
// the NAL units (slices rewritten, others unchanged).
func (r *hevcSliceRenumberer) rewritePacket(pkt []byte) ([][]byte, error) {
	frag, err := r.codec.ReadPacket(pkt)
	if err != nil && frag == nil {
		return nil, fmt.Errorf("parse re-encoded packet: %w", err)
	}
	out := make([][]byte, 0, len(frag.Units))
	for i := range frag.Units {
		u := &frag.Units[i]
		raw := frag.Data[u.Offset : u.Offset+u.RawSize]
		for len(raw) > 0 && raw[len(raw)-1] == 0 {
			raw = raw[:len(raw)-1]
		}
		sl, ok := u.Content.(*cbs.H265RawSlice)
		if !ok {
			out = append(out, raw)
			continue
		}
		if u.Err != nil {
			return nil, fmt.Errorf("parse re-encoded slice: %w", u.Err)
		}
		nal, err := hevcRewriteSlicePPSID(u, sl, r.pps)
		if err != nil {
			return nil, err
		}
		out = append(out, nal)
	}
	return out, nil
}

// hevcRewriteSlicePPSID rewrites one slice segment header's
// slice_pic_parameter_set_id, keeping the slice data byte-aligned.
func hevcRewriteSlicePPSID(u *cbs.Unit, sl *cbs.H265RawSlice, ppsID int) ([]byte, error) {
	rbsp := u.RBSP
	dataStart := (len(rbsp) - len(sl.Data)) * 8
	if sl.DataBitStart != 0 || dataStart <= 16 {
		return nil, fmt.Errorf("slice data not byte-aligned")
	}
	// byte_alignment(): alignment_bit_equal_to_one then zeros to the byte
	// boundary, so the header ends at the last 1 bit before the data.
	align := dataStart - 1
	for align > 16 && getBits(rbsp, align, 1) == 0 {
		align--
	}
	idPos := 16 + 1
	if t := u.Type; t >= hevcNALBLAWLP && t <= 23 {
		idPos++ // no_output_of_prior_pics_flag
	}
	oldLen := ueLen(uint32(sl.Header.SlicePicParameterSetID))
	if got := getBits(rbsp, idPos, oldLen); got != (uint32(sl.Header.SlicePicParameterSetID)+1)&(1<<uint(oldLen)-1) {
		return nil, fmt.Errorf("slice_pic_parameter_set_id position mismatch")
	}
	var w bitBuf
	w.copyBits(rbsp, 0, idPos)
	w.ue(uint32(ppsID))
	w.copyBits(rbsp, idPos+oldLen, align)
	w.put(1, 1)
	for w.n%8 != 0 {
		w.put(0, 1)
	}
	hdr := w.bytes()
	hdr = append(hdr, sl.Data...)
	return escapeRBSP(hdr), nil
}

// joinNALsAnnexB frames nals with 4-byte start codes.
func joinNALsAnnexB(nals [][]byte) []byte {
	out, _ := joinNALs(nals, 0)
	return out
}

// hvcCWithParamSets rebuilds hvcC with extra VPS/SPS/PPS NAL units added to
// the arrays of their type (a new array when the type has none). The
// 22-byte header is kept.
func hvcCWithParamSets(hvcC []byte, extra [][]byte) ([]byte, error) {
	if len(hvcC) < 23 || hvcC[0] != 1 {
		return nil, fmt.Errorf("not an hvcC record")
	}
	type array struct {
		header byte
		nals   [][]byte
	}
	var arrays []array
	pos := 22
	numArrays := int(hvcC[pos])
	pos++
	for a := 0; a < numArrays; a++ {
		if pos+3 > len(hvcC) {
			return nil, fmt.Errorf("truncated hvcC array header")
		}
		arr := array{header: hvcC[pos]}
		numNALs := int(binary.BigEndian.Uint16(hvcC[pos+1:]))
		pos += 3
		for i := 0; i < numNALs; i++ {
			if pos+2 > len(hvcC) {
				return nil, fmt.Errorf("truncated hvcC NAL length")
			}
			size := int(binary.BigEndian.Uint16(hvcC[pos:]))
			pos += 2
			if pos+size > len(hvcC) {
				return nil, fmt.Errorf("truncated hvcC NAL")
			}
			arr.nals = append(arr.nals, hvcC[pos:pos+size])
			pos += size
		}
		arrays = append(arrays, arr)
	}
	for _, n := range extra {
		t := hevcNALType(n)
		if t != hevcNALVPS && t != hevcNALSPS && t != hevcNALPPS {
			continue
		}
		placed := false
		for i := range arrays {
			if int(arrays[i].header&0x3f) == t {
				arrays[i].nals = append(arrays[i].nals, n)
				placed = true
				break
			}
		}
		if !placed {
			arrays = append(arrays, array{header: 0x80 | byte(t), nals: [][]byte{n}})
		}
	}
	out := make([]byte, 0, len(hvcC)+512)
	out = append(out, hvcC[:22]...)
	out = append(out, byte(len(arrays)))
	for _, arr := range arrays {
		out = append(out, arr.header)
		out = binary.BigEndian.AppendUint16(out, uint16(len(arr.nals)))
		for _, n := range arr.nals {
			out = binary.BigEndian.AppendUint16(out, uint16(len(n)))
			out = append(out, n...)
		}
	}
	return out, nil
}
