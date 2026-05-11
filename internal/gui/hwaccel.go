// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package gui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/MediaMolder/MediaMolder/av"
)

// hwCodecInfo mirrors av.HWCodecInfo for the JSON API.
type hwCodecInfo struct {
	Name      string `json:"name"`                // e.g. "h264_cuvid", "hevc_vaapi"
	Role      string `json:"role"`                // "encode" or "decode"
	MediaType string `json:"media_type,omitempty"` // "video", "audio", etc.
	Note      string `json:"note,omitempty"`       // capability limitation at this GPU, if any
}

// hwAccelEntry is the JSON shape returned by GET /api/hwaccel.
type hwAccelEntry struct {
	Type        string `json:"type"`
	Available   bool   `json:"available"`
	Error       string `json:"error,omitempty"`
	// Populated only when Available is true:
	DisplayName string        `json:"display_name,omitempty"` // e.g. "NVIDIA GeForce RTX 3060 Ti"
	SWFormats   []string      `json:"sw_formats,omitempty"`   // software pixel formats
	MaxWidth    int           `json:"max_width,omitempty"`    // 0 = not reported
	MaxHeight   int           `json:"max_height,omitempty"`   // 0 = not reported
	Codecs      []hwCodecInfo `json:"codecs,omitempty"`       // codecs supported by this GPU
	Filters     []string      `json:"filters,omitempty"`      // HW-accelerated filter names
	// CUDA-specific (empty for non-CUDA backends):
	CUDASMVersion string `json:"cuda_sm,omitempty"`   // e.g. "8.9"
	CUDAArch      string `json:"cuda_arch,omitempty"` // e.g. "Ada Lovelace"
}

var (
	hwAccelOnce   sync.Once
	hwAccelResult []hwAccelEntry
)

// probeHWAccelOnce runs av.ProbeHWDevices exactly once and caches the result.
// Hardware availability does not change while the process is running, so
// re-probing on every request is unnecessary and wastes time.
func probeHWAccelOnce() []hwAccelEntry {
	hwAccelOnce.Do(func() {
		probes := av.ProbeHWDevices()
		hwAccelResult = make([]hwAccelEntry, 0, len(probes))
		for _, p := range probes {
			entry := hwAccelEntry{
				Type:      p.Type.String(),
				Available: p.Available,
			}
			if p.Err != "" {
				entry.Error = p.Err
			}
			if p.Available {
				caps := p.Capabilities
				entry.DisplayName = caps.DisplayName
				entry.SWFormats = caps.SWFormats
				entry.MaxWidth = caps.MaxWidth
				entry.MaxHeight = caps.MaxHeight
				if len(caps.Codecs) > 0 {
					entry.Codecs = make([]hwCodecInfo, len(caps.Codecs))
					for i, c := range caps.Codecs {
						entry.Codecs[i] = hwCodecInfo{Name: c.Name, Role: c.Role, MediaType: c.MediaType, Note: c.Note}
					}
				}
				entry.Filters = caps.Filters
				if caps.CUDAArch != "" {
					entry.CUDASMVersion = fmt.Sprintf("%d.%d", caps.CUDASMMajor, caps.CUDASMMinor)
					entry.CUDAArch = caps.CUDAArch
				}
			}
			hwAccelResult = append(hwAccelResult, entry)
		}
	})
	return hwAccelResult
}

// handleListHWAccel implements GET /api/hwaccel.
//
// Returns all hardware acceleration types compiled into the linked FFmpeg
// together with their runtime availability (probed once via
// av_hwdevice_ctx_create). The result is cached after the first probe so
// subsequent requests are instantaneous. A warm-up goroutine in NewServer
// ensures the probe runs in the background at startup, not on the first
// browser request.
func handleListHWAccel(w http.ResponseWriter, _ *http.Request) {
	result := probeHWAccelOnce()
	if result == nil {
		result = []hwAccelEntry{} // return [] not null
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
