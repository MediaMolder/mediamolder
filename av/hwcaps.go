// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "libavutil/hwcontext.h"
// #include "libavutil/pixdesc.h"
// #include "libavutil/bprint.h"
// #include "libavcodec/avcodec.h"
//
// // Returns 1 if codec supports the device type via HW_DEVICE_CTX or
// // HW_FRAMES_CTX methods. Both must be considered: decoders typically use
// // HW_DEVICE_CTX (opened context passed in) while encoders such as the
// // VideoToolbox family use HW_FRAMES_CTX (frames context wraps the device).
// static int mm_codec_supports_hw(const AVCodec *codec,
//                                  enum AVHWDeviceType dev_type) {
//     for (int i = 0;; i++) {
//         const AVCodecHWConfig *cfg = avcodec_get_hw_config(codec, i);
//         if (!cfg) break;
//         if (((cfg->methods & AV_CODEC_HW_CONFIG_METHOD_HW_DEVICE_CTX) ||
//              (cfg->methods & AV_CODEC_HW_CONFIG_METHOD_HW_FRAMES_CTX)) &&
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
//     // INT_MAX is the FFmpeg sentinel meaning "not known / uncapped".
//     // Treat it as unavailable so callers see 0 instead of a garbage value.
//     int w = (int)c->max_width;
//     int h = (int)c->max_height;
//     av_hwframe_constraints_free(&c);
//     if (w == INT_MAX || h == INT_MAX) return -1;
//     *max_w = w;
//     *max_h = h;
//     return 0;
// }
//
// // Returns a '\n'-separated list of "name:role:mediatype" entries for every
// // FFmpeg codec that supports the given device type via HW_DEVICE_CTX or
// // HW_FRAMES_CTX. role is "encode"|"decode"; mediatype is the
// // av_get_media_type_string value ("video", "audio", etc.).
// // This is a static registry query; no open device context is required.
// // Caller must free the returned pointer with av_free.
// // Returns NULL on allocation failure or when no codecs match.
// static char* mm_hw_codec_list(enum AVHWDeviceType dev_type) {
//     AVBPrint bp;
//     av_bprint_init(&bp, 256, AV_BPRINT_SIZE_UNLIMITED);
//     void *iter = NULL;
//     const AVCodec *codec;
//     int first = 1;
//     while ((codec = av_codec_iterate(&iter))) {
//         if (!mm_codec_supports_hw(codec, dev_type)) continue;
//         const char *mt = av_get_media_type_string(codec->type);
//         if (!mt) mt = "unknown";
//         if (!first) av_bprintf(&bp, "\n");
//         av_bprintf(&bp, "%s:%s:%s", codec->name,
//                    av_codec_is_encoder(codec) ? "encode" : "decode",
//                    mt);
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
// for a device type via AV_CODEC_HW_CONFIG_METHOD_HW_DEVICE_CTX or
// AV_CODEC_HW_CONFIG_METHOD_HW_FRAMES_CTX.
type HWCodecInfo struct {
	// Name is the canonical FFmpeg codec name (e.g. "h264_cuvid", "hevc_vaapi").
	Name string
	// Role is "encode" or "decode".
	Role string
	// MediaType is the AVMediaType string from FFmpeg: "video", "audio",
	// "subtitle", "data", or "unknown".
	MediaType string
	// Note is an optional human-readable limitation note for this codec at
	// the current GPU's compute capability (e.g. "4:2:2 profiles require
	// Turing (SM 7.5+)").  Empty when there are no known limitations.
	Note string
}

// DeviceCapabilities summarises the capabilities of an open hardware device,
// combining runtime constraints from av_hwdevice_get_hwframe_constraints with
// a filtered/annotated scan of the FFmpeg codec registry.
type DeviceCapabilities struct {
	// DisplayName is a human-readable marketing name for the physical device,
	// e.g. "NVIDIA GeForce RTX 3060 Ti" (CUDA), "Apple GPU" (VideoToolbox),
	// or "/dev/dri/renderD128" (VAAPI fallback). Empty when the backend
	// cannot report a name.
	DisplayName string

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

	// Filters lists the names of libavfilter filters that advertise
	// AVFILTER_FLAG_HWDEVICE and whose naming suffix matches this device
	// type (e.g. scale_cuda, yadif_cuda for CUDA; scale_vaapi for VAAPI;
	// libplacebo for Vulkan). Always populated when the device is available.
	Filters []string

	// CUDASMMajor / CUDASMMinor are the CUDA compute capability (e.g. 8, 9
	// for Ada Lovelace).  Both are 0 for non-CUDA devices or when the SM
	// version probe fails (no CUDA driver at runtime, macOS, etc.).
	CUDASMMajor int
	CUDASMMinor int

	// CUDAArch is the GPU architecture marketing name derived from the SM
	// version (e.g. "Ada Lovelace", "Ampere", "Turing").
	// Empty when CUDASMMajor is 0.
	CUDAArch string

	// NVENCCaps holds per-codec NVENC encoder capability records queried at
	// runtime from libnvidia-encode via nvEncGetEncodeCaps.  Nil for non-CUDA
	// devices or when the NVENC library is not installed.
	NVENCCaps []NVENCCodecCaps

	// NVDECCaps holds per-codec NVDEC decoder capability records queried at
	// runtime from libnvcuvid via cuvidGetDecoderCaps.  Nil for non-CUDA
	// devices or when the NVDEC library is not installed.
	NVDECCaps []NVDECCodecCaps
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
		if name, err := queryCUDADisplayName(d); err == nil {
			caps.DisplayName = name
		}
		if smMaj, smMin, err := queryCUDASMVersion(d); err == nil {
			caps.CUDASMMajor = smMaj
			caps.CUDASMMinor = smMin
			caps.CUDAArch = nvidiaArchName(smMaj, smMin)
			caps.Codecs = FilterNVIDIACodecs(smMaj, smMin, staticCodecs)
		} else {
			// Driver unavailable (macOS, no NVIDIA drivers) — keep static list.
			caps.Codecs = staticCodecs
		}
		// Dynamic NVENC / NVDEC capability queries (no-ops when libraries absent).
		caps.NVENCCaps = QueryNVENCCaps(d)
		caps.NVDECCaps = QueryNVDECCaps(d)
	} else {
		caps.Codecs = staticCodecs
	}

	// For VideoToolbox, augment the LibAV registry scan with a direct
	// platform probe that can reveal codecs LibAV cannot represent
	// (e.g. ProRes RAW encode/decode on Apple Silicon), and populate the
	// marketing GPU name via IORegistry.
	if d.deviceType == HWDeviceVideoToolbox {
		vtCaps := QueryVTCapabilities()
		caps.Codecs = mergeVTCodecs(caps.Codecs, vtCaps)
		caps.DisplayName = queryVTDisplayName()
	}

	// VAAPI: resolve PCI-ID to a human-readable device name.
	if d.deviceType == HWDeviceVAAPI {
		caps.DisplayName = queryVAAPIDisplayName(d.device)
	}

	// QSV: derive display name from the underlying render node (Linux) or
	// use a generic Intel fallback on other platforms.
	if d.deviceType == HWDeviceQSV {
		caps.DisplayName = queryQSVDisplayName()
	}

	// Hardware-accelerated filters for this device type.
	caps.Filters = FilterHWAccels(d.deviceType)

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
		// Format: "name:role:mediatype" (3 fields).
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 2 {
			continue
		}
		ci := HWCodecInfo{Name: parts[0], Role: parts[1]}
		if len(parts) == 3 {
			ci.MediaType = parts[2]
		}
		out = append(out, ci)
	}
	return out
}

// hwFilterSuffixes maps each device type to the filter-name suffixes and
// exact names that identify hardware-accelerated filters for that backend.
// Used by FilterHWAccels to avoid coupling list.go to hwcaps.go.
var hwFilterSuffixes = map[HWDeviceType][]string{
	HWDeviceCUDA:         {"_cuda"},
	HWDeviceVAAPI:        {"_vaapi"},
	HWDeviceQSV:          {"_qsv"},
	HWDeviceVideoToolbox: {"_videotoolbox"},
}

// hwFilterExact lists filter names that are hardware-accelerated but do not
// follow the _backend suffix convention (e.g. libplacebo uses Vulkan).
var hwFilterExact = map[string]HWDeviceType{
	"libplacebo": HWDeviceType(-1), // matches any Vulkan-capable device; -1 = wildcard
}

// FilterHWAccels returns the names of libavfilter filters that both
// advertise AVFILTER_FLAG_HWDEVICE and belong to the given device type
// (by name suffix or exact-match table). The list is derived from the
// live libavfilter registry so only filters compiled into the running
// binary are returned.
func FilterHWAccels(t HWDeviceType) []string {
	suffixes := hwFilterSuffixes[t]
	var out []string
	for _, f := range ListFilters() {
		if !f.SupportsHWDevice {
			continue
		}
		matched := false
		for _, suf := range suffixes {
			if strings.HasSuffix(f.Name, suf) {
				matched = true
				break
			}
		}
		if !matched {
			if devType, ok := hwFilterExact[f.Name]; ok {
				// -1 = wildcard (libplacebo); include for any HW device type.
				matched = devType == t || devType == HWDeviceType(-1)
			}
		}
		if matched {
			out = append(out, f.Name)
		}
	}
	return out
}

// mergeVTCodecs adds VT-native codec entries (from a direct platform probe)
// to base, skipping any codec name that already appears in base.
func mergeVTCodecs(base []HWCodecInfo, vt VTPlatformCapabilities) []HWCodecInfo {
	seen := make(map[string]bool, len(base))
	for _, c := range base {
		seen[c.Name+":"+c.Role] = true
	}
	for _, c := range vt.ExtraEncoders {
		if !seen[c.Name+":"+c.Role] {
			base = append(base, c)
			seen[c.Name+":"+c.Role] = true
		}
	}
	for _, c := range vt.ExtraDecoders {
		if !seen[c.Name+":"+c.Role] {
			base = append(base, c)
			seen[c.Name+":"+c.Role] = true
		}
	}
	return base
}
