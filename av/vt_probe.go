// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build darwin

package av

// VTProbeCapabilities queries the Apple VideoToolbox platform directly —
// independently of the LibAV codec registry — using the VideoToolbox C API.
// This surfaces codecs that the silicon supports but that LibAV cannot
// represent (e.g. ProRes RAW encode/decode on Apple Silicon M-series).
//
// Call mergeVTProbe on the result of QueryCapabilities to fold any extra
// entries into the Codecs slice before returning.

// #cgo LDFLAGS: -framework VideoToolbox -framework CoreMedia -framework CoreFoundation
//
// #include <VideoToolbox/VideoToolbox.h>
// #include <CoreMedia/CMFormatDescription.h>
// #include <CoreFoundation/CoreFoundation.h>
// #include <stdint.h>
// #include <stdbool.h>
//
// // mm_vt_hw_encoder_codec_types iterates VTCopyVideoEncoderList and returns a
// // '\n'-separated list of four-char-codes (as decimal uint32) for every
// // hardware-accelerated encoder. Returns NULL on failure; caller frees with
// // CFStringGetCStringPtr patterns are not used — we build a plain C string via
// // a manual buffer.
// //
// // We allocate a fixed-size buffer; 64 four-char-code entries × 12 bytes each
// // is well within any realistic VT implementation.
// static char *mm_vt_hw_encoder_codec_types(void) {
//     CFArrayRef list = NULL;
//     if (VTCopyVideoEncoderList(NULL, &list) != noErr || list == NULL)
//         return NULL;
//
//     // Pre-allocate: max 64 encoders × "4294967295\n" = 11+1 chars each.
//     static char buf[64 * 12];
//     buf[0] = '\0';
//     int off = 0;
//     CFIndex n = CFArrayGetCount(list);
//     for (CFIndex i = 0; i < n && off < (int)sizeof(buf) - 16; i++) {
//         CFDictionaryRef entry =
//             (CFDictionaryRef)CFArrayGetValueAtIndex(list, i);
//         if (!entry) continue;
//
//         // Only hardware-accelerated encoders.
//         CFBooleanRef hw = (CFBooleanRef)CFDictionaryGetValue(
//             entry, kVTVideoEncoderList_IsHardwareAccelerated);
//         if (!hw || !CFBooleanGetValue(hw)) continue;
//
//         CFNumberRef ct = (CFNumberRef)CFDictionaryGetValue(
//             entry, kVTVideoEncoderList_CodecType);
//         if (!ct) continue;
//         uint32_t val = 0;
//         CFNumberGetValue(ct, kCFNumberSInt32Type, &val);
//
//         if (off > 0) buf[off++] = '\n';
//         off += snprintf(buf + off, sizeof(buf) - off, "%u", val);
//     }
//     CFRelease(list);
//     return off > 0 ? buf : NULL;
// }
//
// // mm_vt_is_hw_decode_supported returns 1 if the platform reports hardware
// // decode support for the given CMVideoCodecType, 0 otherwise.
// static int mm_vt_is_hw_decode_supported(uint32_t codec_type) {
//     return VTIsHardwareDecodeSupported((CMVideoCodecType)codec_type) ? 1 : 0;
// }
import "C"

import (
	"errors"
	"strings"
)

// vtCodecTypeMap maps a CMVideoCodecType four-char-code (as uint32) to the
// MediaMolder codec name and whether the type is already represented in the
// LibAV registry under a VT name.
//
// Four-char-code values (big-endian ASCII):
//
//	'avc1' = 0x61766331  H.264
//	'hvc1' = 0x68766331  HEVC
//	'apch' = 0x61706368  ProRes 422 HQ
//	'apcn' = 0x6170636e  ProRes 422
//	'apcs' = 0x61706373  ProRes 422 LT
//	'apco' = 0x6170636f  ProRes 422 Proxy
//	'ap4h' = 0x61703468  ProRes 4444
//	'ap4x' = 0x61703478  ProRes 4444 XQ
//	'aprn' = 0x6170726e  ProRes RAW
//	'aprh' = 0x61707268  ProRes RAW HQ
//	'av01' = 0x61763031  AV1
type vtCodecEntry struct {
	name      string // MediaMolder/LibAV codec name
	inLibAV   bool   // true when LibAV's registry already covers this via VT
}

var vtCodecTypes = map[uint32]vtCodecEntry{
	0x61766331: {"h264_videotoolbox", true},
	0x68766331: {"hevc_videotoolbox", true},
	0x61706368: {"prores_videotoolbox", true}, // ProRes 422 HQ → same encoder in LibAV
	0x6170636e: {"prores_videotoolbox", true}, // ProRes 422
	0x61706373: {"prores_videotoolbox", true}, // ProRes 422 LT
	0x6170636f: {"prores_videotoolbox", true}, // ProRes 422 Proxy
	0x61703468: {"prores_videotoolbox", true}, // ProRes 4444
	0x61703478: {"prores_videotoolbox", true}, // ProRes 4444 XQ
	0x6170726e: {"prores_raw_vt", false},      // ProRes RAW — not in LibAV
	0x61707268: {"prores_raw_hq_vt", false},   // ProRes RAW HQ — not in LibAV
	0x61763031: {"av1_videotoolbox", false},    // AV1 — check if LibAV has it
}

// vtDecodeTypes lists CMVideoCodecType values to test with
// VTIsHardwareDecodeSupported. Only types not already covered by the LibAV
// hwaccel scan are included here; the rest come from ListHWCodecs.
var vtDecodeTypes = []struct {
	codecType uint32
	name      string
}{
	{0x6170726e, "prores_raw_vt"},    // ProRes RAW
	{0x61707268, "prores_raw_hq_vt"}, // ProRes RAW HQ
}

// VTPlatformCapabilities holds codecs discovered by querying the VideoToolbox
// platform directly (independently of the LibAV codec registry).
type VTPlatformCapabilities struct {
	// ExtraEncoders are hardware encoders the VT platform exposes that are
	// not already listed in the LibAV codec registry under a VT name.
	ExtraEncoders []HWCodecInfo
	// ExtraDecoders are hardware decoders ditto.
	ExtraDecoders []HWCodecInfo
}

// QueryVTCapabilities probes Apple VideoToolbox for hardware codec support
// independently of the LibAV codec registry. It is safe to call on any macOS
// host; it returns empty slices when no VT hardware is available.
func QueryVTCapabilities() VTPlatformCapabilities {
	var caps VTPlatformCapabilities

	// --- Encoders via VTCopyVideoEncoderList ---
	cStr := C.mm_vt_hw_encoder_codec_types()
	if cStr != nil {
		s := C.GoString(cStr)
		// cStr points into a static buffer in the C helper; no free needed.
		seen := make(map[string]bool)
		for _, tok := range strings.Split(s, "\n") {
			tok = strings.TrimSpace(tok)
			if tok == "" {
				continue
			}
			var val uint32
			_, err := parseUint32(tok, &val)
			if err != nil {
				continue
			}
			entry, ok := vtCodecTypes[val]
			if !ok || entry.inLibAV {
				continue
			}
			if seen[entry.name] {
				continue
			}
			seen[entry.name] = true
			caps.ExtraEncoders = append(caps.ExtraEncoders, HWCodecInfo{
				Name:      entry.name,
				Role:      "encode",
				MediaType: "video",
			})
		}
	}

	// --- Decoders via VTIsHardwareDecodeSupported ---
	for _, d := range vtDecodeTypes {
		if C.mm_vt_is_hw_decode_supported(C.uint32_t(d.codecType)) == 1 {
			caps.ExtraDecoders = append(caps.ExtraDecoders, HWCodecInfo{
				Name:      d.name,
				Role:      "decode",
				MediaType: "video",
			})
		}
	}

	return caps
}

// parseUint32 parses a decimal uint32 string.
func parseUint32(s string, out *uint32) (int, error) {
	if len(s) == 0 {
		return 0, errBadUint32
	}
	var v uint64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errBadUint32
		}
		v = v*10 + uint64(c-'0')
		if v > 0xFFFFFFFF {
			return 0, errBadUint32
		}
	}
	*out = uint32(v)
	return len(s), nil
}

var errBadUint32 = errors.New("vt_probe: invalid uint32")
