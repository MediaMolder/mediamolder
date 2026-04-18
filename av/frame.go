// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "libavutil/frame.h"
import "C"

import "unsafe"

// Frame wraps an AVFrame. It must be closed after use via Close() or defer Close().
type Frame struct {
	p *C.AVFrame
}

// AllocFrame allocates a new AVFrame. The caller must call Close().
func AllocFrame() (*Frame, error) {
	p := C.av_frame_alloc()
	if p == nil {
		return nil, &Err{Code: -12, Message: "av_frame_alloc: out of memory"}
	}
	f := &Frame{p: p}
	leakTrack(unsafe.Pointer(p), "AVFrame")
	return f, nil
}

// Clone creates a new Frame that references the same underlying buffers.
// The clone must be independently closed via Close(). This is equivalent
// to av_frame_clone() — the data buffers are reference-counted, not copied.
func (f *Frame) Clone() (*Frame, error) {
	if f.p == nil {
		return nil, &Err{Code: -22, Message: "av: Clone called on nil frame"}
	}
	p := C.av_frame_clone(f.p)
	if p == nil {
		return nil, &Err{Code: -12, Message: "av_frame_clone: out of memory"}
	}
	c := &Frame{p: p}
	leakTrack(unsafe.Pointer(p), "AVFrame")
	return c, nil
}

// Close unrefs the frame data and frees the AVFrame.
func (f *Frame) Close() error {
	if f.p != nil {
		leakUntrack(unsafe.Pointer(f.p))
		C.av_frame_free(&f.p)
		f.p = nil
	}
	return nil
}

// Unref releases the frame's buffer references without freeing the struct itself,
// making the frame ready for reuse.
func (f *Frame) Unref() {
	if f.p != nil {
		C.av_frame_unref(f.p)
	}
}

// Width returns the frame width in pixels (video only).
func (f *Frame) Width() int { return int(f.p.width) }

// Height returns the frame height in pixels (video only).
func (f *Frame) Height() int { return int(f.p.height) }

// PTS returns the frame presentation timestamp (in stream timebase units).
func (f *Frame) PTS() int64 { return int64(f.p.pts) }

// SetPTS sets the presentation timestamp.
func (f *Frame) SetPTS(pts int64) { f.p.pts = C.int64_t(pts) }

// raw returns the underlying C pointer. For use within the av package only.
func (f *Frame) raw() *C.AVFrame { return f.p }
