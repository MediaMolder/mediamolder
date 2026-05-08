// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

// encoder_view.go — per-output encoder/timing source abstraction used by
// the FFmpeg-CLI exporter. F1.1 introduces this as a pure refactor:
// today the only source is `Output.*` shorthand (resolveOutputViewFromConfig);
// F1.2 will add a normalized-graph source (resolveOutputViewFromGraph) so
// that ExportGraph can run without reading shorthand fields. The formatter
// (buildOutput) reads only from outputView, never directly from
// `pipeline.Output` shorthand fields, which is what makes the F2 schema
// deprecation possible.

import (
	"github.com/MediaMolder/MediaMolder/pipeline"
)

// encoderView is the resolved encoder + timing state for a single
// per-edge-type encoder slot (video / audio / subtitle) of an Output.
// Empty fields mean "not specified by the source"; the formatter must
// not emit a flag for an empty field.
type encoderView struct {
	Codec          string         // -c:<type> value; "copy" for stream-copy
	Params         map[string]any // AVOptions (sorted-keys emit, per buildEncoderParams)
	FPSMode        string         // -fps_mode (video only)
	ForceKeyFrames string         // -force_key_frames (video only)
	SAR            string         // -aspect / setsar shorthand (video only)
	DAR            string         // -aspect / setdar shorthand (video only)
	EncTimeBase    string         // -enc_time_base
	FieldOrder     string         // -field_order (video only)
	Interlaced     bool           // -flags +ilme+ildct (video only)
	Pass           int            // -pass
	PassLogFile    string         // -passlogfile
}

// outputView aggregates the per-edge-type encoder views plus output-wide
// muxer-time fields whose source the formatter wants to abstract over.
type outputView struct {
	Video     encoderView
	Audio     encoderView
	Subtitle  encoderView
	AudioSync int // -async N (lowered from Output.AudioSync today)
}

// resolveOutputViewFromConfig builds an outputView from the Output's
// authoring shorthand fields, with explicit graph encoder/copy nodes
// taking precedence for the codec slot (matches today's
// graphCodecs/buildOutput precedence). This is the back-compat path:
// it preserves today's Export(cfg) semantics exactly. F1.2 adds a
// graph-aware resolver that takes precedence when a normalized graph
// is available.
func resolveOutputViewFromConfig(cfg *pipeline.Config, out pipeline.Output) outputView {
	v := outputView{
		Video: encoderView{
			Codec:          out.CodecVideo,
			Params:         out.EncoderParamsVideo,
			FPSMode:        out.FPSMode,
			ForceKeyFrames: out.ForceKeyFrames,
			SAR:            out.SAR,
			DAR:            out.DAR,
			EncTimeBase:    out.EncoderTimeBase,
			FieldOrder:     out.FieldOrder,
			Interlaced:     out.InterlacedEncode,
			Pass:           out.Pass,
			PassLogFile:    out.PassLogFile,
		},
		Audio: encoderView{
			Codec:  out.CodecAudio,
			Params: out.EncoderParamsAudio,
		},
		Subtitle: encoderView{
			Codec:  out.CodecSubtitle,
			Params: out.EncoderParamsSubtitle,
		},
		AudioSync: out.AudioSync,
	}
	// Explicit graph encoder/copy nodes override the Output codec
	// shorthand (matches today's precedence in buildOutput).
	for typ, codec := range graphCodecsForOutput(cfg, out.ID) {
		switch typ {
		case "v":
			v.Video.Codec = codec
		case "a":
			v.Audio.Codec = codec
		case "s":
			v.Subtitle.Codec = codec
		}
	}
	return v
}

// graphCodecsForOutput is the package-level helper that backs
// (*exporter).graphCodecs; lifted here so resolveOutputView can call
// it without going through an exporter instance.
func graphCodecsForOutput(cfg *pipeline.Config, outID string) map[string]string {
	if cfg == nil || len(cfg.Graph.Nodes) == 0 {
		return nil
	}
	nodeByID := make(map[string]pipeline.NodeDef, len(cfg.Graph.Nodes))
	for _, n := range cfg.Graph.Nodes {
		nodeByID[n.ID] = n
	}
	result := make(map[string]string)
	for _, edge := range cfg.Graph.Edges {
		if portNode(edge.To) != outID {
			continue
		}
		n, ok := nodeByID[portNode(edge.From)]
		if !ok {
			continue
		}
		typ := portType(edge.To, edge.Type)
		switch n.Type {
		case "copy":
			result[typ] = "copy"
		case "encoder":
			if codec, _ := n.Params["codec"].(string); codec != "" {
				result[typ] = codec
			}
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
