// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"fmt"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/graph"
)

// HDR color primaries / transfer constants (from libavutil/pixfmt.h).
const (
	avColPriBT2020       = 9  // AVCOL_PRI_BT2020
	avColTrcSMPTE2084    = 16 // AVCOL_TRC_SMPTE2084 (PQ / HDR10)
	avColTrcARIB_STD_B67 = 18 // AVCOL_TRC_ARIB_STD_B67 (HLG)
)

// deinterlaceFilters is the set of filter names that perform deinterlacing.
var deinterlaceFilters = map[string]bool{
	"yadif":             true,
	"bwdif":             true,
	"w3fdif":            true,
	"kerndeint":         true,
	"deinterlace_vaapi": true,
	"deinterlace_qsv":   true,
}

// tonemapFilters is the set of filter names that perform HDR tone mapping.
var tonemapFilters = map[string]bool{
	"tonemap": true,
	"zscale":  true,
}

// cfvFilters is the set of filter names that enforce a constant frame rate.
var cfrFilters = map[string]bool{
	"fps":        true,
	"mpdecimate": true,
}

// validateProbeVideo runs all probe-assisted video checks for every encoder node
// in the graph that has a video stream feeding it.
func validateProbeVideo(cfg *Config, g *graph.Graph, probed map[string][]av.StreamInfo, r *ValidationReport) {
	if g == nil {
		return
	}
	for _, node := range g.Nodes {
		if node.Kind != graph.KindEncoder {
			continue
		}
		if encoderMediaType(node) != graph.PortVideo {
			continue
		}
		codec := encoderCodec(node)
		inputID, ss := sourceStreamForEncoder(g, node, graph.PortVideo, cfg)
		if inputID == "" || ss == nil {
			continue
		}
		streams, ok := probed[inputID]
		if !ok {
			continue
		}
		if ss.InputIndex < 0 || ss.InputIndex >= len(streams) {
			continue
		}
		stream := streams[ss.InputIndex]

		checkInterlacedNoDeinterlace(node, g, stream, r)
		checkPixFmtEncoderMismatch(node, codec, stream, r)
		checkBitDepthMismatch(node, codec, stream, r)
		checkHDRNoTonemap(node, g, codec, stream, r)
		checkVFRToCFREncoder(node, g, stream, r)
	}
}

// checkInterlacedNoDeinterlace warns when the source stream is interlaced but
// no deinterlace filter is present in the path to the encoder.
func checkInterlacedNoDeinterlace(node *graph.Node, g *graph.Graph, stream av.StreamInfo, r *ValidationReport) {
	if stream.FieldOrder == avFieldUnknown || stream.FieldOrder == avFieldProgressive {
		return
	}
	fieldDesc := fieldOrderName(stream.FieldOrder)
	// Find any source node to use as the path origin for pathContainsFilter.
	// We walk inbound edges backward to find a KindSource ancestor.
	sourceNode := findSourceAncestor(g, node)
	if pathContainsFilter(g, sourceNode, node, deinterlaceFilters) {
		return
	}
	r.add(ValidationIssue{
		Severity: SeverityWarning,
		Code:     "VIDEO_INTERLACED_NO_DEINTERLACE",
		Location: "node:" + node.ID,
		Message: fmt.Sprintf(
			"source stream is interlaced (%s) but no deinterlace filter precedes encoder %q",
			fieldDesc, node.ID,
		),
		Suggestion: "add a yadif=mode=send_frame node before this encoder",
		Fix: &Fix{InsertFilter: &InsertFilterFix{
			BeforeNodeID: node.ID,
			FilterName:   "yadif",
			Params:       map[string]string{"mode": "send_frame"},
		}},
	})
}

// checkPixFmtEncoderMismatch reports when the probed pixel format of the source
// stream is not in the encoder's accepted pixel format list.
func checkPixFmtEncoderMismatch(node *graph.Node, codec string, stream av.StreamInfo, r *ValidationReport) {
	if codec == "" {
		return
	}
	accepted := av.EncoderPixFmts(codec)
	if len(accepted) == 0 {
		return // encoder accepts any format (or is unknown)
	}
	if containsInt(accepted, stream.PixFmt) {
		return
	}
	probedName := av.PixFmtName(stream.PixFmt)
	if probedName == "" {
		probedName = fmt.Sprintf("fmt#%d", stream.PixFmt)
	}
	r.add(ValidationIssue{
		Severity: SeverityError,
		Code:     "VIDEO_PIX_FMT_ENCODER_MISMATCH",
		Location: "node:" + node.ID,
		Message: fmt.Sprintf(
			"pixel format %q is not accepted by encoder %q",
			probedName, codec,
		),
		Suggestion: fmt.Sprintf(
			"add a format=pix_fmts=%s filter before %q, or choose an encoder that supports %s",
			av.PixFmtName(accepted[0]), node.ID, probedName,
		),
		Fix: &Fix{InsertFilter: &InsertFilterFix{
			BeforeNodeID: node.ID,
			FilterName:   "format",
			Params:       map[string]string{"pix_fmts": av.PixFmtName(accepted[0])},
		}},
	})
}

// checkBitDepthMismatch reports when the source stream has a bit depth > 8 but
// the encoder only supports 8-bit pixel formats.
func checkBitDepthMismatch(node *graph.Node, codec string, stream av.StreamInfo, r *ValidationReport) {
	if stream.BitsPerRawSample <= 8 {
		return
	}
	accepted := av.EncoderPixFmts(codec)
	if len(accepted) == 0 {
		return
	}
	// Check if all accepted formats are 8-bit (name does not contain "10", "12", "16").
	for _, pf := range accepted {
		name := av.PixFmtName(pf)
		if is10BitOrHigher(name) {
			return // encoder accepts high-bit-depth formats
		}
	}
	r.add(ValidationIssue{
		Severity: SeverityError,
		Code:     "VIDEO_BIT_DEPTH_MISMATCH",
		Location: "node:" + node.ID,
		Message: fmt.Sprintf(
			"source stream has %d bits per sample but encoder %q only accepts 8-bit pixel formats",
			stream.BitsPerRawSample, codec,
		),
		Suggestion: fmt.Sprintf(
			"use a 10-bit encoder profile (e.g. libx264 -profile:v high10) or add a scale=flags=lanczos,format=yuv420p filter",
		),
	})
}

// checkHDRNoTonemap warns when the source is HDR (BT.2020 primaries or PQ/HLG
// transfer) but no tonemap or zscale filter precedes the encoder.
func checkHDRNoTonemap(node *graph.Node, g *graph.Graph, codec string, stream av.StreamInfo, r *ValidationReport) {
	isHDR := stream.ColorPrimaries == avColPriBT2020 ||
		stream.ColorTransfer == avColTrcSMPTE2084 ||
		stream.ColorTransfer == avColTrcARIB_STD_B67
	if !isHDR {
		return
	}
	sourceNode := findSourceAncestor(g, node)
	if pathContainsFilter(g, sourceNode, node, tonemapFilters) {
		return
	}
	hdrType := hdrTypeName(stream.ColorPrimaries, stream.ColorTransfer)
	r.add(ValidationIssue{
		Severity: SeverityWarning,
		Code:     "VIDEO_HDR_NO_TONEMAP",
		Location: "node:" + node.ID,
		Message: fmt.Sprintf(
			"source stream has %s color properties but no tonemap or zscale filter precedes encoder %q",
			hdrType, node.ID,
		),
		Suggestion: "add a zscale=transfer=bt709,tonemap=hable,format=yuv420p filter chain before this encoder for SDR output",
		Fix: &Fix{InsertFilter: &InsertFilterFix{
			BeforeNodeID: node.ID,
			FilterName:   "zscale",
			Params:       map[string]string{"transfer": "bt709", "matrix": "bt709", "primaries": "bt709"},
		}},
	})
}

// checkVFRToCFREncoder warns when the source has variable frame rate but no fps
// or mpdecimate filter is present before a CFR encoder.
func checkVFRToCFREncoder(node *graph.Node, g *graph.Graph, stream av.StreamInfo, r *ValidationReport) {
	avg := stream.FrameRate
	rfr := stream.RFrameRate
	// Both must be valid (non-zero denominators) to compare.
	if avg[1] == 0 || rfr[1] == 0 {
		return
	}
	// VFR: avg_frame_rate ≠ r_frame_rate (cross-multiply to avoid float).
	if avg[0]*rfr[1] == rfr[0]*avg[1] {
		return // same rate — CFR
	}
	sourceNode := findSourceAncestor(g, node)
	if pathContainsFilter(g, sourceNode, node, cfrFilters) {
		return
	}
	r.add(ValidationIssue{
		Severity: SeverityWarning,
		Code:     "VIDEO_VFR_TO_CFR_ENCODER",
		Location: "node:" + node.ID,
		Message: fmt.Sprintf(
			"source stream has variable frame rate (avg=%d/%d, r=%d/%d) but no fps filter precedes encoder %q",
			avg[0], avg[1], rfr[0], rfr[1], node.ID,
		),
		Suggestion: "add an fps=fps=30 (or desired rate) filter before this encoder to normalise to CFR",
		Fix: &Fix{InsertFilter: &InsertFilterFix{
			BeforeNodeID: node.ID,
			FilterName:   "fps",
			Params:       map[string]string{"fps": fmt.Sprintf("%d/%d", avg[0], avg[1])},
		}},
	})
}

// ---------- helpers ----------

// findSourceAncestor walks backward from node and returns the first KindSource
// ancestor found, or nil.
func findSourceAncestor(g *graph.Graph, node *graph.Node) *graph.Node {
	if g == nil || node == nil {
		return nil
	}
	visited := make(map[string]bool)
	return findSourceAncestorDFS(node, visited)
}

func findSourceAncestorDFS(cur *graph.Node, visited map[string]bool) *graph.Node {
	if visited[cur.ID] {
		return nil
	}
	visited[cur.ID] = true
	for _, e := range cur.Inbound {
		prev := e.From
		if prev.Kind == graph.KindSource {
			return prev
		}
		if found := findSourceAncestorDFS(prev, visited); found != nil {
			return found
		}
	}
	return nil
}

// fieldOrderName returns a human-readable description of an AVFieldOrder value.
func fieldOrderName(fo int) string {
	switch fo {
	case 2:
		return "TFF (top field first)"
	case 3:
		return "BFF (bottom field first)"
	case 4:
		return "TB"
	case 5:
		return "BT"
	default:
		return fmt.Sprintf("interlaced (field_order=%d)", fo)
	}
}

// hdrTypeName returns a concise description of the HDR type from color properties.
func hdrTypeName(primaries, transfer int) string {
	switch {
	case transfer == avColTrcSMPTE2084:
		return "HDR10/PQ"
	case transfer == avColTrcARIB_STD_B67:
		return "HLG"
	case primaries == avColPriBT2020:
		return "BT.2020"
	default:
		return "HDR"
	}
}

// is10BitOrHigher returns true if the pixel format name indicates a bit depth
// greater than 8 (e.g. "yuv420p10le", "p010le", "yuv444p12le").
func is10BitOrHigher(name string) bool {
	for i := 0; i < len(name)-1; i++ {
		if name[i] == '1' && name[i+1] >= '0' && name[i+1] <= '9' {
			return true
		}
	}
	return false
}
