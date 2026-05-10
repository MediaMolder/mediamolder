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
//
// This file also owns codec-specific encoder-params routing: for encoders
// that expose an FFmpeg "-<codec>-params" flag (libx264, libx265,
// libsvtav1, librav1e, libxavs2), all non-reserved AVOption keys are
// packed into that single flag rather than emitted as individual
// "-<key>:<stream> <val>" pairs. This produces the form a user would
// hand-author (e.g. "-x264-params crf=22:preset=slow:me=hex:subme=8")
// and is the canonical path for the long tail of encoder-private
// options that have no first-class FFmpeg flag.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/MediaMolder/MediaMolder/graph"
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

// resolveOutputViewFromGraph builds an outputView from the normalized
// graph (def), layered on top of the shorthand-sourced view as a
// fallback. The graph wins for any slot it actually fills:
//
//   - The codec is read from the encoder/copy node directly upstream
//     (Params["codec"] for encoder, "copy" for copy).
//   - Encoder AVOptions come from that node's Params (reservedEncoderParamKey
//     filtered) — covers both user-authored and "__enc__*" synthetic
//     encoder nodes from expandImplicitEncoders.
//   - Encoder shorthand (FPSMode, ForceKeyFrames, SAR, DAR,
//     EncoderTimeBase, FieldOrder, Interlaced, Pass, PassLogFile)
//     is read from the node's typed Internal.Encoder (populated by
//     NormalizeConfig from Output.* shorthand).
//   - AudioSync is recovered from any "__async__*" filter node
//     synthesised by spliceAudioSyncForOutputs that appears upstream
//     of the output's audio edge — its Filter spec is parsed back
//     from "aresample=async=N[:first_pts=0]" to N.
//
// Slots the graph does not fill (e.g. an output with no edges in
// cfg.Graph) inherit the shorthand value, which keeps round-trip
// parity with Export(cfg) for the wide range of configs that have
// not yet been fully lowered to a graph.
//
// Per-stream Output.Streams[i].Encoder overrides are NOT lowered to
// graph nodes today, so resolveOutputViewFromGraph does not see them;
// the caller (buildOutput) handles those separately by reading from
// cfg.Outputs.
func resolveOutputViewFromGraph(cfg *pipeline.Config, def *graph.Def, out pipeline.Output) outputView {
	// Start from shorthand so unfilled slots round-trip cleanly.
	v := resolveOutputViewFromConfig(cfg, out)
	if def == nil {
		return v
	}
	nodeByID := make(map[string]graph.NodeDef, len(def.Nodes))
	for _, n := range def.Nodes {
		nodeByID[n.ID] = n
	}

	// First pass: walk edges that terminate AT the output. Find the
	// encoder/copy node feeding each per-type slot.
	for _, edge := range def.Edges {
		if portNode(edge.To) != out.ID {
			continue
		}
		typ := portType(edge.To, edge.Type)
		ev := pickEncoderView(&v, typ)
		if ev == nil {
			continue
		}
		n, ok := nodeByID[portNode(edge.From)]
		if !ok {
			continue
		}
		switch n.Type {
		case "copy":
			ev.Codec = "copy"
			ev.Params = nil
		case "encoder":
			// Explicit (user-authored) encoder nodes already have
			// their codec picked up by graphCodecsForOutput in the
			// shorthand seed, and their Params are emitted by
			// (*exporter).buildEncoderNodes (which walks
			// cfg.Graph.Nodes in declaration order). Only override
			// the per-slot view for synthesised encoders — the
			// "__enc__*" nodes stamped by expandImplicitEncoders —
			// where the typed Internal.Encoder carries the lowered
			// shorthand and Params hold the lowered EncoderParams*.
			if n.Internal.Generated == nil {
				continue
			}
			if codec, _ := n.Params["codec"].(string); codec != "" {
				ev.Codec = codec
			}
			ev.Params = n.Params
			if enc := n.Internal.Encoder; enc != nil {
				ev.FPSMode = enc.FPSMode
				ev.ForceKeyFrames = enc.ForceKeyFrames
				ev.SAR = enc.SAR
				ev.DAR = enc.DAR
				ev.EncTimeBase = enc.EncoderTimeBase
				ev.FieldOrder = enc.FieldOrder
				ev.Interlaced = enc.Interlaced
				ev.Pass = enc.Pass
				ev.PassLogFile = enc.PassLogFile
			}
		}
	}

	// Second pass: recover AudioSync by walking back from the
	// output's audio edge to find an "__async__" filter synthesised
	// by spliceAudioSyncForOutputs. Only override the shorthand
	// fallback when we actually find one.
	if n := recoverAudioSyncFromGraph(def, nodeByID, out.ID); n > 0 {
		v.AudioSync = n
	}

	return v
}

// pickEncoderView returns a pointer to the per-type encoderView slot
// of v matching typ ("v"/"a"/"s"), or nil for unknown types.
func pickEncoderView(v *outputView, typ string) *encoderView {
	switch typ {
	case "v":
		return &v.Video
	case "a":
		return &v.Audio
	case "s":
		return &v.Subtitle
	}
	return nil
}

// recoverAudioSyncFromGraph walks edges backwards from outID's audio
// inputs looking for an "__async__" filter node whose Filter spec
// matches "aresample=async=N[:first_pts=0]" (the form emitted by
// pipeline.spliceAudioSyncForOutputs). Returns the recovered N, or
// 0 if no such node is reachable.
func recoverAudioSyncFromGraph(def *graph.Def, nodeByID map[string]graph.NodeDef, outID string) int {
	// Build a reverse adjacency: nodeID -> incoming edges.
	rev := make(map[string][]graph.EdgeDef, len(def.Edges))
	for _, e := range def.Edges {
		rev[portNode(e.To)] = append(rev[portNode(e.To)], e)
	}
	visited := make(map[string]bool)
	queue := []string{outID}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if visited[cur] {
			continue
		}
		visited[cur] = true
		for _, e := range rev[cur] {
			fromID := portNode(e.From)
			if strings.HasPrefix(fromID, "__async__") {
				if n, ok := nodeByID[fromID]; ok {
					if n := parseAresampleAsync(n.Filter); n > 0 {
						return n
					}
				}
			}
			queue = append(queue, fromID)
		}
	}
	return 0
}

// parseAresampleAsync parses an "aresample=async=N[:first_pts=0]"
// filter spec back into N. Returns 0 on any mismatch.
func parseAresampleAsync(spec string) int {
	const prefix = "aresample=async="
	if !strings.HasPrefix(spec, prefix) {
		return 0
	}
	rest := spec[len(prefix):]
	if i := strings.Index(rest, ":"); i >= 0 {
		rest = rest[:i]
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 1 {
		return 0
	}
	return n
}

// codecToParamsFlag enumerates the encoders whose private options are
// commonly passed via a single FFmpeg "-<flag>" argument carrying a
// colon-separated list of "key=value" pairs. The mapping value is the
// flag name without the leading dash.
//
//   - libx264 / libx264rgb: "x264-params" — see libavcodec/libx264.c
//     (X264_OPT_LIST in libavcodec's option table).
//   - libx265: "x265-params" — see libavcodec/libx265.c.
//   - libsvtav1: "svtav1-params" — see libavcodec/libsvtav1.c.
//   - librav1e: "rav1e-params" — see libavcodec/librav1e.c.
//   - libxavs2: "xavs2-params" — see libavcodec/libxavs2.c.
//
// Encoders absent from this map keep the legacy per-key emission
// (-<key>:<stream> <val>) because they have no analogous bulk
// parameter channel.
var codecToParamsFlag = map[string]string{
	"libx264":    "x264-params",
	"libx264rgb": "x264-params",
	"libx265":    "x265-params",
	"libsvtav1":  "svtav1-params",
	"librav1e":   "rav1e-params",
	"libxavs2":   "xavs2-params",
}

// reservedEncoderParamKey returns true for AVOption-map keys that the
// runtime treats as out-of-band (handled by other emitters: -c:<type>,
// -b:<type>, -s, -threads, -thread_type) or as internal Milestone-B
// sentinels that must never reach the CLI. Used by emitEncoderParams
// to filter both the per-key fallback and the "-<codec>-params"
// payload.
func reservedEncoderParamKey(k string) bool {
	if k == "codec" || strings.HasPrefix(k, "__") {
		return true
	}
	switch k {
	case "width", "height", "bitrate", "threads", "thread_type":
		return true
	}
	return false
}

// emitEncoderParams writes the encoder AVOption flags for the given
// per-stream specifier (e.g. "v", "v:0") and codec to the exporter's
// arg list. When the codec is in codecToParamsFlag the non-reserved
// keys are packed into a single "-<flag>:<stream> k1=v1:k2=v2..."
// argument; otherwise each key is emitted as its own
// "-<key>:<stream> <val>" pair (legacy behaviour).
//
// If the params map already contains an entry whose key is the
// codec's own *-params flag (e.g. params["x264-params"]), its value
// is treated as a literal extra payload appended to the packed
// argument so that user-supplied raw strings round-trip correctly.
func (e *exporter) emitEncoderParams(stream, codec string, params map[string]any) {
	if len(params) == 0 {
		return
	}
	flagName, useParamsFlag := codecToParamsFlag[codec]

	keys := make([]string, 0, len(params))
	for k := range params {
		if reservedEncoderParamKey(k) {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	if useParamsFlag {
		var pairs []string
		for _, k := range keys {
			v := params[k]
			if v == nil {
				continue
			}
			s := fmt.Sprint(v)
			if s == "" {
				continue
			}
			if k == flagName {
				// User pre-built a raw "-<codec>-params" payload;
				// append it verbatim instead of double-quoting.
				pairs = append(pairs, s)
				continue
			}
			pairs = append(pairs, k+"="+s)
		}
		if len(pairs) > 0 {
			e.add("-"+flagName+":"+stream, strings.Join(pairs, ":"))
		}
		return
	}

	// Generic per-key emission.
	for _, k := range keys {
		v := params[k]
		if v == nil {
			continue
		}
		s := fmt.Sprint(v)
		if s == "" {
			continue
		}
		e.add("-"+k+":"+stream, s)
	}
}

