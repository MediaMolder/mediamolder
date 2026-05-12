// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"strings"

	"github.com/MediaMolder/MediaMolder/graph"
)

// hwFilterAlts maps a software filter name to its hardware-accelerated
// equivalent, keyed by the HardwareDevice.Type string. Only one entry
// per (filter, device type) is included — the "standard" GPU variant
// that requires the minimum extra build dependency (e.g. scale_cuda
// rather than scale_npp). When the user wants the NPP variant they
// should name scale_npp directly and not use AutoMapHW.
//
// Sources: libavfilter/Makefile — OBJS-$(CONFIG_FOO_FILTER) guards and
// the avfilter_*_dependencies lists.
var hwFilterAlts = map[string]map[string]string{
	// ── Video geometry ──────────────────────────────────────────────────
	"scale": {
		"cuda":         "scale_cuda",
		"vaapi":        "scale_vaapi",
		"qsv":          "scale_qsv",
		"videotoolbox": "scale_vt",
		"vulkan":       "scale_vulkan",
	},
	"overlay": {
		"cuda":  "overlay_cuda",
		"vaapi": "overlay_vaapi",
		"qsv":   "overlay_qsv",
	},
	"transpose": {
		"vaapi": "transpose_vaapi",
		"qsv":   "transpose_qsv",
	},
	"flip": {
		"vulkan": "flip_vulkan",
	},
	"rotate": {
		"vulkan": "rotate_vulkan",
	},
	"pad": {
		"opencl": "pad_opencl",
	},

	// ── Deinterlace / frame-rate ─────────────────────────────────────────
	"yadif": {
		"cuda":  "yadif_cuda",
		"vaapi": "deinterlace_vaapi",
	},
	"deinterlace": {
		"vaapi": "deinterlace_vaapi",
		"qsv":   "deinterlace_qsv",
	},

	// ── Blur / sharpness / noise ─────────────────────────────────────────
	"avgblur": {
		"vulkan": "avgblur_vulkan",
		"opencl": "avgblur_opencl",
	},
	"unsharp": {
		"opencl": "unsharp_opencl",
	},
	"bilateral": {
		"opencl": "bilateral_opencl",
	},
	"nlmeans": {
		"opencl": "nlmeans_opencl",
	},
	"convolution": {
		"opencl": "convolution_opencl",
	},
	"boxblur": {
		"opencl": "boxblur_opencl",
	},
	"sobel": {
		"opencl": "sobel_opencl",
	},
	"deshake": {
		"opencl": "deshake_opencl",
	},

	// ── Colour / tone ────────────────────────────────────────────────────
	"tonemap": {
		"vaapi":  "tonemap_vaapi",
		"opencl": "tonemap_opencl",
	},
	"colorkey": {
		"opencl": "colorkey_opencl",
		"cuda":   "chromakey_cuda",
	},

	// ── Blend / composite ────────────────────────────────────────────────
	"blend": {
		"vulkan": "blend_vulkan",
		"opencl": "blend_opencl",
	},
	"maskedmerge": {
		"opencl": "maskedmerge_opencl",
	},
	"erosion": {
		"opencl": "erosion_opencl",
	},
	"dilation": {
		"opencl": "dilation_opencl",
	},
	"xfade": {
		"opencl": "xfade_opencl",
	},

	// ── Thumbnailing ─────────────────────────────────────────────────────
	"thumbnail": {
		"cuda": "thumbnail_cuda",
	},
}

// expandHWFilterMappings is an opt-in graph expansion pass that runs
// after cfg → def node/edge translation but before expandImplicitEncoders.
//
// For each filter node that has both:
//
//	NodeDef.AutoMapHW == true
//	NodeDef.Device    != ""
//
// the pass:
//  1. Promotes the software filter name to its hardware equivalent
//     (e.g. "scale" on a CUDA device → "scale_cuda").
//  2. Inserts a synthetic "hwupload" node in front of each incoming
//     video edge whose source node is not on the same device (format
//     disagreement: CPU frames → GPU surface).
//  3. Inserts a synthetic "hwdownload" node behind each outgoing
//     video edge whose destination node is not on the same device
//     (GPU surface → CPU frames).
//
// Only video edges are converted; audio and subtitle edges pass
// through unchanged (hardware audio filters are extremely rare and
// never need format conversion at the libavfilter boundary).
//
// The "actual pad-format disagree" check is approximated at config
// time by comparing Device fields: if source and destination are on
// the same named device, no conversion is needed. This is exact for
// single-device pipelines (the common case) and safe for multi-device
// pipelines (inserting a redundant hwupload is a libavfilter error at
// runtime, but nodes on the same device always share the same surface
// type so the guard is sound).
//
// Synthetic node IDs use the "__hwup__" / "__hwdn__" prefix to avoid
// collisions with user-defined node IDs, following the same convention
// as expandImplicitEncoders's "__enc__" prefix.
func expandHWFilterMappings(cfg *Config, def *graph.Def) {
	if len(cfg.HardwareDevices) == 0 {
		return // fast-path: no hardware devices declared
	}

	// Map declared device names → their type strings.
	devTypeByName := make(map[string]string, len(cfg.HardwareDevices))
	for _, hd := range cfg.HardwareDevices {
		devTypeByName[hd.Name] = strings.ToLower(hd.Type)
	}

	// Map node IDs → their declared Device names.
	nodeDevice := make(map[string]string, len(def.Nodes))
	for _, n := range def.Nodes {
		if n.Device != "" {
			nodeDevice[n.ID] = n.Device
		}
	}

	// helper: extract node-ID prefix from "nodeID" or "nodeID:port".
	head := func(ref string) string {
		if i := strings.IndexByte(ref, ':'); i >= 0 {
			return ref[:i]
		}
		return ref
	}

	// Collect the indices of nodes that need expansion first, then
	// process them — we append new nodes to def.Nodes, so iterating
	// def.Nodes by index is safe.
	type candidate struct {
		nodeIdx int
		devName string
		devType string
		hwAlt   string
	}
	var candidates []candidate

	for i, n := range def.Nodes {
		if !n.AutoMapHW || n.Device == "" || n.Type != "filter" {
			continue
		}
		alts, ok := hwFilterAlts[n.Filter]
		if !ok {
			continue
		}
		devType := devTypeByName[n.Device]
		hwAlt, ok := alts[devType]
		if !ok {
			continue
		}
		candidates = append(candidates, candidate{
			nodeIdx: i,
			devName: n.Device,
			devType: devType,
			hwAlt:   hwAlt,
		})
	}

	if len(candidates) == 0 {
		return
	}

	// Build edge adjacency: nodeID → slice of edge indices.
	inEdges := make(map[string][]int, len(def.Edges))
	outEdges := make(map[string][]int, len(def.Edges))
	for i, e := range def.Edges {
		fromID := head(e.From)
		toID := head(e.To)
		outEdges[fromID] = append(outEdges[fromID], i)
		inEdges[toID] = append(inEdges[toID], i)
	}

	for _, c := range candidates {
		// 1. Promote filter name.
		def.Nodes[c.nodeIdx].Filter = c.hwAlt

		nodeID := def.Nodes[c.nodeIdx].ID

		// 2. Insert hwupload on incoming video edges from non-same-device nodes.
		for _, ei := range inEdges[nodeID] {
			e := def.Edges[ei]
			if e.Type != "video" {
				continue
			}
			srcID := head(e.From)
			if nodeDevice[srcID] == c.devName {
				continue // already on the same device; no upload needed
			}
			upID := "__hwup__" + nodeID + "_" + srcID
			if nodeDevice[upID] != "" {
				// already inserted (can happen with multi-pass calling)
				continue
			}
			def.Nodes = append(def.Nodes, graph.NodeDef{
				ID:     upID,
				Type:   "filter",
				Filter: "hwupload",
				Device: c.devName,
			})
			nodeDevice[upID] = c.devName
			// Rewrite original edge to go through hwupload.
			origTo := def.Edges[ei].To
			def.Edges[ei].To = upID
			def.Edges = append(def.Edges, graph.EdgeDef{
				From: upID,
				To:   origTo,
				Type: "video",
			})
			// Update adjacency for the newly-added edge.
			outEdges[upID] = append(outEdges[upID], len(def.Edges)-1)
			inEdges[head(origTo)] = append(inEdges[head(origTo)], len(def.Edges)-1)
		}

		// 3. Insert hwdownload on outgoing video edges to non-same-device nodes.
		for _, ei := range outEdges[nodeID] {
			e := def.Edges[ei]
			if e.Type != "video" {
				continue
			}
			dstID := head(e.To)
			if nodeDevice[dstID] == c.devName {
				continue // stays on device; no download needed
			}
			dnID := "__hwdn__" + nodeID + "_" + dstID
			if nodeDevice[dnID] != "" {
				continue
			}
			def.Nodes = append(def.Nodes, graph.NodeDef{
				ID:     dnID,
				Type:   "filter",
				Filter: "hwdownload",
			})
			// hwdownload itself is CPU-side; no Device tag.
			origFrom := def.Edges[ei].From
			def.Edges[ei].From = dnID
			def.Edges = append(def.Edges, graph.EdgeDef{
				From: origFrom,
				To:   dnID,
				Type: "video",
			})
			outEdges[head(origFrom)] = append(outEdges[head(origFrom)], len(def.Edges)-1)
			inEdges[dnID] = append(inEdges[dnID], len(def.Edges)-1)
		}
	}
}

// HWFilterAlts returns a shallow copy of the hardware filter alternatives
// table, keyed by software filter name → (device type → hardware filter name).
// Exported for documentation tooling and the GUI node-palette builder; callers
// should not modify the inner maps.
func HWFilterAlts() map[string]map[string]string {
	out := make(map[string]map[string]string, len(hwFilterAlts))
	for sw, m := range hwFilterAlts {
		inner := make(map[string]string, len(m))
		for dt, hw := range m {
			inner[dt] = hw
		}
		out[sw] = inner
	}
	return out
}
