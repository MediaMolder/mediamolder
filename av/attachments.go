// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include <stdlib.h>
// #include <string.h>
// #include "libavformat/avformat.h"
// #include "libavcodec/avcodec.h"
// #include "libavutil/dict.h"
// #include "libavutil/mem.h"
//
// // Add an AVMEDIA_TYPE_ATTACHMENT stream to fc carrying the supplied
// // payload as extradata. Mirrors fftools/ffmpeg_mux_init.c
// // of_add_attachments(): create stream with codec_type=ATTACHMENT,
// // copy the file content into codecpar->extradata (with the
// // AV_INPUT_BUFFER_PADDING zeroed tail), set the "filename" stream
// // metadata entry, optionally set "mimetype". codec_id is guessed from
// // the supplied filename via av_guess_codec; mimetype overrides via
// // metadata so muxers that read it (matroska) write the right
// // CodecPrivate/MimeType pair.
// static int add_attachment(AVFormatContext *fc,
//                           const uint8_t *data, int data_len,
//                           const char *filename, const char *mimetype) {
//     if (!fc || data_len < 0) return AVERROR(EINVAL);
//     AVStream *st = avformat_new_stream(fc, NULL);
//     if (!st) return AVERROR(ENOMEM);
//     st->codecpar->codec_type = AVMEDIA_TYPE_ATTACHMENT;
//     // Guess codec_id from the filename extension via the muxer's
//     // attachment table (matroska maps .ttf -> AV_CODEC_ID_TTF, .otf
//     // -> AV_CODEC_ID_OTF, image extensions -> mjpeg/png, etc.).
//     if (fc->oformat && filename) {
//         enum AVCodecID id = av_guess_codec(fc->oformat, NULL, filename,
//                                            NULL, AVMEDIA_TYPE_ATTACHMENT);
//         if (id != AV_CODEC_ID_NONE) st->codecpar->codec_id = id;
//     }
//     uint8_t *buf = av_malloc((size_t)data_len + AV_INPUT_BUFFER_PADDING_SIZE);
//     if (!buf) return AVERROR(ENOMEM);
//     if (data_len > 0) memcpy(buf, data, (size_t)data_len);
//     memset(buf + data_len, 0, AV_INPUT_BUFFER_PADDING_SIZE);
//     st->codecpar->extradata = buf;
//     st->codecpar->extradata_size = data_len;
//     if (filename && filename[0]) {
//         av_dict_set(&st->metadata, "filename", filename, 0);
//     }
//     if (mimetype && mimetype[0]) {
//         av_dict_set(&st->metadata, "mimetype", mimetype, 0);
//     }
//     return st->index;
// }
import "C"

import (
	"fmt"
	"unsafe"
)

// AddAttachment adds an `AVMEDIA_TYPE_ATTACHMENT` stream to the
// output container carrying `data` (typically a font, cover art, or
// chapter sidecar) as the stream's `codecpar->extradata`. `filename`
// is required — it both populates the `filename` stream-metadata key
// and drives codec-id guessing via `av_guess_codec` on the muxer's
// attachment-codec table. `mimetype` (e.g. `"application/x-truetype-font"`,
// `"image/png"`) is optional; matroska / mkv muxers carry it through
// when present. Returns the zero-based stream index. Must be called
// before WriteHeader. Mirrors `fftools/ffmpeg_mux_init.c`'s
// `of_add_attachments`. Wave 6 #31.
func (f *OutputFormatContext) AddAttachment(data []byte, filename, mimetype string) (int, error) {
	if f == nil || f.p == nil {
		return -1, fmt.Errorf("AddAttachment: nil OutputFormatContext")
	}
	if filename == "" {
		return -1, fmt.Errorf("AddAttachment: filename is required")
	}
	cFilename := C.CString(filename)
	defer C.free(unsafe.Pointer(cFilename))
	var cMime *C.char
	if mimetype != "" {
		cMime = C.CString(mimetype)
		defer C.free(unsafe.Pointer(cMime))
	}
	var dataPtr *C.uint8_t
	if len(data) > 0 {
		dataPtr = (*C.uint8_t)(unsafe.Pointer(&data[0]))
	}
	idx := C.add_attachment(f.p, dataPtr, C.int(len(data)), cFilename, cMime)
	if idx < 0 {
		return -1, newErr(idx)
	}
	return int(idx), nil
}
