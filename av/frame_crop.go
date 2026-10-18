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
// static int mm_frame_visible_height(const AVFrame *f) {
//     return f->height - (int)f->crop_top - (int)f->crop_bottom;
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
// static int mm_pix_fmt_log2_chroma_h(int fmt) {
//     const AVPixFmtDescriptor *d = av_pix_fmt_desc_get((enum AVPixelFormat)fmt);
//     return d ? d->log2_chroma_h : -1;
// }
//
// static int mm_frame_hardware(const AVFrame *f) {
//     const AVPixFmtDescriptor *d = av_pix_fmt_desc_get((enum AVPixelFormat)f->format);
//     if (!d || (d->flags & (AV_PIX_FMT_FLAG_HWACCEL | AV_PIX_FMT_FLAG_BITSTREAM)) || f->hw_frames_ctx)
//         return 1;
//     return 0;
// }
//
// // mm_frame_apply_window folds any pending crop on win, then cuts the visible
// // rectangle [x, x+w) × [y, y+h). Both applies use AV_FRAME_CROP_UNALIGNED so
// // libavutil does not move the origin to an aligned pointer. x and y count
// // from the visible picture. Offsets match FFmpeg's crop filter
// // (libavfilter/vf_crop.c): (x >> log2_chroma_w) * comp.step per plane.
// static int mm_frame_apply_window(AVFrame *win, int x, int y, int w, int h) {
//     int ret = 0;
//     if (mm_frame_has_crop(win)) {
//         ret = av_frame_apply_cropping(win, AV_FRAME_CROP_UNALIGNED);
//         if (ret < 0)
//             return ret;
//     }
//     if (x < 0 || y < 0 || w < 1 || h < 1 || x + w > win->width || y + h > win->height)
//         return AVERROR(EINVAL);
//     win->crop_left   = x;
//     win->crop_right  = win->width - x - w;
//     win->crop_top    = y;
//     win->crop_bottom = win->height - y - h;
//     return av_frame_apply_cropping(win, AV_FRAME_CROP_UNALIGNED);
// }
//
// // mm_frame_crop_rect copies the visible rectangle into dst's own aligned
// // buffers. dst carries the source properties and no crop fields.
// static int mm_frame_crop_rect(AVFrame *dst, const AVFrame *src, int x, int y, int w, int h) {
//     const AVPixFmtDescriptor *d = av_pix_fmt_desc_get((enum AVPixelFormat)src->format);
//     if (!d || (d->flags & (AV_PIX_FMT_FLAG_HWACCEL | AV_PIX_FMT_FLAG_BITSTREAM)))
//         return AVERROR(ENOSYS);
//     AVFrame *win = av_frame_alloc();
//     if (!win)
//         return AVERROR(ENOMEM);
//     int ret = av_frame_ref(win, src);
//     if (ret < 0)
//         goto end;
//     ret = mm_frame_apply_window(win, x, y, w, h);
//     if (ret < 0)
//         goto end;
//     dst->format = src->format;
//     dst->width  = w;
//     dst->height = h;
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
//
// // mm_frame_crop_window returns a new reference of src whose data pointers
// // address the visible rectangle. The caller frees *out. src is not modified.
// static int mm_frame_crop_window(AVFrame **out, const AVFrame *src, int x, int y, int w, int h) {
//     const AVPixFmtDescriptor *d = av_pix_fmt_desc_get((enum AVPixelFormat)src->format);
//     if (!d || (d->flags & (AV_PIX_FMT_FLAG_HWACCEL | AV_PIX_FMT_FLAG_BITSTREAM)))
//         return AVERROR(ENOSYS);
//     AVFrame *win = av_frame_alloc();
//     if (!win)
//         return AVERROR(ENOMEM);
//     int ret = av_frame_ref(win, src);
//     if (ret < 0) {
//         av_frame_free(&win);
//         return ret;
//     }
//     ret = mm_frame_apply_window(win, x, y, w, h);
//     if (ret < 0) {
//         av_frame_free(&win);
//         return ret;
//     }
//     *out = win;
//     return 0;
// }
import "C"

import (
	"fmt"
	"unsafe"
)

// Log2ChromaW returns log2 of the horizontal chroma subsampling of the
// frame's pixel format (1 for yuv420p and yuv422p, 0 for yuv444p and RGB),
// or -1 if the format is unknown.
func (f *Frame) Log2ChromaW() int {
	if f == nil || f.p == nil {
		return -1
	}
	return int(C.mm_pix_fmt_log2_chroma_w(f.p.format))
}

// Log2ChromaH returns log2 of the vertical chroma subsampling (1 for yuv420p,
// 0 for yuv422p, yuv444p and RGB), or -1 if the format is unknown.
func (f *Frame) Log2ChromaH() int {
	if f == nil || f.p == nil {
		return -1
	}
	return int(C.mm_pix_fmt_log2_chroma_h(f.p.format))
}

// Hardware reports whether the frame is a hardware or bitstream picture.
// Cropping and swscale read software planes, so those paths reject it.
func (f *Frame) Hardware() bool {
	if f == nil || f.p == nil {
		return false
	}
	return C.mm_frame_hardware(f.p) != 0
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

// VisibleHeight returns the height of the picture once any pending crop is
// applied: Height minus crop_top and crop_bottom. It is 0 for a nil frame.
func (f *Frame) VisibleHeight() int {
	if f == nil || f.p == nil {
		return 0
	}
	return int(C.mm_frame_visible_height(f.p))
}

// CropRect returns a new frame holding the rectangle [x, x+w) × [y, y+h) of
// f's visible picture. Pending crop fields are folded first, so x and y count
// from the visible origin. The window is copied into new aligned buffers.
// Properties and side data are copied from f; the result carries no crop
// fields. Hardware frames are rejected.
func (f *Frame) CropRect(x, y, w, h int) (*Frame, error) {
	if f == nil || f.p == nil {
		return nil, &Err{Code: -22, Message: "av: CropRect called on nil frame"}
	}
	vw, vh := f.VisibleWidth(), f.VisibleHeight()
	if x < 0 || y < 0 || w < 1 || h < 1 || x+w > vw || y+h > vh {
		return nil, fmt.Errorf("av: CropRect: window %d,%d %dx%d outside visible %dx%d", x, y, w, h, vw, vh)
	}
	dst, err := AllocFrame()
	if err != nil {
		return nil, err
	}
	if ret := C.mm_frame_crop_rect(dst.p, f.p, C.int(x), C.int(y), C.int(w), C.int(h)); ret < 0 {
		dst.Close()
		if ret == -C.ENOSYS {
			return nil, fmt.Errorf("av: CropRect: %s frames are not supported", PixFmtName(f.PixFmt()))
		}
		return nil, newErr(ret)
	}
	return dst, nil
}

// CropColumns returns a new frame holding columns [left, left+width) of f's
// visible picture at full height. It is CropRect(left, 0, width, VisibleHeight()).
func (f *Frame) CropColumns(left, width int) (*Frame, error) {
	if f == nil || f.p == nil {
		return nil, &Err{Code: -22, Message: "av: CropColumns called on nil frame"}
	}
	return f.CropRect(left, 0, width, f.VisibleHeight())
}

// CropWindow returns a new frame that references f's buffers with data
// pointers addressing the visible rectangle [x, x+w) × [y, y+h). Pending crop
// is folded on the reference before the window is applied, so a decoder crop
// is not overwritten. f is left unchanged. The caller must Close the result;
// closing f does not free it. Hardware frames are rejected.
func (f *Frame) CropWindow(x, y, w, h int) (*Frame, error) {
	if f == nil || f.p == nil {
		return nil, &Err{Code: -22, Message: "av: CropWindow called on nil frame"}
	}
	vw, vh := f.VisibleWidth(), f.VisibleHeight()
	if x < 0 || y < 0 || w < 1 || h < 1 || x+w > vw || y+h > vh {
		return nil, fmt.Errorf("av: CropWindow: window %d,%d %dx%d outside visible %dx%d", x, y, w, h, vw, vh)
	}
	var out *C.AVFrame
	if ret := C.mm_frame_crop_window(&out, f.p, C.int(x), C.int(y), C.int(w), C.int(h)); ret < 0 {
		if ret == -C.ENOSYS {
			return nil, fmt.Errorf("av: CropWindow: %s frames are not supported", PixFmtName(f.PixFmt()))
		}
		return nil, newErr(ret)
	}
	win := &Frame{p: out}
	leakTrack(unsafe.Pointer(out), "AVFrame")
	return win, nil
}

// setTestPixFmt overwrites the pixel format without reallocating planes.
// Tests use it to present a hardware format to the crop path.
func (f *Frame) setTestPixFmt(pixFmt int) {
	if f != nil && f.p != nil {
		f.p.format = C.int(pixFmt)
	}
}

// SetTestPixFmt overwrites the pixel format without reallocating planes.
// Tests use it to present a hardware format to code that rejects one.
func (f *Frame) SetTestPixFmt(pixFmt int) { f.setTestPixFmt(pixFmt) }
