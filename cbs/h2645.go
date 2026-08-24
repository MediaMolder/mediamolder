// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later
//
// Port of the H.264/H.265 NAL unit splitting machinery:
// ff_h2645_extract_rbsp and ff_h2645_packet_split (libavcodec/h2645_parse.c)
// and ff_cbs_h2645_fragment_add_nals (libavcodec/cbs_h2645.c).
//
// Only the read path is ported, always with H2645_FLAG_SMALL_PADDING
// semantics (CBS sets it unconditionally).

package cbs

// nalRec is one split NAL before CBS unit conversion (H2645NAL), with the
// provenance the port keeps for reporting.
type nalRec struct {
	typ        uint32
	refIDC     uint8 // H.264 nal_ref_idc
	layerID    uint8 // H.265 nuh_layer_id
	temporalID uint8 // H.265 temporal_id

	offset int // byte offset of the NAL's first byte in buf
	prefix int // bytes skipped before it (start code / length prefix)
	raw    int // raw bytes consumed (incl. emulation prevention)

	rbsp []byte
	epb  []int

	skip string // non-empty: listed but never decomposed
}

// extractRBSP is ff_h2645_extract_rbsp (portable scanner branch), with
// small_padding == 1. It stops at the next start code, removes
// emulation-prevention bytes, and returns the bytes consumed from src.
// epb holds, per removed 0x03, the RBSP index of the second zero byte of
// its 00 00 pair (skipped_bytes_pos semantics).
func extractRBSP(src []byte) (rbsp []byte, epb []int, consumed int) {
	length := len(src)
	i := 0
	for ; i+1 < length; i += 2 {
		if src[i] != 0 {
			continue
		}
		if i > 0 && src[i-1] == 0 {
			i--
		}
		// STARTCODE_TEST
		if i+2 < length && src[i+1] == 0 && (src[i+2] == 3 || src[i+2] == 1) {
			if src[i+2] == 1 {
				// startcode, so we must be past the end
				length = i
			}
			break
		}
	}

	if i >= length-1 { // no escaped 0 (small_padding fast path)
		return src[:length], nil, length
	}

	dst := make([]byte, 0, length)
	dst = append(dst, src[:i]...)
	si := i
	for si+2 < length {
		// remove escapes (very rare 1:2^22)
		if src[si+2] > 3 {
			dst = append(dst, src[si], src[si+1])
			si += 2
		} else if src[si] == 0 && src[si+1] == 0 && src[si+2] != 0 {
			if src[si+2] == 3 { // escape
				dst = append(dst, 0, 0)
				si += 3
				epb = append(epb, len(dst)-1)
				continue
			}
			// next start code
			return dst, epb, si
		} else {
			dst = append(dst, src[si])
			si++
		}
	}
	for si < length {
		dst = append(dst, src[si])
		si++
	}
	return dst, epb, si
}

// findNextStartCode is find_next_start_code: buf starts at the current
// position, avail is the distance to next_avc. Returns the number of bytes
// to skip, including the 3-byte start code when one is found.
func findNextStartCode(buf []byte, avail int) int {
	if avail <= 3 {
		return avail
	}
	i := 0
	for i+3 < avail {
		if buf[i] == 0 && buf[i+1] == 0 && buf[i+2] == 1 {
			break
		}
		i++
	}
	return i + 3
}

// getNALSize is get_nalsize (h2645_parse.h): read a big-endian
// nalLengthSize-byte length at buf[0:]. diagTr may be nil.
func getNALSize(nalLengthSize int, buf []byte, tr Tracer) (int, error) {
	if 0 >= len(buf)-nalLengthSize {
		// the end of the buffer is reached
		return 0, ErrInvalidData
	}
	nalsize := 0
	for i := 0; i < nalLengthSize; i++ {
		nalsize = nalsize<<8 | int(buf[i])
	}
	if nalsize <= 0 || nalsize > len(buf)-nalLengthSize {
		if tr != nil {
			tr.Diag(LevelError, sprintf("Invalid NAL unit size (%d > %d).",
				nalsize, len(buf)-nalLengthSize))
		}
		return 0, ErrInvalidData
	}
	return nalsize, nil
}

// h2645PacketSplit is ff_h2645_packet_split for H.264/H.265. isNALFF
// selects length-prefixed mode with the given nalLengthSize; otherwise
// Annex B start codes are scanned. NALs whose header fails to parse, or
// HEVC layer-63 NALs, are returned with skip set. The returned error, if
// any, aborts the fragment (the records split so far are still returned).
func h2645PacketSplit(buf []byte, codec CodecID, nalLengthSize int, isNALFF bool, tr Tracer) ([]nalRec, error) {
	var nals []nalRec
	length := len(buf)
	pos := 0
	nextAVC := length
	if isNALFF {
		nextAVC = 0
	}

	diag := func(level Level, format string, args ...any) {
		if tr != nil {
			tr.Diag(level, sprintf(format, args...))
		}
	}

	for length-pos >= 4 {
		var extractLength, prefix int

		if pos == nextAVC {
			nalsize, err := getNALSize(nalLengthSize, buf[pos:], tr)
			if err != nil {
				return nals, err
			}
			extractLength = nalsize
			prefix = nalLengthSize
			pos += nalLengthSize
			nextAVC = pos + extractLength
		} else {
			if pos > nextAVC {
				diag(LevelWarning, "Exceeded next NALFF position, re-syncing.")
			}
			bufIndex := findNextStartCode(buf[pos:], nextAVC-pos)
			pos += bufIndex
			if pos > length {
				pos = length
			}
			if length-pos == 0 {
				if len(nals) > 0 {
					// No more start codes: we discarded some
					// irrelevant bytes at the end of the packet.
					return nals, nil
				}
				diag(LevelError, "No start code is found.")
				return nals, ErrInvalidData
			}
			extractLength = min(length-pos, nextAVC-pos)
			prefix = bufIndex
			if pos >= nextAVC {
				// skip to the start of the next NAL
				pos = nextAVC
				continue
			}
		}

		rbsp, epb, consumed := extractRBSP(buf[pos : pos+extractLength])
		if isNALFF && extractLength != consumed && extractLength != 0 {
			diag(LevelDebug, "NALFF: Consumed only %d bytes instead of %d",
				consumed, extractLength)
		}

		rec := nalRec{
			offset: pos,
			prefix: prefix,
			raw:    consumed,
			rbsp:   rbsp,
			epb:    epb,
		}
		pos += consumed

		// nal->size <= 0 || nal->size_bits <= 0: an empty or all-zero
		// NAL is dropped before header parsing.
		stripped := len(rbsp)
		for stripped > 0 && rbsp[stripped-1] == 0 {
			stripped--
		}
		if len(rbsp) == 0 || stripped == 0 {
			continue
		}

		var headerErr error
		hr := newBitReader(rbsp)
		if codec == CodecH265 {
			// hevc_parse_nal_header
			if hr.getBit() != 0 {
				headerErr = ErrInvalidData
			} else {
				rec.typ = hr.getBits(6)
				rec.layerID = uint8(hr.getBits(6))
				tid := int(hr.getBits(3)) - 1
				if tid < 0 {
					headerErr = ErrInvalidData
				} else {
					rec.temporalID = uint8(tid)
				}
			}
			if headerErr == nil {
				diag(LevelDebug, "nal_unit_type: %d(%s), nuh_layer_id: %d, temporal_id: %d",
					rec.typ, hevcNALUnitName(rec.typ), rec.layerID, rec.temporalID)
				if rec.layerID == 63 {
					rec.skip = SkipLayerID63
					nals = append(nals, rec)
					continue
				}
			}
		} else {
			// h264_parse_nal_header
			if hr.getBit() != 0 {
				headerErr = ErrInvalidData
			} else {
				rec.refIDC = uint8(hr.getBits(2))
				rec.typ = hr.getBits(5)
				diag(LevelDebug, "nal_unit_type: %d(%s), nal_ref_idc: %d",
					rec.typ, h264NALUnitName(rec.typ), rec.refIDC)
			}
		}
		if headerErr != nil {
			diag(LevelWarning, "Failed to parse header of NALU (type %d): \"%s\". Skipping NALU.",
				rec.typ, "Invalid data found when processing input")
			rec.skip = SkipBadNALHeader
			nals = append(nals, rec)
			continue
		}

		nals = append(nals, rec)
	}

	return nals, nil
}

// h2645AddNALs is ff_cbs_h2645_fragment_add_nals: convert split NALs into
// fragment units, applying the HEVC layer filter and trailing-zero
// stripping. baseOffset translates NAL offsets into fragment offsets (used
// for avcC/hvcC arrays, which are split from a sub-slice).
func h2645AddNALs(frag *Fragment, codec CodecID, nals []nalRec, baseOffset int, typeName func(uint32) string, tr Tracer) {
	for i := range nals {
		nal := &nals[i]
		unit := Unit{
			Type:         nal.typ,
			TypeName:     typeName(nal.typ),
			Offset:       baseOffset + nal.offset,
			PrefixSize:   nal.prefix,
			RawSize:      nal.raw,
			RBSP:         nal.rbsp,
			EPBPositions: nal.epb,
			NalRefIDC:    nal.refIDC,
			NuhLayerID:   nal.layerID,
			TemporalID:   nal.temporalID,
			Skip:         nal.skip,
		}
		if unit.Skip == "" {
			if codec == CodecH265 && nal.layerID > 0 &&
				(nal.typ < hevcNALVPS || nal.typ > hevcNALPPS) {
				unit.Skip = SkipHEVCLayer
			} else {
				// Remove trailing zeroes.
				size := len(nal.rbsp)
				for size > 0 && nal.rbsp[size-1] == 0 {
					size--
				}
				if size == 0 {
					if tr != nil {
						tr.Diag(LevelVerbose, "Discarding empty 0 NAL unit")
					}
					unit.Skip = SkipEmpty
				} else {
					unit.RBSP = nal.rbsp[:size]
				}
			}
		}
		frag.Units = append(frag.Units, unit)
	}
}
