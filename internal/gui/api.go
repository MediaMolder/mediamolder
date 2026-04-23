// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package gui

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/processors"
)

// NodeCatalogEntry describes a node template the palette can present.
type NodeCatalogEntry struct {
	Category    string   `json:"category"` // "Sources" | "Filters" | "Encoders" | "Processors" | "Sinks"
	Type        string   `json:"type"`     // schema NodeDef.type ("filter", "encoder", "go_processor", ...)
	Name        string   `json:"name"`     // display name (also filter/codec/processor name)
	Description string   `json:"description,omitempty"`
	Streams     []string `json:"streams,omitempty"` // ["video"], ["audio"], or both
	NumInputs   int      `json:"num_inputs,omitempty"`
	NumOutputs  int      `json:"num_outputs,omitempty"`
}

// handleListNodes returns the node palette catalogue assembled from the live
// av/* and processors/* registries plus a few synthetic built-ins (input/output).
func handleListNodes(w http.ResponseWriter, _ *http.Request) {
	out := make([]NodeCatalogEntry, 0, 256)

	// Built-ins (sources & sinks). The palette uses these to spawn synthetic
	// input/output nodes that round-trip through jsonAdapter.ts.
	out = append(out,
		NodeCatalogEntry{Category: "Sources", Type: "input", Name: "Input", Description: "File or URL source"},
		NodeCatalogEntry{Category: "Sinks", Type: "output", Name: "Output", Description: "File or URL sink"},
	)

	// Filters from libavfilter.
	for _, f := range av.ListFilters() {
		// Only expose 1→1 filters in the basic palette; multi-IO filters can
		// still be added manually by editing JSON (Phase 3 will expose them).
		if f.NumInputs != 1 || f.NumOutputs != 1 {
			continue
		}
		out = append(out, NodeCatalogEntry{
			Category:    "Filters",
			Type:        "filter",
			Name:        f.Name,
			Description: f.Description,
			NumInputs:   f.NumInputs,
			NumOutputs:  f.NumOutputs,
		})
	}

	// Encoders from libavcodec.
	for _, c := range av.ListCodecs() {
		if !c.IsEncoder {
			continue
		}
		if c.Type != "video" && c.Type != "audio" && c.Type != "subtitle" {
			continue
		}
		out = append(out, NodeCatalogEntry{
			Category:    "Encoders",
			Type:        "encoder",
			Name:        c.Name,
			Description: c.LongName,
			Streams:     []string{c.Type},
		})
	}

	// Go processors from the in-process registry.
	for _, name := range processors.Names() {
		out = append(out, NodeCatalogEntry{
			Category: "Processors",
			Type:     "go_processor",
			Name:     name,
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return categoryOrder(out[i].Category) < categoryOrder(out[j].Category)
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func categoryOrder(c string) int {
	switch c {
	case "Sources":
		return 0
	case "Filters":
		return 1
	case "Encoders":
		return 2
	case "Processors":
		return 3
	case "Sinks":
		return 4
	default:
		return 5
	}
}
