// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: GPL-3.0-or-later

package job

import (
	"fmt"
	"sync"

	"github.com/MediaMolder/MediaMolder/av"
)

// filterSetOnce guards builtInFilterSet, which is built once from
// av.ListFilters() on the first call to filterAvailabilityError.
var (
	filterSetOnce    sync.Once
	builtInFilterSet map[string]struct{}
)

// builtInFilters returns the set of filter names compiled into this
// libavfilter binary. The set is built once (at first call — "process
// start" in practice) from av.ListFilters() so that all subsequent
// lookups in validateFilterAvailability are O(1) pure-Go map reads
// with no CGO overhead, matching the codec-probe harness pattern.
func builtInFilters() map[string]struct{} {
	filterSetOnce.Do(func() {
		infos := av.ListFilters()
		builtInFilterSet = make(map[string]struct{}, len(infos))
		for _, f := range infos {
			builtInFilterSet[f.Name] = struct{}{}
		}
	})
	return builtInFilterSet
}

// optionalFilterLibs maps filter names that require an optional FFmpeg
// build dependency to the configure flag the user must enable. The
// list covers hardware-accelerated and optional-library filters where
// a missing build dependency yields a confusing "filter not found"
// runtime error. Other missing filters still surface the generic
// "not built into this libavfilter" message via filterAvailabilityError.
//
// Sources:
//   - libavfilter/Makefile (`OBJS-$(CONFIG_FOO_FILTER) += vf_foo.o`)
//   - configure (`enabled libfoo` gates).
var optionalFilterLibs = map[string]string{
	// ── CUDA / NVCC ────────────────────────────────────────────────────────
	"chromakey_cuda":  "--enable-cuda-nvcc",
	"colorspace_cuda": "--enable-cuda-nvcc",
	"hwupload_cuda":   "--enable-cuda-nvcc",
	"overlay_cuda":    "--enable-cuda-nvcc",
	"scale_cuda":      "--enable-cuda-nvcc",
	"thumbnail_cuda":  "--enable-cuda-nvcc",
	"yadif_cuda":      "--enable-cuda-nvcc",

	// ── CUDA + libnpp (NVIDIA Performance Primitives) ─────────────────────
	// scale_npp and transpose_npp require both the CUDA compiler and the
	// closed-source NPP library from the CUDA Toolkit. scale_cuda only
	// needs --enable-cuda-nvcc without NPP.
	"scale_npp":     "--enable-libnpp --enable-cuda-nvcc",
	"transpose_npp": "--enable-libnpp --enable-cuda-nvcc",

	// ── VAAPI ──────────────────────────────────────────────────────────────
	"deinterlace_vaapi": "--enable-vaapi",
	"denoise_vaapi":     "--enable-vaapi",
	"hwupload_vaapi":    "--enable-vaapi",
	"overlay_vaapi":     "--enable-vaapi",
	"procamp_vaapi":     "--enable-vaapi",
	"scale_vaapi":       "--enable-vaapi",
	"sharpness_vaapi":   "--enable-vaapi",
	"tonemap_vaapi":     "--enable-vaapi",
	"transpose_vaapi":   "--enable-vaapi",

	// ── Intel QSV (libmfx / oneVPL) ────────────────────────────────────────
	"deinterlace_qsv": "--enable-libmfx (or --enable-libvpl for oneVPL)",
	"overlay_qsv":     "--enable-libmfx (or --enable-libvpl for oneVPL)",
	"scale_qsv":       "--enable-libmfx (or --enable-libvpl for oneVPL)",
	"transpose_qsv":   "--enable-libmfx (or --enable-libvpl for oneVPL)",
	"vpp_qsv":         "--enable-libmfx (or --enable-libvpl for oneVPL)",

	// ── Vulkan ─────────────────────────────────────────────────────────────
	"avgblur_vulkan":   "--enable-vulkan",
	"blend_vulkan":     "--enable-vulkan",
	"chromaber_vulkan": "--enable-vulkan",
	"flip_vulkan":      "--enable-vulkan",
	"overlay_vulkan":   "--enable-vulkan",
	"rotate_vulkan":    "--enable-vulkan",
	"scale_vulkan":     "--enable-vulkan",
	"transpose_vulkan": "--enable-vulkan",

	// ── OpenCL ─────────────────────────────────────────────────────────────
	"afir_opencl":        "--enable-opencl",
	"avgblur_opencl":     "--enable-opencl",
	"bilateral_opencl":   "--enable-opencl",
	"blend_opencl":       "--enable-opencl",
	"boxblur_opencl":     "--enable-opencl",
	"colorkey_opencl":    "--enable-opencl",
	"convolution_opencl": "--enable-opencl",
	"deshake_opencl":     "--enable-opencl",
	"dilation_opencl":    "--enable-opencl",
	"erosion_opencl":     "--enable-opencl",
	"maskedmerge_opencl": "--enable-opencl",
	"nlmeans_opencl":     "--enable-opencl",
	"overlay_opencl":     "--enable-opencl",
	"pad_opencl":         "--enable-opencl",
	"program_opencl":     "--enable-opencl",
	"sobel_opencl":       "--enable-opencl",
	"tonemap_opencl":     "--enable-opencl",
	"transpose_opencl":   "--enable-opencl",
	"unsharp_opencl":     "--enable-opencl",
	"xfade_opencl":       "--enable-opencl",

	// ── VideoToolbox (macOS) ───────────────────────────────────────────────
	"scale_vt":             "--enable-videotoolbox",
	"tonemap_videotoolbox": "--enable-videotoolbox",

	// ── Optional libraries ─────────────────────────────────────────────────
	"arnndn":       "--enable-librnnoise (built into libavfilter when present)",
	"ass":          "--enable-libass",
	"frei0r":       "--enable-frei0r",
	"frei0r_src":   "--enable-frei0r",
	"ladspa":       "--enable-ladspa",
	"libplacebo":   "--enable-libplacebo",
	"libvmaf":      "--enable-libvmaf",
	"libvmaf_cuda": "--enable-libvmaf --enable-cuda-nvcc",
	"lv2":          "--enable-lv2",
	"ocr":          "--enable-libtesseract",
	"sab":          "--enable-libgsm (or libavfilter built without --disable-filter=sab)",
	"signature":    "--enable-libavfilter (filter is gated by CONFIG_SIGNATURE_FILTER; usually present)",
	"smbprotect":   "--enable-libsmbclient",
	"sofalizer":    "--enable-libmysofa",
	"subtitles":    "--enable-libass",
	"vmafmotion":   "--enable-libvmaf",
	"zscale":       "--enable-libzimg",
}

// filterAvailabilityError returns an error describing why the named
// filter is not available in this libavfilter build, or nil when the
// filter is present. The message includes the configure flag (when
// known) so the operator can rebuild without a web search.
//
// The lookup uses the builtInFilters cache (built once from
// av.ListFilters() at first call) rather than calling av.FindFilter
// per invocation, matching the codec-probe harness pattern (Wave 10 #57).
func filterAvailabilityError(name string) error {
	if _, ok := builtInFilters()[name]; ok {
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
		switch node.Type {
		case "filter", "filter_source", "filter_sink":
		default:
			continue
		}
		if node.Filter == "" {
			continue // caught by other validators / runtime
		}
		if err := filterAvailabilityError(node.Filter); err != nil {
			return fmt.Errorf("node[%d] %q: %w", i, node.ID, err)
		}
		if node.Type == "filter_source" {
			if _, ok := knownFilterSources[node.Filter]; !ok {
				return fmt.Errorf("node[%d] %q: filter %q is not a recognised source filter (Wave 7 #36a allow-list)", i, node.ID, node.Filter)
			}
		}
		if node.Type == "filter_sink" {
			if _, ok := knownFilterSinks[node.Filter]; !ok {
				return fmt.Errorf("node[%d] %q: filter %q is not a recognised sink filter (Wave 7 #36a allow-list)", i, node.ID, node.Filter)
			}
		}
	}
	return nil
}

// knownFilterSources is the curated allow-list of libavfilter source
// filters (zero inputs) that may appear as graph nodes with
// type="filter_source". Mirrors fftools/ffmpeg_filter.c source-filter
// detection. Wave 7 #36a.
var knownFilterSources = map[string]struct{}{
	"color":       {},
	"testsrc":     {},
	"testsrc2":    {},
	"smptebars":   {},
	"smptehdbars": {},
	"mandelbrot":  {},
	"life":        {},
	"yuvtestsrc":  {},
	"rgbtestsrc":  {},
	"sine":        {},
	"anullsrc":    {},
	"aevalsrc":    {},
	"movie":       {},
	"amovie":      {},
}

// knownFilterSinks is the curated allow-list of libavfilter sink
// filters (zero outputs) that may appear as graph nodes with
// type="filter_sink". Wave 7 #36a.
var knownFilterSinks = map[string]struct{}{
	"nullsink":  {},
	"anullsink": {},
}
