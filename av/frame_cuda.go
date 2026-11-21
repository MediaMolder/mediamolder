// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include <stdint.h>
// #include "libavutil/hwcontext.h"
// #include "libavutil/pixdesc.h"
//
// // libavutil only needs these opaque types to describe its device context.
// // Suppress the CUDA SDK include to keep the default build SDK-independent.
// typedef struct CUctx_st *CUcontext;
// typedef struct CUstream_st *CUstream;
// #define CUDA_VERSION 1
// #include "libavutil/hwcontext_cuda.h"
//
// typedef struct {
//     uintptr_t context, stream;
//     int format, planes;
//     uintptr_t data[4];
//     int pitch[4];
// } MMCUDAFrame;
//
// static MMCUDAFrame mm_frame_cuda(const AVFrame *f) {
//     MMCUDAFrame view = {0};
//     if (f->format != AV_PIX_FMT_CUDA || !f->hw_frames_ctx)
//         return view;
//     const AVHWFramesContext *pool = (const AVHWFramesContext *)f->hw_frames_ctx->data;
//     const AVHWDeviceContext *device = pool->device_ctx;
//     if (!device || device->type != AV_HWDEVICE_TYPE_CUDA || !device->hwctx)
//         return view;
//     const AVCUDADeviceContext *cuda = device->hwctx;
//     int planes = av_pix_fmt_count_planes(pool->sw_format);
//     if (!cuda->cuda_ctx || planes <= 0 || planes > 4)
//         return view;
//     for (int i = 0; i < planes; i++) {
//         if (!f->data[i] || f->linesize[i] <= 0)
//             return view;
//         view.data[i] = (uintptr_t)f->data[i];
//         view.pitch[i] = f->linesize[i];
//     }
//     view.context = (uintptr_t)cuda->cuda_ctx;
//     view.stream = (uintptr_t)cuda->stream;
//     view.format = pool->sw_format;
//     view.planes = planes;
//     return view;
// }
import "C"

// CUDAPlane describes a CUDA image plane. DevicePtr is a CUdeviceptr and
// Pitch is the row stride in bytes, including padding.
type CUDAPlane struct {
	DevicePtr uintptr
	Pitch     int
}

// CUDAFrame exposes borrowed CUDA handles and pixel layout without moving
// pixels to host memory. Context and Stream are a CUcontext and CUstream.
// A zero Stream denotes the CUDA default stream. PixFmt is the underlying
// FFmpeg pixel layout (e.g. NV12), and the first NumPlanes entries are valid.
type CUDAFrame struct {
	Context   uintptr
	Stream    uintptr
	PixFmt    int
	NumPlanes int
	Planes    [4]CUDAPlane
}

// CUDAFrame returns a CUDA view of f, or false if f has no usable CUDA surface.
// All handles are valid only while f retains the surface. The caller must make
// Context current on the OS thread issuing CUDA calls, and order its work
// against Stream. Complete work using the frame before handing it downstream
// or releasing it. Use AllocHWFrameLike for a separate writable output.
func (f *Frame) CUDAFrame() (CUDAFrame, bool) {
	if f == nil || f.p == nil {
		return CUDAFrame{}, false
	}
	view := C.mm_frame_cuda(f.p)
	if view.planes == 0 {
		return CUDAFrame{}, false
	}
	out := CUDAFrame{
		Context:   uintptr(view.context),
		Stream:    uintptr(view.stream),
		PixFmt:    int(view.format),
		NumPlanes: int(view.planes),
	}
	for i := range out.NumPlanes {
		out.Planes[i] = CUDAPlane{DevicePtr: uintptr(view.data[i]), Pitch: int(view.pitch[i])}
	}
	return out, true
}
