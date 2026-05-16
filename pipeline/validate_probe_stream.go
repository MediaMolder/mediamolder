// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

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
		for _, ss := range inp.Streams {
			checkStreamSelect(inp.ID, ss, streams, r)
		}
	}
}

func checkStreamSelect(inputID string, ss StreamSelect, streams []av.StreamInfo, r *ValidationReport) {
	idx := ss.InputIndex
	if idx < 0 || idx >= len(streams) {
		r.add(ValidationIssue{
			Severity: SeverityError,
			Code:     "STREAM_INDEX_OUT_OF_RANGE",
			Location: fmt.Sprintf("input:%s", inputID),
			Message: fmt.Sprintf(
				"input %q has %d stream(s) but stream index %d was selected (type=%s)",
				inputID, len(streams), idx, ss.Type,
			),
			Suggestion: fmt.Sprintf(
				"check the stream count with 'mediamolder inspect %q' and use an index in [0, %d)",
				inputID, len(streams),
			),
		})
		return
	}

	probed := streams[idx]
	if ss.Type != "" && probed.Type.String() != ss.Type {
		r.add(ValidationIssue{
			Severity: SeverityError,
			Code:     "STREAM_TYPE_MISMATCH",
			Location: fmt.Sprintf("input:%s", inputID),
			Message: fmt.Sprintf(
				"input %q stream index %d has type %q but %q was declared",
				inputID, idx, probed.Type.String(), ss.Type,
			),
			Suggestion: fmt.Sprintf(
				"use the correct stream index for a %s stream, or update the type field",
				ss.Type,
			),
		})
	}
}
