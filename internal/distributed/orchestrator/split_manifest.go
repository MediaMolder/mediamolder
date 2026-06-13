// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package orchestrator

// splitManifest is the JSON document produced by a split_manifest_writer
// processor (in the processors package) and consumed by materializeFanoutDynamic.
// The JSON tags must match the processors.splitManifest definition — the two
// types share only a JSON wire format, not a Go type, to avoid an import cycle
// (pipeline → processors → pipeline).
type splitManifest struct {
	Splitter string         `json:"splitter"`
	InputURI string         `json:"input_uri,omitempty"`
	Segments []splitSegment `json:"segments"`
}

// splitSegment describes one temporal slice of the source media.
type splitSegment struct {
	Index    int     `json:"index"`
	InPoint  float64 `json:"inpoint"`
	OutPoint float64 `json:"outpoint"`
}
