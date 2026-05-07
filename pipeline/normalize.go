// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"github.com/MediaMolder/MediaMolder/graph"
)

// NormalizeWarning is a non-fatal observation produced during
// normalization (the lowering pass that converts an authoring
// pipeline.Config into an executable graph.Def). Warnings are
// deterministic and intended for the inspect/debug CLI; the runtime
// surfaces them through the existing Pipeline events channel.
//
// Wave: normalization-boundary (Milestone B of
// docs/field-ownership.md / private_local/normalization_plan_revised.md).
type NormalizeWarning struct {
	// Code is a stable machine-readable identifier
	// (e.g. "compat.output_encoder_shorthand_ignored").
	Code string
	// Message is a human-readable explanation.
	Message string
	// Path is an optional JSON-pointer-style location into the
	// authoring config (e.g. "outputs[0].codec_video").
	Path string
}

// NormalizeConfig lowers an authoring pipeline.Config into the
// executable graph.Def the runtime consumes, returning any
// non-fatal warnings produced along the way.
//
// This is the single entry point for "config → executable graph"
// conversion: every code path that previously called
// configToGraphDef should call NormalizeConfig instead. The
// boundary exists so that, after Milestone C, runtime code reads
// only node-local fields on the returned graph.Def — never
// FFmpeg-style shorthand on Config.Outputs / Config.GlobalOptions.
//
// Today NormalizeConfig is a thin wrapper around the existing
// configToGraphDef + expandImplicitEncoders + audio-sync /
// audio-adapter / loudnorm-shuttle splicing. Subsequent commits in
// the normalization-boundary effort move that logic behind this
// function and replace the in-graph __* sentinel keys with a typed
// NodeDef.Internal sub-struct (see docs/field-ownership.md).
//
// Contract:
//   - NormalizeConfig must not mutate the input *Config.
//   - NormalizeConfig must be deterministic: the same input
//     produces the same graph.Def and the same warnings in the
//     same order.
//   - Errors are reserved for cross-output validation conditions
//     that Validate() did not catch (rare; see applyLoudnormShuttle
//     for the only current case).
func NormalizeConfig(cfg *Config) (*graph.Def, []NormalizeWarning, error) {
	def := configToGraphDef(cfg)
	// Warnings list is intentionally empty for now: the existing
	// lowering helpers panic on the impossible cross-output
	// validation cases and otherwise produce no diagnostics.
	// Subsequent commits route compat-shorthand-ignored and
	// generated-node provenance through this slice.
	return def, nil, nil
}
