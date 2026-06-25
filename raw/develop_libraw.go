// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build with_libraw

package raw

// The high-precision RAW develop: a 16-bit, linear-light, wide-gamut (Rec.2020) raster with
// recovered highlights — the scene-referred "master" the caller tone-maps and colour-manages.
// It mirrors decode_libraw.go (the canonical 8-bit sRGB path) but with output_bps=16, a linear
// transfer (gamm = {1,1}), highlight=2 (blend recovery) and a wide output colour space. All
// other parameters match the 8-bit path so the two develops agree on demosaic, white balance and
// orientation.

// #include <stdlib.h>
// #include <string.h>
// #include <libraw/libraw.h>
//
// // mm_raw_develop16 develops the RAW at path to 16-bit linear wide-gamut and returns 0 with
// // *out a malloc'd packed RGB16 buffer (host byte order; caller frees), *w/*h the dimensions
// // and *outLen the byte length (= w*h*3*2). Non-zero is a LibRaw code or a negative sentinel.
// static int mm_raw_develop16(const char *path, int output_color,
//                             unsigned short **out, int *w, int *h, int *outLen) {
//   libraw_data_t *lr = libraw_init(0);
//   if (!lr) return -1;
//   int rc = libraw_open_file(lr, path);
//   if (rc != 0) { libraw_close(lr); return rc; }
//   rc = libraw_unpack(lr);
//   if (rc != 0) { libraw_close(lr); return rc; }
//   lr->params.output_bps     = 16;           // 16-bit per channel
//   lr->params.output_color   = output_color; // wide gamut (caller-chosen; 8 = Rec.2020)
//   lr->params.gamm[0]        = 1.0;          // linear: no transfer curve (tone-map downstream)
//   lr->params.gamm[1]        = 1.0;
//   lr->params.no_auto_bright = 1;            // no histogram auto-exposure
//   lr->params.use_camera_wb  = 1;            // as-shot white balance
//   lr->params.use_auto_wb    = 0;            // never auto WB
//   lr->params.user_qual      = 3;            // AHD demosaic (matches the 8-bit path)
//   lr->params.half_size      = 0;            // full resolution
//   lr->params.highlight      = 2;            // blend: recover highlights instead of clipping
//   lr->params.user_flip      = 0;            // no rotation; caller owns orientation
//   lr->params.four_color_rgb = 0;
//   lr->params.output_tiff    = 0;
//   rc = libraw_dcraw_process(lr);
//   if (rc != 0) { libraw_close(lr); return rc; }
//   int errc = 0;
//   libraw_processed_image_t *img = libraw_dcraw_make_mem_image(lr, &errc);
//   if (!img) { libraw_close(lr); return errc != 0 ? errc : -2; }
//   if (img->type != LIBRAW_IMAGE_BITMAP || img->colors != 3 || img->bits != 16) {
//     libraw_dcraw_clear_mem(img); libraw_close(lr); return -3;
//   }
//   size_t n = (size_t)img->data_size;        // bytes = w*h*3*2
//   unsigned short *buf = (unsigned short *)malloc(n);
//   if (!buf) { libraw_dcraw_clear_mem(img); libraw_close(lr); return -4; }
//   memcpy(buf, img->data, n);
//   *out = buf; *w = img->width; *h = img->height; *outLen = (int)n;
//   libraw_dcraw_clear_mem(img);
//   libraw_close(lr);
//   return 0;
// }
import "C"

import (
	"encoding/binary"
	"fmt"
	"image"
	"unsafe"
)

// librawOutputColor maps a [ColorSpace] to LibRaw's output_color (-o) integer. The wide values
// (DCI-P3=7, Rec.2020=8) are present from LibRaw 0.19; we pin 0.21.x (see pins.go).
func librawOutputColor(c ColorSpace) (C.int, bool) {
	switch c {
	case ColorSRGB:
		return 1, true
	case ColorProPhoto:
		return 4, true
	case ColorXYZ:
		return 5, true
	case ColorDisplayP3:
		return 7, true
	case ColorRec2020:
		return 8, true
	default:
		return 0, false
	}
}

// DecodeDevelop produces the high-precision develop (see [Develop]): 16-bit, linear, Rec.2020,
// highlights recovered, un-oriented, opaque. Same determinism guarantees as [Decode] — a given
// file plus the pinned LibRaw version always yields the same raster.
//
// Returns [ErrUnsupported] for a non-RAW path, and a wrapped error on a genuine decode failure.
func DecodeDevelop(path string) (d Develop, err error) {
	defer func() {
		if r := recover(); r != nil {
			d, err = Develop{}, fmt.Errorf("raw: develop %q panicked: %v", path, r)
		}
	}()

	if !IsRAW(path) {
		return Develop{}, ErrUnsupported
	}

	const space = ColorRec2020
	oc, ok := librawOutputColor(space)
	if !ok {
		return Develop{}, fmt.Errorf("raw: develop %q: unsupported colour space %v", path, space)
	}

	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))

	var cbuf *C.ushort
	var w, h, n C.int
	rc := C.mm_raw_develop16(cpath, oc, &cbuf, &w, &h, &n)
	if rc != 0 {
		return Develop{}, fmt.Errorf("raw: develop %q: LibRaw error %d (%s)", path, int(rc), C.GoString(C.libraw_strerror(rc)))
	}
	defer C.free(unsafe.Pointer(cbuf))

	width, height, nbytes := int(w), int(h), int(n)
	if width <= 0 || height <= 0 || nbytes != width*height*3*2 {
		return Develop{}, fmt.Errorf("raw: develop %q: unexpected buffer %d×%d len=%d", path, width, height, nbytes)
	}

	// LibRaw hands back 3 channels of 16-bit samples in host byte order. NRGBA64 stores its
	// 16-bit channels big-endian, with a 4th (alpha) channel, so transcode per sample.
	src := unsafe.Slice((*uint16)(unsafe.Pointer(cbuf)), width*height*3)
	out := image.NewNRGBA64(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		srow := y * width * 3
		drow := out.PixOffset(0, y)
		for x := 0; x < width; x++ {
			s := srow + x*3
			d := drow + x*8
			binary.BigEndian.PutUint16(out.Pix[d+0:d+2], src[s+0])
			binary.BigEndian.PutUint16(out.Pix[d+2:d+4], src[s+1])
			binary.BigEndian.PutUint16(out.Pix[d+4:d+6], src[s+2])
			binary.BigEndian.PutUint16(out.Pix[d+6:d+8], 0xFFFF)
		}
	}
	return Develop{Image: out, ColorSpace: space, Version: DevelopVersion}, nil
}
