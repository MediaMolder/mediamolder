// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"fmt"

	"github.com/MediaMolder/MediaMolder/graph"
)

// NormalizeWarning is a non-fatal observation produced during
// normalization (the lowering pass that converts an authoring
// job.Config into an executable graph.Def). Warnings are
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

// NormalizeConfig lowers an authoring job.Config into the
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
	warnings := detectEncoderShorthandAmbiguity(cfg, def)
	warnings = append(warnings, networkInputWarnings(cfg.Inputs)...)
	return def, warnings, nil
}

// detectEncoderShorthandAmbiguity reports
// `compat.output_encoder_shorthand_ignored` warnings for every
// authoring shorthand field on an Output whose encoder is satisfied
// by an explicit graph node (so the shorthand is silently ignored
// by expandImplicitEncoders). Milestone C of
// private_local/normalization_plan_revised.md: surface the conflict
// instead of silently dropping the user's input.
//
// Two shapes are detected:
//
//  1. Per-edge: an output edge whose source is already a user-defined
//     `encoder` or `copy` node, while the corresponding `Output` row
//     still carries `CodecVideo` / `EncoderParamsVideo` / `FPSMode` /
//     etc. The explicit node wins; the shorthand is dropped.
//  2. Per-stream: an `Output.Streams[i].Encoder` override that
//     references a stream whose source is an explicit encoder node.
//     The override is meaningful only for synthetic encoders.
func detectEncoderShorthandAmbiguity(cfg *Config, def *graph.Def) []NormalizeWarning {
	if cfg == nil || def == nil {
		return nil
	}
	nodeByID := make(map[string]graph.NodeDef, len(def.Nodes))
	for _, n := range def.Nodes {
		nodeByID[n.ID] = n
	}
	head := func(ref string) string {
		for i := 0; i < len(ref); i++ {
			if ref[i] == ':' {
				return ref[:i]
			}
		}
		return ref
	}
	// Map output ID → (edge type → has explicit-encoder source).
	explicitByOutput := make(map[string]map[string]bool)
	for _, e := range def.Edges {
		toID := head(e.To)
		fromID := head(e.From)
		n, ok := nodeByID[fromID]
		if !ok {
			continue
		}
		if n.Type != "encoder" && n.Type != "copy" {
			continue
		}
		// Synthetic encoders inserted by expandImplicitEncoders carry
		// Internal.Generated provenance. Those nodes ARE the
		// shorthand's lowered form; they don't conflict with it.
		if n.Internal.Generated != nil && n.Internal.Generated.By == "expandImplicitEncoders" {
			continue
		}
		if _, isOut := outputIndex(cfg, toID); !isOut {
			continue
		}
		if explicitByOutput[toID] == nil {
			explicitByOutput[toID] = make(map[string]bool)
		}
		explicitByOutput[toID][e.Type] = true
	}
	if len(explicitByOutput) == 0 {
		return nil
	}
	var warnings []NormalizeWarning
	emit := func(out Output, idx int, typ, field, jsonName string, val any) {
		if isZero(val) {
			return
		}
		warnings = append(warnings, NormalizeWarning{
			Code: "compat.output_encoder_shorthand_ignored",
			Message: fmt.Sprintf(
				"output %q has an explicit %s encoder node; shorthand field %q is ignored",
				out.ID, typ, field),
			Path: fmt.Sprintf("outputs[%d].%s", idx, jsonName),
		})
	}
	for i, out := range cfg.Outputs {
		typesWithExplicit := explicitByOutput[out.ID]
		if len(typesWithExplicit) == 0 {
			continue
		}
		if typesWithExplicit["video"] {
			emit(out, i, "video", "CodecVideo", "codec_video", out.CodecVideo)
			emit(out, i, "video", "EncoderParamsVideo", "encoder_params_video", out.EncoderParamsVideo)
			emit(out, i, "video", "FPSMode", "fps_mode", out.FPSMode)
			emit(out, i, "video", "ForceKeyFrames", "force_key_frames", out.ForceKeyFrames)
			emit(out, i, "video", "SAR", "sar", out.SAR)
			emit(out, i, "video", "DAR", "dar", out.DAR)
			emit(out, i, "video", "EncoderTimeBase", "enc_time_base", out.EncoderTimeBase)
			emit(out, i, "video", "FieldOrder", "field_order", out.FieldOrder)
			emit(out, i, "video", "InterlacedEncode", "interlaced_encode", out.InterlacedEncode)
			emit(out, i, "video", "Pass", "pass", out.Pass)
			emit(out, i, "video", "PassLogFile", "passlogfile", out.PassLogFile)
		}
		if typesWithExplicit["audio"] {
			emit(out, i, "audio", "CodecAudio", "codec_audio", out.CodecAudio)
			emit(out, i, "audio", "EncoderParamsAudio", "encoder_params_audio", out.EncoderParamsAudio)
		}
		if typesWithExplicit["subtitle"] {
			emit(out, i, "subtitle", "CodecSubtitle", "codec_subtitle", out.CodecSubtitle)
			emit(out, i, "subtitle", "EncoderParamsSubtitle", "encoder_params_subtitle", out.EncoderParamsSubtitle)
		}
		// Per-stream Encoder overrides also lose to explicit nodes.
		for si, ss := range out.Streams {
			if ss.Encoder == nil {
				continue
			}
			var typ string
			switch ss.Type {
			case "v":
				typ = "video"
			case "a":
				typ = "audio"
			case "s":
				typ = "subtitle"
			}
			if typ == "" || !typesWithExplicit[typ] {
				continue
			}
			warnings = append(warnings, NormalizeWarning{
				Code: "compat.output_encoder_shorthand_ignored",
				Message: fmt.Sprintf(
					"output %q has an explicit %s encoder node; per-stream Encoder override on streams[%d] is ignored",
					out.ID, typ, si),
				Path: fmt.Sprintf("outputs[%d].streams[%d].encoder", i, si),
			})
		}
	}
	return warnings
}

// outputIndex returns (i, true) if id matches cfg.Outputs[i].ID.
func outputIndex(cfg *Config, id string) (int, bool) {
	for i := range cfg.Outputs {
		if cfg.Outputs[i].ID == id {
			return i, true
		}
	}
	return 0, false
}

// isZero reports whether v is the zero value for its type, used by
// detectEncoderShorthandAmbiguity to skip un-set shorthand fields.
func isZero(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case int:
		return x == 0
	case bool:
		return !x
	case map[string]any:
		return len(x) == 0
	default:
		return false
	}
}
