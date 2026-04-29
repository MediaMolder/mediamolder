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

// AVPictureType constants mirror libavutil/avutil.h's AVPictureType
// enum. Used by SetPictType to mark a frame as a forced keyframe
// (AV_PICTURE_TYPE_I) — libavcodec's `forced_kf_apply` path in
// fftools/ffmpeg_enc.c (line 738) sets exactly this on frames that
// match the `-force_key_frames` spec, and the encoder honours it as
// an IDR request regardless of the configured GOP cadence.
const (
	PictureTypeNone = 0 // AV_PICTURE_TYPE_NONE
	PictureTypeI    = 1 // AV_PICTURE_TYPE_I — Intra (also forces IDR)
	PictureTypeP    = 2 // AV_PICTURE_TYPE_P
	PictureTypeB    = 3 // AV_PICTURE_TYPE_B
	PictureTypeS    = 4 // AV_PICTURE_TYPE_S — S(GMC)-VOP MPEG-4
	PictureTypeSI   = 5 // AV_PICTURE_TYPE_SI
	PictureTypeSP   = 6 // AV_PICTURE_TYPE_SP
	PictureTypeBI   = 7 // AV_PICTURE_TYPE_BI
)

// PictType returns the frame's AVPictureType.
func (f *Frame) PictType() int { return int(f.p.pict_type) }

// SetPictType sets the frame's AVPictureType. Setting PictureTypeI on
// a video frame before sending it to a video encoder forces an IDR
// keyframe — exactly the mechanism FFmpeg's `-force_key_frames` flag
// uses (fftools/ffmpeg_enc.c::forced_kf_apply).
func (f *Frame) SetPictType(pt int) { f.p.pict_type = uint32(pt) }

// raw returns the underlying C pointer. For use within the av package only.
func (f *Frame) raw() *C.AVFrame { return f.p }
