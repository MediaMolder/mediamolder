// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"fmt"

	"github.com/MediaMolder/MediaMolder/av"
)

// validateProbeStreams checks that each declared StreamSelect references a valid
// stream index and that the declared type matches the probed stream type.
func validateProbeStreams(cfg *Config, probed map[string][]av.StreamInfo, r *ValidationReport) {
	for _, inp := range cfg.Inputs {
		streams, ok := probed[inp.ID]
		if !ok {
			continue // probe failed — already reported
		}
		consumers := configInputConsumers(cfg.Graph.Edges, inp.ID)
		for _, ss := range inp.Streams {
			// An input stream that no edge consumes is never demuxed, so
			// don't reject the job when the file lacks it (mirrors the
			// runtime selection in openSource).
			if streamSelectionDropped(consumers, ss) {
				continue
			}
			checkStreamSelect(inp.ID, ss, streams, r)
		}
	}
}

func checkStreamSelect(inputID string, ss StreamSelect, streams []av.StreamInfo, r *ValidationReport) {
	if ss.Type == "" {
		return
	}
	// Find the ss.Track-th stream of ss.Type, mirroring resolveStreamSelection.
	// InputIndex is the FFmpeg file index (for multi-file inputs), not the
	// stream array index — do not use it here.
	_, ok := findProbedStream(&ss, streams)
	if ok || ss.Optional {
		return
	}
	typeCount := 0
	for _, si := range streams {
		if si.Type.String() == ss.Type {
			typeCount++
		}
	}
	r.add(ValidationIssue{
		Severity: SeverityError,
		Code:     "STREAM_INDEX_OUT_OF_RANGE",
		Location: fmt.Sprintf("input:%s", inputID),
		Message: fmt.Sprintf(
			"input %q has %d %s stream(s) but track %d was selected",
			inputID, typeCount, ss.Type, ss.Track,
		),
		Suggestion: fmt.Sprintf(
			"check the stream count with 'mediamolder inspect %q' and use a track in [0, %d)",
			inputID, typeCount,
		),
	})
}
