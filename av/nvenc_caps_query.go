// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// Dynamic NVENC encoder capability query.
//
// Loads libnvidia-encode.so (Linux) or nvEncodeAPI64.dll (Windows) at runtime
// via dlopen, opens a throw-away encode session against the supplied CUDA
// context, queries NV_ENC_CAPS_* for H.264, HEVC, and AV1, then destroys the
// session and unloads the library.  The code compiles on every platform;
// on macOS or any system without the NVENC library it returns an empty slice
// (not an error).
//
// No NVENC SDK headers are required at compile time.  All struct layouts and
// version constants are replicated inline from the public NVENC SDK 12.1 ABI.
//
// Sources:
//   - NVIDIA Video Codec SDK 12.1 nvEncodeAPI.h
//   - https://github.com/FFmpeg/nv-codec-headers

// #cgo linux LDFLAGS: -ldl
//
// #include <stdint.h>
// #include <string.h>
// #include "libavutil/hwcontext.h"
//
// // ── Dynamic-loader shim (duplicated from nvcaps.go — each CGo TU is independent) ──
// #ifdef _WIN32
// #  include <windows.h>
// typedef HMODULE mm_nve_dl_t;
// static mm_nve_dl_t  mm_nve_dlopen(const char *n)             { return LoadLibraryA(n); }
// static void        *mm_nve_dlsym(mm_nve_dl_t h, const char *s){ return (void *)GetProcAddress(h, s); }
// static void         mm_nve_dlclose(mm_nve_dl_t h)             { FreeLibrary(h); }
// #else
// #  include <dlfcn.h>
// typedef void *mm_nve_dl_t;
// static mm_nve_dl_t  mm_nve_dlopen(const char *n)             { return dlopen(n, RTLD_LAZY | RTLD_LOCAL); }
// static void        *mm_nve_dlsym(mm_nve_dl_t h, const char *s){ return dlsym(h, s); }
// static void         mm_nve_dlclose(mm_nve_dl_t h)             { dlclose(h); }
// #endif
//
// // ── Extract CUcontext from AVHWDeviceContext (same layout trick as nvcaps.go) ──
// typedef void *mm_nve_CUcontext;
// static mm_nve_CUcontext mm_nve_get_cuda_ctx(AVBufferRef *ref) {
//     if (!ref || !ref->data) return NULL;
//     AVHWDeviceContext *dev = (AVHWDeviceContext *)ref->data;
//     if (dev->type != AV_HWDEVICE_TYPE_CUDA || !dev->hwctx) return NULL;
//     return *((mm_nve_CUcontext *)dev->hwctx);
// }
//
// // ── Minimal NVENC ABI types (SDK 12.1) ────────────────────────────────────────
// //
// // NVENCAPI_VERSION = major | (minor << 24) = 12 | (1 << 24) = 0x0100000C
// // NVENCAPI_STRUCT_VERSION(n) = NVENCAPI_VERSION | (n<<16) | (7<<28)
// #define MM_NVENCAPI_VER                 0x0100000CU
// #define MM_NVENC_STRUCT_VER(n)          (MM_NVENCAPI_VER | ((n)<<16) | (0x7U<<28))
// #define MM_NV_ENC_CAPS_PARAM_VER        MM_NVENC_STRUCT_VER(1)   // 0x7101000C
// #define MM_NV_ENC_OPEN_SESSION_EX_VER   MM_NVENC_STRUCT_VER(1)   // 0x7101000C
// #define MM_NV_ENC_FN_LIST_VER           MM_NVENC_STRUCT_VER(2)   // 0x7102000C
// #define MM_NV_ENC_DEVICE_TYPE_CUDA      1
//
// // NV_ENC_CAPS enum values (stable since SDK 9; verified against SDK 12.1).
// #define MM_NV_ENC_CAPS_NUM_MAX_BFRAMES          0
// #define MM_NV_ENC_CAPS_LEVEL_MAX               13
// #define MM_NV_ENC_CAPS_LEVEL_MIN               14
// #define MM_NV_ENC_CAPS_WIDTH_MAX               16
// #define MM_NV_ENC_CAPS_HEIGHT_MAX              17
// #define MM_NV_ENC_CAPS_MB_NUM_MAX              31
// #define MM_NV_ENC_CAPS_MB_PER_SEC_MAX          32
// #define MM_NV_ENC_CAPS_SUPPORT_YUV444_ENCODE   33
// #define MM_NV_ENC_CAPS_SUPPORT_LOSSLESS_ENCODE 34
// #define MM_NV_ENC_CAPS_SUPPORT_LOOKAHEAD       37
// #define MM_NV_ENC_CAPS_SUPPORT_TEMPORAL_AQ     38
// #define MM_NV_ENC_CAPS_SUPPORT_10BIT_ENCODE    39
// #define MM_NV_ENC_CAPS_SUPPORT_WEIGHTED_PREDICTION 41
// #define MM_NV_ENC_CAPS_SUPPORT_BFRAME_REF_MODE 43
// #define MM_NV_ENC_CAPS_WIDTH_MIN               45
// #define MM_NV_ENC_CAPS_HEIGHT_MIN              46
// #define MM_NV_ENC_CAPS_NUM_ENCODER_ENGINES     49
//
// // NV_ENC_GUID (= Windows GUID layout: {d1, d2, d3, d4[8]}).
// typedef struct { uint32_t d1; uint16_t d2; uint16_t d3; uint8_t d4[8]; } mm_nv_enc_guid;
//
// // Codec GUIDs from nvEncodeAPI.h.
// static const mm_nv_enc_guid MM_NV_ENC_H264_GUID =
//     {0x6bc82762, 0x4e63, 0x4ca4, {0xaa,0x85,0x1e,0x50,0xf3,0x21,0xf6,0xbf}};
// static const mm_nv_enc_guid MM_NV_ENC_HEVC_GUID =
//     {0x790cdc88, 0x4522, 0x4d7b, {0x94,0x25,0xbd,0xa9,0x97,0x5f,0x76,0x03}};
// static const mm_nv_enc_guid MM_NV_ENC_AV1_GUID  =
//     {0x0a352289, 0x0aa7, 0x4759, {0x86,0x2d,0x5d,0x15,0xcd,0x16,0xd2,0x54}};
//
// // NV_ENC_OPEN_ENCODE_SESSION_EX_PARAMS (SDK 12.1 layout on 64-bit).
// // version(4) + deviceType(4) + device(8) + reserved_guid_ptr(8) + apiVersion(4) + _pad(4) + reserved1[253](2024)
// typedef struct {
//     uint32_t version;           // [in] NV_ENC_OPEN_ENCODE_SESSION_EX_PARAMS_VER
//     uint32_t deviceType;        // [in] NV_ENC_DEVICE_TYPE_CUDA = 1
//     void    *device;            // [in] CUcontext
//     void    *reserved_guid;     // [in] must be NULL
//     uint32_t apiVersion;        // [in] NVENCAPI_VERSION
//     uint32_t _pad;              // implicit alignment padding
//     void    *reserved1[253];    // [in] must be NULL
// } mm_nv_enc_open_session_params;
//
// // NV_ENC_CAPS_PARAM (SDK 12.1 layout).
// // version(4) + capsToQuery(4) + reserved[62](248) = 256 bytes total.
// typedef struct {
//     uint32_t version;
//     uint32_t capsToQuery;
//     uint32_t reserved[62];
// } mm_nv_enc_caps_param;
//
// // NV_ENCODE_API_FUNCTION_LIST (SDK 12.1 on 64-bit).
// // We declare 64 fn-pointer slots (SDK 12.1 uses ~40) plus trailing reserved,
// // which is safe: the driver only fills slots it knows and we zero the rest.
// // Function pointer slot indices (0-based from fn[0]).
// // Verified against NV_ENCODE_API_FUNCTION_LIST in nvEncodeAPI.h SDK 12.1–13.0:
// //   0  nvEncOpenEncodeSession
// //   1  nvEncGetEncodeGUIDCount
// //   2  nvEncGetEncodeProfileGUIDCount
// //   3  nvEncGetEncodeProfileGUIDs
// //   4  nvEncGetEncodeGUIDs
// //   5  nvEncGetInputFormatCount
// //   6  nvEncGetInputFormats
// //   7  nvEncGetEncodeCaps          ← we use this
// //  ...
// //  27  nvEncDestroyEncoder         ← we use this
// //  28  nvEncInvalidateRefFrames
// //  29  nvEncOpenEncodeSessionEx    ← we use this
// #define MM_NV_ENC_FN_SLOT_GET_CAPS    7
// #define MM_NV_ENC_FN_SLOT_OPEN_EX    29
// #define MM_NV_ENC_FN_SLOT_DESTROY    27
// #define MM_NV_ENC_FN_TOTAL           64
//
// typedef struct {
//     uint32_t version;
//     uint32_t reserved;
//     void    *fn[MM_NV_ENC_FN_TOTAL];
//     uint32_t reserved2[64];
// } mm_nv_encode_api_fn_list;
//
// // Function pointer typedefs for the three calls we make.
// typedef uint32_t (*mm_pfn_nvEncOpenEncodeSessionEx)(mm_nv_enc_open_session_params *, void **);
// typedef uint32_t (*mm_pfn_nvEncGetEncodeCaps)(void *, mm_nv_enc_guid, mm_nv_enc_caps_param *, int *);
// typedef uint32_t (*mm_pfn_nvEncDestroyEncoder)(void *);
//
// // Output record for one codec.
// typedef struct {
//     int supported;
//     int width_max, height_max;
//     int width_min, height_min;
//     int mb_per_sec_max;
//     int num_engines;
//     int level_max, level_min;
//     int num_bframes_max;
//     int support_10bit;
//     int support_yuv444;
//     int support_lossless;
//     int support_lookahead;
//     int support_temporal_aq;
//     int support_weighted_pred;
//     int support_bframe_ref;
// } mm_nvenc_codec_caps;
//
// // mm_nvenc_query_caps: open nvenc, query caps for H.264 / HEVC / AV1.
// // Returns number of codecs for which caps were written (0-3), or -1 on error.
// // out must point to an array of at least 3 mm_nvenc_codec_caps.
// static int mm_nvenc_query_caps(AVBufferRef *dev_ref, mm_nvenc_codec_caps *out) {
//     mm_nve_CUcontext cuda_ctx = mm_nve_get_cuda_ctx(dev_ref);
//     if (!cuda_ctx) return -1;
//
//     // Load NVENC library.
//     mm_nve_dl_t lib;
// #ifdef _WIN32
//     lib = mm_nve_dlopen("nvEncodeAPI64.dll");
//     if (!lib) lib = mm_nve_dlopen("nvEncodeAPI.dll");
// #else
//     lib = mm_nve_dlopen("libnvidia-encode.so.1");
//     if (!lib) lib = mm_nve_dlopen("libnvidia-encode.so");
// #endif
//     if (!lib) return 0; // NVENC not installed — not an error
//
//     typedef uint32_t (*pfnCreateInstance)(mm_nv_encode_api_fn_list *);
//     pfnCreateInstance createInstance =
//         (pfnCreateInstance)mm_nve_dlsym(lib, "NvEncodeAPICreateInstance");
//     if (!createInstance) { mm_nve_dlclose(lib); return 0; }
//
//     // Initialise function list.
//     mm_nv_encode_api_fn_list fnList;
//     memset(&fnList, 0, sizeof(fnList));
//     fnList.version = MM_NV_ENC_FN_LIST_VER;
//     if (createInstance(&fnList) != 0) { mm_nve_dlclose(lib); return 0; }
//
//     mm_pfn_nvEncOpenEncodeSessionEx openEx =
//         (mm_pfn_nvEncOpenEncodeSessionEx)fnList.fn[MM_NV_ENC_FN_SLOT_OPEN_EX];
//     mm_pfn_nvEncGetEncodeCaps getCaps =
//         (mm_pfn_nvEncGetEncodeCaps)fnList.fn[MM_NV_ENC_FN_SLOT_GET_CAPS];
//     mm_pfn_nvEncDestroyEncoder destroyEnc =
//         (mm_pfn_nvEncDestroyEncoder)fnList.fn[MM_NV_ENC_FN_SLOT_DESTROY];
//
//     if (!openEx || !getCaps || !destroyEnc) { mm_nve_dlclose(lib); return 0; }
//
//     // Open encode session with the CUDA context.
//     mm_nv_enc_open_session_params sp;
//     memset(&sp, 0, sizeof(sp));
//     sp.version    = MM_NV_ENC_OPEN_SESSION_EX_VER;
//     sp.deviceType = MM_NV_ENC_DEVICE_TYPE_CUDA;
//     sp.device     = cuda_ctx;
//     sp.apiVersion = MM_NVENCAPI_VER;
//
//     void *encoder = NULL;
//     if (openEx(&sp, &encoder) != 0 || !encoder) {
//         mm_nve_dlclose(lib); return 0;
//     }
//
//     // Per-codec queries.
//     static const mm_nv_enc_guid codec_guids[3] = {
//         {0x6bc82762, 0x4e63, 0x4ca4, {0xaa,0x85,0x1e,0x50,0xf3,0x21,0xf6,0xbf}}, // H.264
//         {0x790cdc88, 0x4522, 0x4d7b, {0x94,0x25,0xbd,0xa9,0x97,0x5f,0x76,0x03}}, // HEVC
//         {0x0a352289, 0x0aa7, 0x4759, {0x86,0x2d,0x5d,0x15,0xcd,0x16,0xd2,0x54}}, // AV1
//     };
//
//     int n_queried = 0;
//     for (int ci = 0; ci < 3; ci++) {
//         mm_nvenc_codec_caps *c = &out[ci];
//         memset(c, 0, sizeof(*c));
//
//         mm_nv_enc_caps_param cp;
//         int val = 0;
//
//         // Probe codec support by querying width_max; non-zero status → unsupported.
//         memset(&cp, 0, sizeof(cp));
//         cp.version = MM_NV_ENC_CAPS_PARAM_VER;
//         cp.capsToQuery = MM_NV_ENC_CAPS_WIDTH_MAX;
//         if (getCaps(encoder, codec_guids[ci], &cp, &val) != 0) continue;
//
//         c->supported  = 1;
//         c->width_max  = val;
//
// #define MM_QUERY(cap_id, field) do { \
//         memset(&cp, 0, sizeof(cp)); \
//         cp.version = MM_NV_ENC_CAPS_PARAM_VER; \
//         cp.capsToQuery = (cap_id); val = 0; \
//         getCaps(encoder, codec_guids[ci], &cp, &val); c->field = val; } while(0)
//
//         MM_QUERY(MM_NV_ENC_CAPS_HEIGHT_MAX,              height_max);
//         MM_QUERY(MM_NV_ENC_CAPS_WIDTH_MIN,               width_min);
//         MM_QUERY(MM_NV_ENC_CAPS_HEIGHT_MIN,              height_min);
//         MM_QUERY(MM_NV_ENC_CAPS_MB_PER_SEC_MAX,          mb_per_sec_max);
//         MM_QUERY(MM_NV_ENC_CAPS_NUM_ENCODER_ENGINES,     num_engines);
//         MM_QUERY(MM_NV_ENC_CAPS_LEVEL_MAX,               level_max);
//         MM_QUERY(MM_NV_ENC_CAPS_LEVEL_MIN,               level_min);
//         MM_QUERY(MM_NV_ENC_CAPS_NUM_MAX_BFRAMES,         num_bframes_max);
//         MM_QUERY(MM_NV_ENC_CAPS_SUPPORT_10BIT_ENCODE,    support_10bit);
//         MM_QUERY(MM_NV_ENC_CAPS_SUPPORT_YUV444_ENCODE,   support_yuv444);
//         MM_QUERY(MM_NV_ENC_CAPS_SUPPORT_LOSSLESS_ENCODE, support_lossless);
//         MM_QUERY(MM_NV_ENC_CAPS_SUPPORT_LOOKAHEAD,       support_lookahead);
//         MM_QUERY(MM_NV_ENC_CAPS_SUPPORT_TEMPORAL_AQ,     support_temporal_aq);
//         MM_QUERY(MM_NV_ENC_CAPS_SUPPORT_WEIGHTED_PREDICTION, support_weighted_pred);
//         MM_QUERY(MM_NV_ENC_CAPS_SUPPORT_BFRAME_REF_MODE, support_bframe_ref);
// #undef MM_QUERY
//
//         n_queried++;
//     }
//
//     destroyEnc(encoder);
//     mm_nve_dlclose(lib);
//     return n_queried;
// }
import "C"

import "unsafe"

// NVENCCodecCaps holds the runtime hardware capabilities for a single NVENC
// codec as returned by nvEncGetEncodeCaps.  All integer fields are zero when
// the capability could not be queried.
type NVENCCodecCaps struct {
	// CodecName is the FFmpeg codec name (e.g. "h264_nvenc").
	CodecName string `json:"codec_name"`

	// MaxWidth / MaxHeight / MinWidth / MinHeight are the encoder's pixel
	// dimension limits for this codec on this GPU.
	MaxWidth  int `json:"max_width"`
	MaxHeight int `json:"max_height"`
	MinWidth  int `json:"min_width"`
	MinHeight int `json:"min_height"`

	// MBPerSecMax is the theoretical maximum macroblock throughput per second
	// (NV_ENC_CAPS_MB_PER_SEC_MAX).  Divide by (width/16)*(height/16)*fps to
	// estimate the maximum frame rate at a given resolution.
	MBPerSecMax int `json:"mb_per_sec_max"`

	// NumEncoderEngines is the number of independent NVENC hardware engines on
	// this GPU.  Each engine can handle simultaneous encode sessions.
	NumEncoderEngines int `json:"num_encoder_engines"`

	// LevelMax / LevelMin are the codec level limits (e.g. H.264 level 5.2 is
	// reported as 52; HEVC level 6.2 as 186).
	LevelMax int `json:"level_max"`
	LevelMin int `json:"level_min"`

	// MaxBFrames is the maximum number of consecutive B-frames supported.
	MaxBFrames int `json:"max_bframes"`

	// Feature-support flags.
	Support10Bit        bool `json:"support_10bit"`
	SupportYUV444       bool `json:"support_yuv444"`
	SupportLossless     bool `json:"support_lossless"`
	SupportLookahead    bool `json:"support_lookahead"`
	SupportTemporalAQ   bool `json:"support_temporal_aq"`
	SupportWeightedPred bool `json:"support_weighted_pred"`
	SupportBFrameRef    bool `json:"support_bframe_ref"`
}

// nvencCodecNames maps CGo array index → FFmpeg codec name.
var nvencCodecNames = [3]string{"h264_nvenc", "hevc_nvenc", "av1_nvenc"}

// QueryNVENCCaps queries the NVENC encoder hardware capabilities for each
// supported codec (H.264, HEVC, AV1) on the CUDA device associated with d.
//
// Returns an empty slice (not an error) when the device is not a CUDA device,
// the NVENC library is not installed, or the GPU does not support NVENC.
func QueryNVENCCaps(d *HWDeviceContext) []NVENCCodecCaps {
	if d == nil || d.deviceType != HWDeviceCUDA {
		return nil
	}

	var raw [3]C.mm_nvenc_codec_caps
	n := int(C.mm_nvenc_query_caps(d.ref, (*C.mm_nvenc_codec_caps)(unsafe.Pointer(&raw[0]))))
	if n <= 0 {
		return nil
	}

	out := make([]NVENCCodecCaps, 0, n)
	for i := 0; i < 3; i++ {
		r := raw[i]
		if r.supported == 0 {
			continue
		}
		out = append(out, NVENCCodecCaps{
			CodecName:           nvencCodecNames[i],
			MaxWidth:            int(r.width_max),
			MaxHeight:           int(r.height_max),
			MinWidth:            int(r.width_min),
			MinHeight:           int(r.height_min),
			MBPerSecMax:         int(r.mb_per_sec_max),
			NumEncoderEngines:   int(r.num_engines),
			LevelMax:            int(r.level_max),
			LevelMin:            int(r.level_min),
			MaxBFrames:          int(r.num_bframes_max),
			Support10Bit:        r.support_10bit != 0,
			SupportYUV444:       r.support_yuv444 != 0,
			SupportLossless:     r.support_lossless != 0,
			SupportLookahead:    r.support_lookahead != 0,
			SupportTemporalAQ:   r.support_temporal_aq != 0,
			SupportWeightedPred: r.support_weighted_pred != 0,
			SupportBFrameRef:    r.support_bframe_ref != 0,
		})
	}
	return out
}
