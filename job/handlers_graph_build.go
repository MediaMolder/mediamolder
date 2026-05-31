// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"fmt"
	"strings"

	"github.com/MediaMolder/MediaMolder/graph"
)

func configToGraphDef(cfg *Config) *graph.Def {
	def := &graph.Def{}
	for _, inp := range cfg.Inputs {
		def.Inputs = append(def.Inputs, graph.InputDef{ID: inp.ID})
	}
	for _, node := range cfg.Graph.Nodes {
		// metadata_reader / metadata_writer nodes (Wave 2 #11) do
		// not move media frames; they are resolved by the runtime
		// at WriteHeader time via r.cfg.Graph directly. Skip them
		// here so the DAG compiler does not require media edges
		// for them.
		if node.Type == "metadata_reader" || node.Type == "metadata_writer" {
			continue
		}
		params := node.Params
		var internal graph.Internal
		// Wave 7 #38: propagate per-node thread cap (or pipeline-wide
		// default) into the runtime's filter graph allocator via the
		// node's typed Internal field. Per-node `Threads` wins over the
		// pipeline-wide `Config.FilterComplexThreads`. Both are applied
		// only to filter nodes — encoders have their own `threads`
		// AVOption.
		if node.Type == "filter" {
			eff := node.Threads
			if eff == 0 {
				eff = cfg.FilterComplexThreads
			}
			if eff > 0 {
				internal.Filter = &graph.FilterInternal{Threads: eff}
			}
		}
		// Wave 7 #37: auto-fill OutputMediaType from the cross-media-type
		// registry when the user did not declare one, so the runtime can
		// route showwavespic / showspectrum / showvolume / ... through the
		// complex-filter-graph path without the user having to spell it out
		// in the JSON.
		omt := graph.PortType(node.OutputMediaType)
		if omt == "" && node.Type == "filter" {
			if pt, ok := crossMediaTypeFilters[node.Filter]; ok {
				omt = pt
			}
		}
		def.Nodes = append(def.Nodes, graph.NodeDef{
			ID:              node.ID,
			Type:            node.Type,
			Filter:          node.Filter,
			Processor:       node.Processor,
			Params:          params,
			OutputMediaType: omt,
			Device:          node.Device,
			AutoMapHW:       node.AutoMapHW,
			Internal:        internal,
		})
	}
	for _, out := range cfg.Outputs {
		def.Outputs = append(def.Outputs, graph.OutputDef{ID: out.ID})
	}
	// Index outputs by ID for the disable-by-media-type filter below.
	outByID := make(map[string]*Output, len(cfg.Outputs))
	for i := range cfg.Outputs {
		outByID[cfg.Outputs[i].ID] = &cfg.Outputs[i]
	}
	for _, e := range cfg.Graph.Edges {
		// Skip metadata-routing, events-routing, and file-routing edges —
		// they are runtime-only annotations and carry no AV frames.
		if e.Type == "metadata" || e.Type == "events" || e.Type == "file" {
			continue
		}
		// Drop edges feeding a sink whose Output has the corresponding
		// `-vn`/`-an`/`-sn`/`-dn` flag set. Filtering here, before
		// expandImplicitEncoders, prevents the implicit-encoder pass
		// from synthesising an encoder for the disabled type and
		// keeps the stream-copy path from registering a copy stream
		// at the sink. Mirrors fftools/ffmpeg_opt.c L1977/2078/2115/2187
		// (the OPT_OUTPUT half of the dual-purpose disable flags).
		if out := outByID[e.To]; out != nil {
			switch e.Type {
			case "video":
				if out.DisableVideo {
					continue
				}
			case "audio":
				if out.DisableAudio {
					continue
				}
			case "subtitle":
				if out.DisableSubtitle {
					continue
				}
			case "data":
				if out.DisableData {
					continue
				}
			}
		}
		def.Edges = append(def.Edges, graph.EdgeDef{
			From: e.From,
			To:   e.To,
			Type: e.Type,
		})
	}
	expandHWFilterMappings(cfg, def)
	expandImplicitEncoders(cfg, def)
	rewriteGoProcessorCopyEdges(def)
	spliceAudioMergeForMultiInputEncoders(def)
	spliceAudioAdaptersForEncoders(def)
	spliceAudioSyncForOutputs(cfg, def)
	// Loudnorm two-pass shuttle: walk loudnorm filter nodes and stamp
	// pass / stats-file markers + (pass 1) print_format/stats_file
	// AVOptions. Pass-2 measurement read deferred to createFilter so
	// the file-not-found error flows through normal graph-init paths.
	// Errors here only fire on cross-output validation (e.g.
	// conflicting LoudnormPass values); those would have been caught
	// by Config.Validate already, so we panic on the impossible case
	// to keep the BuildDef signature stable.
	if err := applyLoudnormShuttle(cfg, def); err != nil {
		panic(err)
	}
	return def
}

// audioEncoderRequirement describes the sample format and per-frame
// sample count a fixed-frame-size audio encoder requires on its input.
// Used by spliceAudioAdaptersForEncoders to insert an aformat +
// asetnsamples chain in front of the encoder when the upstream is a
// raw decoder (no user-supplied filter chain).
type audioEncoderRequirement struct {
	sampleFmt  string // libavfilter sample-fmt name (e.g. "fltp", "s16")
	frameSize  int    // exact samples per frame the encoder demands
	hasFrameSz bool   // whether frameSize must be enforced (some codecs accept variable n)
}

// audioEncoderRequirements lists encoders for which the runtime knows
// the required sample format and (when fixed) frame size. Codecs not
// listed here get no auto-adapter; users can still wire an aformat /
// asetnsamples chain by hand if they need one.
var audioEncoderRequirements = map[string]audioEncoderRequirement{
	"aac":        {sampleFmt: "fltp", frameSize: 1024, hasFrameSz: true},
	"libfdk_aac": {sampleFmt: "s16", frameSize: 1024, hasFrameSz: true},
	"libmp3lame": {sampleFmt: "fltp", frameSize: 1152, hasFrameSz: true},
	"libopus":    {sampleFmt: "flt", frameSize: 960, hasFrameSz: true},
	"libvorbis":  {sampleFmt: "fltp"}, // variable frame size
	"flac":       {sampleFmt: "s16"},  // variable frame size
	"pcm_s16le":  {sampleFmt: "s16"},
	"pcm_s16be":  {sampleFmt: "s16"},
	"pcm_s24le":  {sampleFmt: "s32"},
	"pcm_s32le":  {sampleFmt: "s32"},
	"pcm_f32le":  {sampleFmt: "flt"},
	"ac3":        {sampleFmt: "fltp", frameSize: 1536, hasFrameSz: true},
	"eac3":       {sampleFmt: "fltp", frameSize: 1536, hasFrameSz: true},
}

// spliceAudioMergeForMultiInputEncoders inserts a synthetic amerge filter
// in front of every encoder node that has more than one inbound audio edge.
// This lets users wire multiple audio source handles directly to an audio
// encoder node and have the merge happen transparently, without placing an
// explicit amerge node on the canvas.
//
// Synthetic nodes use the "__amerge__" prefix to avoid colliding with
// user-supplied node IDs. The pass runs after expandImplicitEncoders and
// before spliceAudioAdaptersForEncoders so the resulting single-input
// encoder edge still gets the aformat/asetnsamples adapter when needed.
func spliceAudioMergeForMultiInputEncoders(def *graph.Def) {
	head := func(ref string) string {
		if i := strings.IndexByte(ref, ':'); i >= 0 {
			return ref[:i]
		}
		return ref
	}

	// Collect all encoder node IDs.
	encoders := make(map[string]bool)
	for _, n := range def.Nodes {
		if n.Type == "encoder" {
			encoders[n.ID] = true
		}
	}

	// Group audio edge indices by target encoder ID.
	encEdges := make(map[string][]int)
	for i := range def.Edges {
		e := &def.Edges[i]
		if e.Type != "audio" {
			continue
		}
		toID := head(e.To)
		if encoders[toID] {
			encEdges[toID] = append(encEdges[toID], i)
		}
	}

	var addedNodes []graph.NodeDef
	var addedEdges []graph.EdgeDef
	for encID, idxs := range encEdges {
		if len(idxs) < 2 {
			continue // single input — no merge needed
		}
		mergeID := "__amerge__" + encID
		mergeNode := graph.NodeDef{
			ID:     mergeID,
			Type:   "filter",
			Filter: fmt.Sprintf("amerge=inputs=%d", len(idxs)),
			Internal: graph.Internal{
				Generated: &graph.GeneratedNode{
					By:     "spliceAudioMergeForMultiInputEncoders",
					From:   encID,
					Reason: fmt.Sprintf("auto-merge %d audio inputs for encoder %s", len(idxs), encID),
				},
			},
		}
		addedNodes = append(addedNodes, mergeNode)
		for slot, idx := range idxs {
			// Assign each inbound edge a distinct input pad name (in0, in1, …)
			// so that the topology validator does not flag multiple edges on the
			// same port.  The scheduler uses Inbound slice order for channel
			// assignment, not the port name, so renaming is safe.
			def.Edges[idx].To = fmt.Sprintf("%s:in%d", mergeID, slot)
		}
		addedEdges = append(addedEdges, graph.EdgeDef{From: mergeID, To: encID, Type: "audio"})
	}
	def.Nodes = append(def.Nodes, addedNodes...)
	def.Edges = append(def.Edges, addedEdges...)
}

// spliceAudioAdaptersForEncoders rewrites edges that feed an audio
// encoder directly from a source / decoder, inserting a synthetic
// libavfilter node that conforms the stream to the encoder's sample
// format and (when fixed) per-frame sample count.
//
// This makes the common "input → AAC encoder → output" topology work
// even when the source's audio doesn't already match the encoder
// (e.g. an MP3-in-AVI track that delivers 1152-sample s16p frames to
// the native AAC encoder which requires 1024-sample fltp frames). An
// existing filter / processor upstream is left alone — the user is
// assumed to have set up the conversion deliberately.
//
// Synthetic filter nodes use the "__aspl__" prefix to avoid colliding
// with user-supplied node IDs.
func spliceAudioAdaptersForEncoders(def *graph.Def) {
	nodeByID := make(map[string]graph.NodeDef, len(def.Nodes))
	for _, n := range def.Nodes {
		nodeByID[n.ID] = n
	}
	head := func(ref string) string {
		if i := strings.IndexByte(ref, ':'); i >= 0 {
			return ref[:i]
		}
		return ref
	}

	var added []graph.EdgeDef
	for i := range def.Edges {
		e := &def.Edges[i]
		if e.Type != "audio" {
			continue
		}
		dstNode, ok := nodeByID[head(e.To)]
		if !ok || dstNode.Type != "encoder" {
			continue
		}
		codec, _ := dstNode.Params["codec"].(string)
		req, known := audioEncoderRequirements[codec]
		if !known {
			continue
		}
		// Skip only if the immediate upstream is a synthetic adapter
		// already inserted by this pass or by spliceAudioSyncForOutputs
		// (IDs start with "__aspl__" or "__async__"), or a go_processor
		// that is assumed to own its output format.
		// Do NOT skip for arbitrary user-placed filters (e.g. amerge,
		// aresample, loudnorm): they do not guarantee the sample format
		// or frame size the encoder requires, so the adapter must still
		// be inserted.
		if srcNode, ok := nodeByID[head(e.From)]; ok {
			if srcNode.Type == "go_processor" {
				continue
			}
			if srcNode.Type == "filter" &&
				(strings.HasPrefix(head(e.From), "__aspl__") ||
					strings.HasPrefix(head(e.From), "__async__")) {
				continue
			}
		}
		spec := "aformat=sample_fmts=" + req.sampleFmt
		if req.hasFrameSz {
			spec += fmt.Sprintf(",asetnsamples=n=%d:p=0", req.frameSize)
		}
		filtID := fmt.Sprintf("__aspl__%s_%d", dstNode.ID, i)
		filtNode := graph.NodeDef{
			ID:     filtID,
			Type:   "filter",
			Filter: spec,
			Internal: graph.Internal{
				Generated: &graph.GeneratedNode{
					By:     "spliceAudioAdaptersForEncoders",
					From:   dstNode.ID,
					Reason: "audio sample-fmt + frame-size adapter for encoder " + codec,
				},
			},
		}
		def.Nodes = append(def.Nodes, filtNode)
		nodeByID[filtID] = filtNode
		added = append(added, graph.EdgeDef{From: e.From, To: filtID, Type: "audio"})
		e.From = filtID
	}
	def.Edges = append(def.Edges, added...)
}

// spliceAudioSyncForOutputs implements the legacy `-async N` flag (now
// removed from the FFmpeg 8.0 CLI) by injecting an `aresample`
// libavfilter node in front of every audio encoder that feeds an
// output with `Output.AudioSync != 0`. The aresample filter wraps
// libswresample's compensation engine
// (libswresample/swresample.c::swr_next_pts → swr_inject_silence /
// swr_drop_output for hard corrections, swr_set_compensation for soft),
// so the runtime gets the same sample-clock locking ffmpeg.c used to
// configure on the swresample handle directly.
//
// Spec emitted:
//
//	N == 1 → "aresample=async=1:first_pts=0"   (start-only correction;
//	                                            the FFmpeg `-async 1`
//	                                            historical semantics)
//	N >  1 → "aresample=async=N"                (continuous compensation
//	                                            up to N samples/sec)
//
// The pass runs after `spliceAudioAdaptersForEncoders` so the new
// aresample node sits *upstream* of any synthetic aformat /
// asetnsamples chain — the resampler's output is then re-conformed to
// the encoder's required sample format and frame size, matching the
// order ffmpeg uses internally (resampler first, packer second).
//
// Synthetic node IDs use the "__async__" prefix to avoid colliding
// with user-supplied node IDs.
func spliceAudioSyncForOutputs(cfg *Config, def *graph.Def) {
	syncByOutput := make(map[string]int, len(cfg.Outputs))
	for _, out := range cfg.Outputs {
		if out.AudioSync != 0 {
			syncByOutput[out.ID] = out.AudioSync
		}
	}
	if len(syncByOutput) == 0 {
		return
	}
	nodeByID := make(map[string]graph.NodeDef, len(def.Nodes))
	for _, n := range def.Nodes {
		nodeByID[n.ID] = n
	}
	head := func(ref string) string {
		if i := strings.IndexByte(ref, ':'); i >= 0 {
			return ref[:i]
		}
		return ref
	}
	// Map each encoder node → output ID by scanning encoder→sink
	// edges. (After expandImplicitEncoders every audio sink edge has
	// an encoder or copy node as its source.)
	encoderToOutput := make(map[string]string)
	for _, e := range def.Edges {
		if e.Type != "audio" {
			continue
		}
		fromID := head(e.From)
		toID := head(e.To)
		if _, isOutput := syncByOutput[toID]; !isOutput {
			continue
		}
		if src, ok := nodeByID[fromID]; ok && src.Type == "encoder" {
			encoderToOutput[fromID] = toID
		}
	}
	if len(encoderToOutput) == 0 {
		return
	}

	// We want the aresample filter to sit *upstream* of any
	// `__aspl__` aformat+asetnsamples chain (the resampler may add or
	// drop samples for compensation, which would invalidate any
	// downstream `asetnsamples` exact-frame-size guarantee). So treat
	// any single-hop `__aspl__` adapter as transparent: the splice
	// target is the edge feeding *that* node, not the edge feeding
	// the encoder.
	targetToOutput := make(map[string]string, len(encoderToOutput))
	for encID, outID := range encoderToOutput {
		spliceTarget := encID
		for _, e := range def.Edges {
			if e.Type != "audio" || head(e.To) != encID {
				continue
			}
			src, ok := nodeByID[head(e.From)]
			if ok && strings.HasPrefix(src.ID, "__aspl__") {
				spliceTarget = src.ID
			}
			break
		}
		targetToOutput[spliceTarget] = outID
	}

	var added []graph.EdgeDef
	for i := range def.Edges {
		e := &def.Edges[i]
		if e.Type != "audio" {
			continue
		}
		dstID := head(e.To)
		outID, ok := targetToOutput[dstID]
		if !ok {
			continue
		}
		// Skip if the source is already an __async__ filter (idempotent).
		if src, ok := nodeByID[head(e.From)]; ok && strings.HasPrefix(src.ID, "__async__") {
			continue
		}
		n := syncByOutput[outID]
		var spec string
		if n == 1 {
			spec = "aresample=async=1:first_pts=0"
		} else {
			spec = fmt.Sprintf("aresample=async=%d", n)
		}
		filtID := fmt.Sprintf("__async__%s_%d", dstID, i)
		filtNode := graph.NodeDef{
			ID:     filtID,
			Type:   "filter",
			Filter: spec,
			Internal: graph.Internal{
				Generated: &graph.GeneratedNode{
					By:     "spliceAudioSyncForOutputs",
					From:   outID,
					Reason: fmt.Sprintf("audio_sync=%d resampler", n),
				},
			},
		}
		def.Nodes = append(def.Nodes, filtNode)
		nodeByID[filtID] = filtNode
		added = append(added, graph.EdgeDef{From: e.From, To: filtID, Type: "audio"})
		e.From = filtID
	}
	def.Edges = append(def.Edges, added...)
}

// expandImplicitEncoders rewrites edges feeding a sink whose source is
// not already an encoder, splicing in a synthetic encoder node that
// uses the sink's codec_video / codec_audio (defaulting to libx264 /
// aac / mov_text when the field is empty). This lets compact
// JobConfigs run end-to-end without the user having to declare an
// encoder node by hand, including the common case of a filter chain
// (e.g. scale -> fps -> out0:v) sitting between the input and the
// output.
//
// rewriteGoProcessorCopyEdges detects AV edges where a copy node is directly
// downstream of a go_processor (or a chain of them) and rewires the copy node
// to receive raw packets directly from the original source input. This allows
// the topology  in0 → scene → copy → sink  to work correctly: scene receives
// decoded frames for side-channel analysis while copy receives raw demuxer
// packets from in0. Without this rewrite the copy node would receive *av.Frame
// values from the go_processor channel and panic on the type assertion.
//
// The go_processor is left with its inbound AV edge intact but gains no
// outbound AV edge; it becomes a terminal frame processor (frame sink). The
// engine's dead_node warning filter suppresses the spurious dead-branch warning
// for such go_processor nodes.
func rewriteGoProcessorCopyEdges(def *graph.Def) {
	head := func(ref string) string {
		if i := strings.IndexByte(ref, ':'); i >= 0 {
			return ref[:i]
		}
		return ref
	}

	// Index node types from def.Nodes.
	nodeTypeByID := make(map[string]string, len(def.Nodes))
	for _, n := range def.Nodes {
		nodeTypeByID[n.ID] = n.Type
	}
	// Build source-input set from def.Inputs.
	inputIDs := make(map[string]bool, len(def.Inputs))
	for _, inp := range def.Inputs {
		inputIDs[inp.ID] = true
	}

	// avEdgesTo maps nodeID → slice of (from-ref, edge-type) pairs for all
	// AV edges targeting that node. Used to trace upstream chains.
	type inEdge struct{ from, typ string }
	avEdgesTo := make(map[string][]inEdge, len(def.Edges))
	for _, e := range def.Edges {
		avEdgesTo[head(e.To)] = append(avEdgesTo[head(e.To)], inEdge{e.From, e.Type})
	}

	// findSource traces backward through go_processor nodes to find the
	// source input that ultimately feeds a given (nodeID, edgeType) pair.
	// Returns the full From reference (e.g. "in0:v:0") or "" if not found.
	var findSource func(nodeID, edgeType string, depth int) string
	findSource = func(nodeID, edgeType string, depth int) string {
		if depth > 16 {
			return "" // guard against cycles
		}
		var match inEdge
		found := false
		for _, e := range avEdgesTo[nodeID] {
			if e.typ == edgeType {
				if found {
					return "" // fan-in: ambiguous source
				}
				match = e
				found = true
			}
		}
		if !found {
			return ""
		}
		srcID := head(match.from)
		if inputIDs[srcID] {
			return match.from // reached a source input
		}
		if nodeTypeByID[srcID] == "go_processor" {
			return findSource(srcID, edgeType, depth+1)
		}
		return "" // upstream is neither a source nor a go_processor
	}

	for i := range def.Edges {
		e := &def.Edges[i]
		fromID := head(e.From)
		toID := head(e.To)
		if nodeTypeByID[fromID] != "go_processor" || nodeTypeByID[toID] != "copy" {
			continue
		}
		// This is a go_processor → copy edge. Trace back to the source.
		if src := findSource(toID, e.Type, 0); src != "" {
			e.From = src
		}
	}
}

// The GUI mirrors this pass in `materializeImplicitEncoders` so the
// implicit encoder appears as a real editable node in the canvas; this
// runtime fallback is what makes the JSON also work when fed directly
// to `mediamolder run` without going through the GUI.
//
// Synthetic encoder nodes use the "__enc__" prefix to avoid colliding
// with user-supplied node IDs.
func expandImplicitEncoders(cfg *Config, def *graph.Def) {
	outputs := make(map[string]Output, len(cfg.Outputs))
	for _, out := range cfg.Outputs {
		outputs[out.ID] = out
	}
	// Index existing graph nodes so we can tell encoder sources from
	// filter / processor / input sources.
	nodeByID := make(map[string]graph.NodeDef, len(def.Nodes))
	for _, n := range def.Nodes {
		nodeByID[n.ID] = n
	}

	head := func(ref string) string {
		if i := strings.IndexByte(ref, ':'); i >= 0 {
			return ref[:i]
		}
		return ref
	}

	var added []graph.EdgeDef
	// Per-output, per-media-type counter so we can resolve
	// per-stream encoder overrides (Wave 6 #30) by their muxer-add
	// index (which mirrors edge declaration order on the sink).
	typeIdx := make(map[string]int) // key = output-id + ":" + edge-type
	for i := range def.Edges {
		e := &def.Edges[i]
		fromID := head(e.From)
		toID := head(e.To)
		out, ok := outputs[toID]
		if !ok {
			continue
		}
		// Already encoded: source is a graph encoder node, or already a
		// stream-copy node (which forwards demuxer packets directly to the
		// muxer). Inputs and filter nodes fall through to the splice below.
		if n, ok := nodeByID[fromID]; ok && (n.Type == "encoder" || n.Type == "copy") {
			continue
		}
		var codec string
		var extraParams map[string]any
		switch e.Type {
		case "video":
			codec = out.CodecVideo
			if codec == "" {
				codec = "libx264"
			}
			extraParams = out.EncoderParamsVideo
		case "audio":
			codec = out.CodecAudio
			if codec == "" {
				codec = "aac"
			}
			extraParams = out.EncoderParamsAudio
		case "subtitle":
			codec = out.CodecSubtitle
			if codec == "" {
				codec = "mov_text"
			}
			extraParams = out.EncoderParamsSubtitle
		}
		// Per-stream encoder override (Wave 6 #30). Counts edges of
		// this media type in declaration order, matching how the
		// muxer adds streams in openSink. The override's Codec (if
		// non-empty) replaces the output-level codec, and Options
		// overlay extraParams for this synthetic encoder only.
		var streamOverride *EncoderOverride
		switch e.Type {
		case "video", "audio", "subtitle":
			letter := map[string]string{"video": "v", "audio": "a", "subtitle": "s"}[e.Type]
			key := toID + ":" + e.Type
			idx := typeIdx[key]
			typeIdx[key] = idx + 1
			for si := range out.Streams {
				ss := &out.Streams[si]
				if ss.Type == letter && ss.Index == idx && ss.Encoder != nil {
					streamOverride = ss.Encoder
					if ss.Encoder.Codec != "" {
						codec = ss.Encoder.Codec
					}
					break
				}
			}
		}
		if codec == "" {
			continue
		}
		encID := fmt.Sprintf("__enc__%s_%s_%d", toID, e.Type, i)
		nodeType := "encoder"
		nodeParams := map[string]any{"codec": codec}
		for k, v := range extraParams {
			if k == "codec" {
				continue
			}
			nodeParams[k] = v
		}
		// Overlay per-stream encoder Options on top of extraParams
		// so a per-stream `-b:v:1 2.5M` wins over the output-level
		// `-b:v 5M`. Wave 6 #30.
		if streamOverride != nil {
			for k, v := range streamOverride.Options {
				if k == "codec" {
					continue
				}
				nodeParams[k] = v
			}
		}
		// Build the typed lowering bag (Milestone B). Encoder-only
		// shorthand fields (FPSMode, ForceKeyFrames, SAR, DAR,
		// EncoderTimeBase, FieldOrder, Interlaced, Pass, PassLogFile)
		// flow into Internal.Encoder; createEncoder reads them from
		// there. Audio / subtitle edges keep an empty Internal.Encoder
		// since none of these fields apply to non-video streams.
		var encInt *graph.EncoderInternal
		if e.Type == "video" {
			encInt = &graph.EncoderInternal{
				FPSMode:         out.FPSMode,
				ForceKeyFrames:  out.ForceKeyFrames,
				SAR:             out.SAR,
				DAR:             out.DAR,
				EncoderTimeBase: out.EncoderTimeBase,
				FieldOrder:      out.FieldOrder,
				Interlaced:      out.InterlacedEncode,
				Pass:            out.Pass,
			}
			if out.Pass != 0 && out.PassLogFile != "" {
				encInt.PassLogFile = out.PassLogFile
			}
		}
		if codec == "copy" {
			nodeType = "copy"
			nodeParams = nil
			encInt = nil
		}
		encNode := graph.NodeDef{
			ID:     encID,
			Type:   nodeType,
			Params: nodeParams,
			Internal: graph.Internal{
				Encoder: encInt,
				Generated: &graph.GeneratedNode{
					By:     "expandImplicitEncoders",
					From:   toID,
					Reason: "implicit " + nodeType + " for output " + e.Type + " stream",
				},
			},
		}
		def.Nodes = append(def.Nodes, encNode)
		nodeByID[encID] = encNode
		added = append(added, graph.EdgeDef{From: encID, To: e.To, Type: e.Type})
		e.To = encID
	}
	def.Edges = append(def.Edges, added...)

	// Assign sequential PassIndex to each video encoder that
	// requested two-pass mode, in node-declaration order. The index
	// drives the `<prefix>-<idx>.log` naming so multiple two-pass
	// video streams in one run (e.g. ABR ladder) get unique stats
	// files — mirrors FFmpeg's `ost_idx` computation in
	// fftools/ffmpeg_mux_init.c.
	passIdx := 0
	for i := range def.Nodes {
		n := &def.Nodes[i]
		if n.Type != "encoder" || n.Internal.Encoder == nil {
			continue
		}
		if n.Internal.Encoder.Pass == 0 {
			continue
		}
		n.Internal.Encoder.PassIndex = passIdx
		passIdx++
	}
}
