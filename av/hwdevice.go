// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "libavutil/hwcontext.h"
// #include "libavutil/pixdesc.h"
// #include "libavutil/buffer.h"
// #include "libavutil/frame.h"
// #include "libavcodec/avcodec.h"
//
// // Helper: get the hardware pixel format for a device type.
// static enum AVPixelFormat hw_pix_fmt_for_type(enum AVHWDeviceType type) {
//     switch (type) {
//         case AV_HWDEVICE_TYPE_CUDA:        return AV_PIX_FMT_CUDA;
//         case AV_HWDEVICE_TYPE_VAAPI:       return AV_PIX_FMT_VAAPI;
//         case AV_HWDEVICE_TYPE_QSV:         return AV_PIX_FMT_QSV;
//         case AV_HWDEVICE_TYPE_VIDEOTOOLBOX: return AV_PIX_FMT_VIDEOTOOLBOX;
//         default:                            return AV_PIX_FMT_NONE;
//     }
// }
//
// // Helper: allocate a hw frames context and set basic parameters.
// static AVBufferRef* alloc_hw_frames(AVBufferRef *device_ref,
//                                      enum AVPixelFormat hw_fmt,
//                                      enum AVPixelFormat sw_fmt,
//                                      int width, int height, int pool_size) {
//     AVBufferRef *frames_ref = av_hwframe_ctx_alloc(device_ref);
//     if (!frames_ref) return NULL;
//     AVHWFramesContext *frames_ctx = (AVHWFramesContext *)frames_ref->data;
//     frames_ctx->format    = hw_fmt;
//     frames_ctx->sw_format = sw_fmt;
//     frames_ctx->width     = width;
//     frames_ctx->height    = height;
//     frames_ctx->initial_pool_size = pool_size;
//     if (av_hwframe_ctx_init(frames_ref) < 0) {
//         av_buffer_unref(&frames_ref);
//         return NULL;
//     }
//     return frames_ref;
// }
//
// // Helper: transfer frame from hw to sw.
// static int transfer_frame_hw_to_sw(AVFrame *dst, const AVFrame *src) {
//     return av_hwframe_transfer_data(dst, src, 0);
// }
//
// // Helper: transfer frame from sw to hw.
// static int transfer_frame_sw_to_hw(AVFrame *dst, const AVFrame *src) {
//     return av_hwframe_transfer_data(dst, src, 0);
// }
//
// // Helper: check if a pixel format is a hardware format.
// static int is_hw_pix_fmt(enum AVPixelFormat fmt) {
//     const AVPixFmtDescriptor *desc = av_pix_fmt_desc_get(fmt);
//     if (!desc) return 0;
//     return (desc->flags & AV_PIX_FMT_FLAG_HWACCEL) ? 1 : 0;
// }
//
// // Helper: get the frame format.
// static enum AVPixelFormat frame_format(const AVFrame *f) {
//     return (enum AVPixelFormat)f->format;
// }
//
// // Helper: get frame hw_frames_ctx.
// static AVBufferRef* frame_hw_frames_ctx(const AVFrame *f) {
//     return f->hw_frames_ctx;
// }
//
// // Helper: set hw_frames_ctx on an allocated frame and allocate hw buffer.
// static int alloc_hw_frame(AVFrame *f, AVBufferRef *hw_frames_ref) {
//     f->hw_frames_ctx = av_buffer_ref(hw_frames_ref);
//     if (!f->hw_frames_ctx) return -1;
//     return av_hwframe_get_buffer(f->hw_frames_ctx, f, 0);
// }
import "C"

import (
	"fmt"
	"strings"
	"unsafe"
)

// HWDeviceType enumerates supported hardware acceleration backends.
type HWDeviceType int

const (
	HWDeviceCUDA         HWDeviceType = HWDeviceType(C.AV_HWDEVICE_TYPE_CUDA)
	HWDeviceVAAPI        HWDeviceType = HWDeviceType(C.AV_HWDEVICE_TYPE_VAAPI)
	HWDeviceQSV          HWDeviceType = HWDeviceType(C.AV_HWDEVICE_TYPE_QSV)
	HWDeviceVideoToolbox HWDeviceType = HWDeviceType(C.AV_HWDEVICE_TYPE_VIDEOTOOLBOX)
	HWDeviceNone         HWDeviceType = HWDeviceType(C.AV_HWDEVICE_TYPE_NONE)
)

func (t HWDeviceType) String() string {
	switch t {
	case HWDeviceCUDA:
		return "cuda"
	case HWDeviceVAAPI:
		return "vaapi"
	case HWDeviceQSV:
		return "qsv"
	case HWDeviceVideoToolbox:
		return "videotoolbox"
	default:
		return "none"
	}
}

// ParseHWDeviceType parses a hardware device type name string.
func ParseHWDeviceType(name string) HWDeviceType {
	cName := C.CString(strings.ToLower(name))
	defer C.free(unsafe.Pointer(cName))
	t := C.av_hwdevice_find_type_by_name(cName)
	return HWDeviceType(t)
}

// HWPixelFormat returns the hardware pixel format associated with this device type.
func (t HWDeviceType) HWPixelFormat() int {
	return int(C.hw_pix_fmt_for_type(C.enum_AVHWDeviceType(t)))
}

// HWDeviceContext wraps an AVBufferRef* for a hardware device (e.g. CUDA, VAAPI).
// It must be closed via Close().
type HWDeviceContext struct {
	ref        *C.AVBufferRef
	deviceType HWDeviceType
}

// OpenHWDevice creates a hardware device context of the given type.
// device is the device name/path (e.g. "/dev/dri/renderD128" for VAAPI, "" for default).
// Returns an error if the hardware type is unavailable on this system.
func OpenHWDevice(deviceType HWDeviceType, device string) (*HWDeviceContext, error) {
	var ref *C.AVBufferRef
	var cDevice *C.char
	if device != "" {
		cDevice = C.CString(device)
		defer C.free(unsafe.Pointer(cDevice))
	}

	ret := C.av_hwdevice_ctx_create(&ref, C.enum_AVHWDeviceType(deviceType), cDevice, nil, 0)
	if ret < 0 {
		return nil, fmt.Errorf("av_hwdevice_ctx_create(%s, %q): %w", deviceType, device, newErr(ret))
	}

	return &HWDeviceContext{ref: ref, deviceType: deviceType}, nil
}

// Close releases the hardware device context.
func (d *HWDeviceContext) Close() error {
	if d.ref != nil {
		C.av_buffer_unref(&d.ref)
		d.ref = nil
	}
	return nil
}

// Type returns the device type.
func (d *HWDeviceContext) Type() HWDeviceType { return d.deviceType }

// raw returns the underlying AVBufferRef for use by other av package functions.
func (d *HWDeviceContext) raw() *C.AVBufferRef { return d.ref }

// HWFramesContext wraps an AVBufferRef* for a hardware frame pool.
type HWFramesContext struct {
	ref        *C.AVBufferRef
	deviceType HWDeviceType
}

// NewHWFramesContext allocates a hardware frames context (frame pool) on the given device.
// swPixFmt is the software pixel format for transferred frames (e.g. AV_PIX_FMT_NV12).
// poolSize is the number of frames to pre-allocate (0 = dynamic).
func NewHWFramesContext(device *HWDeviceContext, width, height, swPixFmt, poolSize int) (*HWFramesContext, error) {
	hwFmt := C.hw_pix_fmt_for_type(C.enum_AVHWDeviceType(device.deviceType))
	if hwFmt == C.AV_PIX_FMT_NONE {
		return nil, fmt.Errorf("no hw pixel format for device type %s", device.deviceType)
	}

	ref := C.alloc_hw_frames(device.ref, hwFmt,
		C.enum_AVPixelFormat(swPixFmt),
		C.int(width), C.int(height), C.int(poolSize))
	if ref == nil {
		return nil, fmt.Errorf("failed to allocate hw frames context for %s", device.deviceType)
	}

	return &HWFramesContext{ref: ref, deviceType: device.deviceType}, nil
}

// Close releases the hardware frames context.
func (f *HWFramesContext) Close() error {
	if f.ref != nil {
		C.av_buffer_unref(&f.ref)
		f.ref = nil
	}
	return nil
}

// raw returns the underlying AVBufferRef.
func (f *HWFramesContext) raw() *C.AVBufferRef { return f.ref }

// TransferToSW transfers a hardware frame to a software frame.
// The caller must provide an allocated destination frame (via AllocFrame).
// After the call, dst contains the frame data in system memory.
func TransferToSW(dst, src *Frame) error {
	ret := C.transfer_frame_hw_to_sw(dst.raw(), src.raw())
	if ret < 0 {
		return fmt.Errorf("hw→sw transfer: %w", newErr(ret))
	}
	return nil
}

// TransferToHW transfers a software frame to a hardware frame.
// dst must have a hw_frames_ctx set (via AllocHWFrame).
func TransferToHW(dst, src *Frame) error {
	ret := C.transfer_frame_sw_to_hw(dst.raw(), src.raw())
	if ret < 0 {
		return fmt.Errorf("sw→hw transfer: %w", newErr(ret))
	}
	return nil
}

// AllocHWFrame allocates a hardware frame from the given frames context.
// The returned frame has its hw_frames_ctx set and buffer allocated.
func AllocHWFrame(framesCtx *HWFramesContext) (*Frame, error) {
	f, err := AllocFrame()
	if err != nil {
		return nil, err
	}
	ret := C.alloc_hw_frame(f.raw(), framesCtx.ref)
	if ret < 0 {
		f.Close()
		return nil, fmt.Errorf("alloc hw frame: %w", newErr(ret))
	}
	return f, nil
}

// IsHWFrame reports whether the frame's pixel format is a hardware format.
func IsHWFrame(f *Frame) bool {
	return C.is_hw_pix_fmt(C.frame_format(f.raw())) != 0
}

// FrameFormat returns the pixel format of the frame.
func FrameFormat(f *Frame) int {
	return int(C.frame_format(f.raw()))
}

// ListHWDeviceTypes returns all hardware device types supported by the linked FFmpeg.
func ListHWDeviceTypes() []HWDeviceType {
	var types []HWDeviceType
	t := C.enum_AVHWDeviceType(C.AV_HWDEVICE_TYPE_NONE)
	for {
		t = C.av_hwdevice_iterate_types(t)
		if t == C.AV_HWDEVICE_TYPE_NONE {
			break
		}
		types = append(types, HWDeviceType(t))
	}
	return types
}
