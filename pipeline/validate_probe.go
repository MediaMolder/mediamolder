// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"fmt"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/graph"
)

// ValidateConfig runs Phase A static analysis followed by Phase B probe-assisted
// analysis. It opens each input URL, probes stream metadata, and runs additional
// checks that require knowing the actual pixel format, field order, color
// properties, and stream layout of the source media.
//
// Probe failures are reported as WARNING-level PROBE_FAILED issues rather than
// being treated as hard errors, so the Phase A results are always returned.
// sec may be nil.
func ValidateConfig(cfg *Config, sec *SecurityConfig) (*ValidationReport, error) {
	// Phase A: static checks (no I/O).
	r := ValidateConfigStatic(cfg, sec)

	// Phase B: probe each input.
	probed := make(map[string][]av.StreamInfo)
	for _, inp := range cfg.Inputs {
		streams, err := probeInput(inp)
		if err != nil {
			r.add(ValidationIssue{
				Severity: SeverityWarning,
				Code:     "PROBE_FAILED",
				Location: "input:" + inp.ID,
				Message:  fmt.Sprintf("could not probe input %q: %v", inp.URL, err),
			})
			continue
		}
		probed[inp.ID] = streams
	}

	// Build graph for path analysis (errors already in r from Phase A).
	def := configToGraphDef(cfg)
	g, _ := graph.Build(def)

	validateProbeStreams(cfg, probed, r)
	validateProbeVideo(cfg, g, probed, r)
	validateProbeAudio(cfg, g, probed, r)

	return r, nil
}

// probeInput opens the input URL and returns the probed stream list.
func probeInput(inp Input) ([]av.StreamInfo, error) {
	ctx, err := av.OpenInput(inp.URL, nil)
	if err != nil {
		return nil, err
	}
	defer ctx.Close()
	return ctx.AllStreams()
}

// sourceStreamForEncoder walks backward through the graph from node along edges
// of the given portType and returns the first source input found, along with the
// StreamSelect entry declared in cfg that matches portType.
// Returns "", nil if no path exists.
func sourceStreamForEncoder(g *graph.Graph, node *graph.Node, portType graph.PortType, cfg *Config) (string, *StreamSelect) {
	if g == nil || node == nil {
		return "", nil
	}
	visited := make(map[string]bool)
	return walkBackToSource(node, portType, cfg, visited)
}

func walkBackToSource(cur *graph.Node, portType graph.PortType, cfg *Config, visited map[string]bool) (string, *StreamSelect) {
	if visited[cur.ID] {
		return "", nil
	}
	visited[cur.ID] = true
	for _, e := range cur.Inbound {
		if e.Type != portType {
			continue
		}
		prev := e.From
		if prev.Kind == graph.KindSource {
			// Find matching StreamSelect in cfg for this input + type.
			for i := range cfg.Inputs {
				if cfg.Inputs[i].ID != prev.ID {
					continue
				}
				for j := range cfg.Inputs[i].Streams {
					ss := &cfg.Inputs[i].Streams[j]
					if ss.Type == string(portType) {
						return prev.ID, ss
					}
				}
			}
			return prev.ID, nil
		}
		if inputID, ss := walkBackToSource(prev, portType, cfg, visited); inputID != "" {
			return inputID, ss
		}
	}
	return "", nil
}

// encoderMediaType returns the media type of the first inbound edge of node,
// or "" if none.
func encoderMediaType(node *graph.Node) graph.PortType {
	for _, e := range node.Inbound {
		return e.Type
	}
	return ""
}

// encoderCodec returns the codec name from a node's Params, or "".
func encoderCodec(node *graph.Node) string {
	if node.Params == nil {
		return ""
	}
	if v, ok := node.Params["codec"]; ok {
		return fmt.Sprintf("%v", v)
	}
	return ""
}

// findProbedStream returns the ss.Track-th stream of ss.Type from streams,
// mirroring resolveStreamSelection's counting logic.  ss.InputIndex is the
// FFmpeg file index (for multi-file inputs) and is NOT the array index — do
// not use it here.  Returns (zero, false) when no match is found.
func findProbedStream(ss *StreamSelect, streams []av.StreamInfo) (av.StreamInfo, bool) {
	count := 0
	for _, si := range streams {
		if si.Type.String() != ss.Type {
			continue
		}
		if ss.All || count == ss.Track {
			return si, true
		}
		count++
	}
	return av.StreamInfo{}, false
}

// pixFmtConversionFilters is the set of filter names that explicitly change the
// pixel format of a video stream, making the source stream's pixel format
// irrelevant for downstream encoder compatibility checks.
var pixFmtConversionFilters = map[string]bool{
	"format": true,
	"zscale": true,
}
