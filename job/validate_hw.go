// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/MediaMolder/MediaMolder/av"
)

// hwCodecPlatforms maps hardware codec suffix patterns to the platforms that
// support them.
var hwCodecPlatforms = map[string][]string{
	"_nvenc":        {"linux", "windows"},
	"_vaapi":        {"linux"},
	"_videotoolbox": {"darwin"},
	"_amf":          {"windows"},
	"_qsv":          {"linux", "windows"},
	"_cuda":         {"linux", "windows"},
}

// isHWCodec returns the HW suffix for codec, or "" if it is a software codec.
func isHWCodec(codec string) string {
	for suffix := range hwCodecPlatforms {
		if strings.HasSuffix(codec, suffix) {
			return suffix
		}
	}
	return ""
}

// validateHardware checks hardware encoder availability and platform
// compatibility for encoder nodes and output codec fields.
func validateHardware(cfg *Config, r *ValidationReport) {
	seen := make(map[string]bool) // avoid duplicate reports for same codec

	checkHWCodec := func(codec, location string) {
		if codec == "" || codec == "copy" {
			return
		}
		suffix := isHWCodec(codec)
		if suffix == "" {
			return
		}

		// HW_CODEC_UNAVAILABLE — query the live encoder registry.
		if !seen["avail:"+codec] {
			seen["avail:"+codec] = true
			if !av.FindEncoder(codec) {
				r.add(ValidationIssue{
					Severity:   SeverityError,
					Code:       "HW_CODEC_UNAVAILABLE",
					Location:   location,
					Message:    fmt.Sprintf("hardware encoder %q is not available in this build", codec),
					Suggestion: "ensure FFmpeg was compiled with the required hardware acceleration library, or use a software encoder",
				})
			}
		}

		// HW_PLATFORM_MISMATCH — GOOS check.
		platforms := hwCodecPlatforms[suffix]
		if !seen["plat:"+codec] && !containsStr(platforms, runtime.GOOS) {
			seen["plat:"+codec] = true
			r.add(ValidationIssue{
				Severity: SeverityWarning,
				Code:     "HW_PLATFORM_MISMATCH",
				Location: location,
				Message: fmt.Sprintf(
					"hardware encoder %q is only supported on %v; current platform is %q",
					codec, platforms, runtime.GOOS),
				Suggestion: "use a software encoder or switch to the hardware encoder appropriate for this platform",
			})
		}
	}

	for _, nd := range cfg.Graph.Nodes {
		if nd.Type != "encoder" {
			continue
		}
		checkHWCodec(nodeParamString(nd, "codec"), "node:"+nd.ID)
	}

	for _, out := range cfg.Outputs {
		checkHWCodec(out.CodecVideo, "output:"+out.ID)
		checkHWCodec(out.CodecAudio, "output:"+out.ID)
	}
}
