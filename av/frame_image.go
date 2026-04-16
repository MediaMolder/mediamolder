// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "libavutil/frame.h"
// #include "libavutil/pixfmt.h"
// #include "libavutil/pixdesc.h"
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
// // sad_8bit computes the Sum of Absolute Differences between two 8-bit
// // planes, respecting stride. This simple loop auto-vectorizes well with
// // -O2 on both ARM64 (NEON) and x86_64 (SSE/AVX).
// static uint64_t sad_8bit(const uint8_t *src1, ptrdiff_t stride1,
//                          const uint8_t *src2, ptrdiff_t stride2,
//                          int width, int height) {
//     uint64_t sad = 0;
//     for (int y = 0; y < height; y++) {
//         for (int x = 0; x < width; x++) {
//             int d = (int)src1[x] - (int)src2[x];
//             sad += (d < 0) ? -d : d;
//         }
//         src1 += stride1;
//         src2 += stride2;
//     }
//     return sad;
// }
//
// // is_yuv_planar returns 1 if the pixel format is a planar YUV format with
// // at least 3 components (so plane 0 is the full-resolution luma plane).
// static int is_yuv_planar(int pix_fmt) {
//     const AVPixFmtDescriptor *desc = av_pix_fmt_desc_get(pix_fmt);
//     if (!desc) return 0;
//     return !(desc->flags & AV_PIX_FMT_FLAG_RGB) &&
//             (desc->flags & AV_PIX_FMT_FLAG_PLANAR) &&
//             desc->nb_components >= 3;
// }
//
// // frame_luma_sad computes the Mean Absolute Frame Difference (MAFD) between
// // two frames on the luma channel, matching FFmpeg's scdet filter algorithm.
// //
// // For YUV planar formats: operates directly on plane 0 (the Y luma plane)
// // without any pixel format conversion — zero-copy, zero-allocation.
// //
// // For all other formats (RGB, packed, etc.): falls back to swscale GRAY8
// // conversion.
// //
// // Returns MAFD on a 0–100 scale (matching scdet), or -1.0 on error.
// static double frame_luma_sad(const AVFrame *a, const AVFrame *b) {
//     if (!a || !b || a->width <= 0 || a->height <= 0)
//         return -1.0;
//     if (a->width != b->width || a->height != b->height)
//         return -1.0;
//
//     int w = a->width;
//     int h = a->height;
//     uint64_t sad;
//     uint64_t count;
//
//     // Fast path: YUV planar — use Y plane directly (no conversion).
//     if (is_yuv_planar(a->format) && is_yuv_planar(b->format)) {
//         // For 8-bit YUV, data[0] is the luma plane with linesize[0] stride.
//         // av_image_get_linesize gives the actual data width (w/o padding).
//         int luma_w = av_image_get_linesize(a->format, w, 0);
//         if (luma_w <= 0) return -1.0;
//         sad = sad_8bit(a->data[0], a->linesize[0],
//                        b->data[0], b->linesize[0],
//                        luma_w, h);
//         count = (uint64_t)luma_w * h;
//     } else {
//         // Slow path: convert both frames to GRAY8 via swscale.
//         struct SwsContext *sws_a = sws_getContext(
//             w, h, a->format, w, h, AV_PIX_FMT_GRAY8,
//             SWS_BILINEAR, NULL, NULL, NULL);
//         if (!sws_a) return -1.0;
//
//         struct SwsContext *sws_b = sws_getContext(
//             w, h, b->format, w, h, AV_PIX_FMT_GRAY8,
//             SWS_BILINEAR, NULL, NULL, NULL);
//         if (!sws_b) { sws_freeContext(sws_a); return -1.0; }
//
//         uint8_t *buf_a = (uint8_t *)av_malloc(w * h);
//         uint8_t *buf_b = (uint8_t *)av_malloc(w * h);
//         if (!buf_a || !buf_b) {
//             av_free(buf_a); av_free(buf_b);
//             sws_freeContext(sws_a); sws_freeContext(sws_b);
//             return -1.0;
//         }
//
//         uint8_t *dst_a[4] = { buf_a, NULL, NULL, NULL };
//         int dst_ls_a[4] = { w, 0, 0, 0 };
//         sws_scale(sws_a, (const uint8_t *const *)a->data, a->linesize,
//                   0, h, dst_a, dst_ls_a);
//
//         uint8_t *dst_b[4] = { buf_b, NULL, NULL, NULL };
//         int dst_ls_b[4] = { w, 0, 0, 0 };
//         sws_scale(sws_b, (const uint8_t *const *)b->data, b->linesize,
//                   0, h, dst_b, dst_ls_b);
//
//         sad = sad_8bit(buf_a, w, buf_b, w, w, h);
//         count = (uint64_t)w * h;
//
//         av_free(buf_a); av_free(buf_b);
//         sws_freeContext(sws_a); sws_freeContext(sws_b);
//     }
//
//     // MAFD on 0–100 scale, matching FFmpeg scdet.
//     return (double)sad * 100.0 / (double)count / 255.0;
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

// FrameSceneScore computes the Mean Absolute Frame Difference (MAFD) between
// two video frames on the luma (brightness) channel, using the same algorithm
// as FFmpeg's scdet filter.
//
// For YUV planar formats (YUV420P, YUV422P, YUV444P, etc.): operates directly
// on the Y plane — zero pixel-format conversion, zero allocation.
//
// For other formats (RGB24, RGBA, packed YUV, etc.): falls back to a GRAY8
// conversion via libswscale before computing the difference.
//
// Returns MAFD on a 0–100 scale (matching FFmpeg scdet):
//   - 0 means the frames are identical
//   - Higher values mean more change
//   - Typical hard cuts score 20–80+
//
// Both frames must have the same dimensions. Returns an error if either frame
// is nil, has zero dimensions, or the dimensions don't match.
func FrameSceneScore(a, b *Frame) (float64, error) {
	if a == nil || a.p == nil || b == nil || b.p == nil {
		return 0, fmt.Errorf("av: FrameSceneScore: nil frame")
	}
	aw, ah := a.Width(), a.Height()
	bw, bh := b.Width(), b.Height()
	if aw <= 0 || ah <= 0 || bw <= 0 || bh <= 0 {
		return 0, fmt.Errorf("av: FrameSceneScore: invalid dimensions %d×%d vs %d×%d", aw, ah, bw, bh)
	}
	if aw != bw || ah != bh {
		return 0, fmt.Errorf("av: FrameSceneScore: dimension mismatch %d×%d vs %d×%d", aw, ah, bw, bh)
	}

	mafd := float64(C.frame_luma_sad(a.p, b.p))
	if mafd < 0 {
		return 0, fmt.Errorf("av: FrameSceneScore: SAD computation failed")
	}
	return mafd, nil
}
