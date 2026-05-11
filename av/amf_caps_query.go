// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build linux

package av

// Standalone AMD AMF encoder capability query.
//
// Loads libamfrt64.so.1 via dlopen at runtime and calls the AMF factory API
// to query per-codec encoder capabilities (max resolution, max simultaneous
// streams). No AMF SDK headers are required at compile time.
//
// Vtable layout is based on the public AMF SDK 1.4.36 (Apache 2.0):
//   https://github.com/GPUOpen-LibrariesAndSDKs/AMF
//
// AMFFactory vtable (no AMFInterface base):
//   [0] CreateContext(factory, &context) → AMF_OK
//   [1] CreateComponent(factory, ctx, wchar_t* id, &component) → AMF_OK
//   [2] GetTrace, [3] GetDebug
//
// AMFPropertyStorageEx vtable (base for all components): 16 entries (0–15)
//   AMFInterface (0–2): Acquire, Release, QueryInterface
//   AMFPropertyStorage (3–12): SetProperty…RemoveObserver
//   AMFPropertyStorageEx (13–15): GetPropertyInfo, GetPropertiesInfoByInterface,
//                                  ValidateProperty
//
// AMFComponent own methods start at index 16:
//   [16] Init, [17] ReInit, [18] Terminate, [19] Flush, [20] Drain, [21] Reset
//   [22] SubmitInput, [23] QueryOutput, [24] GetContext
//   [25] SetOutputDataAllocatorCB
//   [26] GetCaps(component, &caps) ← used below
//   [27] Optimize
//
// AMFCaps vtable (inherits AMFInterface 0–2):
//   [3] GetInputCaps(caps, &ioCaps)
//   [4] GetOutputCaps(caps, &ioCaps)
//
// AMFEncoderCaps extends AMFCaps:
//   [5] GetMaxNumOfStreams(encCaps, &n)
//
// AMFIOCaps vtable (inherits AMFInterface 0–2):
//   [3] GetWidthRange(ioCaps, &minW, &maxW)
//   [4] GetHeightRange(ioCaps, &minH, &maxH)
//
// AMFContext vtable (inherits AMFPropertyStorageEx 0–15):
//   [16] Terminate, [17] InitDX9, [18] GetDX9Device, [19] LockDX9, [20] UnlockDX9
//   [21] InitDX11, [22] GetDX11Device, [23] LockDX11, [24] UnlockDX11
//   [25] InitOpenCL(context, commandQueue)   ← used below (ignored on failure)
//
// AMFInit function (exported from libamfrt64.so.1):
//   AMF_RESULT AMFInit(uint64_t version, AMFFactory** ppFactory)
//   version major must match runtime major (use major=1, minor/release=0).

// #cgo linux LDFLAGS: -ldl
//
// #include <stdint.h>
// #include <wchar.h>
// #include <dlfcn.h>
//
// // AMF result code — 0 = AMF_OK.
// #define MM_AMF_OK 0
//
// // Minimum compatible AMF version: major=1. Any 1.x runtime accepts this.
// #define MM_AMF_COMPAT_VERSION ((uint64_t)1 << 48)
//
// // Helpers: get vtable pointer and cast a specific slot.
// #define MM_AMF_VTL(obj)    (*(void***)(obj))
// #define MM_AMF_VTL_FN(obj, n) (MM_AMF_VTL(obj)[(n)])
//
// // Release any AMF interface object (vtable index 1).
// static void mm_amf_release(void* obj) {
//     if (!obj) return;
//     typedef long (*rel_fn)(void*);
//     ((rel_fn)MM_AMF_VTL_FN(obj, 1))(obj);
// }
//
// // Component IDs used to create encoders.
// static const wchar_t mm_amf_avc_id[]  = L"AMFVideoEncoderVCE_AVC";
// static const wchar_t mm_amf_hevc_id[] = L"AMFVideoEncoder_HEVC";
// static const wchar_t mm_amf_av1_id[]  = L"AMFVideoEncoder_AV1";
//
// typedef struct {
//     int valid;
//     int max_streams;
//     int min_width,  max_width;
//     int min_height, max_height;
// } mm_amf_codec_caps;
//
// // Query caps for a single encoder component. Returns 0 on success.
// static int mm_amf_query_one(void* factory, void* context,
//                             const wchar_t* id, mm_amf_codec_caps* out) {
//     typedef int (*create_comp_fn)(void*, void*, const wchar_t*, void**);
//     void* component = NULL;
//     int res = ((create_comp_fn)MM_AMF_VTL_FN(factory, 1))(
//                 factory, context, id, &component);
//     if (res != MM_AMF_OK || !component) return -1;
//
//     typedef int (*get_caps_fn)(void*, void**);
//     void* caps = NULL;
//     res = ((get_caps_fn)MM_AMF_VTL_FN(component, 26))(component, &caps);
//     if (res != MM_AMF_OK || !caps) {
//         typedef int (*terminate_fn)(void*);
//         ((terminate_fn)MM_AMF_VTL_FN(component, 18))(component);
//         mm_amf_release(component);
//         return -1;
//     }
//
//     // GetMaxNumOfStreams (AMFEncoderCaps vtable index 5).
//     typedef int (*get_streams_fn)(void*, int*);
//     int streams = 0;
//     ((get_streams_fn)MM_AMF_VTL_FN(caps, 5))(caps, &streams);
//     out->max_streams = streams;
//
//     // GetInputCaps (AMFCaps vtable index 3).
//     typedef int (*get_input_caps_fn)(void*, void**);
//     void* input_caps = NULL;
//     res = ((get_input_caps_fn)MM_AMF_VTL_FN(caps, 3))(caps, &input_caps);
//     if (res == MM_AMF_OK && input_caps) {
//         typedef int (*get_range_fn)(void*, int*, int*);
//         int minW = 0, maxW = 0, minH = 0, maxH = 0;
//         ((get_range_fn)MM_AMF_VTL_FN(input_caps, 3))(input_caps, &minW, &maxW);
//         ((get_range_fn)MM_AMF_VTL_FN(input_caps, 4))(input_caps, &minH, &maxH);
//         out->min_width  = minW;
//         out->max_width  = maxW;
//         out->min_height = minH;
//         out->max_height = maxH;
//         mm_amf_release(input_caps);
//     }
//
//     mm_amf_release(caps);
//     typedef int (*terminate_fn)(void*);
//     ((terminate_fn)MM_AMF_VTL_FN(component, 18))(component);
//     mm_amf_release(component);
//     out->valid = 1;
//     return 0;
// }
//
// // Main AMF probe. Loads libamfrt64.so.1, initialises a context, queries
// // H.264 / HEVC / AV1 encoder caps, releases everything, and unloads the
// // library. Writes results into out[0..2]; returns the number of codecs that
// // were successfully queried (may be < 3 if a codec is unsupported).
// // Returns 0 when the AMF runtime is not available.
// static int mm_amf_probe(mm_amf_codec_caps out[3]) {
//     void* lib = dlopen("libamfrt64.so.1", RTLD_LAZY | RTLD_LOCAL);
//     if (!lib) return 0;
//
//     typedef int (*amf_init_fn)(uint64_t, void**);
//     amf_init_fn amf_init = (amf_init_fn)dlsym(lib, "AMFInit");
//     if (!amf_init) { dlclose(lib); return 0; }
//
//     void* factory = NULL;
//     if (amf_init(MM_AMF_COMPAT_VERSION, &factory) != MM_AMF_OK || !factory) {
//         dlclose(lib);
//         return 0;
//     }
//
//     // CreateContext (factory vtable index 0).
//     typedef int (*create_ctx_fn)(void*, void**);
//     void* context = NULL;
//     if (((create_ctx_fn)MM_AMF_VTL_FN(factory, 0))(factory, &context)
//             != MM_AMF_OK || !context) {
//         dlclose(lib);
//         return 0;
//     }
//
//     // InitOpenCL(NULL) — context vtable index 25.
//     // AMF on Linux silently ignores failure; we do the same.
//     typedef int (*init_opencl_fn)(void*, void*);
//     ((init_opencl_fn)MM_AMF_VTL_FN(context, 25))(context, NULL);
//
//     static const wchar_t* ids[3] = {
//         mm_amf_avc_id, mm_amf_hevc_id, mm_amf_av1_id
//     };
//     int n = 0;
//     for (int i = 0; i < 3; i++) {
//         if (mm_amf_query_one(factory, context, ids[i], &out[i]) == 0) n++;
//     }
//
//     // Release context (vtable index 1 = Release, but AMFContext needs
//     // Terminate first — vtable index 16).
//     typedef int (*terminate_fn)(void*);
//     ((terminate_fn)MM_AMF_VTL_FN(context, 16))(context);
//     mm_amf_release(context);
//
//     dlclose(lib);
//     return n;
// }
import "C"

import "unsafe"

// AMFCodecCaps holds AMD AMF encoder capability data for one codec, queried
// from libamfrt64.so.1 via a standalone dlopen probe (no SDK headers required).
type AMFCodecCaps struct {
	// CodecName is the FFmpeg codec name (e.g. "h264_amf", "hevc_amf").
	CodecName string `json:"codec_name"`

	// MaxNumOfStreams is the maximum number of simultaneous encode sessions
	// the AMF runtime permits for this codec on the current GPU.
	MaxNumOfStreams int `json:"max_num_of_streams"`

	// MinWidth / MaxWidth / MinHeight / MaxHeight are input frame dimension
	// limits reported by AMFIOCaps::GetWidthRange and GetHeightRange.
	MinWidth  int `json:"min_width"`
	MaxWidth  int `json:"max_width"`
	MinHeight int `json:"min_height"`
	MaxHeight int `json:"max_height"`
}

// amfCodecNames maps C array index → FFmpeg codec name.
var amfCodecNames = [3]string{"h264_amf", "hevc_amf", "av1_amf"}

// QueryAMFCaps performs a standalone AMF capability probe by loading
// libamfrt64.so.1 at runtime.  It does not use an open HWDeviceContext —
// the library itself is sufficient.
//
// Returns nil when libamfrt64.so.1 is not present or AMFInit fails (i.e.
// no AMD GPU / AMF runtime available).
func QueryAMFCaps() []AMFCodecCaps {
	var raw [3]C.mm_amf_codec_caps
	n := int(C.mm_amf_probe((*C.mm_amf_codec_caps)(unsafe.Pointer(&raw[0]))))
	if n == 0 {
		return nil
	}

	out := make([]AMFCodecCaps, 0, n)
	for i := 0; i < 3; i++ {
		if raw[i].valid == 0 {
			continue
		}
		out = append(out, AMFCodecCaps{
			CodecName:      amfCodecNames[i],
			MaxNumOfStreams: int(raw[i].max_streams),
			MinWidth:        int(raw[i].min_width),
			MaxWidth:        int(raw[i].max_width),
			MinHeight:       int(raw[i].min_height),
			MaxHeight:       int(raw[i].max_height),
		})
	}
	return out
}
