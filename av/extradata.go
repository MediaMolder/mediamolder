// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "libavformat/avformat.h"
// #include "libavcodec/avcodec.h"
//
// static AVCodecParameters *mm_stream_codecpar(AVFormatContext *ctx, int i) {
//     return ctx->streams[i]->codecpar;
// }
import "C"
import "unsafe"

// StreamExtraData returns a copy of the codec extradata for stream i
// (codecpar->extradata: avcC / hvcC / av1C configuration records or raw
// Annex B parameter sets, depending on the container), or nil when the
// stream has none or i is out of range.
// Pos returns the byte position of the packet in its input stream
// (AVPacket.pos), or -1 when unknown.
func (p *Packet) Pos() int64 {
	if p == nil || p.p == nil {
		return -1
	}
	return int64(p.p.pos)
}

func (f *InputFormatContext) StreamExtraData(i int) []byte {
	if f == nil || f.p == nil || i < 0 || i >= int(f.p.nb_streams) {
		return nil
	}
	cp := C.mm_stream_codecpar(f.p, C.int(i))
	if cp == nil || cp.extradata == nil || cp.extradata_size <= 0 {
		return nil
	}
	return C.GoBytes(unsafe.Pointer(cp.extradata), cp.extradata_size)
}
