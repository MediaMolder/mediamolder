// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"encoding/binary"
	"fmt"

	"github.com/MediaMolder/MediaMolder/cbs"
)

// NAL-unit framing helpers for smartcopy. The boundary encoder emits Annex B
// (start-code delimited) NAL units; an ISOBMFF source carries length-prefixed
// NAL units and an avcC/hvcC record, so every re-encoded packet has to be
// re-framed to match the copied interior before it reaches the muxer (the
// mov muxer only reformats packets when the stream's extradata itself is
// Annex B — see movenc.c mov_write_single_packet).

const (
	h264NALSlice    = 1
	h264NALIDR      = 5
	h264NALSEI      = 6
	h264NALSPS      = 7
	h264NALPPS      = 8
	h264NALAUD      = 9
	h264NALAuxSlice = 19

	hevcNALBLAWLP   = 16
	hevcNALBLAWRADL = 17
	hevcNALBLANLP   = 18
	hevcNALIDRWRADL = 19
	hevcNALIDRNLP   = 20
	hevcNALCRA      = 21
	hevcNALVPS      = 32
	hevcNALSPS      = 33
	hevcNALPPS      = 34
	hevcNALAUD      = 35
)

// isomNALLengthSize returns the NAL length-prefix size (1..4) encoded in an
// avcC (H.264) or hvcC (HEVC) configuration record, or 0 when extradata is
// absent or Annex B (so the stream's packets are start-code delimited).
func isomNALLengthSize(codec cbs.CodecID, extradata []byte) int {
	if len(extradata) == 0 || extradata[0] != 1 {
		return 0
	}
	switch codec {
	case cbs.CodecH264:
		if len(extradata) < 7 {
			return 0
		}
		return int(extradata[4]&3) + 1
	case cbs.CodecH265:
		if len(extradata) < 23 {
			return 0
		}
		return int(extradata[21]&3) + 1
	}
	return 0
}

// splitAnnexB returns the NAL units of a start-code delimited buffer, with
// start codes and trailing zero bytes removed.
func splitAnnexB(data []byte) [][]byte {
	var nals [][]byte
	n := len(data)
	i := 0
	// Skip leading bytes up to the first start code.
	for i+2 < n && !(data[i] == 0 && data[i+1] == 0 && data[i+2] == 1) {
		i++
	}
	for i+2 < n {
		i += 3 // past 00 00 01
		start := i
		for i+2 < n && !(data[i] == 0 && data[i+1] == 0 && data[i+2] == 1) {
			i++
		}
		end := i
		if i+2 >= n {
			end = n
			i = n
		}
		for end > start && data[end-1] == 0 {
			end--
		}
		if end > start {
			nals = append(nals, data[start:end])
		}
	}
	return nals
}

// lengthPrefixedNALs returns the NAL units of a length-prefixed buffer.
func lengthPrefixedNALs(data []byte, lengthSize int) ([][]byte, error) {
	var nals [][]byte
	for pos := 0; pos < len(data); {
		if pos+lengthSize > len(data) {
			return nil, fmt.Errorf("truncated NAL length at %d", pos)
		}
		size := 0
		for i := 0; i < lengthSize; i++ {
			size = size<<8 | int(data[pos+i])
		}
		pos += lengthSize
		if size <= 0 || pos+size > len(data) {
			return nil, fmt.Errorf("invalid NAL size %d at %d (%d bytes left)", size, pos, len(data)-pos)
		}
		nals = append(nals, data[pos:pos+size])
		pos += size
	}
	return nals, nil
}

// splitNALs splits a packet in either framing: lengthSize 0 means Annex B.
func splitNALs(data []byte, lengthSize int) ([][]byte, error) {
	if lengthSize == 0 {
		return splitAnnexB(data), nil
	}
	return lengthPrefixedNALs(data, lengthSize)
}

// joinNALs frames nals with lengthSize-byte big-endian prefixes, or with
// 4-byte start codes when lengthSize is 0. A NAL longer than a 1-, 2- or
// 3-byte prefix can express is an error rather than a wrapped length.
func joinNALs(nals [][]byte, lengthSize int) ([]byte, error) {
	if lengthSize < 0 || lengthSize > 4 {
		return nil, fmt.Errorf("invalid NAL length size %d", lengthSize)
	}
	total := 0
	for _, n := range nals {
		total += len(n) + 4
	}
	out := make([]byte, 0, total)
	for _, n := range nals {
		if lengthSize == 0 {
			out = append(out, 0, 0, 0, 1)
		} else {
			if lengthSize < 4 && len(n) >= 1<<(8*lengthSize) {
				return nil, fmt.Errorf("NAL unit of %d bytes does not fit a %d-byte length prefix", len(n), lengthSize)
			}
			var pfx [4]byte
			binary.BigEndian.PutUint32(pfx[:], uint32(len(n)))
			out = append(out, pfx[4-lengthSize:]...)
		}
		out = append(out, n...)
	}
	return out, nil
}

// annexBToLengthPrefixed re-frames an Annex B packet as length-prefixed NAL
// units (the avcC/hvcC "NALFF" framing).
func annexBToLengthPrefixed(data []byte, lengthSize int) ([]byte, error) {
	return joinNALs(splitAnnexB(data), lengthSize)
}

func h264NALType(nal []byte) int {
	if len(nal) == 0 {
		return -1
	}
	return int(nal[0] & 0x1f)
}

func hevcNALType(nal []byte) int {
	if len(nal) < 2 {
		return -1
	}
	return int(nal[0]>>1) & 0x3f
}

// nalIsParamSet reports whether nal is a VPS/SPS/PPS of the given codec.
func nalIsParamSet(codec cbs.CodecID, nal []byte) bool {
	switch codec {
	case cbs.CodecH264:
		t := h264NALType(nal)
		return t == h264NALSPS || t == h264NALPPS
	case cbs.CodecH265:
		t := hevcNALType(nal)
		return t == hevcNALVPS || t == hevcNALSPS || t == hevcNALPPS
	}
	return false
}

// nalIsVCL reports whether nal carries slice data.
func nalIsVCL(codec cbs.CodecID, nal []byte) bool {
	switch codec {
	case cbs.CodecH264:
		t := h264NALType(nal)
		return t == h264NALSlice || t == h264NALIDR || t == h264NALAuxSlice || (t >= 2 && t <= 4) || t == 20
	case cbs.CodecH265:
		t := hevcNALType(nal)
		return t >= 0 && t <= 31
	}
	return false
}

// packetHasParamSets reports whether any NAL in pkt is a parameter set.
func packetHasParamSets(codec cbs.CodecID, pkt []byte, lengthSize int) bool {
	nals, err := splitNALs(pkt, lengthSize)
	if err != nil {
		return false
	}
	for _, n := range nals {
		if nalIsParamSet(codec, n) {
			return true
		}
	}
	return false
}

// prependNALs returns pkt with nals inserted before its first NAL unit,
// framed like pkt. An AUD, if pkt starts with one, stays first.
func prependNALs(codec cbs.CodecID, pkt []byte, lengthSize int, nals [][]byte) ([]byte, error) {
	if len(nals) == 0 {
		return pkt, nil
	}
	existing, err := splitNALs(pkt, lengthSize)
	if err != nil {
		return nil, err
	}
	out := make([][]byte, 0, len(existing)+len(nals))
	i := 0
	if len(existing) > 0 {
		isAUD := (codec == cbs.CodecH264 && h264NALType(existing[0]) == h264NALAUD) ||
			(codec == cbs.CodecH265 && hevcNALType(existing[0]) == hevcNALAUD)
		if isAUD {
			out = append(out, existing[0])
			i = 1
		}
	}
	out = append(out, nals...)
	out = append(out, existing[i:]...)
	return joinNALs(out, lengthSize)
}

// avcCParamSets splits an avcC record into its SPS and PPS NAL units and
// the trailing bytes after the PPS array (the High-profile chroma/bit-depth
// extension, kept verbatim when the record is rebuilt).
func avcCParamSets(avcC []byte) (sps, pps [][]byte, tail []byte, err error) {
	if len(avcC) < 7 || avcC[0] != 1 {
		return nil, nil, nil, fmt.Errorf("not an avcC record")
	}
	pos := 5
	numSPS := int(avcC[pos] & 0x1f)
	pos++
	for i := 0; i < numSPS; i++ {
		if pos+2 > len(avcC) {
			return nil, nil, nil, fmt.Errorf("truncated avcC SPS array")
		}
		size := int(binary.BigEndian.Uint16(avcC[pos:]))
		pos += 2
		if pos+size > len(avcC) {
			return nil, nil, nil, fmt.Errorf("truncated avcC SPS")
		}
		sps = append(sps, avcC[pos:pos+size])
		pos += size
	}
	if pos >= len(avcC) {
		return nil, nil, nil, fmt.Errorf("truncated avcC PPS count")
	}
	numPPS := int(avcC[pos])
	pos++
	for i := 0; i < numPPS; i++ {
		if pos+2 > len(avcC) {
			return nil, nil, nil, fmt.Errorf("truncated avcC PPS array")
		}
		size := int(binary.BigEndian.Uint16(avcC[pos:]))
		pos += 2
		if pos+size > len(avcC) {
			return nil, nil, nil, fmt.Errorf("truncated avcC PPS")
		}
		pps = append(pps, avcC[pos:pos+size])
		pos += size
	}
	return sps, pps, avcC[pos:], nil
}

// avcCWithParamSets rebuilds avcC with extraSPS / extraPPS appended to its
// parameter-set arrays. The 5-byte header (profile, compatibility, level,
// NAL length size) and any trailing extension bytes are kept.
func avcCWithParamSets(avcC []byte, extraSPS, extraPPS [][]byte) ([]byte, error) {
	sps, pps, tail, err := avcCParamSets(avcC)
	if err != nil {
		return nil, err
	}
	sps = append(sps, extraSPS...)
	pps = append(pps, extraPPS...)
	if len(sps) > 31 || len(pps) > 255 {
		return nil, fmt.Errorf("too many parameter sets for avcC (%d SPS, %d PPS)", len(sps), len(pps))
	}
	out := make([]byte, 0, len(avcC)+256)
	out = append(out, avcC[:5]...)
	out = append(out, 0xE0|byte(len(sps)))
	for _, s := range sps {
		out = binary.BigEndian.AppendUint16(out, uint16(len(s)))
		out = append(out, s...)
	}
	out = append(out, byte(len(pps)))
	for _, p := range pps {
		out = binary.BigEndian.AppendUint16(out, uint16(len(p)))
		out = append(out, p...)
	}
	out = append(out, tail...)
	return out, nil
}

// hvcCParamSets returns every NAL unit stored in an hvcC record's arrays, in
// array order (VPS, SPS, PPS, SEI as written by the muxer).
func hvcCParamSets(hvcC []byte) ([][]byte, error) {
	if len(hvcC) < 23 || hvcC[0] != 1 {
		return nil, fmt.Errorf("not an hvcC record")
	}
	var nals [][]byte
	pos := 22
	numArrays := int(hvcC[pos])
	pos++
	for a := 0; a < numArrays; a++ {
		if pos+3 > len(hvcC) {
			return nil, fmt.Errorf("truncated hvcC array header")
		}
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
			nals = append(nals, hvcC[pos:pos+size])
			pos += size
		}
	}
	return nals, nil
}

// hevcCRAToBLA rewrites every CRA slice NAL in pkt (in place) to
// BLA_N_LP, the HEVC splicing tool: the decoder then starts a new coded
// video sequence at this picture (POC msb reset, reference pictures
// dropped, prior pictures still output) and discards any leading pictures,
// which smartcopy has already removed. Returns true when a NAL changed.
func hevcCRAToBLA(pkt []byte, lengthSize int) bool {
	changed := false
	rewrite := func(nal []byte) {
		if hevcNALType(nal) == hevcNALCRA {
			nal[0] = nal[0]&0x81 | hevcNALBLANLP<<1
			changed = true
		}
	}
	if lengthSize == 0 {
		for _, n := range splitAnnexB(pkt) {
			rewrite(n)
		}
		return changed
	}
	nals, err := lengthPrefixedNALs(pkt, lengthSize)
	if err != nil {
		return false
	}
	for _, n := range nals {
		rewrite(n)
	}
	return changed
}

// packetKeyframeNALType returns the NAL unit type of the first VCL NAL in
// pkt, or -1.
func packetFirstVCLType(codec cbs.CodecID, pkt []byte, lengthSize int) int {
	nals, err := splitNALs(pkt, lengthSize)
	if err != nil {
		return -1
	}
	for _, n := range nals {
		if nalIsVCL(codec, n) {
			if codec == cbs.CodecH264 {
				return h264NALType(n)
			}
			return hevcNALType(n)
		}
	}
	return -1
}

// escapeRBSP inserts emulation_prevention_three_byte where an RBSP would
// otherwise contain a 00 00 0x (x <= 3) sequence.
func escapeRBSP(rbsp []byte) []byte {
	out := make([]byte, 0, len(rbsp)+len(rbsp)/32+4)
	zeros := 0
	for _, b := range rbsp {
		if zeros >= 2 && b <= 3 {
			out = append(out, 3)
			zeros = 0
		}
		out = append(out, b)
		if b == 0 {
			zeros++
		} else {
			zeros = 0
		}
	}
	return out
}

// ueLen is the bit length of the Exp-Golomb code for v.
func ueLen(v uint32) int {
	n := 0
	for x := v + 1; x > 1; x >>= 1 {
		n++
	}
	return 2*n + 1
}

// setBits writes the low width bits of value at bit offset pos (MSB first).
func setBits(buf []byte, pos, width int, value uint32) {
	for i := 0; i < width; i++ {
		bit := (value >> uint(width-1-i)) & 1
		idx := pos + i
		mask := byte(0x80 >> uint(idx%8))
		if bit != 0 {
			buf[idx/8] |= mask
		} else {
			buf[idx/8] &^= mask
		}
	}
}

func getBits(buf []byte, pos, width int) uint32 {
	var v uint32
	for i := 0; i < width; i++ {
		idx := pos + i
		v <<= 1
		if buf[idx/8]&(0x80>>uint(idx%8)) != 0 {
			v |= 1
		}
	}
	return v
}
