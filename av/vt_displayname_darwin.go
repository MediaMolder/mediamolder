// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build darwin

package av

// queryVTDisplayName returns a human-readable name for the GPU powering Apple
// VideoToolbox.  It queries the IORegistry for the first AGXAccelerator service
// entry (Apple Silicon) and falls back to the Metal device name string if the
// IORegistry path is unavailable.  A final fallback of "Apple GPU" is returned
// when nothing succeeds.

// #cgo LDFLAGS: -framework IOKit -framework CoreFoundation
//
// #include <IOKit/IOKitLib.h>
// #include <CoreFoundation/CoreFoundation.h>
// #include <string.h>
//
// // mm_vt_gpu_name queries the IORegistry for the AGXAccelerator service and
// // returns its "model" property string into buf[0..len-1].
// // Returns 0 on success, -1 on failure (buf is unchanged).
// static int mm_vt_gpu_name(char *buf, int len) {
//     io_iterator_t iter = 0;
//     kern_return_t kr = IOServiceGetMatchingServices(
//         kIOMainPortDefault,
//         IOServiceMatching("AGXAccelerator"),
//         &iter);
//     if (kr != KERN_SUCCESS) return -1;
//
//     int found = -1;
//     io_service_t svc;
//     while ((svc = IOIteratorNext(iter)) != IO_OBJECT_NULL) {
//         if (found == 0) { IOObjectRelease(svc); continue; } // drain
//         CFStringRef model = (CFStringRef)IORegistryEntryCreateCFProperty(
//             svc, CFSTR("model"), kCFAllocatorDefault, 0);
//         if (model) {
//             if (CFStringGetCString(model, buf, len, kCFStringEncodingUTF8))
//                 found = 0;
//             CFRelease(model);
//         }
//         IOObjectRelease(svc);
//     }
//     IOObjectRelease(iter);
//     return found;
// }
import "C"

import "unsafe"

// queryVTDisplayName returns the marketing name of the Apple GPU used by
// VideoToolbox (e.g. "Apple M3 Pro").  Falls back to "Apple GPU" on failure.
func queryVTDisplayName() string {
	const bufLen = 256
	buf := make([]byte, bufLen)
	cbuf := (*C.char)(unsafe.Pointer(&buf[0]))
	if C.mm_vt_gpu_name(cbuf, C.int(bufLen)) == 0 {
		name := C.GoString(cbuf)
		if name != "" {
			return name
		}
	}
	return "Apple GPU"
}
