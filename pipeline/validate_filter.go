// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"fmt"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/graph"
)

// filterArity describes the input arity and media type of a known filter.
type filterArity struct {
	MinInputs int
	MaxInputs int    // -1 = variable (controlled by an "inputs=" or "n=" param)
	MediaType string // "video", "audio", "video+audio"
}

// knownFilterArities is the static arity table for well-known filters.
// Filters not in this table are not checked for arity (only name existence).
var knownFilterArities = map[string]filterArity{
	"overlay":           {2, 2, "video"},
	"hstack":            {2, -1, "video"},
	"vstack":            {2, -1, "video"},
	"xstack":            {2, -1, "video"},
	"concat":            {2, -1, "video+audio"},
	"amix":              {2, -1, "audio"},
	"amerge":            {2, -1, "audio"},
	"split":             {1, 1, "video"},
	"asplit":            {1, 1, "audio"},
	"scale":             {1, 1, "video"},
	"yadif":             {1, 1, "video"},
	"bwdif":             {1, 1, "video"},
	"w3fdif":            {1, 1, "video"},
	"kerndeint":         {1, 1, "video"},
	"fps":               {1, 1, "video"},
	"format":            {1, 1, "video"},
	"crop":              {1, 1, "video"},
	"pad":               {1, 1, "video"},
	"hflip":             {1, 1, "video"},
	"vflip":             {1, 1, "video"},
	"drawtext":          {1, 1, "video"},
	"setpts":            {1, 1, "video"},
	"zscale":            {1, 1, "video"},
	"tonemap":           {1, 1, "video"},
	"loudnorm":          {1, 1, "audio"},
	"dynaudnorm":        {1, 1, "audio"},
	"aresample":         {1, 1, "audio"},
	"aformat":           {1, 1, "audio"},
	"pan":               {1, 1, "audio"},
	"adelay":            {1, 1, "audio"},
	"compand":           {1, 1, "audio"},
	"hwupload":          {1, 1, "video"},
	"hwdownload":        {1, 1, "video"},
	"hwupload_cuda":     {1, 1, "video"},
	"scale_cuda":        {1, 1, "video"},
	"scale_vaapi":       {1, 1, "video"},
	"deinterlace_vaapi": {1, 1, "video"},
	"deinterlace_qsv":   {1, 1, "video"},
}

// videoOnlyFilters is the set of filters that accept only video edges.
var videoOnlyFilters = map[string]bool{
	"scale": true, "hflip": true, "vflip": true, "crop": true, "pad": true,
	"yadif": true, "bwdif": true, "w3fdif": true, "kerndeint": true,
	"fps": true, "format": true, "overlay": true, "hstack": true,
	"vstack": true, "xstack": true, "split": true, "drawtext": true,
	"setpts": true, "zscale": true, "tonemap": true,
	"hwupload": true, "hwdownload": true, "hwupload_cuda": true,
	"scale_cuda": true, "scale_vaapi": true,
	"deinterlace_vaapi": true, "deinterlace_qsv": true,
}

// audioOnlyFilters is the set of filters that accept only audio edges.
var audioOnlyFilters = map[string]bool{
	"loudnorm": true, "dynaudnorm": true, "aresample": true, "aformat": true,
	"pan": true, "amerge": true, "amix": true, "asplit": true,
	"adelay": true, "compand": true,
}

// validateFilters checks filter name existence, arity, and media-type
// compatibility for every filter node in the graph.
func validateFilters(cfg *Config, g *graph.Graph, r *ValidationReport) {
	for _, nd := range cfg.Graph.Nodes {
		if nd.Type != "filter" {
			continue
		}
		name := nd.Filter
		if name == "" {
			continue
		}

		// FILTER_UNKNOWN_NAME — consult the live avfilter registry.
		if !av.FindFilter(name) {
			r.add(ValidationIssue{
				Severity:   SeverityError,
				Code:       "FILTER_UNKNOWN_NAME",
				Location:   "node:" + nd.ID,
				Message:    fmt.Sprintf("filter %q is not registered in this libavfilter build", name),
				Suggestion: "check the filter name spelling; use `mediamolder list-filters` to see available filters",
			})
			continue // arity/type checks are meaningless for an unknown filter
		}

		n := nodeOrNil(g, nd.ID)
		if n == nil {
			continue // graph build failed; topology checked elsewhere
		}

		checkFilterMediaType(nd, n, r)
		checkFilterArity(nd, n, r)
		checkSplitOutputCount(nd, n, r)
	}
}

// checkFilterMediaType verifies that inbound edge types match the filter's
// expected media type.
func checkFilterMediaType(nd NodeDef, n *graph.Node, r *ValidationReport) {
	name := nd.Filter
	isVideoOnly := videoOnlyFilters[name]
	isAudioOnly := audioOnlyFilters[name]
	if !isVideoOnly && !isAudioOnly {
		return
	}

	for _, e := range n.Inbound {
		switch {
		case isVideoOnly && e.Type != graph.PortVideo:
			r.add(ValidationIssue{
				Severity:   SeverityError,
				Code:       "FILTER_WRONG_MEDIA_TYPE",
				Location:   "node:" + nd.ID,
				Message:    fmt.Sprintf("video filter %q received a %s edge from %q", name, e.Type, e.From.ID),
				Suggestion: "connect a video stream to this filter, or replace it with the audio equivalent",
			})
		case isAudioOnly && e.Type != graph.PortAudio:
			r.add(ValidationIssue{
				Severity:   SeverityError,
				Code:       "FILTER_WRONG_MEDIA_TYPE",
				Location:   "node:" + nd.ID,
				Message:    fmt.Sprintf("audio filter %q received a %s edge from %q", name, e.Type, e.From.ID),
				Suggestion: "connect an audio stream to this filter, or replace it with the video equivalent",
			})
		}
	}
}

// checkFilterArity validates that the number of inbound edges falls within the
// filter's required range.
func checkFilterArity(nd NodeDef, n *graph.Node, r *ValidationReport) {
	arity, known := knownFilterArities[nd.Filter]
	if !known {
		return
	}

	got := len(n.Inbound)

	// For variable-input filters, the required minimum is always arity.MinInputs.
	// The maximum is either the declared value or the inputs=/n= param value.
	maxInputs := arity.MaxInputs
	if maxInputs == -1 {
		// Variable arity: check if inputs= or n= param overrides the minimum.
		if v, ok := nd.Params["inputs"]; ok {
			if n := paramToInt(v); n > 0 {
				maxInputs = n
			}
		}
		if v, ok := nd.Params["n"]; ok {
			if n := paramToInt(v); n > 0 {
				maxInputs = n
			}
		}
		if maxInputs == -1 {
			maxInputs = got // treat as unbounded; only check minimum
		}
	}

	if got < arity.MinInputs {
		r.add(ValidationIssue{
			Severity:   SeverityError,
			Code:       "FILTER_TOO_FEW_INPUTS",
			Location:   "node:" + nd.ID,
			Message:    fmt.Sprintf("filter %q requires at least %d input(s) but has %d", nd.Filter, arity.MinInputs, got),
			Suggestion: fmt.Sprintf("add %d more inbound edge(s) to node %q", arity.MinInputs-got, nd.ID),
		})
	} else if got > maxInputs {
		r.add(ValidationIssue{
			Severity:   SeverityError,
			Code:       "FILTER_TOO_MANY_INPUTS",
			Location:   "node:" + nd.ID,
			Message:    fmt.Sprintf("filter %q accepts at most %d input(s) but has %d", nd.Filter, maxInputs, got),
			Suggestion: "remove the excess inbound edges or use a different filter",
		})
	}
}

// checkSplitOutputCount warns when split/asplit declares N outputs but fewer
// than N outbound edges are wired.
func checkSplitOutputCount(nd NodeDef, n *graph.Node, r *ValidationReport) {
	if nd.Filter != "split" && nd.Filter != "asplit" {
		return
	}
	declared := paramToInt(nd.Params["outputs"])
	if declared <= 0 {
		declared = 2 // FFmpeg default for split
	}
	got := len(n.Outbound)
	if got < declared {
		r.add(ValidationIssue{
			Severity: SeverityWarning,
			Code:     "FILTER_OUTPUT_COUNT_MISMATCH",
			Location: "node:" + nd.ID,
			Message: fmt.Sprintf(
				"filter %q declares outputs=%d but only %d outbound edge(s) are wired; unused outputs waste buffer memory",
				nd.Filter, declared, got),
			Suggestion: fmt.Sprintf("add %d more outbound edge(s) or reduce outputs=%d to %d", declared-got, declared, got),
		})
	}
}
