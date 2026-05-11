// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include <stdlib.h>
// #include "libavformat/avformat.h"
// #include "libavcodec/avcodec.h"
// #include "libavutil/avutil.h"
//
// // add_cover_art opens the image at `path`, adds an AVMEDIA_TYPE_VIDEO
// // stream with AV_DISPOSITION_ATTACHED_PIC to `fc`, reads the first
// // video frame into a new AVPacket (owned by the caller), and returns
// // the stream index via `st_idx_out` and the packet via `pkt_out`.
// //
// // The caller must:
// //   1. Call avformat_write_header on `fc` (WriteHeaderWithOptions).
// //   2. Write the returned packet via av_interleaved_write_frame.
// //   3. Free the packet with av_packet_free when done.
// //
// // Mirrors the shape fftools/ffmpeg_mux_init.c uses when the user passes
// // `-i cover.jpg -map 1:v -c:v:1 copy -disposition:v:1 attached_pic`.
// static int add_cover_art(AVFormatContext *fc, const char *path,
//                           AVPacket **pkt_out, int *st_idx_out) {
//     if (!fc || !path || !pkt_out || !st_idx_out) return AVERROR(EINVAL);
//
//     AVFormatContext *img = NULL;
//     int ret = avformat_open_input(&img, path, NULL, NULL);
//     if (ret < 0) return ret;
//
//     ret = avformat_find_stream_info(img, NULL);
//     if (ret < 0) { avformat_close_input(&img); return ret; }
//
//     // Find the first video stream in the image file.
//     int img_vi = -1;
//     for (unsigned i = 0; i < img->nb_streams; i++) {
//         if (img->streams[i]->codecpar->codec_type == AVMEDIA_TYPE_VIDEO) {
//             img_vi = (int)i;
//             break;
//         }
//     }
//     if (img_vi < 0) {
//         avformat_close_input(&img);
//         return AVERROR_INVALIDDATA;
//     }
//
//     // Add a new video stream to the output container.
//     AVStream *st = avformat_new_stream(fc, NULL);
//     if (!st) { avformat_close_input(&img); return AVERROR(ENOMEM); }
//
//     ret = avcodec_parameters_copy(st->codecpar, img->streams[img_vi]->codecpar);
//     if (ret < 0) { avformat_close_input(&img); return ret; }
//
//     // Clear codec_tag so the muxer can pick a container-appropriate one
//     // (same as AddStreamFromInput). Set the attached-pic disposition so
//     // the MP4/MOV/MKV muxer treats this stream as cover art.
//     st->codecpar->codec_tag = 0;
//     st->disposition = AV_DISPOSITION_ATTACHED_PIC;
//     // Use a fine-grained time_base; most still-image files report
//     // {1,90000} or {1,1} — we inherit the source's value so rescaling
//     // is a no-op, but fall back to {1,90000} if the source reported
//     // a degenerate base.
//     if (img->streams[img_vi]->time_base.num > 0 &&
//         img->streams[img_vi]->time_base.den > 0) {
//         st->time_base = img->streams[img_vi]->time_base;
//     } else {
//         st->time_base = (AVRational){1, 90000};
//     }
//
//     // Read the first video packet from the image demuxer.
//     AVPacket *pkt = av_packet_alloc();
//     if (!pkt) { avformat_close_input(&img); return AVERROR(ENOMEM); }
//
//     while ((ret = av_read_frame(img, pkt)) >= 0) {
//         if (pkt->stream_index == img_vi) break;
//         av_packet_unref(pkt);
//     }
//     avformat_close_input(&img);
//
//     if (ret < 0) { av_packet_free(&pkt); return ret; }
//
//     // Retarget the packet at the new output stream and clear
//     // timestamps (still images carry no meaningful PTS).
//     pkt->stream_index = st->index;
//     pkt->pts          = AV_NOPTS_VALUE;
//     pkt->dts          = AV_NOPTS_VALUE;
//     pkt->duration     = 0;
//     pkt->pos          = -1;
//
//     *pkt_out     = pkt;
//     *st_idx_out  = st->index;
//     return 0;
// }
import "C"

import (
	"fmt"
	"unsafe"
)

// AddCoverArt opens the image file at path, adds an AVMEDIA_TYPE_VIDEO
// stream with AV_DISPOSITION_ATTACHED_PIC to the output container, reads
// the first video frame, and returns the stream index and a single-frame
// Packet that must be written to the muxer (via WritePacket) **after**
// WriteHeader. The caller must call pkt.Close() once the packet has been
// written.
//
// Mirrors the behaviour of `ffmpeg -i cover.jpg -map 1:v -c:v:1 copy
// -disposition:v:1 attached_pic` for MP4 / M4A / MOV / MKV containers.
// Must be called before WriteHeader. Wave 11 #64.
func (f *OutputFormatContext) AddCoverArt(path string) (int, *Packet, error) {
	if f == nil || f.p == nil {
		return -1, nil, fmt.Errorf("AddCoverArt: nil OutputFormatContext")
	}
	if path == "" {
		return -1, nil, fmt.Errorf("AddCoverArt: path is required")
	}
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	var cPkt *C.AVPacket
	var stIdx C.int
	ret := C.add_cover_art(f.p, cPath, &cPkt, &stIdx)
	if ret < 0 {
		return -1, nil, newErr(ret)
	}
	pkt := &Packet{p: cPkt}
	leakTrack(unsafe.Pointer(cPkt), "AVPacket")
	return int(stIdx), pkt, nil
}
