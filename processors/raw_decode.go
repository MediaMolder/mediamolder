// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build with_libraw

// raw_decode is a FrameSource go_processor node that develops a single camera-RAW file (via the
// bundled LibRaw, see the raw package) into one full-resolution 8-bit sRGB RGBA frame and emits
// it into the graph. It exists because libav renders camera RAW to a black/garbled frame; this
// node is how a graph gets a real develop to scale/encode/export. Compiled only under
// with_libraw, so it appears in the node catalog only when LibRaw is built in.

package processors

import (
	"context"
	"fmt"
	"image"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/raw"
)

// RawDecode develops the RAW at its "input" path once (in Init) and emits it as a single video
// frame in Run. The deterministic develop parameters are fixed by the raw package (camera WB,
// sRGB, AHD, un-oriented); this node exposes only the file to decode.
type RawDecode struct {
	path string
	img  *image.RGBA
	w, h int
}

// Init reads the required "input" param (a camera-RAW file path) and eagerly develops it, so
// OutputStreamInfo can report exact dimensions before Run. The decoded image is cached (RAW
// develop is expensive — decode once, not per call).
func (r *RawDecode) Init(params map[string]any) error {
	if !raw.Capable() {
		return fmt.Errorf("raw_decode: this build has no LibRaw — rebuild with -tags with_libraw")
	}
	p, _ := params["input"].(string)
	if p == "" {
		return fmt.Errorf("raw_decode: missing required \"input\" (path to a camera-RAW file)")
	}
	if !raw.IsRAW(p) {
		return fmt.Errorf("raw_decode: %q is not a recognised camera-RAW file", p)
	}
	img, err := raw.Decode(p)
	if err != nil {
		return fmt.Errorf("raw_decode: %w", err)
	}
	rgba, ok := img.(*image.RGBA)
	if !ok {
		return fmt.Errorf("raw_decode: decoder returned %T, want *image.RGBA", img)
	}
	b := rgba.Bounds()
	r.path, r.img, r.w, r.h = p, rgba, b.Dx(), b.Dy()
	return nil
}

// OutputStreamInfo reports the developed raster's format so downstream nodes (scale, encoder, …)
// are configured before Run emits the frame. One still image: frame rate 1/1.
func (r *RawDecode) OutputStreamInfo() (av.StreamInfo, error) {
	if r.img == nil {
		return av.StreamInfo{}, fmt.Errorf("raw_decode: not initialised")
	}
	return av.StreamInfo{
		Type:      av.MediaTypeVideo,
		Width:     r.w,
		Height:    r.h,
		PixFmt:    av.PixFmtRGBA(),
		FrameRate: [2]int{1, 1},
		TimeBase:  [2]int{1, 1},
		BitDepth:  8,
	}, nil
}

// OutputFrameCount reports the single frame this source emits (processors.FrameSourceProgress).
func (r *RawDecode) OutputFrameCount() int64 { return 1 }

// Run emits the developed image as one RGBA video frame. The send callback takes ownership of a
// successfully sent frame, so Run only closes the frame when send fails.
func (r *RawDecode) Run(ctx context.Context, send func(*av.Frame) error) error {
	if r.img == nil {
		return fmt.Errorf("raw_decode: not initialised")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := av.NewVideoFrame(r.w, r.h, av.PixFmtRGBA())
	if err != nil {
		return fmt.Errorf("raw_decode: alloc frame: %w", err)
	}
	dst, dstStride := f.Plane(0), f.Linesize(0)
	rowBytes := r.w * 4 // packed RGBA
	for y := 0; y < r.h; y++ {
		copy(dst[y*dstStride:y*dstStride+rowBytes], r.img.Pix[y*r.img.Stride:y*r.img.Stride+rowBytes])
	}
	f.SetPTS(0)
	if err := send(f); err != nil {
		f.Close()
		return err
	}
	return nil
}

// Process must never be called for a FrameSource.
func (r *RawDecode) Process(*av.Frame, ProcessorContext) (*av.Frame, *Metadata, error) {
	return nil, nil, fmt.Errorf("raw_decode: Process() called on FrameSource node — runtime bug")
}

// Close drops the cached image.
func (r *RawDecode) Close() error {
	r.img = nil
	return nil
}

func init() {
	Register("raw_decode", func() Processor { return &RawDecode{} })
}
