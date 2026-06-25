// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build with_libraw

package raw

// The real RAW develop: a cgo binding to the bundled, version-pinned LibRaw (see pins.go). The
// C helper runs the full LibRaw sequence under the fixed deterministic parameters (see
// params.go) and hands back a packed RGB8 buffer; the Go side copies it into an *image.RGBA. A
// recover() converts any cgo panic into an error so one bad file never crashes the host.

// #include <stdlib.h>
// #include <string.h>
// #include <libraw/libraw.h>
//
// // mm_raw_decode develops the RAW at path under the pinned deterministic parameters and, on
// // success, returns 0 with *out pointing at a malloc'd packed RGB8 buffer (caller frees it
// // with free()), *w/*h the dimensions and *outLen the byte length. On any failure it returns
// // a non-zero code (a LibRaw error code, or a negative sentinel) and allocates nothing.
// static int mm_raw_decode(const char *path, unsigned char **out, int *w, int *h, int *outLen) {
//   libraw_data_t *lr = libraw_init(0);
//   if (!lr) return -1;
//   int rc = libraw_open_file(lr, path);
//   if (rc != 0) { libraw_close(lr); return rc; }
//   rc = libraw_unpack(lr);
//   if (rc != 0) { libraw_close(lr); return rc; }
//   lr->params.output_bps     = 8;        // 8-bit
//   lr->params.output_color   = 1;        // sRGB
//   lr->params.gamm[0]        = 1.0/2.4;  // sRGB transfer power
//   lr->params.gamm[1]        = 12.92;    // sRGB transfer slope
//   lr->params.no_auto_bright = 1;        // no histogram auto-exposure
//   lr->params.use_camera_wb  = 1;        // as-shot white balance
//   lr->params.use_auto_wb    = 0;        // never auto WB
//   lr->params.user_qual      = 3;        // AHD demosaic
//   lr->params.half_size      = 0;        // full resolution
//   lr->params.highlight      = 0;        // clip highlights
//   lr->params.user_flip      = 0;        // no rotation; caller owns orientation
//   lr->params.four_color_rgb = 0;
//   lr->params.output_tiff    = 0;
//   rc = libraw_dcraw_process(lr);
//   if (rc != 0) { libraw_close(lr); return rc; }
//   int errc = 0;
//   libraw_processed_image_t *img = libraw_dcraw_make_mem_image(lr, &errc);
//   if (!img) { libraw_close(lr); return errc != 0 ? errc : -2; }
//   if (img->type != LIBRAW_IMAGE_BITMAP || img->colors != 3 || img->bits != 8) {
//     libraw_dcraw_clear_mem(img); libraw_close(lr); return -3;
//   }
//   size_t n = (size_t)img->data_size;
//   unsigned char *buf = (unsigned char *)malloc(n);
//   if (!buf) { libraw_dcraw_clear_mem(img); libraw_close(lr); return -4; }
//   memcpy(buf, img->data, n);
//   *out = buf; *w = img->width; *h = img->height; *outLen = (int)n;
//   libraw_dcraw_clear_mem(img);
//   libraw_close(lr);
//   return 0;
// }
import "C"

import (
	"fmt"
	"image"
	"unsafe"
)

// Capable reports whether this build can develop RAW. The with_libraw build links LibRaw, so it
// can.
func Capable() bool { return true }

// Decode develops a camera-RAW file to a fully demosaiced *image.RGBA: 8-bit, sRGB primaries +
// transfer, un-oriented (no camera flip applied — the caller owns orientation), full resolution
// and fully opaque (A=255). It uses the pinned deterministic parameters (see [DefaultParams]),
// so a given file + the pinned LibRaw version always yields the same raster.
//
// Returns [ErrUnsupported] when path is not a recognised RAW extension, and a wrapped error on a
// genuine decode failure (corrupt file, unsupported camera). It never returns a black/uniform
// frame for a valid RAW; callers may still guard with [IsUniform].
func Decode(path string) (img image.Image, err error) {
	defer func() {
		if r := recover(); r != nil {
			img, err = nil, fmt.Errorf("raw: decode %q panicked: %v", path, r)
		}
	}()

	if !IsRAW(path) {
		return nil, ErrUnsupported
	}

	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))

	var cbuf *C.uchar
	var w, h, n C.int
	rc := C.mm_raw_decode(cpath, &cbuf, &w, &h, &n)
	if rc != 0 {
		return nil, fmt.Errorf("raw: decode %q: LibRaw error %d (%s)", path, int(rc), C.GoString(C.libraw_strerror(rc)))
	}
	defer C.free(unsafe.Pointer(cbuf))

	width, height, length := int(w), int(h), int(n)
	if width <= 0 || height <= 0 || length != width*height*3 {
		return nil, fmt.Errorf("raw: decode %q: unexpected buffer %d×%d len=%d", path, width, height, length)
	}

	src := unsafe.Slice((*byte)(unsafe.Pointer(cbuf)), length)
	out := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		srow := y * width * 3
		drow := out.PixOffset(0, y)
		for x := 0; x < width; x++ {
			s := srow + x*3
			d := drow + x*4
			out.Pix[d+0] = src[s+0]
			out.Pix[d+1] = src[s+1]
			out.Pix[d+2] = src[s+2]
			out.Pix[d+3] = 0xFF
		}
	}
	return out, nil
}
