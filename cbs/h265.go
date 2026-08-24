// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later
//
// Port of the H.265 codec glue: cbs_h265_split_fragment,
// cbs_h265_read_nal_unit, cbs_h2645_replace_ps and cbs_h265_flush
// (libavcodec/cbs_h265.c), plus the ff_cbs_sei_h265_types table.

package cbs

// H265Context is CodedBitstreamH265Context (+ CodedBitstreamH2645Context).
type H265Context struct {
	tr Tracer

	// CodedBitstreamH2645Context
	mp4           bool
	nalLengthSize int

	vps [hevcMaxVPSCount]*H265RawVPS
	sps [hevcMaxSPSCount]*H265RawSPS
	pps [hevcMaxPPSCount]*H265RawPPS

	activeVPS *H265RawVPS
	activeSPS *H265RawSPS
	activePPS *H265RawPPS

	seiTypes []seiTypeDescriptor
}

func newH265Context(tr Tracer) *H265Context {
	h := &H265Context{tr: tr}
	// ff_cbs_sei_h265_types (cbs_h265.c).
	h.seiTypes = []seiTypeDescriptor{
		{seiTypeBufferingPeriod, true, false,
			func() any { return new(H265RawSEIBufferingPeriod) },
			func(r *Reader, cur any, s *SEIMessageState) {
				h.seiBufferingPeriod(r, cur.(*H265RawSEIBufferingPeriod), s)
			}},
		{seiTypePicTiming, true, false,
			func() any { return new(H265RawSEIPicTiming) },
			func(r *Reader, cur any, s *SEIMessageState) {
				h.seiPicTiming(r, cur.(*H265RawSEIPicTiming), s)
			}},
		{seiTypePanScanRect, true, false,
			func() any { return new(H265RawSEIPanScanRect) },
			func(r *Reader, cur any, s *SEIMessageState) {
				h.seiPanScanRect(r, cur.(*H265RawSEIPanScanRect), s)
			}},
		{seiTypeRecoveryPoint, true, false,
			func() any { return new(H265RawSEIRecoveryPoint) },
			func(r *Reader, cur any, s *SEIMessageState) {
				h.seiRecoveryPoint(r, cur.(*H265RawSEIRecoveryPoint), s)
			}},
		{seiTypeFilmGrainCharacteristics, true, false,
			func() any { return new(H265RawFilmGrainCharacteristics) },
			func(r *Reader, cur any, s *SEIMessageState) {
				h.seiFilmGrainCharacteristics(r, cur.(*H265RawFilmGrainCharacteristics), s)
			}},
		{seiTypeDisplayOrientation, true, false,
			func() any { return new(H265RawSEIDisplayOrientation) },
			func(r *Reader, cur any, s *SEIMessageState) {
				h.seiDisplayOrientation(r, cur.(*H265RawSEIDisplayOrientation), s)
			}},
		{seiTypeActiveParameterSets, true, false,
			func() any { return new(H265RawSEIActiveParameterSets) },
			func(r *Reader, cur any, s *SEIMessageState) {
				h.seiActiveParameterSets(r, cur.(*H265RawSEIActiveParameterSets), s)
			}},
		{seiTypeDecodedPictureHash, false, true,
			func() any { return new(H265RawSEIDecodedPictureHash) },
			func(r *Reader, cur any, s *SEIMessageState) {
				h.seiDecodedPictureHash(r, cur.(*H265RawSEIDecodedPictureHash), s)
			}},
		{seiTypeTimeCode, true, false,
			func() any { return new(H265RawSEITimeCode) },
			func(r *Reader, cur any, s *SEIMessageState) {
				h.seiTimeCode(r, cur.(*H265RawSEITimeCode), s)
			}},
		{seiTypeAlphaChannelInfo, true, false,
			func() any { return new(H265RawSEIAlphaChannelInfo) },
			func(r *Reader, cur any, s *SEIMessageState) {
				h.seiAlphaChannelInfo(r, cur.(*H265RawSEIAlphaChannelInfo), s)
			}},
		{seiTypeThreeDimensionalReferenceDisplaysInfo, true, false,
			func() any { return new(H265RawSEI3DReferenceDisplaysInfo) },
			func(r *Reader, cur any, s *SEIMessageState) {
				h.sei3DReferenceDisplaysInfo(r, cur.(*H265RawSEI3DReferenceDisplaysInfo), s)
			}},
	}
	return h
}

func (h *H265Context) seiFindType(payloadType int) *seiTypeDescriptor {
	if d := seiFindIn(h.seiTypes, payloadType); d != nil {
		return d
	}
	return seiFindIn(seiCommonTypes, payloadType)
}

func (h *H265Context) diag(level Level, format string, args ...any) {
	if h.tr != nil {
		h.tr.Diag(level, sprintf(format, args...))
	}
}

// splitFragment is cbs_h265_split_fragment.
func (h *H265Context) splitFragment(frag *Fragment, header bool) error {
	data := frag.Data
	if len(data) == 0 {
		return nil
	}

	if header && data[0] != 0 {
		// HVCC header.
		h.mp4 = true

		// bytestream2 mirror: saturating byte reads over data.
		pos := 0
		getByte := func() int {
			if pos < len(data) {
				b := data[pos]
				pos++
				return int(b)
			}
			return 0
		}
		getBE16 := func() int { return getByte()<<8 | getByte() }
		skip := func(n int) { pos = min(pos+n, len(data)) }
		left := func() int { return len(data) - pos }

		if left() < 23 {
			return ErrInvalidData
		}

		version := getByte()
		if version != 1 {
			h.diag(LevelError, "Invalid HVCC header: first byte %d.", version)
			return ErrInvalidData
		}

		skip(20)
		h.nalLengthSize = (getByte() & 3) + 1

		nbArrays := getByte()
		for i := 0; i < nbArrays; i++ {
			nalUnitType := getByte() & 0x3f
			nbNals := getBE16()

			start := pos
			for j := 0; j < nbNals; j++ {
				if left() < 2 {
					return ErrInvalidData
				}
				size := getBE16()
				if left() < size {
					return ErrInvalidData
				}
				skip(size)
			}
			end := pos

			nals, err := h2645PacketSplit(data[start:end], CodecH265, 2, true, h.tr)
			if err != nil {
				h.diag(LevelError, "Failed to split HVCC array %d (%d NAL units of type %d).",
					i, nbNals, nalUnitType)
				return err
			}
			h2645AddNALs(frag, CodecH265, nals, start, hevcNALUnitName, h.tr)
		}
	} else {
		// Annex B, or later MP4 with already-known parameters.
		nals, err := h2645PacketSplit(data, CodecH265, h.nalLengthSize, h.mp4, h.tr)
		h2645AddNALs(frag, CodecH265, nals, 0, hevcNALUnitName, h.tr)
		if err != nil {
			return err
		}
	}

	return nil
}

// replaceVPS / replaceSPS / replacePPS are cbs_h2645_replace_ps: install
// only after a fully successful read; clear the active pointer when its
// slot changes.
func (h *H265Context) replaceVPS(vps *H265RawVPS) {
	id := vps.VpsVideoParameterSetID
	if h.vps[id] == h.activeVPS {
		h.activeVPS = nil
	}
	h.vps[id] = vps
}

func (h *H265Context) replaceSPS(sps *H265RawSPS) {
	id := sps.SpsSeqParameterSetID
	if h.sps[id] == h.activeSPS {
		h.activeSPS = nil
	}
	h.sps[id] = sps
}

func (h *H265Context) replacePPS(pps *H265RawPPS) {
	id := pps.PpsPicParameterSetID
	if h.pps[id] == h.activePPS {
		h.activePPS = nil
	}
	h.pps[id] = pps
}

// readUnit is cbs_h265_read_nal_unit, wrapped with the recover that turns
// template unwinding into the unit's error.
func (h *H265Context) readUnit(unit *Unit) {
	defer recoverUnit(unit)

	r := newReader(unit.RBSP, h.tr)

	switch unit.Type {
	case hevcNALVPS:
		vps := new(H265RawVPS)
		unit.Content = vps
		h.readVPS(&r, vps)
		h.replaceVPS(vps)

	case hevcNALSPS:
		sps := new(H265RawSPS)
		unit.Content = sps
		h.readSPS(&r, sps)
		h.replaceSPS(sps)

	case hevcNALPPS:
		pps := new(H265RawPPS)
		unit.Content = pps
		h.readPPS(&r, pps)
		h.replacePPS(pps)

	case hevcNALTrailN, hevcNALTrailR,
		hevcNALTSAN, hevcNALTSAR,
		hevcNALSTSAN, hevcNALSTSAR,
		hevcNALRADLN, hevcNALRADLR,
		hevcNALRASLN, hevcNALRASLR,
		hevcNALBLAWLP, hevcNALBLAWRADL, hevcNALBLANLP,
		hevcNALIDRWRADL, hevcNALIDRNLP, hevcNALCRANUT:
		slice := new(H265RawSlice)
		unit.Content = slice
		h.sliceSegmentHeader(&r, &slice.Header)

		if !r.moreRBSPData() {
			r.fail(ErrInvalidData)
		}

		pos := r.br.count()
		slice.Data = unit.RBSP[pos/8:]
		slice.DataBitStart = pos % 8

	case hevcNALAUD:
		aud := new(H265RawAUD)
		unit.Content = aud
		h.aud(&r, aud)

	case hevcNALFDNUT:
		filler := new(H265RawFiller)
		unit.Content = filler
		h.filler(&r, filler)

	case hevcNALSEIPrefix, hevcNALSEISuffix:
		sei := new(H265RawSEI)
		unit.Content = sei
		h.sei(&r, sei, unit.Type == hevcNALSEIPrefix)

	default:
		unit.Skip = SkipUnimplemented
		unit.Err = ErrUnsupported
		return
	}

	unit.Decomposed = true
}

// readFragmentContent is cbs_read_fragment_content, with per-unit error
// isolation instead of aborting the fragment.
func (h *H265Context) readFragmentContent(frag *Fragment) {
	readFragmentUnits(frag, h.tr, func(u *Unit) { h.readUnit(u) })
}

func (h *H265Context) ReadExtradata(data []byte) (*Fragment, error) {
	frag := &Fragment{Data: data}
	if err := h.splitFragment(frag, true); err != nil {
		return frag, err
	}
	h.readFragmentContent(frag)
	return frag, nil
}

func (h *H265Context) ReadPacket(data []byte) (*Fragment, error) {
	frag := &Fragment{Data: data}
	if err := h.splitFragment(frag, false); err != nil {
		return frag, err
	}
	h.readFragmentContent(frag)
	return frag, nil
}

// Flush is cbs_h265_flush.
func (h *H265Context) Flush() {
	for i := range h.vps {
		h.vps[i] = nil
	}
	for i := range h.sps {
		h.sps[i] = nil
	}
	for i := range h.pps {
		h.pps[i] = nil
	}
	h.activeVPS = nil
	h.activeSPS = nil
	h.activePPS = nil
}
