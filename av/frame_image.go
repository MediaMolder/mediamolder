// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "frame_cgo.h"
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

// ToBGR24 converts the frame to packed BGR24 pixels (3 bytes per pixel, B/G/R order).
// Returns a []byte of length width×height×3. Supports any input pixel format
// that swscale handles (YUV420P, NV12, RGB24, etc.).
func (f *Frame) ToBGR24() ([]byte, error) {
	if f == nil || f.p == nil {
		return nil, fmt.Errorf("av: ToBGR24 called on nil frame")
	}
	w, h := f.Width(), f.Height()
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("av: ToBGR24: invalid frame dimensions %d\u00d7%d", w, h)
	}

	var outData *C.uint8_t
	ret := C.frame_to_bgr24(f.p, &outData)
	if ret < 0 {
		return nil, newErr(ret)
	}
	defer C.av_free(unsafe.Pointer(outData))

	size := w * h * 3
	out := make([]byte, size)
	copy(out, unsafe.Slice((*byte)(unsafe.Pointer(outData)), size))
	return out, nil
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
