// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "libavcodec/avcodec.h"
// #include "libavutil/buffer.h"
// #include "libavutil/error.h"
// #include <errno.h>
// #include <string.h>
//
// // Swap a packet's payload for a fresh reference-counted copy of data,
// // keeping every other field (timestamps, flags, stream index, side
// // data). The previous buffer is unreferenced, so a clone that shared it
// // is unaffected.
// static int mm_packet_replace_data(AVPacket *pkt, const uint8_t *data, int size) {
//     AVBufferRef *buf = av_buffer_alloc(size + AV_INPUT_BUFFER_PADDING_SIZE);
//     if (!buf) return AVERROR(ENOMEM);
//     if (size > 0) memcpy(buf->data, data, size);
//     memset(buf->data + size, 0, AV_INPUT_BUFFER_PADDING_SIZE);
//     av_buffer_unref(&pkt->buf);
//     pkt->buf = buf;
//     pkt->data = buf->data;
//     pkt->size = size;
//     return 0;
// }
import "C"

import "unsafe"

// ReplaceData swaps the packet's payload for a copy of data, leaving its
// timestamps, duration, flags, stream index and side data untouched. Clones
// that shared the previous buffer keep the old bytes.
func (pkt *Packet) ReplaceData(data []byte) error {
	if pkt == nil || pkt.p == nil {
		return &Err{Code: -22, Message: "av: ReplaceData on nil packet"}
	}
	var ptr *C.uint8_t
	if len(data) > 0 {
		ptr = (*C.uint8_t)(unsafe.Pointer(&data[0]))
	}
	return newErr(C.mm_packet_replace_data(pkt.p, ptr, C.int(len(data))))
}
