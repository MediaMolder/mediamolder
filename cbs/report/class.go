// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package report

import "github.com/MediaMolder/MediaMolder/cbs"

// Unit classes, for separating picture data from side information when
// plotting bit rate or filtering reports:
//
//	vcl   — coded picture data (slices, AV1 frame / tile group OBUs)
//	ps    — parameter sets (SPS/PPS/VPS, AV1 sequence header)
//	sei   — supplemental enhancement information (SEI, AV1 metadata OBUs)
//	other — AUD, filler, delimiters, reserved / unspecified types
const (
	classVCL   = "vcl"
	classPS    = "ps"
	classSEI   = "sei"
	classOther = "other"
)

// classify buckets a unit by its numeric type. It needs only the type, so
// undecomposed and failed units classify too.
func classify(codec string, u *cbs.Unit) string {
	t := u.Type
	switch codec {
	case "h264":
		switch {
		case t >= 1 && t <= 5, t >= 19 && t <= 21: // slices, partitions, aux, extensions
			return classVCL
		case t == 7, t == 8, t == 13, t == 15, t == 16: // SPS, PPS, SPS ext, subset SPS, DPS
			return classPS
		case t == 6:
			return classSEI
		}
	case "hevc", "h265":
		switch {
		case t <= 31: // VCL range
			return classVCL
		case t >= 32 && t <= 34: // VPS, SPS, PPS
			return classPS
		case t == 39, t == 40: // SEI prefix / suffix
			return classSEI
		}
	case "av1":
		switch t {
		case 3, 4, 6, 7, 8: // FRAME_HEADER, TILE_GROUP, FRAME, REDUNDANT_FRAME_HEADER, TILE_LIST
			return classVCL
		case 1: // SEQUENCE_HEADER
			return classPS
		case 5: // METADATA
			return classSEI
		}
	}
	return classOther
}

// packetTime converts a stream-time-base timestamp to seconds; ok is
// false when the time base is unknown.
func packetTime(tb [2]int, ts int64) (float64, bool) {
	if tb[0] <= 0 || tb[1] <= 0 {
		return 0, false
	}
	return float64(ts) * float64(tb[0]) / float64(tb[1]), true
}
