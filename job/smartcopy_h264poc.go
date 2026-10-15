// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"fmt"

	"github.com/MediaMolder/MediaMolder/cbs"
)

// H.264 picture-order-count continuity across a smartcopy splice.
//
// A re-encoded head run starts with an IDR, whose POC is 0 by definition,
// so its pictures are numbered from 0. An open-GOP source continues at the
// copied keyframe with a non-IDR I picture whose pic_order_cnt_lsb carries
// the source's own numbering. Decoders derive POC incrementally from the
// previous reference picture (Rec. ITU-T H.264 §8.2.1.1), so at the splice
// the copied picture can land below the pictures already decoded and be
// output out of order or dropped (libavcodec h264_select_output_frame).
// HEVC has BLA pictures for exactly this; H.264 has nothing, so the copied
// slices after the splice get their pic_order_cnt_lsb shifted by a constant
// until the next IDR — a fixed-width field rewritten in place, so slice
// data stays byte-identical. The shift is chosen so the first copied picture
// derives to just above the run's highest POC.

// h264POCState parses slice headers of one H.264 elementary stream (the
// cbs context keeps its parameter sets) and tracks the §8.2.1.1 derivation
// state for pic_order_cnt_type 0.
type h264POCState struct {
	codec      cbs.Codec
	sps        map[uint8]*cbs.H264RawSPS
	pps        map[uint8]*cbs.H264RawPPS
	lengthSize int

	hasPrev          bool
	prevMsb, prevLsb int32 // previous reference picture

	// Summary of the pictures observed so far.
	pictures        int
	lastRefLsb      int32
	lastRefPOC      int32
	lastRefFrameNum int32
	maxPOC          int32
	maxPocLsb       int32 // MaxPicOrderCntLsb of the last picture's SPS
	pocType         int
}

// h264PicInfo describes the first slice of a packet.
type h264PicInfo struct {
	ok       bool
	idr      bool
	ref      bool
	mmco5    bool
	lsb      uint32
	frameNum uint32
	poc      int32
	sps      *cbs.H264RawSPS
}

func newH264POCState(extradata []byte, lengthSize int) (*h264POCState, error) {
	c, err := cbs.New(cbs.CodecH264, nil)
	if err != nil {
		return nil, err
	}
	s := &h264POCState{
		codec:      c,
		sps:        map[uint8]*cbs.H264RawSPS{},
		pps:        map[uint8]*cbs.H264RawPPS{},
		lengthSize: lengthSize,
	}
	if len(extradata) > 0 {
		frag, err := c.ReadExtradata(extradata)
		if err != nil {
			return nil, fmt.Errorf("parse H.264 extradata: %w", err)
		}
		s.collectPS(frag)
	}
	return s, nil
}

func (s *h264POCState) collectPS(frag *cbs.Fragment) {
	for i := range frag.Units {
		switch c := frag.Units[i].Content.(type) {
		case *cbs.H264RawSPS:
			s.sps[c.SeqParameterSetID] = c
		case *cbs.H264RawPPS:
			s.pps[c.PicParameterSetID] = c
		}
	}
}

// firstSPS returns the lowest-id SPS seen (the source's, for a plan).
func (s *h264POCState) firstSPS() *cbs.H264RawSPS {
	for id := 0; id < 32; id++ {
		if sp := s.sps[uint8(id)]; sp != nil {
			return sp
		}
	}
	return nil
}

func (s *h264POCState) spsForSlice(h *cbs.H264RawSliceHeader) *cbs.H264RawSPS {
	pps := s.pps[h.PicParameterSetID]
	if pps == nil {
		return nil
	}
	return s.sps[pps.SeqParameterSetID]
}

func h264SliceHasMMCO5(h *cbs.H264RawSliceHeader) bool {
	if h.AdaptiveRefPicMarkingModeFlag == 0 {
		return false
	}
	for i := range h.Mmco {
		switch h.Mmco[i].MemoryManagementControlOperation {
		case 0:
			return false
		case 5:
			return true
		}
	}
	return false
}

// observe parses pkt and advances the POC derivation with its first slice.
func (s *h264POCState) observe(pkt []byte) h264PicInfo {
	frag, err := s.codec.ReadPacket(pkt)
	if err != nil && frag == nil {
		return h264PicInfo{}
	}
	s.collectPS(frag)
	for i := range frag.Units {
		u := &frag.Units[i]
		sl, ok := u.Content.(*cbs.H264RawSlice)
		if !ok {
			continue
		}
		return s.derive(u, &sl.Header)
	}
	return h264PicInfo{}
}

// derive is §8.2.1.1 for frames (fields are tracked by their top POC).
func (s *h264POCState) derive(u *cbs.Unit, h *cbs.H264RawSliceHeader) h264PicInfo {
	sps := s.spsForSlice(h)
	if sps == nil {
		return h264PicInfo{}
	}
	info := h264PicInfo{
		ok:       true,
		idr:      u.Type == h264NALIDR,
		ref:      u.NalRefIDC != 0,
		mmco5:    h264SliceHasMMCO5(h),
		lsb:      uint32(h.PicOrderCntLsb),
		frameNum: uint32(h.FrameNum),
		sps:      sps,
	}
	s.pocType = int(sps.PicOrderCntType)
	if info.ref {
		s.lastRefFrameNum = int32(h.FrameNum)
		if info.mmco5 {
			s.lastRefFrameNum = 0
		}
	}
	if sps.PicOrderCntType != 0 {
		s.pictures++
		return info
	}
	maxLsb := int32(1) << (sps.Log2MaxPicOrderCntLsbMinus4 + 4)
	lsb := int32(h.PicOrderCntLsb)
	var msb int32
	switch {
	case info.idr || !s.hasPrev:
		msb = 0
		s.prevMsb, s.prevLsb = 0, 0
		if !info.idr {
			// Stream starts on a non-IDR picture: derive from (0, 0).
			if lsb > maxLsb/2 {
				msb = -maxLsb
			}
		}
	case lsb < s.prevLsb && s.prevLsb-lsb >= maxLsb/2:
		msb = s.prevMsb + maxLsb
	case lsb > s.prevLsb && lsb-s.prevLsb > maxLsb/2:
		msb = s.prevMsb - maxLsb
	default:
		msb = s.prevMsb
	}
	info.poc = msb + lsb
	s.hasPrev = true
	if info.ref {
		if info.mmco5 {
			s.prevMsb, s.prevLsb = 0, 0
		} else {
			s.prevMsb, s.prevLsb = msb, lsb
		}
		s.lastRefLsb, s.lastRefPOC = lsb, info.poc
	}
	if s.pictures == 0 || info.poc > s.maxPOC {
		s.maxPOC = info.poc
	}
	s.maxPocLsb = maxLsb
	s.pictures++
	return info
}

// spliceDelta computes the shifts for copied pictures following a
// re-encoded run. first describes the first copied picture (a non-IDR
// keyframe). Its pic_order_cnt_lsb must derive, from the run's last
// reference picture, to two above the run's highest POC; its frame_num
// must follow the run's last reference picture without a gap, so the
// memory-management operations in the copied slices (which address
// pictures relative to the current frame_num) keep resolving and the
// decoder never infers missing frames.
func (s *h264POCState) spliceDelta(first h264PicInfo) (lsbDelta, frameNumDelta int32, err error) {
	if s.pictures == 0 || s.pocType != 0 || first.sps == nil {
		return 0, 0, fmt.Errorf("no type-0 POC state for the re-encoded run")
	}
	srcMaxLsb := int32(1) << (first.sps.Log2MaxPicOrderCntLsbMinus4 + 4)
	if s.maxPocLsb > srcMaxLsb {
		return 0, 0, fmt.Errorf("re-encoded MaxPicOrderCntLsb %d exceeds the source's %d", s.maxPocLsb, srcMaxLsb)
	}
	step := s.maxPOC + 2 - s.lastRefPOC
	if step < 1 || step > srcMaxLsb/2 {
		return 0, 0, fmt.Errorf("re-encoded run POC spread %d exceeds the source's MaxPicOrderCntLsb/2 (%d)", step, srcMaxLsb/2)
	}
	target := (s.lastRefLsb + step) % srcMaxLsb
	lsbDelta = (target - int32(first.lsb)) % srcMaxLsb
	if lsbDelta < 0 {
		lsbDelta += srcMaxLsb
	}
	maxFrameNum := int32(1) << (first.sps.Log2MaxFrameNumMinus4 + 4)
	frameNumDelta = (s.lastRefFrameNum + 1 - int32(first.frameNum)) % maxFrameNum
	if frameNumDelta < 0 {
		frameNumDelta += maxFrameNum
	}
	return lsbDelta, frameNumDelta, nil
}

// h264HeaderFieldPositions returns the bit offsets of frame_num and
// pic_order_cnt_lsb within a slice NAL's RBSP (NAL header included) and
// their widths. Both are fixed-width fields, so they can be rewritten in
// place without disturbing the rest of the header or the slice data.
func h264HeaderFieldPositions(u *cbs.Unit, h *cbs.H264RawSliceHeader, sps *cbs.H264RawSPS) (fnPos, fnWidth, lsbPos, lsbWidth int) {
	pos := 8 + ueLen(h.FirstMbInSlice) + ueLen(uint32(h.SliceType)) + ueLen(uint32(h.PicParameterSetID))
	if sps.SeparateColourPlaneFlag == 1 {
		pos += 2
	}
	fnPos, fnWidth = pos, int(sps.Log2MaxFrameNumMinus4)+4
	pos += fnWidth
	if sps.FrameMbsOnlyFlag == 0 {
		pos++
		if h.FieldPicFlag == 1 {
			pos++
		}
	}
	if u.Type == h264NALIDR {
		pos += ueLen(uint32(h.IdrPicID))
	}
	return fnPos, fnWidth, pos, int(sps.Log2MaxPicOrderCntLsbMinus4) + 4
}

// renumber rewrites every slice in pkt: pic_order_cnt_lsb becomes
// (lsb + lsbDelta) mod MaxPicOrderCntLsb and frame_num becomes
// (frame_num + frameNumDelta) mod MaxFrameNum. It returns the rebuilt packet
// in the stream's framing, plus the first slice's description. A packet
// whose first slice is an IDR is returned unchanged with info.idr set: the
// caller stops renumbering there (the IDR restarts both numberings).
func (s *h264POCState) renumber(pkt []byte, lsbDelta, frameNumDelta int32) ([]byte, h264PicInfo, error) {
	frag, err := s.codec.ReadPacket(pkt)
	if err != nil && frag == nil {
		return nil, h264PicInfo{}, fmt.Errorf("parse packet: %w", err)
	}
	s.collectPS(frag)
	var first h264PicInfo
	nals := make([][]byte, 0, len(frag.Units))
	changed := false
	for i := range frag.Units {
		u := &frag.Units[i]
		raw := frag.Data[u.Offset : u.Offset+u.RawSize]
		if s.lengthSize == 0 {
			for len(raw) > 0 && raw[len(raw)-1] == 0 {
				raw = raw[:len(raw)-1]
			}
		}
		sl, ok := u.Content.(*cbs.H264RawSlice)
		if !ok || u.Err != nil {
			nals = append(nals, raw)
			continue
		}
		sps := s.spsForSlice(&sl.Header)
		if sps == nil {
			return nil, h264PicInfo{}, fmt.Errorf("slice references unknown PPS %d", sl.Header.PicParameterSetID)
		}
		if !first.ok {
			first = h264PicInfo{
				ok:       true,
				idr:      u.Type == h264NALIDR,
				ref:      u.NalRefIDC != 0,
				mmco5:    h264SliceHasMMCO5(&sl.Header),
				lsb:      uint32(sl.Header.PicOrderCntLsb),
				frameNum: uint32(sl.Header.FrameNum),
				sps:      sps,
			}
			if first.idr {
				return pkt, first, nil
			}
		}
		if lsbDelta == 0 && frameNumDelta == 0 {
			nals = append(nals, raw)
			continue
		}
		fnPos, fnWidth, lsbPos, lsbWidth := h264HeaderFieldPositions(u, &sl.Header, sps)
		if (lsbPos+lsbWidth+7)/8 > len(u.RBSP) {
			return nil, first, fmt.Errorf("slice header shorter than the pic_order_cnt_lsb position")
		}
		if got := getBits(u.RBSP, fnPos, fnWidth); got != uint32(sl.Header.FrameNum) {
			return nil, first, fmt.Errorf("frame_num position mismatch (read %d, parsed %d)", got, sl.Header.FrameNum)
		}
		rbsp := make([]byte, len(u.RBSP))
		copy(rbsp, u.RBSP)
		if frameNumDelta != 0 {
			fn := (int32(sl.Header.FrameNum) + frameNumDelta) % (int32(1) << fnWidth)
			setBits(rbsp, fnPos, fnWidth, uint32(fn))
		}
		if lsbDelta != 0 && sps.PicOrderCntType == 0 {
			if got := getBits(u.RBSP, lsbPos, lsbWidth); got != uint32(sl.Header.PicOrderCntLsb) {
				return nil, first, fmt.Errorf("pic_order_cnt_lsb position mismatch (read %d, parsed %d)", got, sl.Header.PicOrderCntLsb)
			}
			lsb := (int32(sl.Header.PicOrderCntLsb) + lsbDelta) % (int32(1) << lsbWidth)
			setBits(rbsp, lsbPos, lsbWidth, uint32(lsb))
		}
		nals = append(nals, escapeRBSP(rbsp))
		changed = true
	}
	if !changed {
		return pkt, first, nil
	}
	out, err := joinNALs(nals, s.lengthSize)
	if err != nil {
		return nil, first, err
	}
	return out, first, nil
}
