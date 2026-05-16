// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"fmt"

	"github.com/MediaMolder/MediaMolder/graph"
)

// validateVideo performs static video format checks that require no probe data.
// Checks VIDEO_ZERO_DIMENSION on encoder and scale nodes, VIDEO_ZERO_FRAMERATE
// on fps filter nodes.
func validateVideo(cfg *Config, g *graph.Graph, r *ValidationReport) {
	for _, nd := range cfg.Graph.Nodes {
		switch {
		case nd.Type == "encoder":
			checkEncoderDimensions(nd, r)
		case nd.Type == "filter" && nd.Filter == "fps":
			checkFpsFilterZero(nd, r)
		case nd.Type == "filter" && nd.Filter == "scale":
			checkScaleZeroDimension(nd, r)
		}
	}
}

func checkEncoderDimensions(nd NodeDef, r *ValidationReport) {
	for _, key := range []string{"width", "w", "height", "h"} {
		v, ok := nd.Params[key]
		if !ok {
			continue
		}
		// Only flag literal "0"; expressions like "iw/2" are handled at runtime.
		if fmt.Sprintf("%v", v) != "0" {
			continue
		}
		r.add(ValidationIssue{
			Severity:   SeverityError,
			Code:       "VIDEO_ZERO_DIMENSION",
			Location:   "node:" + nd.ID,
			Message:    fmt.Sprintf("encoder node %q has %s=0; encoders require positive dimensions", nd.ID, key),
			Suggestion: "set a positive width and height in the encoder params",
		})
	}
}

func checkFpsFilterZero(nd NodeDef, r *ValidationReport) {
	for _, key := range []string{"fps", "r", "rate"} {
		v, ok := nd.Params[key]
		if !ok {
			continue
		}
		if isZeroRateParam(v) {
			r.add(ValidationIssue{
				Severity:   SeverityError,
				Code:       "VIDEO_ZERO_FRAMERATE",
				Location:   "node:" + nd.ID,
				Message:    fmt.Sprintf("fps filter node %q has %s=0; frame rate must be positive", nd.ID, key),
				Suggestion: "set a positive frame rate, e.g. fps=30 or fps=24000/1001",
			})
		}
	}
}

func checkScaleZeroDimension(nd NodeDef, r *ValidationReport) {
	for _, key := range []string{"w", "width", "h", "height"} {
		v, ok := nd.Params[key]
		if !ok {
			continue
		}
		if fmt.Sprintf("%v", v) != "0" {
			continue
		}
		r.add(ValidationIssue{
			Severity:   SeverityError,
			Code:       "VIDEO_ZERO_DIMENSION",
			Location:   "node:" + nd.ID,
			Message:    fmt.Sprintf("scale filter node %q has %s=0", nd.ID, key),
			Suggestion: "use a positive dimension value or a valid expression such as iw/2",
		})
	}
}
