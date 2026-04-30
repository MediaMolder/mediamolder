// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: GPL-3.0-or-later

package pipeline

import (
	"fmt"

	"github.com/MediaMolder/MediaMolder/av"
)

// optionalFilterLibs maps filter names that require an optional FFmpeg
// build dependency to the configure flag the user must enable. The
// list is conservative: it covers the filters most commonly requested
// by HDR / colour-science / ML workflows where a missing build
// dependency yields a confusing "filter not found" runtime error
// hundreds of milliseconds into job startup. Other missing filters
// still surface a generic "filter %q not built into this libavfilter"
// message via filterAvailabilityError below.
//
// Sources:
//   - libavfilter/Makefile (`OBJS-$(CONFIG_FOO_FILTER) += vf_foo.o`)
//   - configure (`enabled libfoo` gates).
var optionalFilterLibs = map[string]string{
	"zscale":             "--enable-libzimg",
	"libplacebo":         "--enable-libplacebo",
	"sab":                "--enable-libgsm (or libavfilter built without --disable-filter=sab)",
	"frei0r":             "--enable-frei0r",
	"frei0r_src":         "--enable-frei0r",
	"ocr":                "--enable-libtesseract",
	"ladspa":             "--enable-ladspa",
	"lv2":                "--enable-lv2",
	"arnndn":             "--enable-librnnoise (built into libavfilter when present)",
	"sofalizer":          "--enable-libmysofa",
	"vmafmotion":         "--enable-libvmaf",
	"libvmaf":            "--enable-libvmaf",
	"libvmaf_cuda":       "--enable-libvmaf --enable-cuda-nvcc",
	"signature":          "--enable-libavfilter (filter is gated by CONFIG_SIGNATURE_FILTER; usually present)",
	"subtitles":          "--enable-libass",
	"ass":                "--enable-libass",
	"smbprotect":         "--enable-libsmbclient",
	"chromaber_vulkan":   "--enable-vulkan",
	"chromakey_cuda":     "--enable-cuda-nvcc",
	"colorspace_cuda":    "--enable-cuda-nvcc",
	"hwupload_cuda":      "--enable-cuda-nvcc",
	"scale_cuda":         "--enable-cuda-nvcc",
	"thumbnail_cuda":     "--enable-cuda-nvcc",
	"yadif_cuda":         "--enable-cuda-nvcc",
	"deinterlace_vaapi":  "--enable-vaapi",
	"scale_vaapi":        "--enable-vaapi",
	"scale_npp":          "--enable-libnpp --enable-cuda-nvcc",
	"hwupload_vaapi":     "--enable-vaapi",
	"tonemap_opencl":     "--enable-opencl",
	"tonemap_vaapi":      "--enable-vaapi",
	"tonemap_videotoolbox": "--enable-videotoolbox",
}

// filterAvailabilityError returns an error describing why the named
// filter is not available in this libavfilter build, or nil when the
// filter is present. The message includes the configure flag (when
// known) so the operator can rebuild without a web search.
func filterAvailabilityError(name string) error {
	if av.FindFilter(name) {
		return nil
	}
	if hint, ok := optionalFilterLibs[name]; ok {
		return fmt.Errorf("filter %q is not built into this libavfilter (rebuild FFmpeg with %s)", name, hint)
	}
	return fmt.Errorf("filter %q is not built into this libavfilter (no such filter)", name)
}

// validateFilterAvailability rejects graphs that reference filters not
// compiled into the running libavfilter build. Catches Wave 7 #42
// libzimg / libplacebo / libtesseract / etc. dependency gaps before
// runtime opens the graph.
func validateFilterAvailability(cfg *Config) error {
	for i, node := range cfg.Graph.Nodes {
		if node.Type != "filter" {
			continue
		}
		if node.Filter == "" {
			continue // caught by other validators / runtime
		}
		if err := filterAvailabilityError(node.Filter); err != nil {
			return fmt.Errorf("node[%d] %q: %w", i, node.ID, err)
		}
	}
	return nil
}
