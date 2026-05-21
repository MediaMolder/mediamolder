//
//            PySceneDetect: Python-Based Video Scene Detector
//  -------------------------------------------------------------------
//     [  Site:   https://scenedetect.com                           ]
//     [  Docs:   https://scenedetect.com/docs/                     ]
//     [  Github: https://github.com/Breakthrough/PySceneDetect/    ]
//
// Copyright (C) 2014-2024 Brandon Castellano <http://www.bcastell.com>.
// PySceneDetect is licensed under the BSD 3-Clause License; see the
// included LICENSE file, or visit one of the above pages for details.
// License: https://github.com/Breakthrough/PySceneDetect/blob/main/LICENSE

package imgmath

// #include "libswscale/swscale.h"
// #include "libavutil/pixfmt.h"
// #include "libavutil/mem.h"
//
// // resize_packed resizes a packed (non-planar) pixel buffer.
// // src_fmt / dst_fmt must be the same pixel format (only the dimensions differ
// // when upscaling/downscaling a single-format image).
// // Returns 0 on success, -1 on failure.
// static int resize_packed(
//     const uint8_t *src, int srcW, int srcH, int src_stride,
//     uint8_t      *dst, int dstW, int dstH, int dst_stride,
//     int src_fmt, int sws_flags)
// {
//     struct SwsContext *sws = sws_getContext(
//         srcW, srcH, (enum AVPixelFormat)src_fmt,
//         dstW, dstH, (enum AVPixelFormat)src_fmt,
//         sws_flags, NULL, NULL, NULL);
//     if (!sws)
//         return -1;
//
//     const uint8_t *src_slice[4] = { src, NULL, NULL, NULL };
//     int            src_ls[4]   = { src_stride, 0, 0, 0 };
//     uint8_t       *dst_slice[4] = { dst, NULL, NULL, NULL };
//     int            dst_ls[4]   = { dst_stride, 0, 0, 0 };
//
//     int ret = sws_scale(sws, src_slice, src_ls, 0, srcH, dst_slice, dst_ls);
//     sws_freeContext(sws);
//     return (ret == dstH) ? 0 : -1;
// }
import "C"

import (
	"fmt"
	"unsafe"
)

// Interp specifies the resampling algorithm used by ResizeBGR24/ResizeGRAY8.
// Constant values match the corresponding libswscale SWS_* flag bits.
type Interp int

const (
	InterpNearest Interp = 16  // SWS_POINT     (1<<4) — nearest-neighbour
	InterpLinear  Interp = 2   // SWS_BILINEAR  (1<<1)
	InterpCubic   Interp = 4   // SWS_BICUBIC   (1<<2)
	InterpArea    Interp = 32  // SWS_AREA      (1<<5) — best for downscaling
	InterpLanczos Interp = 512 // SWS_LANCZOS   (1<<9) — 3-tap sinc
)

// ResizeBGR24 scales a packed BGR24 pixel buffer from (srcW×srcH) to
// (dstW×dstH) using libswscale with the specified interpolation filter.
// The returned slice has length dstW*dstH*3. Use InterpArea for downscaling
// (matches cv2.INTER_AREA).
func ResizeBGR24(src []byte, srcW, srcH, dstW, dstH int, interp Interp) ([]byte, error) {
	if len(src) != srcW*srcH*3 {
		return nil, fmt.Errorf("imgmath: ResizeBGR24: src len %d != %d*%d*3", len(src), srcW, srcH)
	}
	if dstW <= 0 || dstH <= 0 {
		return nil, fmt.Errorf("imgmath: ResizeBGR24: invalid dst size %dx%d", dstW, dstH)
	}
	dst := make([]byte, dstW*dstH*3)
	rc := C.resize_packed(
		(*C.uint8_t)(unsafe.Pointer(&src[0])), C.int(srcW), C.int(srcH), C.int(srcW*3),
		(*C.uint8_t)(unsafe.Pointer(&dst[0])), C.int(dstW), C.int(dstH), C.int(dstW*3),
		C.int(C.AV_PIX_FMT_BGR24), C.int(interp),
	)
	if rc != 0 {
		return nil, fmt.Errorf("imgmath: ResizeBGR24: sws_scale failed")
	}
	return dst, nil
}

// ResizeGRAY8 scales a grayscale (GRAY8) pixel buffer from (srcW×srcH) to
// (dstW×dstH) using libswscale with the specified interpolation filter.
// The returned slice has length dstW*dstH.
func ResizeGRAY8(src []byte, srcW, srcH, dstW, dstH int, interp Interp) ([]byte, error) {
	if len(src) != srcW*srcH {
		return nil, fmt.Errorf("imgmath: ResizeGRAY8: src len %d != %d*%d", len(src), srcW, srcH)
	}
	if dstW <= 0 || dstH <= 0 {
		return nil, fmt.Errorf("imgmath: ResizeGRAY8: invalid dst size %dx%d", dstW, dstH)
	}
	dst := make([]byte, dstW*dstH)
	rc := C.resize_packed(
		(*C.uint8_t)(unsafe.Pointer(&src[0])), C.int(srcW), C.int(srcH), C.int(srcW),
		(*C.uint8_t)(unsafe.Pointer(&dst[0])), C.int(dstW), C.int(dstH), C.int(dstW),
		C.int(C.AV_PIX_FMT_GRAY8), C.int(interp),
	)
	if rc != 0 {
		return nil, fmt.Errorf("imgmath: ResizeGRAY8: sws_scale failed")
	}
	return dst, nil
}
