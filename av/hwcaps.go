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
	// Note is an optional human-readable limitation note for this codec at
	// the current GPU's compute capability (e.g. "4:2:2 profiles require
	// Turing (SM 7.5+)").  Empty when there are no known limitations.
	Note string
}

// DeviceCapabilities summarises the capabilities of an open hardware device,
// combining runtime constraints from av_hwdevice_get_hwframe_constraints with
// a filtered/annotated scan of the FFmpeg codec registry.
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

	// Codecs lists every codec that is supported by this device.
	// For CUDA devices where the SM version could be probed, codecs
	// unsupported by the GPU generation are removed and those with
	// profile/feature gaps carry a non-empty HWCodecInfo.Note.
	// For other backends (VAAPI, VideoToolbox, QSV) this is the full
	// FFmpeg registry scan without filtering.
	Codecs []HWCodecInfo

	// CUDASMMajor / CUDASMMinor are the CUDA compute capability (e.g. 8, 9
	// for Ada Lovelace).  Both are 0 for non-CUDA devices or when the SM
	// version probe fails (no CUDA driver at runtime, macOS, etc.).
	CUDASMMajor int
	CUDASMMinor int

	// CUDAArch is the GPU architecture marketing name derived from the SM
	// version (e.g. "Ada Lovelace", "Ampere", "Turing").
	// Empty when CUDASMMajor is 0.
	CUDAArch string
}

// QueryCapabilities interrogates an open HWDeviceContext for its runtime
// frame-format constraints and enumerates the matching FFmpeg codecs.
//
// For CUDA devices the SM (compute capability) version is probed via the
// CUDA driver API and used to:
//   - remove codecs that the GPU generation cannot execute, and
//   - annotate codecs that have profile/feature gaps at this SM version.
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

	// Codec list: static registry scan, refined for CUDA with per-GPU filtering.
	staticCodecs := ListHWCodecs(d.deviceType)
	if d.deviceType == HWDeviceCUDA {
		if smMaj, smMin, err := queryCUDASMVersion(d); err == nil {
			caps.CUDASMMajor = smMaj
			caps.CUDASMMinor = smMin
			caps.CUDAArch = nvidiaArchName(smMaj, smMin)
			caps.Codecs = FilterNVIDIACodecs(smMaj, smMin, staticCodecs)
		} else {
			// Driver unavailable (macOS, no NVIDIA drivers) — keep static list.
			caps.Codecs = staticCodecs
		}
	} else {
		caps.Codecs = staticCodecs
	}

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
