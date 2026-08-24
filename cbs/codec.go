// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package cbs

import "fmt"

// CodecID selects the bitstream syntax to parse.
type CodecID int

const (
	CodecH264 CodecID = iota + 1
	CodecH265
	CodecAV1
)

func (c CodecID) String() string {
	switch c {
	case CodecH264:
		return "h264"
	case CodecH265:
		return "hevc"
	case CodecAV1:
		return "av1"
	}
	return fmt.Sprintf("CodecID(%d)", int(c))
}

// CodecFromName maps an FFmpeg codec short name (av.CodecName) to a
// CodecID. Preferred over enum values, which shift between FFmpeg
// versions.
func CodecFromName(name string) (CodecID, bool) {
	switch name {
	case "h264":
		return CodecH264, true
	case "hevc", "h265":
		return CodecH265, true
	case "av1":
		return CodecAV1, true
	}
	return 0, false
}

// Codec parses fragments of one elementary stream, accumulating parameter
// set state across calls (CodedBitstreamContext).
//
// Errors: a per-unit parse failure is recorded on the Unit and does not
// abort the fragment. The returned error is non-nil only for failures of
// the fragment split itself (bad avcC/hvcC/av1C, bad NALFF length, no
// start code); the units split so far are still returned.
type Codec interface {
	// ReadExtradata parses codec extradata (avcC / hvcC / av1C or raw
	// Annex B / OBUs), seeding parameter-set state (header == 1).
	ReadExtradata(data []byte) (*Fragment, error)
	// ReadPacket parses one packet's payload (header == 0).
	ReadPacket(data []byte) (*Fragment, error)
	// Flush drops inter-frame state (but keeps parameter sets), for use
	// after a seek; ports cbs_h264_flush / cbs_av1_flush.
	Flush()
}

// New returns a Codec for the given bitstream syntax. tr may be nil to
// disable tracing.
func New(codec CodecID, tr Tracer) (Codec, error) {
	switch codec {
	case CodecH264:
		return newH264Context(tr), nil
	case CodecH265:
		return newH265Context(tr), nil
	case CodecAV1:
		return newAV1Context(tr), nil
	}
	return nil, fmt.Errorf("cbs: %w: codec %v", ErrUnsupported, codec)
}
