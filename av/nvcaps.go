// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// NVIDIA per-GPU codec capability filtering.
//
// The strategy avoids any compile-time dependency on the CUDA SDK:
//   - The CUDA context pointer is extracted directly from AVHWDeviceContext.hwctx
//     by reading its first pointer-sized field (always CUcontext in
//     AVCUDADeviceContext, which is defined in libavutil/hwcontext_cuda.h).
//   - cuCtxGetDevice + cuDeviceGetAttribute are resolved at runtime via
//     dlopen/LoadLibrary so that the code compiles and links on macOS and
//     Windows builds that do not have the CUDA SDK installed.
//   - If the dynamic probe fails (CUDA unavailable, macOS, etc.) the Go layer
//     falls back gracefully to the unfiltered static codec list.

// #cgo linux LDFLAGS: -ldl
//
// #include "libavutil/hwcontext.h"
//
// // ── Minimal CUDA driver-API types ─────────────────────────────────────
// // CUcontext / CUstream are opaque handle types (pointers in all CUDA
// // versions). Treat them as void* to avoid including cuda.h.
// typedef void *mm_CUcontext;
// typedef int   mm_CUdevice;
// typedef int   mm_CUresult;
// // Stable ABI attribute indices from cuda.h (unchanged since CUDA 2.0).
// #define MM_CU_CC_MAJOR 75
// #define MM_CU_CC_MINOR 76
//
// // ── Dynamic-loader shim ───────────────────────────────────────────────
// #ifdef _WIN32
// #  include <windows.h>
// typedef HMODULE mm_dl_t;
// static mm_dl_t  mm_dlopen(const char *n) { return LoadLibraryA(n); }
// static void    *mm_dlsym(mm_dl_t h, const char *s) { return (void *)GetProcAddress(h, s); }
// static void     mm_dlclose(mm_dl_t h)              { FreeLibrary(h); }
// #else
// #  include <dlfcn.h>
// typedef void *mm_dl_t;
// static mm_dl_t  mm_dlopen(const char *n) { return dlopen(n, RTLD_LAZY | RTLD_LOCAL); }
// static void    *mm_dlsym(mm_dl_t h, const char *s) { return dlsym(h, s); }
// static void     mm_dlclose(mm_dl_t h)              { dlclose(h); }
// #endif
//
// // ── Extract CUcontext from an open CUDA AVHWDeviceContext ─────────────
// // AVCUDADeviceContext (hwcontext_cuda.h) layout:
// //   { CUcontext cuda_ctx; CUstream stream; }
// // Both fields are pointer-sized. Reading *(void**)hwctx gives cuda_ctx
// // without pulling in hwcontext_cuda.h (which drags in cuda.h).
// static mm_CUcontext mm_get_cuda_ctx(AVBufferRef *ref) {
//     if (!ref || !ref->data) return NULL;
//     AVHWDeviceContext *dev = (AVHWDeviceContext *)ref->data;
//     if (dev->type != AV_HWDEVICE_TYPE_CUDA || !dev->hwctx) return NULL;
//     return *((mm_CUcontext *)dev->hwctx);
// }
//
// // ── Shared helper: open CUDA driver lib and get device from context ───
// // Fills *dev_out with the CUdevice for the context in *ref.
// // Returns a live mm_dl_t on success (caller must mm_dlclose it),
// // or NULL on failure.
// static mm_dl_t mm_cuda_open_get_device(AVBufferRef *ref,
//                                         mm_CUdevice *dev_out) {
//     mm_CUcontext ctx = mm_get_cuda_ctx(ref);
//     if (!ctx) return NULL;
//
//     mm_dl_t lib;
// #ifdef _WIN32
//     lib = mm_dlopen("nvcuda.dll");
// #else
//     lib = mm_dlopen("libcuda.so.1");
//     if (!lib) lib = mm_dlopen("libcuda.so");
// #endif
//     if (!lib) return NULL;
//
//     typedef mm_CUresult (*pfnPush)(mm_CUcontext);
//     typedef mm_CUresult (*pfnGetDev)(mm_CUdevice *);
//     typedef mm_CUresult (*pfnPop)(mm_CUcontext *);
//
//     pfnPush   push   = (pfnPush)  mm_dlsym(lib, "cuCtxPushCurrent_v2");
//     pfnGetDev getDev = (pfnGetDev)mm_dlsym(lib, "cuCtxGetDevice");
//     pfnPop    pop    = (pfnPop)   mm_dlsym(lib, "cuCtxPopCurrent_v2");
//
//     if (!getDev) { mm_dlclose(lib); return NULL; }
//
//     if (push) push(ctx);
//     mm_CUresult r = getDev(dev_out);
//     if (pop) pop(NULL);
//
//     if (r != 0) { mm_dlclose(lib); return NULL; }
//     return lib;
// }
//
// // ── Query SM (compute capability) major.minor ─────────────────────────
// // Returns 0 on success, -1 when CUDA driver is unavailable or any
// // query fails.  Thread-safe (dlopen/dlclose are per-call; the CUDA
// // context is already open so no expensive initialisation occurs).
// static int mm_cuda_sm_version(AVBufferRef *ref, int *maj, int *min) {
//     mm_CUdevice dev = 0;
//     mm_dl_t lib = mm_cuda_open_get_device(ref, &dev);
//     if (!lib) return -1;
//
//     typedef mm_CUresult (*pfnGetAttr)(int *, int, mm_CUdevice);
//     pfnGetAttr getAttr = (pfnGetAttr)mm_dlsym(lib, "cuDeviceGetAttribute");
//     int ret = -1;
//     if (getAttr) {
//         int a = 0, b = 0;
//         if (getAttr(&a, MM_CU_CC_MAJOR, dev) == 0 &&
//             getAttr(&b, MM_CU_CC_MINOR, dev) == 0) {
//             *maj = a; *min = b; ret = 0;
//         }
//     }
//     mm_dlclose(lib);
//     return ret;
// }
//
// // ── Query GPU marketing name via cuDeviceGetName ───────────────────────
// // Writes a NUL-terminated string into buf[0..len-1].
// // Returns 0 on success, -1 on failure.
// static int mm_cuda_device_name(AVBufferRef *ref, char *buf, int len) {
//     mm_CUdevice dev = 0;
//     mm_dl_t lib = mm_cuda_open_get_device(ref, &dev);
//     if (!lib) return -1;
//
//     typedef mm_CUresult (*pfnGetName)(char *, int, mm_CUdevice);
//     pfnGetName getName = (pfnGetName)mm_dlsym(lib, "cuDeviceGetName");
//     int ret = -1;
//     if (getName && getName(buf, len, dev) == 0) ret = 0;
//     mm_dlclose(lib);
//     return ret;
// }
import "C"

import (
	"fmt"
	"strings"
	"unsafe"
)

// queryCUDASMVersion returns the CUDA compute capability (SM major, minor)
// by dlopen-ing the CUDA driver at runtime against the already-open device
// context.  Returns an error when CUDA is unavailable (e.g. macOS) or the
// context is not a CUDA device.
func queryCUDASMVersion(d *HWDeviceContext) (int, int, error) {
	var maj, min C.int
	if C.mm_cuda_sm_version(d.ref, &maj, &min) != 0 {
		return 0, 0, fmt.Errorf("CUDA SM version query unavailable")
	}
	return int(maj), int(min), nil
}

// queryCUDADisplayName returns the marketing name of the GPU associated with
// the open CUDA device context (e.g. "NVIDIA GeForce RTX 3060 Ti").
// Returns an error when CUDA is unavailable or the context is not CUDA.
func queryCUDADisplayName(d *HWDeviceContext) (string, error) {
	const bufLen = 256
	buf := make([]byte, bufLen)
	cbuf := (*C.char)(unsafe.Pointer(&buf[0]))
	if C.mm_cuda_device_name(d.ref, cbuf, C.int(bufLen)) != 0 {
		return "", fmt.Errorf("CUDA device name query unavailable")
	}
	name := strings.TrimRight(C.GoString(cbuf), "\x00")
	if name == "" {
		return "", fmt.Errorf("empty CUDA device name")
	}
	return name, nil
}

// nvidiaArchName maps a compute capability SM version to the NVIDIA
// architecture marketing name.  Returns an empty string for unknown versions.
func nvidiaArchName(major, minor int) string {
	switch {
	case smGE(major, minor, 10, 0):
		return "Blackwell"
	case smGE(major, minor, 9, 0):
		return "Hopper"
	case smGE(major, minor, 8, 9):
		return "Ada Lovelace"
	case smGE(major, minor, 8, 0):
		return "Ampere"
	case major == 7 && minor == 5:
		return "Turing"
	case major == 7:
		return "Volta"
	case major == 6:
		return "Pascal"
	case major == 5:
		return "Maxwell"
	case major == 3:
		return "Kepler"
	case major == 2:
		return "Fermi"
	default:
		return ""
	}
}

// smGE reports whether compute capability (major, minor) is ≥ (reqMajor, reqMinor).
func smGE(major, minor, reqMajor, reqMinor int) bool {
	return major > reqMajor || (major == reqMajor && minor >= reqMinor)
}

// nvCapEntry records the minimum SM version for an NVENC/NVDEC codec and an
// optional note function for cases where the codec is supported but with
// profile or feature gaps at lower SM versions.
type nvCapEntry struct {
	name               string
	minMajor, minMinor int
	// note returns "" when there are no limitations at the given SM version,
	// or a short sentence describing what features are unavailable.
	// nil means no limitations at any SM version where the codec is supported.
	note func(major, minor int) string
}

// nvCaps is the static NVENC/NVDEC per-SM capability table.
//
// Sources:
//   - NVIDIA Video Codec SDK programmer's guide
//   - https://developer.nvidia.com/video-encode-and-decode-gpu-support-matrix-new
var nvCaps = []nvCapEntry{
	// ── NVENC encoders ────────────────────────────────────────────────────
	{
		name:     "h264_nvenc",
		minMajor: 3, minMinor: 0, // Kepler+
		note: func(maj, min int) string {
			if smGE(maj, min, 7, 5) {
				return ""
			}
			return "B-frame encode requires Turing (SM 7.5+)"
		},
	},
	{
		name:     "hevc_nvenc",
		minMajor: 5, minMinor: 0, // Maxwell+
		note: func(maj, min int) string {
			if smGE(maj, min, 7, 5) {
				return ""
			}
			return "4:2:2 and 4:4:4 profiles require Turing (SM 7.5+)"
		},
	},
	{
		name:     "av1_nvenc",
		minMajor: 8, minMinor: 9, // Ada Lovelace+
	},
	// ── NVDEC decoders (cuvid) ────────────────────────────────────────────
	{name: "h264_cuvid", minMajor: 3, minMinor: 0},  // Kepler+
	{name: "mpeg2_cuvid", minMajor: 3, minMinor: 0}, // Kepler+
	{name: "mpeg4_cuvid", minMajor: 5, minMinor: 0}, // Maxwell+
	{name: "vc1_cuvid", minMajor: 3, minMinor: 0},   // Kepler+
	{name: "mjpeg_cuvid", minMajor: 5, minMinor: 0}, // Maxwell+
	{name: "vp8_cuvid", minMajor: 5, minMinor: 0},   // Maxwell+
	{name: "mpeg1video_cuvid", minMajor: 3, minMinor: 0},
	{
		name:     "hevc_cuvid",
		minMajor: 5, minMinor: 0, // Maxwell+
		note: func(maj, min int) string {
			if smGE(maj, min, 7, 5) {
				return ""
			}
			return "4:2:2 decode requires Turing (SM 7.5+)"
		},
	},
	{name: "vp9_cuvid", minMajor: 6, minMinor: 0}, // Pascal+
	{name: "av1_cuvid", minMajor: 8, minMinor: 0}, // Ampere+
}

// nvCapsIndex maps codec name → *nvCapEntry for O(1) lookup.
var nvCapsIndex map[string]*nvCapEntry

func init() {
	nvCapsIndex = make(map[string]*nvCapEntry, len(nvCaps))
	for i := range nvCaps {
		nvCapsIndex[nvCaps[i].name] = &nvCaps[i]
	}
}

// FilterNVIDIACodecs removes CUDA codecs that are unsupported by the GPU at
// the given compute capability and annotates those that have profile or
// feature limitations at this SM version.
//
// Codecs absent from nvCaps are passed through unmodified (forward-compat:
// unknown future codec, assume supported).
func FilterNVIDIACodecs(smMajor, smMinor int, all []HWCodecInfo) []HWCodecInfo {
	out := make([]HWCodecInfo, 0, len(all))
	for _, c := range all {
		entry, known := nvCapsIndex[c.Name]
		if known {
			if !smGE(smMajor, smMinor, entry.minMajor, entry.minMinor) {
				continue // GPU too old for this codec; omit
			}
			if entry.note != nil {
				c.Note = entry.note(smMajor, smMinor)
			}
		}
		out = append(out, c)
	}
	return out
}
