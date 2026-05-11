// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "libavutil/hwcontext.h"
// #include "libavutil/pixdesc.h"
// #include "libavutil/bprint.h"
// #include "libavcodec/avcodec.h"
//
// // Returns 1 if codec supports the device type via HW_DEVICE_CTX method.
// static int mm_codec_supports_hw_device(const AVCodec *codec,
//                                         enum AVHWDeviceType dev_type) {
//     for (int i = 0;; i++) {
//         const AVCodecHWConfig *cfg = avcodec_get_hw_config(codec, i);
//         if (!cfg) break;
//         if ((cfg->methods & AV_CODEC_HW_CONFIG_METHOD_HW_DEVICE_CTX) &&
//             cfg->device_type == dev_type)
//             return 1;
//     }
//     return 0;
// }
//
// // Returns a '\n'-separated list of SW pixel format names from the device's
// // frame constraints (hwconfig=NULL → max capabilities of the device).
// // Each entry is the av_get_pix_fmt_name string.
// // Caller must free the returned pointer with av_free.
// // Returns NULL if the backend does not implement the constraints API
// // (e.g. VideoToolbox) or on allocation failure.
// static char* mm_device_sw_fmt_list(AVBufferRef *device_ref) {
//     AVHWFramesConstraints *c =
//         av_hwdevice_get_hwframe_constraints(device_ref, NULL);
//     if (!c || !c->valid_sw_formats) {
//         if (c) av_hwframe_constraints_free(&c);
//         return NULL;
//     }
//     AVBPrint bp;
//     av_bprint_init(&bp, 64, AV_BPRINT_SIZE_UNLIMITED);
//     int first = 1;
//     for (int i = 0; c->valid_sw_formats[i] != AV_PIX_FMT_NONE; i++) {
//         const char *name = av_get_pix_fmt_name(c->valid_sw_formats[i]);
//         if (!name) continue;
//         if (!first) av_bprintf(&bp, "\n");
//         av_bprintf(&bp, "%s", name);
//         first = 0;
//     }
//     av_hwframe_constraints_free(&c);
//     char *result = NULL;
//     if (av_bprint_finalize(&bp, &result) < 0) return NULL;
//     return result;
// }
//
// // Queries the maximum frame resolution from the device's frame constraints.
// // Sets *max_w and *max_h. Returns 0 on success, -1 when unavailable.
// static int mm_device_max_resolution(AVBufferRef *device_ref,
//                                      int *max_w, int *max_h) {
//     AVHWFramesConstraints *c =
//         av_hwdevice_get_hwframe_constraints(device_ref, NULL);
//     if (!c) return -1;
//     *max_w = (int)c->max_width;
//     *max_h = (int)c->max_height;
//     av_hwframe_constraints_free(&c);
//     return 0;
// }
//
// // Returns a '\n'-separated list of "name:encode" or "name:decode" entries
// // for every FFmpeg codec registered with AV_CODEC_HW_CONFIG_METHOD_HW_DEVICE_CTX
// // for the given device type. This is a static registry query; it does not
// // require an open device context and does not probe the actual GPU.
// // Caller must free the returned pointer with av_free.
// // Returns NULL on allocation failure or when no codecs match.
// static char* mm_hw_codec_list(enum AVHWDeviceType dev_type) {
//     AVBPrint bp;
//     av_bprint_init(&bp, 256, AV_BPRINT_SIZE_UNLIMITED);
//     void *iter = NULL;
//     const AVCodec *codec;
//     int first = 1;
//     while ((codec = av_codec_iterate(&iter))) {
//         if (!mm_codec_supports_hw_device(codec, dev_type)) continue;
//         if (!first) av_bprintf(&bp, "\n");
//         av_bprintf(&bp, "%s:%s", codec->name,
//                    av_codec_is_encoder(codec) ? "encode" : "decode");
//         first = 0;
//     }
//     char *result = NULL;
//     if (av_bprint_finalize(&bp, &result) < 0) return NULL;
//     return result;
// }
import "C"

import (
	"strings"
	"unsafe"
)

// HWCodecInfo names a codec that advertises hardware-acceleration support
// for a device type via AV_CODEC_HW_CONFIG_METHOD_HW_DEVICE_CTX.
type HWCodecInfo struct {
	// Name is the canonical FFmpeg codec name (e.g. "h264_cuvid", "hevc_vaapi").
	Name string
	// Role is "encode" or "decode".
	Role string
}

// DeviceCapabilities summarises the capabilities of an open hardware device,
// combining runtime constraints from av_hwdevice_get_hwframe_constraints with
// a static scan of the FFmpeg codec registry.
type DeviceCapabilities struct {
	// SWFormats lists the software pixel formats the device can transfer
	// frames to/from (AVHWFramesConstraints.valid_sw_formats). Empty when
	// the backend does not implement the constraints API (e.g. VideoToolbox,
	// CUDA with null hwconfig).
	SWFormats []string

	// MaxWidth / MaxHeight are the maximum frame dimensions supported.
	// Both are 0 when the backend does not report resolution limits.
	MaxWidth  int
	MaxHeight int

	// Codecs lists every codec in the FFmpeg registry that declares
	// AV_CODEC_HW_CONFIG_METHOD_HW_DEVICE_CTX support for this device type.
	// This is a static per-build enumeration — it reflects what FFmpeg was
	// compiled with, not what the physical device supports (e.g. an old
	// Kepler CUDA GPU will still list av1_cuvid even though NVDEC on Kepler
	// cannot decode AV1). Use the SWFormats / MaxWidth constraints and the
	// NVIDIA support matrix to cross-reference actual per-GPU capabilities.
	Codecs []HWCodecInfo
}

// QueryCapabilities interrogates an open HWDeviceContext for its runtime
// frame-format constraints and enumerates the matching FFmpeg codecs.
func (d *HWDeviceContext) QueryCapabilities() DeviceCapabilities {
	var caps DeviceCapabilities

	// SW pixel format constraints (backend-specific; may return empty).
	if cStr := C.mm_device_sw_fmt_list(d.ref); cStr != nil {
		s := C.GoString(cStr)
		C.av_free(unsafe.Pointer(cStr))
		if s != "" {
			caps.SWFormats = strings.Split(s, "\n")
		}
	}

	// Resolution limits (backend-specific; zero when unreported).
	var cMaxW, cMaxH C.int
	if C.mm_device_max_resolution(d.ref, &cMaxW, &cMaxH) == 0 {
		caps.MaxWidth = int(cMaxW)
		caps.MaxHeight = int(cMaxH)
	}

	// Static codec registry scan.
	caps.Codecs = ListHWCodecs(d.deviceType)

	return caps
}

// ListHWCodecs enumerates all FFmpeg codecs registered with
// AV_CODEC_HW_CONFIG_METHOD_HW_DEVICE_CTX for the given device type.
// This is a static build-time query; no open device context is required.
func ListHWCodecs(t HWDeviceType) []HWCodecInfo {
	cStr := C.mm_hw_codec_list(C.enum_AVHWDeviceType(t))
	if cStr == nil {
		return nil
	}
	s := C.GoString(cStr)
	C.av_free(unsafe.Pointer(cStr))
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	out := make([]HWCodecInfo, 0, len(lines))
	for _, line := range lines {
		if i := strings.IndexByte(line, ':'); i > 0 {
			out = append(out, HWCodecInfo{Name: line[:i], Role: line[i+1:]})
		}
	}
	return out
}
