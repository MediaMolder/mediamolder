// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "libavutil/frame.h"
// #include "libavutil/common.h"
// #include "libavutil/pixdesc.h"
// #include "libavutil/imgutils.h"
//
// // Plane geometry helpers. For planar subsampled formats (e.g. yuv420p) the
// // chroma planes (index 1 and 2) have fewer rows/columns than the frame; we
// // round up so odd dimensions are covered (matching av_image_fill_*).
// static int mm_frame_nb_planes(const AVFrame *f) {
//     return av_pix_fmt_count_planes((enum AVPixelFormat)f->format);
// }
// static int mm_frame_plane_height(const AVFrame *f, int plane) {
//     const AVPixFmtDescriptor *d = av_pix_fmt_desc_get((enum AVPixelFormat)f->format);
//     if (!d) return 0;
//     if (plane == 1 || plane == 2) return AV_CEIL_RSHIFT(f->height, d->log2_chroma_h);
//     return f->height;
// }
// static int mm_frame_plane_width(const AVFrame *f, int plane) {
//     const AVPixFmtDescriptor *d = av_pix_fmt_desc_get((enum AVPixelFormat)f->format);
//     if (!d) return 0;
//     if (plane == 1 || plane == 2) return AV_CEIL_RSHIFT(f->width, d->log2_chroma_w);
//     return f->width;
// }
// static uint8_t *mm_frame_plane_data(const AVFrame *f, int plane) { return f->data[plane]; }
// static int mm_frame_linesize(const AVFrame *f, int plane) { return f->linesize[plane]; }
import "C"

import "unsafe"

// NumPlanes returns the number of data planes for the frame's pixel format
// (e.g. 3 for yuv420p). Returns 0 for a nil/format-less frame.
func (f *Frame) NumPlanes() int {
	if f == nil || f.p == nil {
		return 0
	}
	return int(C.mm_frame_nb_planes(f.p))
}

// Linesize returns the byte stride (row size including any padding) of plane i.
func (f *Frame) Linesize(i int) int {
	if f == nil || f.p == nil {
		return 0
	}
	return int(C.mm_frame_linesize(f.p, C.int(i)))
}

// PlaneHeight returns the number of rows in plane i. Chroma planes of a
// subsampled format (yuv420p plane 1/2) have fewer rows than the frame.
func (f *Frame) PlaneHeight(i int) int {
	if f == nil || f.p == nil {
		return 0
	}
	return int(C.mm_frame_plane_height(f.p, C.int(i)))
}

// PlaneWidth returns the number of samples per row in plane i (chroma planes of
// a subsampled format are narrower than the frame). For 8-bit formats this is
// also the count of meaningful data bytes per row (always ≤ Linesize(i)).
func (f *Frame) PlaneWidth(i int) int {
	if f == nil || f.p == nil {
		return 0
	}
	return int(C.mm_frame_plane_width(f.p, C.int(i)))
}

// Plane returns plane i's pixel bytes as a Go slice aliasing the frame's C
// buffer (length = Linesize(i) * PlaneHeight(i)). Writes through the slice edit
// the frame in place. The slice is only valid until the frame is closed or
// unref'd — do not retain it past that.
func (f *Frame) Plane(i int) []byte {
	if f == nil || f.p == nil {
		return nil
	}
	data := C.mm_frame_plane_data(f.p, C.int(i))
	if data == nil {
		return nil
	}
	n := f.Linesize(i) * f.PlaneHeight(i)
	if n <= 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(data)), n)
}

// NewVideoFrame allocates a writable video frame of the given dimensions and
// pixel format (an AVPixelFormat value), backed by freshly allocated,
// reference-counted plane buffers. The caller must Close() it.
func NewVideoFrame(width, height, pixFmt int) (*Frame, error) {
	f, err := AllocFrame()
	if err != nil {
		return nil, err
	}
	f.p.format = C.int(pixFmt)
	f.p.width = C.int(width)
	f.p.height = C.int(height)
	if ret := C.av_frame_get_buffer(f.p, 0); ret < 0 {
		f.Close()
		return nil, newErr(ret)
	}
	return f, nil
}

// CopyPropsFrom copies frame properties — timestamps, colorimetry, sample
// aspect ratio, side data, etc. but NOT pixel data — from src onto f, via
// av_frame_copy_props.
func (f *Frame) CopyPropsFrom(src *Frame) error {
	if f == nil || f.p == nil || src == nil || src.p == nil {
		return &Err{Code: -22, Message: "av: CopyPropsFrom called on nil frame"}
	}
	return newErr(C.av_frame_copy_props(f.p, src.p))
}
