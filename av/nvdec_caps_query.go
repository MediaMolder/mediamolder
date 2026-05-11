// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// Dynamic NVDEC decoder capability query.
//
// Loads libnvcuvid.so (Linux) or nvcuvid.dll (Windows) at runtime via dlopen
// and calls cuvidGetDecoderCaps for each codec × chroma-format × bit-depth
// combination.  The code compiles on every platform; on macOS or any system
// without the NVDEC library it returns an empty slice (not an error).
//
// No cuvid SDK headers are required at compile time.  All struct layouts and
// enum values are replicated inline from the public Video Codec SDK 12.1 ABI.
//
// Sources:
//   - NVIDIA Video Codec SDK 12.1 cuviddec.h
//   - https://github.com/FFmpeg/nv-codec-headers

// #cgo linux LDFLAGS: -ldl
//
// #include <stdint.h>
// #include <string.h>
// #include "libavutil/hwcontext.h"
//
// // ── Dynamic-loader shim ─────────────────────────────────────────────────────
// #ifdef _WIN32
// #  include <windows.h>
// typedef HMODULE mm_nvd_dl_t;
// static mm_nvd_dl_t  mm_nvd_dlopen(const char *n)              { return LoadLibraryA(n); }
// static void        *mm_nvd_dlsym(mm_nvd_dl_t h, const char *s){ return (void *)GetProcAddress(h, s); }
// static void         mm_nvd_dlclose(mm_nvd_dl_t h)              { FreeLibrary(h); }
// #else
// #  include <dlfcn.h>
// typedef void *mm_nvd_dl_t;
// static mm_nvd_dl_t  mm_nvd_dlopen(const char *n)              { return dlopen(n, RTLD_LAZY | RTLD_LOCAL); }
// static void        *mm_nvd_dlsym(mm_nvd_dl_t h, const char *s){ return dlsym(h, s); }
// static void         mm_nvd_dlclose(mm_nvd_dl_t h)              { dlclose(h); }
// #endif
//
// // ── Extract CUcontext from AVHWDeviceContext ───────────────────────────────
// typedef void *mm_nvd_CUcontext;
// static mm_nvd_CUcontext mm_nvd_get_cuda_ctx(AVBufferRef *ref) {
//     if (!ref || !ref->data) return NULL;
//     AVHWDeviceContext *dev = (AVHWDeviceContext *)ref->data;
//     if (dev->type != AV_HWDEVICE_TYPE_CUDA || !dev->hwctx) return NULL;
//     return *((mm_nvd_CUcontext *)dev->hwctx);
// }
//
// // ── CUVIDDECODECAPS (SDK 12.1 layout) ─────────────────────────────────────
// //
// // cudaVideoCodec enum (stable since SDK 8):
// //   0 MPEG1, 1 MPEG2, 2 MPEG4, 3 VC1, 4 H264, 5 JPEG, 6 H264_SVC,
// //   7 H264_MVC, 8 HEVC, 9 VP8, 10 VP9, 11 AV1
// //
// // cudaVideoChromaFormat enum:
// //   0 Monochrome, 1 420, 2 422, 3 444
// //
// // nBitDepthMinus8: 0 = 8-bit, 2 = 10-bit, 4 = 12-bit
// typedef struct {
//     uint32_t  eCodecType;          // IN: cudaVideoCodec
//     uint32_t  eChromaFormat;       // IN: cudaVideoChromaFormat
//     uint32_t  nBitDepthMinus8;     // IN: 0/2/4
//     uint32_t  reserved1[3];        // IN: zero
//     uint8_t   bIsSupported;        // OUT
//     uint8_t   nNumNVDECs;          // OUT: number of NVDEC engines
//     uint16_t  nOutputFormatMask;   // OUT: bitmask of supported output formats
//     uint32_t  nMaxWidth;           // OUT
//     uint32_t  nMaxHeight;          // OUT
//     uint32_t  nMaxMBCount;         // OUT: max macroblocks
//     uint16_t  nMinWidth;           // OUT
//     uint16_t  nMinHeight;          // OUT
//     uint8_t   bIsHistogramSupported; // OUT
//     uint8_t   nCounterBitDepth;    // OUT
//     uint16_t  nMaxHistogramBins;   // OUT
//     uint32_t  reserved3[10];       // OUT: zero
// } mm_CUVIDDECODECAPS;
//
// typedef uint32_t (*mm_pfn_cuvidGetDecoderCaps)(mm_CUVIDDECODECAPS *);
//
// // Output record written for each probed codec combination.
// typedef struct {
//     uint32_t codec;          // cudaVideoCodec index
//     uint32_t chroma_fmt;     // cudaVideoChromaFormat (0-3)
//     uint32_t bit_depth;      // actual bit depth (8/10/12)
//     uint8_t  is_supported;
//     uint8_t  num_nvdecs;
//     uint16_t output_fmt_mask;
//     uint32_t max_width, max_height, min_width, min_height;
//     uint32_t max_mb_count;
// } mm_nvdec_entry;
//
// // Number of codec × chroma × bit-depth combinations we probe.
// // We probe codecs 0-11 × chroma 420/422/444 × bit_depth 8/10/12.
// // Max entries = 12 * 3 * 3 = 108, but we skip 422/444 for codecs that
// // never support it.  Allocate for the worst case.
// #define MM_NVDEC_MAX_ENTRIES 108
//
// // mm_nvdec_query_caps: query NVDEC caps for all codec/chroma/bitdepth combos.
// // Returns number of entries written to out_entries, or -1 on error.
// // out_entries must hold at least MM_NVDEC_MAX_ENTRIES.
// static int mm_nvdec_query_caps(AVBufferRef *dev_ref,
//                                mm_nvdec_entry *out_entries) {
//     mm_nvd_CUcontext cuda_ctx = mm_nvd_get_cuda_ctx(dev_ref);
//     if (!cuda_ctx) return -1;
//
//     mm_nvd_dl_t lib;
// #ifdef _WIN32
//     lib = mm_nvd_dlopen("nvcuvid.dll");
// #else
//     lib = mm_nvd_dlopen("libnvcuvid.so.1");
//     if (!lib) lib = mm_nvd_dlopen("libnvcuvid.so");
// #endif
//     if (!lib) return 0; // NVDEC not installed — not an error
//
//     mm_pfn_cuvidGetDecoderCaps getCaps =
//         (mm_pfn_cuvidGetDecoderCaps)mm_nvd_dlsym(lib, "cuvidGetDecoderCaps");
//     if (!getCaps) { mm_nvd_dlclose(lib); return 0; }
//
//     // cudaVideoCodec indices and their names (up to AV1 = 11).
//     static const uint32_t codecs[]  = {4, 8, 10, 11, 0, 1, 2, 3, 5, 9};
//     // cudaVideoChromaFormat: 1=420, 2=422, 3=444
//     static const uint32_t chromas[] = {1, 2, 3};
//     // bit depth (nBitDepthMinus8): 0→8, 2→10, 4→12
//     static const uint32_t bds[]     = {0, 2, 4};
//
//     int n = 0;
//     for (int ci = 0; ci < (int)(sizeof(codecs)/sizeof(codecs[0])); ci++) {
//         for (int chi = 0; chi < 3; chi++) {
//             for (int bdi = 0; bdi < 3; bdi++) {
//                 if (n >= MM_NVDEC_MAX_ENTRIES) goto done;
//                 mm_CUVIDDECODECAPS caps;
//                 memset(&caps, 0, sizeof(caps));
//                 caps.eCodecType      = codecs[ci];
//                 caps.eChromaFormat   = chromas[chi];
//                 caps.nBitDepthMinus8 = bds[bdi];
//                 if (getCaps(&caps) != 0) continue;
//                 if (!caps.bIsSupported) continue;
//
//                 mm_nvdec_entry *e = &out_entries[n++];
//                 e->codec        = codecs[ci];
//                 e->chroma_fmt   = chromas[chi];
//                 e->bit_depth    = (uint32_t)(8 + bds[bdi]);
//                 e->is_supported = caps.bIsSupported;
//                 e->num_nvdecs   = caps.nNumNVDECs;
//                 e->output_fmt_mask = caps.nOutputFormatMask;
//                 e->max_width    = caps.nMaxWidth;
//                 e->max_height   = caps.nMaxHeight;
//                 e->min_width    = caps.nMinWidth;
//                 e->min_height   = caps.nMinHeight;
//                 e->max_mb_count = caps.nMaxMBCount;
//             }
//         }
//     }
// done:
//     mm_nvd_dlclose(lib);
//     return n;
// }
import "C"

import "unsafe"

// NVDECCodecCaps holds the runtime hardware decoder capabilities for one
// codec × chroma-format × bit-depth combination as returned by
// cuvidGetDecoderCaps.
type NVDECCodecCaps struct {
	// CodecName is the FFmpeg codec name (e.g. "h264_cuvid").
	CodecName string `json:"codec_name"`

	// ChromaFmt is the chroma format: "yuv420", "yuv422", or "yuv444".
	ChromaFmt string `json:"chroma_fmt"`

	// BitDepth is the decoded bit depth: 8, 10, or 12.
	BitDepth int `json:"bit_depth"`

	// NumNVDECs is the number of dedicated NVDEC hardware engines that can
	// service this codec/format combination simultaneously.
	NumNVDECs int `json:"num_nvdecs"`

	// OutputFormatMask is the raw bitmask of supported output pixel formats
	// (see cuvidDecodeStatus for bit definitions).
	OutputFormatMask uint16 `json:"output_format_mask"`

	// MaxWidth / MaxHeight / MinWidth / MinHeight are pixel dimension limits.
	MaxWidth  int `json:"max_width"`
	MaxHeight int `json:"max_height"`
	MinWidth  int `json:"min_width"`
	MinHeight int `json:"min_height"`

	// MaxMBCount is the maximum macroblock count per frame.
	MaxMBCount int `json:"max_mb_count"`
}

// nvdecCodecFFmpegName maps cudaVideoCodec index to FFmpeg cuvid codec name.
var nvdecCodecFFmpegName = map[uint32]string{
	0:  "mpeg1video_cuvid",
	1:  "mpeg2_cuvid",
	2:  "mpeg4_cuvid",
	3:  "vc1_cuvid",
	4:  "h264_cuvid",
	5:  "mjpeg_cuvid",
	8:  "hevc_cuvid",
	9:  "vp8_cuvid",
	10: "vp9_cuvid",
	11: "av1_cuvid",
}

// nvdecChromaName maps cudaVideoChromaFormat to human-readable string.
var nvdecChromaName = map[uint32]string{
	1: "yuv420",
	2: "yuv422",
	3: "yuv444",
}

// QueryNVDECCaps queries the NVDEC hardware decoder capabilities for every
// supported codec × chroma-format × bit-depth combination on the CUDA device
// associated with d.
//
// Returns an empty slice (not an error) when the device is not a CUDA device,
// the NVDEC library is not installed, or the GPU does not support NVDEC.
func QueryNVDECCaps(d *HWDeviceContext) []NVDECCodecCaps {
	if d == nil || d.deviceType != HWDeviceCUDA {
		return nil
	}

	var raw [108]C.mm_nvdec_entry
	n := int(C.mm_nvdec_query_caps(d.ref, (*C.mm_nvdec_entry)(unsafe.Pointer(&raw[0]))))
	if n <= 0 {
		return nil
	}

	out := make([]NVDECCodecCaps, 0, n)
	for i := 0; i < n; i++ {
		e := raw[i]
		codecName, ok := nvdecCodecFFmpegName[uint32(e.codec)]
		if !ok {
			codecName = "unknown_cuvid"
		}
		chromaFmt, ok := nvdecChromaName[uint32(e.chroma_fmt)]
		if !ok {
			chromaFmt = "unknown"
		}
		out = append(out, NVDECCodecCaps{
			CodecName:        codecName,
			ChromaFmt:        chromaFmt,
			BitDepth:         int(e.bit_depth),
			NumNVDECs:        int(e.num_nvdecs),
			OutputFormatMask: uint16(e.output_fmt_mask),
			MaxWidth:         int(e.max_width),
			MaxHeight:        int(e.max_height),
			MinWidth:         int(e.min_width),
			MinHeight:        int(e.min_height),
			MaxMBCount:       int(e.max_mb_count),
		})
	}
	return out
}
