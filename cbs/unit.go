// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package cbs

// Skip reasons for units that are listed but not decomposed. FFmpeg either
// drops these silently or logs at verbose level; the port keeps them in the
// fragment so a report can show the complete unit inventory.
const (
	// SkipUnimplemented mirrors AVERROR(ENOSYS): a valid unit type whose
	// decomposition CBS does not implement (e.g. H.264 SVC/MVC NALs).
	SkipUnimplemented = "unimplemented"
	// SkipHEVCLayer: HEVC nuh_layer_id > 0 and not VPS/SPS/PPS
	// (cbs_h2645.c fragment_add_nals drops these).
	SkipHEVCLayer = "hevc_layer"
	// SkipLayerID63: HEVC nuh_layer_id == 63 (h2645_parse.c drops these).
	SkipLayerID63 = "layer_id_63"
	// SkipEmpty: the NAL was empty after trailing-zero stripping.
	SkipEmpty = "empty"
	// SkipBadNALHeader: forbidden_zero_bit set or invalid temporal id;
	// FFmpeg warns and skips the NAL.
	SkipBadNALHeader = "bad_nal_header"
	// SkipDroppedOBU: AV1 OBU dropped by operating-point selection
	// (AVERROR(EAGAIN) in cbs_av1).
	SkipDroppedOBU = "dropped_obu"
)

// Unit is one NAL unit (H.26x) or OBU (AV1) within a fragment
// (CodedBitstreamUnit, with added provenance FFmpeg does not keep).
type Unit struct {
	// Type is nal_unit_type (H.26x) or obu_type (AV1).
	Type uint32
	// TypeName is a short human name for Type ("SPS", "IDR", ...).
	TypeName string

	// Offset is the byte offset of the unit's first byte (after any start
	// code or length prefix) within the fragment data.
	Offset int
	// PrefixSize is the size in bytes of the start code (3 or 4,
	// including a zero_byte), length prefix (nal_length_size, or 2 inside
	// avcC/hvcC arrays), or 0 for AV1 OBUs.
	PrefixSize int
	// RawSize is the number of payload bytes consumed from the fragment,
	// including emulation-prevention bytes and trailing zeros.
	RawSize int

	// RBSP is the unit's payload with emulation-prevention bytes removed
	// and trailing zero bytes stripped; this is what the bit positions in
	// traced Elements are relative to. For AV1 it is the whole OBU
	// including its header (AV1 has no emulation prevention).
	RBSP []byte
	// EPBPositions lists, for each removed emulation_prevention_three_byte,
	// the RBSP index of the byte immediately after the two zero bytes
	// (nal->skipped_bytes_pos semantics from h2645_parse.c).
	EPBPositions []int

	// NAL/OBU header fields available even for undecomposed units.
	NalRefIDC  uint8 // H.264
	NuhLayerID uint8 // H.265
	TemporalID uint8 // H.265 / AV1 extension
	SpatialID  uint8 // AV1 extension

	// Decomposed reports whether Content was parsed (FFmpeg would have
	// produced trace lines for this unit).
	Decomposed bool
	// Skip is the non-empty reason when the unit was deliberately not
	// decomposed (see the Skip* constants).
	Skip string
	// Content is the decomposed header struct (*H264RawSPS, ...); nil
	// when !Decomposed.
	Content any
	// Err is the parse error for this unit, if any; the codec context is
	// left exactly as it was at the failure point.
	Err error
}

// Fragment is a split packet, extradata blob, or side-data blob
// (CodedBitstreamFragment).
type Fragment struct {
	// Data is the input the fragment was split from.
	Data []byte
	// Units are the fragment's units in bitstream order, including
	// undecomposed ones.
	Units []Unit
}
