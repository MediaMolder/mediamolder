// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: GPL-3.0-or-later

package pipeline

import (
	"fmt"

	"github.com/MediaMolder/MediaMolder/graph"
)

// crossMediaTypeFilters maps libavfilter filter names whose output media
// type differs from their input media type to the produced media type.
// Mirrors libavfilter's `avf_*` (audio-in, video-out) registration prefix
// — every entry below corresponds to an `extern const FFFilter ff_avf_…`
// declaration in libavfilter/allfilters.c and a source file under
// libavfilter/avf_*.c.
//
// When a filter node names one of these filters, the pipeline validator
// requires every outbound edge's `type` to match the produced type and
// the runtime forces the node through the complex-filter-graph code path
// (NewComplexFilterGraph) so the buffersink media type is set
// independently of the buffersrc media type. (Wave 7 #37)
var crossMediaTypeFilters = map[string]graph.PortType{
	"a3dscope":       graph.PortVideo,
	"abitscope":      graph.PortVideo,
	"adrawgraph":     graph.PortVideo,
	"agraphmonitor":  graph.PortVideo,
	"ahistogram":     graph.PortVideo,
	"aphasemeter":    graph.PortVideo,
	"avectorscope":   graph.PortVideo,
	"showcqt":        graph.PortVideo,
	"showcwt":        graph.PortVideo,
	"showfreqs":      graph.PortVideo,
	"showspatial":    graph.PortVideo,
	"showspectrum":   graph.PortVideo,
	"showspectrumpic": graph.PortVideo,
	"showvolume":     graph.PortVideo,
	"showwaves":      graph.PortVideo,
	"showwavespic":   graph.PortVideo,
}

// validateCrossMediaTypeFilters enforces that filter nodes naming a
// known cross-media-type filter:
//
//   - Either declare `output_media_type` matching the registry entry, or
//     omit it (the runtime will route based on the registry).
//   - Have every outbound edge `type` matching the produced media type.
//
// Catches mis-wired waveform / spectrogram graphs at config-load time
// rather than as an opaque libavfilter pad-type mismatch hundreds of
// milliseconds into job startup.
func validateCrossMediaTypeFilters(cfg *Config) error {
	for i, node := range cfg.Graph.Nodes {
		if node.Type != "filter" {
			continue
		}
		want, isCross := crossMediaTypeFilters[node.Filter]
		if !isCross {
			if node.OutputMediaType != "" {
				switch graph.PortType(node.OutputMediaType) {
				case graph.PortVideo, graph.PortAudio, graph.PortSubtitle, graph.PortData:
				default:
					return fmt.Errorf("node[%d] %q: invalid output_media_type %q", i, node.ID, node.OutputMediaType)
				}
			}
			continue
		}
		if node.OutputMediaType != "" && graph.PortType(node.OutputMediaType) != want {
			return fmt.Errorf("node[%d] %q: filter %q produces %s, but output_media_type=%q", i, node.ID, node.Filter, want, node.OutputMediaType)
		}
		for _, e := range cfg.Graph.Edges {
			if !edgeFromsNode(e.From, node.ID) {
				continue
			}
			if graph.PortType(e.Type) != want {
				return fmt.Errorf("node[%d] %q: filter %q produces %s, but outbound edge to %q has type %q", i, node.ID, node.Filter, want, e.To, e.Type)
			}
		}
	}
	return nil
}

// edgeFromsNode reports whether an edge endpoint reference (e.g.
// "myfilter", "myfilter:default", "myfilter:0") names the given node ID.
func edgeFromsNode(ref, nodeID string) bool {
	if ref == nodeID {
		return true
	}
	for i := 0; i < len(ref); i++ {
		if ref[i] == ':' {
			return ref[:i] == nodeID
		}
	}
	return false
}
