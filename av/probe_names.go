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
