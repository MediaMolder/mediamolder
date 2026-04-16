// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "libavutil/frame.h"
// #include "libavutil/pixfmt.h"
// #include "libavutil/imgutils.h"
// #include "libswscale/swscale.h"
//
// // frame_to_rgba converts an AVFrame of any pixel format to packed RGBA.
// // The caller must av_free(*out_data) when done. Returns 0 on success.
// static int frame_to_rgba(const AVFrame *frame, uint8_t **out_data, int *out_linesize) {
//     if (!frame || frame->width <= 0 || frame->height <= 0)
//         return -1;
//
//     struct SwsContext *sws = sws_getContext(
//         frame->width, frame->height, frame->format,
//         frame->width, frame->height, AV_PIX_FMT_RGBA,
//         SWS_BILINEAR, NULL, NULL, NULL);
//     if (!sws) return AVERROR(ENOMEM);
//
//     *out_linesize = frame->width * 4;
//     *out_data = (uint8_t *)av_malloc((size_t)(*out_linesize) * frame->height);
//     if (!*out_data) {
//         sws_freeContext(sws);
//         return AVERROR(ENOMEM);
//     }
//
//     uint8_t *dst[4] = { *out_data, NULL, NULL, NULL };
//     int dst_linesize[4] = { *out_linesize, 0, 0, 0 };
//
//     sws_scale(sws, (const uint8_t *const *)frame->data, frame->linesize,
//               0, frame->height, dst, dst_linesize);
//
//     sws_freeContext(sws);
//     return 0;
// }
//
// static int get_frame_pix_fmt(const AVFrame *frame) {
//     return frame->format;
// }
//
// static int pix_fmt_rgba(void) { return AV_PIX_FMT_RGBA; }
import "C"

import (
	"fmt"
	"image"
	"unsafe"
)

// PixFmt returns the pixel format of the frame (an AVPixelFormat value).
// Returns -1 if the frame has no format set.
func (f *Frame) PixFmt() int {
	return int(C.get_frame_pix_fmt(f.p))
}

// PixFmtRGBA returns the AVPixelFormat value for AV_PIX_FMT_RGBA.
func PixFmtRGBA() int {
	return int(C.pix_fmt_rgba())
}

// ToRGBA converts the frame to an *image.RGBA using libswscale.
// Supports any input pixel format that swscale can handle (YUV420P, NV12,
// RGB24, etc.). The returned image owns its own Go-allocated pixel buffer.
//
// Returns an error if the frame is nil, has zero dimensions, or the
// conversion fails (e.g. hardware-surface frames that haven't been
// transferred to system memory).
func (f *Frame) ToRGBA() (*image.RGBA, error) {
	if f == nil || f.p == nil {
		return nil, fmt.Errorf("av: ToRGBA called on nil frame")
	}
	w, h := f.Width(), f.Height()
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("av: ToRGBA: invalid frame dimensions %d×%d", w, h)
	}

	var outData *C.uint8_t
	var outLinesize C.int

	ret := C.frame_to_rgba(f.p, &outData, &outLinesize)
	if ret < 0 {
		return nil, newErr(ret)
	}
	defer C.av_free(unsafe.Pointer(outData))

	stride := int(outLinesize)
	totalBytes := stride * h

	// Copy from C-allocated buffer into Go-managed memory.
	pix := make([]byte, totalBytes)
	copy(pix, unsafe.Slice((*byte)(unsafe.Pointer(outData)), totalBytes))

	return &image.RGBA{
		Pix:    pix,
		Stride: stride,
		Rect:   image.Rect(0, 0, w, h),
	}, nil
}
