// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

import "strings"

// HWStaticCaps holds hardware capability data derived from vendor-published
// look-up tables and empirical testing rather than runtime API queries.
//
// These are "best-known" values; they may not apply to every SKU within an
// architecture family. All numeric fields use 0 to mean "unknown / not
// applicable for this device type".
type HWStaticCaps struct {
	// NVDECMaxSessions is the maximum number of concurrent NVDEC decode
	// sessions supported by this GPU architecture family.
	// Source: NVIDIA Video Codec SDK release notes.
	// Consumer GPUs (Turing–Ada): typically 3; professional GPUs: 5–8.
	NVDECMaxSessions int `json:"nvdec_max_sessions,omitempty"`

	// NVENCMaxBitrateKbps maps FFmpeg codec name to the maximum encode
	// bitrate in Kbps for this GPU architecture.
	// Source: NVIDIA NVENC developer guide.
	NVENCMaxBitrateKbps map[string]int `json:"nvenc_max_bitrate_kbps,omitempty"`

	// VTMaxWidth / VTMaxHeight are empirical VideoToolbox maximum encode/
	// decode resolution limits for this Apple chip family. Both are 0 on
	// non-VideoToolbox devices or when the chip is not recognised.
	VTMaxWidth  int `json:"vt_max_width,omitempty"`
	VTMaxHeight int `json:"vt_max_height,omitempty"`
}

// nvdecMaxSessions maps CUDA arch name → consumer GPU max concurrent NVDEC
// sessions. Source: NVIDIA Video Codec SDK release notes per-generation.
// Professional/data-centre SKUs have higher limits (A100: 5, RTX 6000 Ada: 8)
// but are not distinguishable by arch name alone without a further PCI-ID
// query, so the conservative consumer count is used as the lower bound.
var nvdecMaxSessions = map[string]int{
	// Maxwell / Pascal — 2 NVDEC engines on consumer parts
	"Maxwell": 2,
	"Pascal":  2,
	// Volta — data-centre only (V100 has 5 NVDEC on the SXM variant)
	"Volta": 5,
	// Turing — RTX 20xx consumer: 3; Quadro/Titan RTX: up to 5
	"Turing": 3,
	// Ampere — RTX 30xx consumer: 3; A100: 5
	"Ampere": 3,
	// Ada Lovelace — RTX 40xx consumer: 3; RTX 6000 Ada: 8
	"Ada Lovelace": 3,
	// Hopper — H100: 7
	"Hopper": 7,
	// Blackwell — B100/B200 projections (SDK 12.x notes, subject to change)
	"Blackwell": 8,
}

// nvencMaxBitrateKbps maps codec name → (archName → max bitrate Kbps).
// Source: NVIDIA NVENC developer guide, SDK 12.1.
var nvencMaxBitrateKbps = map[string]map[string]int{
	"h264_nvenc": {
		// H.264 max bitrate is 240 Mbps across all architectures that support NVENC.
		"default": 240_000,
	},
	"hevc_nvenc": {
		// HEVC max bitrate: 800 Mbps (Turing+). Older arches: 400 Mbps.
		"default":      400_000,
		"Turing":       800_000,
		"Ampere":       800_000,
		"Ada Lovelace": 800_000,
		"Hopper":       800_000,
		"Blackwell":    800_000,
	},
	"av1_nvenc": {
		// AV1 NVENC introduced in Ada Lovelace at 400 Mbps; increased to
		// 800 Mbps on Ada professional and Hopper/Blackwell.
		"Ada Lovelace": 400_000,
		"Hopper":       800_000,
		"Blackwell":    800_000,
	},
}

// vtMaxResolution maps Apple chip family keyword → (maxW, maxH).
// Source: Apple Silicon platform notes, Final Cut Pro tech specs, and
// community testing. All M-series support 8K H.264/HEVC encode/decode.
// ProRes 8K hardware encode requires M2+ Pro/Max; M1 is limited to 4K ProRes.
// The table uses the most permissive limit applicable to the chip line.
var vtMaxResolution = []struct {
	keyword    string
	w, h       int
}{
	{"M4", 7680, 4320},
	{"M3", 7680, 4320},
	{"M2", 7680, 4320},
	{"M1", 7680, 4320},
	// A-series iPhone/iPad: typically up to 4K HEVC
	{"A17", 3840, 2160},
	{"A16", 3840, 2160},
	{"A15", 3840, 2160},
}

// QueryStaticCaps derives static capability data for the given device from
// published vendor tables. It is called by QueryCapabilities after the
// runtime fields (CUDAArch, DisplayName, device type) have been filled.
func QueryStaticCaps(caps DeviceCapabilities) HWStaticCaps {
	var sc HWStaticCaps

	// CUDA (NVENC / NVDEC) static caps — keyed by CUDAArch string.
	if caps.CUDAArch != "" {
		if n, ok := nvdecMaxSessions[caps.CUDAArch]; ok {
			sc.NVDECMaxSessions = n
		}
		bitrateMap := make(map[string]int)
		for codec, archMap := range nvencMaxBitrateKbps {
			// Look up arch-specific value, fall back to "default".
			if v, ok := archMap[caps.CUDAArch]; ok {
				bitrateMap[codec] = v
			} else if v, ok := archMap["default"]; ok {
				bitrateMap[codec] = v
			}
		}
		if len(bitrateMap) > 0 {
			sc.NVENCMaxBitrateKbps = bitrateMap
		}
	}

	// VideoToolbox static caps — keyed by chip family in DisplayName.
	if caps.DisplayName != "" {
		for _, entry := range vtMaxResolution {
			if strings.Contains(caps.DisplayName, entry.keyword) {
				sc.VTMaxWidth = entry.w
				sc.VTMaxHeight = entry.h
				break
			}
		}
	}

	return sc
}
