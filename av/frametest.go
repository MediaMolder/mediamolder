// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "libavutil/frame.h"
// #include "libavutil/pixfmt.h"
// #include "libavutil/imgutils.h"
//
// // alloc_test_frame creates a video frame with allocated buffers.
// static int alloc_test_frame(AVFrame *f, int w, int h, int pix_fmt) {
//     f->format = pix_fmt;
//     f->width  = w;
//     f->height = h;
//     return av_frame_get_buffer(f, 0);
// }
//
// // fill_rgb24 fills plane 0 of an RGB24 frame with a constant colour.
// static void fill_rgb24(AVFrame *f, uint8_t r, uint8_t g, uint8_t b) {
//     for (int y = 0; y < f->height; y++) {
//         uint8_t *row = f->data[0] + y * f->linesize[0];
//         for (int x = 0; x < f->width; x++) {
//             row[x*3]   = r;
//             row[x*3+1] = g;
//             row[x*3+2] = b;
//         }
//     }
// }
//
// // fill_y_checker writes a 1px checkerboard (0/255) to plane 0 (Y) — maximal high-frequency content.
// static void fill_y_checker(AVFrame *f) {
//     for (int y = 0; y < f->height; y++) {
//         uint8_t *row = f->data[0] + y * f->linesize[0];
//         for (int x = 0; x < f->width; x++) row[x] = ((x ^ y) & 1) ? 255 : 0;
//     }
// }
//
// // fill_y_flat writes a constant value to plane 0 (Y) — zero high-frequency content.
// static void fill_y_flat(AVFrame *f, uint8_t v) {
//     for (int y = 0; y < f->height; y++) {
//         uint8_t *row = f->data[0] + y * f->linesize[0];
//         for (int x = 0; x < f->width; x++) row[x] = v;
//     }
// }
import "C"

import "testing"

// NewTestFrame creates a Frame with allocated pixel buffers for testing.
// pixFmt uses FFmpeg AVPixelFormat values (0 = YUV420P, 2 = RGB24, etc.).
// The frame data is zeroed. Caller must Close() when done.
func NewTestFrame(t *testing.T, w, h, pixFmt int) *Frame {
	t.Helper()
	f, err := AllocFrame()
	if err != nil {
		t.Fatal(err)
	}
	ret := C.alloc_test_frame(f.p, C.int(w), C.int(h), C.int(pixFmt))
	if ret < 0 {
		f.Close()
		t.Fatalf("alloc_test_frame(%d×%d, fmt=%d): %v", w, h, pixFmt, newErr(ret))
	}
	return f
}

// FillTestFrameRGB24 fills an RGB24 frame (pixFmt=2) with a constant colour.
// Panics if the frame is not RGB24.
func FillTestFrameRGB24(f *Frame, r, g, b uint8) {
	C.fill_rgb24(f.p, C.uint8_t(r), C.uint8_t(g), C.uint8_t(b))
}

// FillTestFrameYChecker writes a 1px checkerboard to a planar-YUV frame's Y plane (maximal
// high-frequency content — a "perfectly sharp" frame for a sharpness test).
func FillTestFrameYChecker(f *Frame) { C.fill_y_checker(f.p) }

// FillTestFrameYFlat writes a constant value to a planar-YUV frame's Y plane (no high-frequency
// content — a "fully blurred/flat" frame for a sharpness test).
func FillTestFrameYFlat(f *Frame, v uint8) { C.fill_y_flat(f.p, C.uint8_t(v)) }
