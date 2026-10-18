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
// static int mm_frame_visible_width(const AVFrame *f) {
//     return f->width - (int)f->crop_left - (int)f->crop_right;
// }
//
// static void mm_frame_set_crop(AVFrame *f, int top, int bottom, int left, int right) {
//     f->crop_top = top; f->crop_bottom = bottom; f->crop_left = left; f->crop_right = right;
// }
//
// static int mm_pix_fmt_log2_chroma_w(int fmt) {
//     const AVPixFmtDescriptor *d = av_pix_fmt_desc_get((enum AVPixelFormat)fmt);
//     return d ? d->log2_chroma_w : -1;
// }
//
// // mm_frame_crop_columns fills dst with columns [left, left+width) of the
// // visible picture of src. Cropping src still carries (a decoder run with
// // apply_cropping=0 leaves the conformance window in crop_*) is folded first,
// // as av_frame_apply_cropping would in the decoder. av_frame_apply_cropping
// // computes the window offsets the way FFmpeg's crop filter does
// // (libavfilter/vf_crop.c): (x >> log2_chroma_w) * comp.step per plane.
// // AV_FRAME_CROP_UNALIGNED keeps left exact instead of moving it to an
// // aligned pointer. av_frame_copy then copies the window into dst's own
// // aligned buffers, so dst is an ordinary frame with no crop fields.
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
//     if (mm_frame_has_crop(win)) {
//         ret = av_frame_apply_cropping(win, AV_FRAME_CROP_UNALIGNED);
//         if (ret < 0)
//             goto end;
//     }
//     if (left < 0 || width < 1 || left + width > win->width) {
//         ret = AVERROR(EINVAL);
//         goto end;
//     }
//     win->crop_left  = left;
//     win->crop_right = win->width - left - width;
//     ret = av_frame_apply_cropping(win, AV_FRAME_CROP_UNALIGNED);
//     if (ret < 0)
//         goto end;
//     dst->format = src->format;
//     dst->width  = width;
//     dst->height = win->height;
//     ret = av_frame_get_buffer(dst, 0);
//     if (ret < 0)
//         goto end;
//     ret = av_frame_copy(dst, win);
//     if (ret < 0)
//         goto end;
//     ret = av_frame_copy_props(dst, src);
//     dst->crop_top = dst->crop_bottom = dst->crop_left = dst->crop_right = 0;
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

// Crop returns the frame's pending crop fields (AVFrame crop_top,
// crop_bottom, crop_left, crop_right): the border a decoder with
// apply_cropping off leaves for the consumer to remove.
func (f *Frame) Crop() (top, bottom, left, right int) {
	if f == nil || f.p == nil {
		return 0, 0, 0, 0
	}
	return int(f.p.crop_top), int(f.p.crop_bottom), int(f.p.crop_left), int(f.p.crop_right)
}

// SetCrop sets the frame's pending crop fields. Negative values are clamped
// to zero. A nil frame is ignored.
func (f *Frame) SetCrop(top, bottom, left, right int) {
	if f == nil || f.p == nil {
		return
	}
	C.mm_frame_set_crop(f.p, C.int(max(top, 0)), C.int(max(bottom, 0)), C.int(max(left, 0)), C.int(max(right, 0)))
}

// VisibleWidth returns the width of the picture once any cropping the frame
// still carries is applied: Width minus crop_left and crop_right, which a
// decoder leaves set when apply_cropping is off. It is Width for a frame out
// of a filter graph, and 0 for a nil frame.
func (f *Frame) VisibleWidth() int {
	if f == nil || f.p == nil {
		return 0
	}
	return int(C.mm_frame_visible_width(f.p))
}

// CropColumns returns a new frame holding columns [left, left+width) of f's
// visible picture at full height. Cropping f still carries (crop_* fields)
// is applied first, so left counts from the visible left edge. Plane offsets
// come from av_frame_apply_cropping, as in FFmpeg's crop filter, so every
// software pixel format is cut correctly: 8-bit and high-bit-depth planar,
// semi-planar (nv12, p010) and packed. The window is copied into new aligned
// buffers. Properties and side data are copied from f; the result carries no
// crop fields. Hardware frames are rejected.
func (f *Frame) CropColumns(left, width int) (*Frame, error) {
	if f == nil || f.p == nil {
		return nil, &Err{Code: -22, Message: "av: CropColumns called on nil frame"}
	}
	if vw := f.VisibleWidth(); left < 0 || width < 1 || left+width > vw {
		return nil, fmt.Errorf("av: CropColumns: window %d+%d outside frame width %d", left, width, vw)
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
