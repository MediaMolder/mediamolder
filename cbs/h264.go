// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later
//
// Port of the H.264 codec glue: cbs_h264_split_fragment,
// cbs_h264_read_nal_unit, cbs_h2645_replace_ps and cbs_h264_flush
// (libavcodec/cbs_h264.c), plus the ff_cbs_sei_h264_types table.

package cbs

// H264Context is CodedBitstreamH264Context (+ CodedBitstreamH2645Context).
type H264Context struct {
	tr Tracer

	// CodedBitstreamH2645Context
	mp4           bool
	nalLengthSize int

	sps [h264MaxSPSCount]*H264RawSPS
	pps [h264MaxPPSCount]*H264RawPPS

	activeSPS *H264RawSPS
	activePPS *H264RawPPS

	lastSliceNALUnitType uint8

	seiTypes []seiTypeDescriptor
}

func newH264Context(tr Tracer) *H264Context {
	h := &H264Context{tr: tr}
	// ff_cbs_sei_h264_types (cbs_h264.c).
	h.seiTypes = []seiTypeDescriptor{
		{seiTypeBufferingPeriod, true, false,
			func() any { return new(H264RawSEIBufferingPeriod) },
			func(r *Reader, cur any, s *SEIMessageState) {
				h.seiBufferingPeriod(r, cur.(*H264RawSEIBufferingPeriod), s)
			}},
		{seiTypePicTiming, true, false,
			func() any { return new(H264RawSEIPicTiming) },
			func(r *Reader, cur any, s *SEIMessageState) {
				h.seiPicTiming(r, cur.(*H264RawSEIPicTiming), s)
			}},
		{seiTypePanScanRect, true, false,
			func() any { return new(H264RawSEIPanScanRect) },
			func(r *Reader, cur any, s *SEIMessageState) {
				h.seiPanScanRect(r, cur.(*H264RawSEIPanScanRect), s)
			}},
		{seiTypeRecoveryPoint, true, false,
			func() any { return new(H264RawSEIRecoveryPoint) },
			func(r *Reader, cur any, s *SEIMessageState) {
				h.seiRecoveryPoint(r, cur.(*H264RawSEIRecoveryPoint), s)
			}},
		{seiTypeFilmGrainCharacteristics, true, false,
			func() any { return new(H264RawFilmGrainCharacteristics) },
			func(r *Reader, cur any, s *SEIMessageState) {
				h.seiFilmGrainCharacteristics(r, cur.(*H264RawFilmGrainCharacteristics), s)
			}},
		{seiTypeFramePackingArrangement, true, false,
			func() any { return new(H264RawSEIFramePackingArrangement) },
			func(r *Reader, cur any, s *SEIMessageState) {
				h.seiFramePackingArrangement(r, cur.(*H264RawSEIFramePackingArrangement), s)
			}},
		{seiTypeDisplayOrientation, true, false,
			func() any { return new(H264RawSEIDisplayOrientation) },
			func(r *Reader, cur any, s *SEIMessageState) {
				h.seiDisplayOrientation(r, cur.(*H264RawSEIDisplayOrientation), s)
			}},
	}
	return h
}

func (h *H264Context) seiFindType(payloadType int) *seiTypeDescriptor {
	if d := seiFindIn(h.seiTypes, payloadType); d != nil {
		return d
	}
	return seiFindIn(seiCommonTypes, payloadType)
}

func (h *H264Context) diag(level Level, format string, args ...any) {
	if h.tr != nil {
		h.tr.Diag(level, sprintf(format, args...))
	}
}

// splitFragment is cbs_h264_split_fragment.
func (h *H264Context) splitFragment(frag *Fragment, header bool) error {
	data := frag.Data
	if len(data) == 0 {
		return nil
	}

	if header && data[0] != 0 {
		// AVCC header.
		h.mp4 = true

		if len(data) < 6 {
			return ErrInvalidData
		}

		version := data[0]
		if version != 1 {
			h.diag(LevelError, "Invalid AVCC header: first byte %d.", version)
			return ErrInvalidData
		}

		h.nalLengthSize = int(data[4]&3) + 1

		// SPS array.
		count := int(data[5] & 0x1f)
		pos := 6
		start := pos
		for i := 0; i < count; i++ {
			if len(data)-pos < 2*(count-i) {
				return ErrInvalidData
			}
			size := int(data[pos])<<8 | int(data[pos+1])
			pos += 2
			if len(data)-pos < size {
				return ErrInvalidData
			}
			pos += size
		}
		end := pos

		nals, err := h2645PacketSplit(data[start:end], CodecH264, 2, true, h.tr)
		if err != nil {
			h.diag(LevelError, "Failed to split AVCC SPS array.")
			return err
		}
		h2645AddNALs(frag, CodecH264, nals, start, h264NALUnitName, h.tr)

		// PPS array.
		if len(data)-pos < 1 {
			return ErrInvalidData
		}
		count = int(data[pos])
		pos++
		start = pos
		for i := 0; i < count; i++ {
			if len(data)-pos < 2*(count-i) {
				return ErrInvalidData
			}
			size := int(data[pos])<<8 | int(data[pos+1])
			pos += 2
			if len(data)-pos < size {
				return ErrInvalidData
			}
			pos += size
		}
		end = pos

		nals, err = h2645PacketSplit(data[start:end], CodecH264, 2, true, h.tr)
		if err != nil {
			h.diag(LevelError, "Failed to split AVCC PPS array.")
			return err
		}
		h2645AddNALs(frag, CodecH264, nals, start, h264NALUnitName, h.tr)

		if len(data)-pos > 0 {
			h.diag(LevelWarning, "%d bytes left at end of AVCC header.", len(data)-pos)
		}
	} else {
		// Annex B, or later MP4 with already-known parameters.
		nals, err := h2645PacketSplit(data, CodecH264, h.nalLengthSize, h.mp4, h.tr)
		h2645AddNALs(frag, CodecH264, nals, 0, h264NALUnitName, h.tr)
		if err != nil {
			return err
		}
	}

	return nil
}

// replaceSPS / replacePPS are cbs_h2645_replace_ps: install only after a
// fully successful read; clear the active pointer when its slot changes.
func (h *H264Context) replaceSPS(sps *H264RawSPS) {
	id := sps.SeqParameterSetID
	if h.sps[id] == h.activeSPS {
		h.activeSPS = nil
	}
	h.sps[id] = sps
}

func (h *H264Context) replacePPS(pps *H264RawPPS) {
	id := pps.PicParameterSetID
	if h.pps[id] == h.activePPS {
		h.activePPS = nil
	}
	h.pps[id] = pps
}

// readUnit is cbs_h264_read_nal_unit, wrapped with the recover that turns
// template unwinding into the unit's error.
func (h *H264Context) readUnit(unit *Unit) {
	defer recoverUnit(unit)

	r := newReader(unit.RBSP, h.tr)

	switch unit.Type {
	case h264NALSPS:
		sps := new(H264RawSPS)
		unit.Content = sps
		h.readSPS(&r, sps)
		h.replaceSPS(sps)

	case h264NALSPSExt:
		spsExt := new(H264RawSPSExtension)
		h.spsExtension(&r, spsExt)
		unit.Content = spsExt

	case h264NALPPS:
		pps := new(H264RawPPS)
		unit.Content = pps
		h.readPPS(&r, pps)
		h.replacePPS(pps)

	case h264NALSlice, h264NALIDRSlice, h264NALAuxiliarySlice:
		slice := new(H264RawSlice)
		unit.Content = slice
		h.sliceHeader(&r, &slice.Header)

		if !r.moreRBSPData() {
			r.fail(ErrInvalidData)
		}

		pos := r.br.count()
		slice.Data = unit.RBSP[pos/8:]
		slice.DataBitStart = pos % 8

	case h264NALAUD:
		aud := new(H264RawAUD)
		unit.Content = aud
		h.aud(&r, aud)

	case h264NALSEI:
		sei := new(H264RawSEI)
		unit.Content = sei
		h.sei(&r, sei)

	case h264NALFillerData:
		filler := new(H264RawFiller)
		unit.Content = filler
		h.filler(&r, filler)

	case h264NALEndSequence:
		hdr := new(H264RawNALUnitHeader)
		unit.Content = hdr
		h.endOfSequence(&r, hdr)

	case h264NALEndStream:
		hdr := new(H264RawNALUnitHeader)
		unit.Content = hdr
		h.endOfStream(&r, hdr)

	default:
		unit.Skip = SkipUnimplemented
		unit.Err = ErrUnsupported
		return
	}

	unit.Decomposed = true
}

// readFragmentContent is cbs_read_fragment_content, with per-unit error
// isolation instead of aborting the fragment.
func (h *H264Context) readFragmentContent(frag *Fragment) {
	readFragmentUnits(frag, h.tr, func(u *Unit) { h.readUnit(u) })
}

func (h *H264Context) ReadExtradata(data []byte) (*Fragment, error) {
	frag := &Fragment{Data: data}
	if err := h.splitFragment(frag, true); err != nil {
		return frag, err
	}
	h.readFragmentContent(frag)
	return frag, nil
}

func (h *H264Context) ReadPacket(data []byte) (*Fragment, error) {
	frag := &Fragment{Data: data}
	if err := h.splitFragment(frag, false); err != nil {
		return frag, err
	}
	h.readFragmentContent(frag)
	return frag, nil
}

// Flush is cbs_h264_flush.
func (h *H264Context) Flush() {
	for i := range h.sps {
		h.sps[i] = nil
	}
	for i := range h.pps {
		h.pps[i] = nil
	}
	h.activeSPS = nil
	h.activePPS = nil
	h.lastSliceNALUnitType = 0
}
