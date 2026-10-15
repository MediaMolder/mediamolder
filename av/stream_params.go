// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "libavformat/avformat.h"
// #include "libavcodec/avcodec.h"
// #include "libavutil/mem.h"
// #include <string.h>
//
// // Replace an output stream's codecpar->extradata with a padded copy of
// // data (av_malloc + AV_INPUT_BUFFER_PADDING_SIZE, like
// // ff_alloc_extradata). The old buffer is freed.
// static int mm_set_stream_extradata(AVFormatContext *ctx, int idx,
//                                    const uint8_t *data, int size) {
//     if (!ctx || idx < 0 || idx >= (int)ctx->nb_streams) return -1;
//     AVCodecParameters *par = ctx->streams[idx]->codecpar;
//     uint8_t *buf = NULL;
//     if (size > 0) {
//         buf = av_mallocz(size + AV_INPUT_BUFFER_PADDING_SIZE);
//         if (!buf) return -2;
//         memcpy(buf, data, size);
//     }
//     av_freep(&par->extradata);
//     par->extradata = buf;
//     par->extradata_size = size > 0 ? size : 0;
//     return 0;
// }
// static uint32_t mm_stream_codec_tag(AVFormatContext *ctx, int idx) {
//     if (!ctx || idx < 0 || idx >= (int)ctx->nb_streams) return 0;
//     return ctx->streams[idx]->codecpar->codec_tag;
// }
// static const char *mm_oformat_name(AVFormatContext *ctx) {
//     if (!ctx || !ctx->oformat) return "";
//     return ctx->oformat->name;
// }
import "C"

import (
	"fmt"
	"unsafe"
)

// SetStreamExtradata replaces output stream idx's codecpar extradata (avcC /
// hvcC / Annex B parameter sets) with a copy of data. Must be called after
// AddStream / AddStreamFromInput and before WriteHeader. An empty data
// clears the extradata.
func (f *OutputFormatContext) SetStreamExtradata(idx int, data []byte) error {
	if f == nil || f.p == nil {
		return fmt.Errorf("SetStreamExtradata: nil format context")
	}
	var ptr *C.uint8_t
	if len(data) > 0 {
		ptr = (*C.uint8_t)(unsafe.Pointer(&data[0]))
	}
	switch C.mm_set_stream_extradata(f.p, C.int(idx), ptr, C.int(len(data))) {
	case 0:
		return nil
	case -2:
		return fmt.Errorf("SetStreamExtradata: out of memory")
	default:
		return fmt.Errorf("SetStreamExtradata: invalid stream index %d", idx)
	}
}

// StreamCodecTag returns output stream idx's codecpar.codec_tag as a
// FourCC string ("avc1", "hvc1", ...), or "" when unset or idx is invalid.
func (f *OutputFormatContext) StreamCodecTag(idx int) string {
	if f == nil || f.p == nil {
		return ""
	}
	v := uint32(C.mm_stream_codec_tag(f.p, C.int(idx)))
	if v == 0 {
		return ""
	}
	return string([]byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)})
}

// FormatName returns the muxer's short name ("mov", "mp4", "matroska", ...).
func (f *OutputFormatContext) FormatName() string {
	if f == nil || f.p == nil {
		return ""
	}
	return C.GoString(C.mm_oformat_name(f.p))
}
