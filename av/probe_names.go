// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "libavcodec/avcodec.h"
// #include "libavutil/pixdesc.h"
// #include "libavutil/samplefmt.h"
// #include "libavutil/channel_layout.h"
// #include <stdlib.h>
//
// // av_channel_layout_default + av_channel_layout_describe in one helper so
// // callers don't have to manage AVChannelLayout lifetime.
// static int channel_layout_default_describe(int nb_channels, char *buf, int buflen) {
//     AVChannelLayout chl;
//     av_channel_layout_default(&chl, nb_channels);
//     int n = av_channel_layout_describe(&chl, buf, buflen);
//     av_channel_layout_uninit(&chl);
//     return n;
// }
// // Profile name lookup wrapper for an AVCodecID + profile int.
// static const char *profile_name_for(int codec_id, int profile) {
//     const AVCodec *c = avcodec_find_decoder((enum AVCodecID)codec_id);
//     if (!c) return NULL;
//     return av_get_profile_name(c, profile);
// }
// // FourCC tag → ASCII helper.
// static void tag_to_string(unsigned int tag, char *buf) {
//     buf[0] = (char)((tag >>  0) & 0xff);
//     buf[1] = (char)((tag >>  8) & 0xff);
//     buf[2] = (char)((tag >> 16) & 0xff);
//     buf[3] = (char)((tag >> 24) & 0xff);
//     buf[4] = '\0';
// }
import "C"

import "unsafe"

// PixFmtName returns the canonical FFmpeg name for an AVPixelFormat value
// (e.g. "yuv420p"). Returns "" if the format is unknown.
func PixFmtName(pixFmt int) string {
	cstr := C.av_get_pix_fmt_name(C.enum_AVPixelFormat(pixFmt))
	if cstr == nil {
		return ""
	}
	return C.GoString(cstr)
}

// SampleFmtName returns the canonical FFmpeg name for an AVSampleFormat value
// (e.g. "fltp"). Returns "" if the format is unknown.
func SampleFmtName(sampleFmt int) string {
	cstr := C.av_get_sample_fmt_name(C.enum_AVSampleFormat(sampleFmt))
	if cstr == nil {
		return ""
	}
	return C.GoString(cstr)
}

// CodecName returns the canonical short name for an AVCodecID value
// (e.g. "h264"). Returns "" if the codec id is unknown.
func CodecName(codecID uint32) string {
	cstr := C.avcodec_get_name(C.enum_AVCodecID(codecID))
	if cstr == nil {
		return ""
	}
	return C.GoString(cstr)
}

// DefaultChannelLayoutName returns the canonical name for the default channel
// layout of `nbChannels` channels (e.g. 1 → "mono", 2 → "stereo",
// 6 → "5.1"). Returns "" if the lookup fails.
func DefaultChannelLayoutName(nbChannels int) string {
	if nbChannels <= 0 {
		return ""
	}
	const bufLen = 64
	buf := (*C.char)(C.malloc(bufLen))
	defer C.free(unsafe.Pointer(buf))
	n := C.channel_layout_default_describe(C.int(nbChannels), buf, C.int(bufLen))
	if n <= 0 {
		return ""
	}
	return C.GoString(buf)
}

// ColorSpaceName returns the canonical name for an AVColorSpace value
// (e.g. "bt709", "bt2020nc"). Returns "" for unspecified/unknown values.
func ColorSpaceName(v int) string {
	cstr := C.av_color_space_name(C.enum_AVColorSpace(v))
	if cstr == nil {
		return ""
	}
	s := C.GoString(cstr)
	if s == "unspecified" || s == "unknown" {
		return ""
	}
	return s
}

// ColorRangeName returns the canonical name for an AVColorRange value
// (e.g. "tv", "pc"). Returns "" for unspecified/unknown values.
func ColorRangeName(v int) string {
	cstr := C.av_color_range_name(C.enum_AVColorRange(v))
	if cstr == nil {
		return ""
	}
	s := C.GoString(cstr)
	if s == "unspecified" || s == "unknown" {
		return ""
	}
	return s
}

// ColorPrimariesName returns the canonical name for an AVColorPrimaries value
// (e.g. "bt709", "bt2020"). Returns "" for unspecified/unknown values.
func ColorPrimariesName(v int) string {
	cstr := C.av_color_primaries_name(C.enum_AVColorPrimaries(v))
	if cstr == nil {
		return ""
	}
	s := C.GoString(cstr)
	if s == "unspecified" || s == "unknown" {
		return ""
	}
	return s
}

// ColorTransferName returns the canonical name for an
// AVColorTransferCharacteristic value (e.g. "bt709", "smpte2084").
// Returns "" for unspecified/unknown values.
func ColorTransferName(v int) string {
	cstr := C.av_color_transfer_name(C.enum_AVColorTransferCharacteristic(v))
	if cstr == nil {
		return ""
	}
	s := C.GoString(cstr)
	if s == "unspecified" || s == "unknown" {
		return ""
	}
	return s
}

// ProfileName returns the codec-specific profile name for a given AVCodecID
// + profile int (e.g. h264 + 100 → "High"). Returns "" if unknown.
func ProfileName(codecID uint32, profile int) string {
	if profile < 0 {
		return ""
	}
	cstr := C.profile_name_for(C.int(codecID), C.int(profile))
	if cstr == nil {
		return ""
	}
	return C.GoString(cstr)
}

// FieldOrderName maps an AVFieldOrder value to a friendly string.
// Returns "" for unknown.
func FieldOrderName(v int) string {
	switch v {
	case int(C.AV_FIELD_PROGRESSIVE):
		return "progressive"
	case int(C.AV_FIELD_TT):
		return "tt" // top coded first, top displayed first
	case int(C.AV_FIELD_BB):
		return "bb"
	case int(C.AV_FIELD_TB):
		return "tb"
	case int(C.AV_FIELD_BT):
		return "bt"
	default:
		return ""
	}
}

// CodecTagString returns the four-CC codec tag as ASCII (e.g. "avc1", "mp4a").
// Returns "" if all bytes are zero.
func CodecTagString(tag uint32) string {
	if tag == 0 {
		return ""
	}
	buf := (*C.char)(C.malloc(5))
	defer C.free(unsafe.Pointer(buf))
	C.tag_to_string(C.uint(tag), buf)
	s := C.GoString(buf)
	// Filter out non-printable tags.
	for _, r := range s {
		if r < 0x20 || r > 0x7e {
			return ""
		}
	}
	return s
}
