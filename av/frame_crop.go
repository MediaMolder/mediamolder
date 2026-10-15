// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include <errno.h>
// #include "libavutil/frame.h"
// #include "libavutil/pixdesc.h"
//
// static int mm_frame_has_crop(const AVFrame *f) {
//     return f->crop_top || f->crop_bottom || f->crop_left || f->crop_right;
// }
//
// static int mm_pix_fmt_log2_chroma_w(int fmt) {
//     const AVPixFmtDescriptor *d = av_pix_fmt_desc_get((enum AVPixelFormat)fmt);
//     return d ? d->log2_chroma_w : -1;
// }
//
// // mm_frame_crop_columns fills dst with columns [left, left+width) of src.
// // av_frame_apply_cropping computes the window offsets the way FFmpeg's crop
// // filter does (libavfilter/vf_crop.c): (x >> log2_chroma_w) * comp.step per
// // plane. AV_FRAME_CROP_UNALIGNED keeps left exact instead of moving it to an
// // aligned pointer. av_frame_copy then copies the window into dst's own
// // aligned buffers, so dst is an ordinary frame.
// static int mm_frame_crop_columns(AVFrame *dst, const AVFrame *src, int left, int width) {
//     const AVPixFmtDescriptor *d = av_pix_fmt_desc_get((enum AVPixelFormat)src->format);
//     if (!d || (d->flags & (AV_PIX_FMT_FLAG_HWACCEL | AV_PIX_FMT_FLAG_BITSTREAM)))
//         return AVERROR(ENOSYS);
//     AVFrame *win = av_frame_alloc();
//     if (!win)
//         return AVERROR(ENOMEM);
//     int ret = av_frame_ref(win, src);
//     if (ret < 0)
//         goto end;
//     win->crop_left  = left;
//     win->crop_right = src->width - left - width;
//     ret = av_frame_apply_cropping(win, AV_FRAME_CROP_UNALIGNED);
//     if (ret < 0)
//         goto end;
//     dst->format = src->format;
//     dst->width  = width;
//     dst->height = src->height;
//     ret = av_frame_get_buffer(dst, 0);
//     if (ret < 0)
//         goto end;
//     ret = av_frame_copy(dst, win);
//     if (ret < 0)
//         goto end;
//     ret = av_frame_copy_props(dst, src);
// end:
//     av_frame_free(&win);
//     return ret;
// }
import "C"

import "fmt"

// Log2ChromaW returns log2 of the horizontal chroma subsampling of the
// frame's pixel format (1 for yuv420p and yuv422p, 0 for yuv444p and RGB),
// or -1 if the format is unknown.
func (f *Frame) Log2ChromaW() int {
	if f == nil || f.p == nil {
		return -1
	}
	return int(C.mm_pix_fmt_log2_chroma_w(f.p.format))
}

// CropColumns returns a new frame holding columns [left, left+width) of f at
// full height. Plane offsets come from av_frame_apply_cropping, as in FFmpeg's
// crop filter, so every software pixel format is cut correctly: 8-bit and
// high-bit-depth planar, semi-planar (nv12, p010) and packed. The window is
// copied into new aligned buffers. Properties and side data are copied from
// f. Hardware frames, and frames with pending crop fields, are rejected.
func (f *Frame) CropColumns(left, width int) (*Frame, error) {
	if f == nil || f.p == nil {
		return nil, &Err{Code: -22, Message: "av: CropColumns called on nil frame"}
	}
	if left < 0 || width < 1 || left+width > f.Width() {
		return nil, fmt.Errorf("av: CropColumns: window %d+%d outside frame width %d", left, width, f.Width())
	}
	if C.mm_frame_has_crop(f.p) != 0 {
		return nil, fmt.Errorf("av: CropColumns: frame has pending crop fields")
	}
	dst, err := AllocFrame()
	if err != nil {
		return nil, err
	}
	if ret := C.mm_frame_crop_columns(dst.p, f.p, C.int(left), C.int(width)); ret < 0 {
		dst.Close()
		if ret == -C.ENOSYS {
			return nil, fmt.Errorf("av: CropColumns: %s frames are not supported", PixFmtName(f.PixFmt()))
		}
		return nil, newErr(ret)
	}
	return dst, nil
}
