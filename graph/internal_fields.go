// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package graph

// Internal carries the typed lowering output produced by
// job.NormalizeConfig and consumed by the runtime handlers.
// It is the typed replacement for the historical __* sentinel keys
// that lived in NodeDef.Params (e.g. __fps_mode, __force_key_frames,
// __pass, __sar, __dar, __enc_time_base, __field_order,
// __interlaced, __filter_threads, __loudnorm_pass,
// __loudnorm_stats).
//
// Only fields relevant to the node's Kind are populated. The
// per-kind sub-structs are pointers so the JSON marshalled form of a
// NodeDef stays compact for nodes that carry no internal state.
//
// All fields are populated by NormalizeConfig and read by the
// runtime; nothing in this struct ever reaches an FFmpeg AVOption /
// libavfilter expression directly.
//
// See docs/field-ownership.md and
// private_local/normalization_plan_revised.md (Milestone B).
type Internal struct {
	// Encoder, when non-nil, carries lowering state for a synthetic
	// or user-declared encoder node (NodeKind == KindEncoder).
	Encoder *EncoderInternal `json:"encoder,omitempty"`
	// Filter, when non-nil, carries lowering state for a filter
	// node (NodeKind == KindFilter / KindFilterSource /
	// KindFilterSink).
	Filter *FilterInternal `json:"filter,omitempty"`
	// Generated, when non-nil, records that the node was synthesised
	// by NormalizeConfig (rather than authored by the user) and
	// carries provenance for diagnostics.
	Generated *GeneratedNode `json:"generated,omitempty"`
}

// EncoderInternal is the typed encoder lowering bag.
type EncoderInternal struct {
	// FPSMode mirrors FFmpeg's `-fps_mode {passthrough|cfr|vfr|drop}`
	// and drives handleEncoder's per-frame renumberer. Empty means
	// "no rewrite" (passthrough).
	FPSMode string `json:"fps_mode,omitempty"`
	// ForceKeyFrames is the raw `-force_key_frames` spec; the
	// encoder handler parses it into a forceKeyFramesMatcher.
	ForceKeyFrames string `json:"force_key_frames,omitempty"`
	// SAR is the raw `-aspect`/`setsar` shorthand; the encoder
	// handler resolves it to a numeric SampleAspectRatio once the
	// encoder's width/height are known. Mutually exclusive with DAR.
	SAR string `json:"sar,omitempty"`
	// DAR is the raw `-aspect`/`setdar` shorthand. Mutually
	// exclusive with SAR.
	DAR string `json:"dar,omitempty"`
	// EncoderTimeBase is the raw `-enc_time_base` shorthand
	// (rational "N/D" or sentinel "demux" / "filter").
	EncoderTimeBase string `json:"enc_time_base,omitempty"`
	// FieldOrder mirrors FFmpeg's `-field_order` ("tt", "bb", "tb", "bt").
	FieldOrder string `json:"field_order,omitempty"`
	// Interlaced mirrors FFmpeg's `-flags +ilme+ildct` shortcut.
	Interlaced bool `json:"interlaced,omitempty"`
	// Pass is the two-pass video encoding pass number (1, 2, or 3
	// for `-pass 1|2|3`). Zero means single-pass.
	Pass int `json:"pass,omitempty"`
	// PassLogFile is the user-supplied prefix; the encoder handler
	// sanitises it and appends "-<PassIndex>.log" before opening.
	PassLogFile string `json:"passlogfile,omitempty"`
	// PassIndex is the global ordinal of this encoder among all
	// two-pass video encoders in the run, assigned by
	// NormalizeConfig in node-declaration order.
	PassIndex int `json:"pass_index,omitempty"`
}

// FilterInternal is the typed filter lowering bag.
type FilterInternal struct {
	// Threads is the per-graph thread cap written to the filter's
	// AVFilterGraph.nb_threads. Mirrors FFmpeg's `-filter_threads`.
	Threads int `json:"threads,omitempty"`
	// LoudnormPass and LoudnormStatsFile carry the EBU R128
	// two-pass shuttle state for `loudnorm` filter nodes; see
	// pipeline/loudnorm.go for the protocol.
	LoudnormPass      int    `json:"loudnorm_pass,omitempty"`
	LoudnormStatsFile string `json:"loudnorm_stats,omitempty"`
}

// GeneratedNode records provenance for nodes synthesised by
// NormalizeConfig. Populated for "__enc__*", "__async__*",
// "__aspl__*", and the loudnorm shuttle's stamped nodes. Diagnostic
// only — runtime decisions never branch on these fields.
type GeneratedNode struct {
	// By identifies the lowering pass that synthesised the node
	// (e.g. "expandImplicitEncoders", "spliceAudioSyncForOutputs",
	// "spliceAudioAdaptersForEncoders", "applyLoudnormShuttle").
	By string `json:"by"`
	// From is the authoring entity that triggered the synthesis
	// (e.g. an output ID for __enc__/__async__ nodes, or a filter
	// node ID for __aspl__ nodes).
	From string `json:"from,omitempty"`
	// Reason is a short human-readable explanation suitable for the
	// inspect/debug CLI.
	Reason string `json:"reason,omitempty"`
}
