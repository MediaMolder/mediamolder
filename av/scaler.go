// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include <libswscale/swscale.h>
// #include <libavutil/frame.h>
//
// static void mm_frame_copy_color(AVFrame *dst, const AVFrame *src) {
//     dst->color_range     = src->color_range;
//     dst->color_primaries = src->color_primaries;
//     dst->color_trc       = src->color_trc;
//     dst->colorspace      = src->colorspace;
//     dst->chroma_location = src->chroma_location;
// }
//
// static void mm_frame_clear_crop(AVFrame *f) {
//     f->crop_top = f->crop_bottom = f->crop_left = f->crop_right = 0;
// }
//
// static int mm_frame_color_range(const AVFrame *f)     { return f->color_range; }
// static int mm_frame_color_primaries(const AVFrame *f) { return f->color_primaries; }
// static int mm_frame_color_trc(const AVFrame *f)       { return f->color_trc; }
// static int mm_frame_colorspace(const AVFrame *f)      { return f->colorspace; }
// static int mm_frame_chroma_loc(const AVFrame *f)      { return f->chroma_location; }
//
// static void mm_frame_set_color(AVFrame *f, int range, int prim, int trc, int spc, int loc) {
//     f->color_range = range;
//     f->color_primaries = prim;
//     f->color_trc = trc;
//     f->colorspace = spc;
//     f->chroma_location = loc;
// }
import "C"

import "fmt"

// ScalerOptions selects the destination of an av.Scaler. Width and Height
// are the destination size. PixFmt is the destination AVPixelFormat; a
// negative value keeps the source format. Flags names the swscale algorithm
// the same way the scale filter does. BitExact adds SWS_BITEXACT and
// SWS_ACCURATE_RND.
type ScalerOptions struct {
	Width, Height int
	PixFmt        int
	Flags         string
	BitExact      bool
}

// Scaler is one SwsContext kept for a run. Dynamic-mode sws_scale_frame reads
// both geometries from the frames, so the context stores only flags and an
// automatic thread count.
type Scaler struct {
	p       *C.SwsContext
	opt     ScalerOptions
	reinits int
	seen    bool
	prevSrc swsFmt
	prevDst swsFmt
}

// swsFmt is the part of a frame that ff_fmt_equal compares, minus per-frame
// mastering data. A change here is what makes swscale rebuild its graph.
type swsFmt struct {
	w, h, format        int
	rng, csp, prim, trc int
	loc                 int
	interlaced          bool
}

// NewScaler allocates a scaler. The context is not initialised for the legacy
// API: the first Scale call builds the graph from the frames.
func NewScaler(o ScalerOptions) (*Scaler, error) {
	if o.Flags == "" {
		o.Flags = "bicubic"
	}
	flags, err := swsFlagBits(o.Flags, o.BitExact)
	if err != nil {
		return nil, err
	}
	p := C.sws_alloc_context()
	if p == nil {
		return nil, &Err{Code: -12, Message: "sws_alloc_context: out of memory"}
	}
	p.flags = C.uint(flags)
	p.threads = 0
	return &Scaler{p: p, opt: o}, nil
}

// Close frees the scaler. It is safe to call on nil.
func (s *Scaler) Close() {
	if s == nil || s.p == nil {
		return
	}
	C.sws_free_context(&s.p)
}

// ReinitCount reports how many times the swscale graph was built or rebuilt.
// The first Scale counts as one. A later call counts again when the source
// or destination geometry or colour, as ff_fmt_equal sees them, has changed.
func (s *Scaler) ReinitCount() int {
	if s == nil {
		return 0
	}
	return s.reinits
}

// Scale scales src into a new frame of the configured size and format.
// The five colour fields are copied onto the destination before
// sws_scale_frame, because an unspecified destination range is treated as
// limited and would convert a full-range source. Properties are copied
// afterwards and those five fields are written again. The result carries no
// crop. An interlaced source is an error: the destination is progressive and
// sws_frame_setup rejects the pair. The caller closes the result.
func (s *Scaler) Scale(src *Frame) (*Frame, error) {
	if s == nil || s.p == nil {
		return nil, fmt.Errorf("av: scaler: closed")
	}
	if src == nil || src.p == nil {
		return nil, fmt.Errorf("av: scaler: nil frame")
	}
	if src.Hardware() {
		return nil, fmt.Errorf("av: scaler: %s frames are not supported", PixFmtName(src.PixFmt()))
	}
	if src.Interlaced() {
		return nil, fmt.Errorf("av: scaler: interlaced frames are not supported")
	}
	w, h := s.opt.Width, s.opt.Height
	if w <= 0 || h <= 0 {
		w, h = src.Width(), src.Height()
	}
	format := s.opt.PixFmt
	if format < 0 {
		format = src.PixFmt()
	}
	dst, err := AllocFrame()
	if err != nil {
		return nil, err
	}
	dst.p.width = C.int(w)
	dst.p.height = C.int(h)
	dst.p.format = C.int(format)
	C.mm_frame_copy_color(dst.p, src.p)
	s.note(src, dst)
	if ret := C.sws_scale_frame(s.p, dst.p, src.p); ret < 0 {
		dst.Close()
		return nil, fmt.Errorf("av: scaler: %w", newErr(ret))
	}
	if err := dst.CopyPropsFrom(src); err != nil {
		dst.Close()
		return nil, err
	}
	C.mm_frame_copy_color(dst.p, src.p)
	C.mm_frame_clear_crop(dst.p)
	return dst, nil
}

func (s *Scaler) note(src, dst *Frame) {
	curSrc := frameSwsFmt(src)
	curDst := frameSwsFmt(dst)
	if !s.seen || curSrc != s.prevSrc || curDst != s.prevDst {
		s.reinits++
		s.seen = true
		s.prevSrc = curSrc
		s.prevDst = curDst
	}
}

func frameSwsFmt(f *Frame) swsFmt {
	return swsFmt{
		w: f.Width(), h: f.Height(), format: f.PixFmt(),
		rng:        int(C.mm_frame_color_range(f.p)),
		csp:        int(C.mm_frame_colorspace(f.p)),
		prim:       int(C.mm_frame_color_primaries(f.p)),
		trc:        int(C.mm_frame_color_trc(f.p)),
		loc:        int(C.mm_frame_chroma_loc(f.p)),
		interlaced: f.Interlaced(),
	}
}

func swsFlagBits(name string, bitExact bool) (int, error) {
	var bits int
	switch name {
	case "bilinear":
		bits = int(C.SWS_BILINEAR)
	case "bicubic":
		bits = int(C.SWS_BICUBIC)
	case "lanczos":
		bits = int(C.SWS_LANCZOS)
	case "spline":
		bits = int(C.SWS_SPLINE)
	case "area":
		bits = int(C.SWS_AREA)
	case "neighbor":
		bits = int(C.SWS_POINT)
	default:
		return 0, fmt.Errorf("av: scaler: unknown flags %q", name)
	}
	if bitExact {
		bits |= int(C.SWS_BITEXACT) | int(C.SWS_ACCURATE_RND)
	}
	return bits, nil
}

// setTestColor writes the five colour fields. It exists for scaler tests.
func (f *Frame) setTestColor(rng, prim, trc, spc, loc int) {
	if f == nil || f.p == nil {
		return
	}
	C.mm_frame_set_color(f.p, C.int(rng), C.int(prim), C.int(trc), C.int(spc), C.int(loc))
}

// testColor reads the five colour fields back.
func (f *Frame) testColor() (rng, prim, trc, spc, loc int) {
	if f == nil || f.p == nil {
		return
	}
	return int(C.mm_frame_color_range(f.p)),
		int(C.mm_frame_color_primaries(f.p)),
		int(C.mm_frame_color_trc(f.p)),
		int(C.mm_frame_colorspace(f.p)),
		int(C.mm_frame_chroma_loc(f.p))
}
