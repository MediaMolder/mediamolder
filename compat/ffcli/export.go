// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

// export.go — Convert a pipeline.Config into an FFmpeg command-line string.
// This is the inverse of Parse / ParseArgs (ffcli.go). The round-trip is not
// perfectly lossless — MediaMolder features that have no CLI equivalent are
// reported in the returned Unsupported slice rather than being dropped silently.
//
// Design notes
//
//   - Arg order follows the canonical FFmpeg convention:
//     global flags → inputs (with per-input flags before each -i) →
//     -filter_complex → outputs (with per-output flags before the URL).
//   - Each output that uses map selects gets explicit -map flags; outputs
//     that receive every stream from a single un-filtered input omit -map for
//     brevity (matches FFmpeg default implicit mapping).
//   - Mediamolder-only constructs (Assets, ErrorPolicy, go_processor nodes,
//     per-node Threads overrides, LoudnormPass, LoudnormStatsFile, PassLogFile,
//     and ConcatList entries with inpoints / metadata) are reported in
//     Unsupported and skipped or simplified.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/MediaMolder/MediaMolder/pipeline"
)

// ExportResult holds the exported FFmpeg command and any feature-parity notes.
type ExportResult struct {
	// Command is the full ffmpeg command line, starting with "ffmpeg ".
	Command string
	// Lines is the same command split on " \\\n  " for display purposes.
	Lines []string
	// Unsupported lists features that cannot be expressed in a plain
	// ffmpeg command line (MediaMolder-only constructs). Each entry is a
	// human-readable sentence suitable for display in a warning panel.
	Unsupported []string
}

// Export converts a pipeline.Config into an ffmpeg command-line string.
// It returns an ExportResult whose Command field is the generated command
// and whose Unsupported field lists any mediamolder-only features that were
// skipped or simplified.
func Export(cfg *pipeline.Config) ExportResult {
	e := &exporter{cfg: cfg}
	e.build()
	return e.result()
}

// ────────────────────────────────────────────────────────────────────────────
// Internal

type exporter struct {
	cfg   *pipeline.Config
	args  []string
	unsup []string
}

func (e *exporter) warn(msg string) { e.unsup = append(e.unsup, msg) }

func (e *exporter) add(args ...string) { e.args = append(e.args, args...) }

func (e *exporter) build() {
	e.buildGlobal()
	e.buildInputs()
	if hasGraph(e.cfg) {
		e.buildFilterComplex()
	}
	e.buildOutputs()
}

// ── global flags ──────────────────────────────────────────────────────────

func (e *exporter) buildGlobal() {
	o := e.cfg.GlobalOptions
	if o.Threads > 0 {
		e.add("-threads", strconv.Itoa(o.Threads))
	}
	if o.ThreadType != "" {
		e.add("-thread_type", o.ThreadType)
	}
	if o.HardwareDevice != "" {
		e.add("-init_hw_device", o.HardwareDevice)
	}
	if o.HardwareAccel != "" {
		e.add("-hwaccel", o.HardwareAccel)
	}
	if e.cfg.FilterComplexThreads > 0 {
		e.add("-filter_complex_threads", strconv.Itoa(e.cfg.FilterComplexThreads))
	}
	if e.cfg.CopyTS {
		e.add("-copyts")
	}
	if e.cfg.StartAtZero {
		e.add("-start_at_zero")
	}
	if len(e.cfg.Assets) > 0 {
		e.warn("Config.Assets: asset references ($asset:<name>) cannot be expressed in a plain ffmpeg command line — resolve each asset to its filesystem path manually before running the command.")
	}
}

// ── inputs ────────────────────────────────────────────────────────────────

func (e *exporter) buildInputs() {
	for idx, in := range e.cfg.Inputs {
		e.buildInput(idx, in)
	}
}

func (e *exporter) buildInput(idx int, in pipeline.Input) {
	_ = idx // kept for future error messages

	// Per-input flags must precede -i URL.
	if in.StreamLoop != 0 {
		e.add("-stream_loop", strconv.Itoa(in.StreamLoop))
	}
	if in.ITSOffset != 0 {
		e.add("-itsoffset", formatFloat(in.ITSOffset))
	}
	if in.ReadRate != 0 {
		if in.ReadRate == 1.0 {
			e.add("-re")
		} else {
			e.add("-readrate", formatFloat(in.ReadRate))
		}
		if in.ReadRateInitialBurst != 0 {
			e.add("-readrate_initial_burst", formatFloat(in.ReadRateInitialBurst))
		}
		if in.ReadRateCatchup != 0 {
			e.add("-readrate_catchup", formatFloat(in.ReadRateCatchup))
		}
	}
	if in.Format != "" {
		e.add("-f", in.Format)
	} else if in.Kind == "lavfi" {
		e.add("-f", "lavfi")
	} else if in.Kind == "concat" && len(in.ConcatList) > 0 {
		// ConcatList entries are serialised by the runtime into a
		// temp listfile — there is no direct CLI equivalent.
		e.warn(fmt.Sprintf("Input %q: ConcatList entries cannot be expressed in a plain ffmpeg command line — write a concat listfile and use \"-f concat -i <listfile>\" instead.", in.ID))
		e.add("-f", "concat")
	}
	// Raw demuxer parameters.
	if in.FrameRate != 0 {
		e.add("-framerate", formatFloat(in.FrameRate))
	}
	if in.PixelFormat != "" {
		e.add("-pixel_format", in.PixelFormat)
	}
	if in.VideoSize != "" {
		e.add("-video_size", in.VideoSize)
	}
	if in.SampleRate > 0 {
		e.add("-ar", strconv.Itoa(in.SampleRate))
	}
	if in.Channels > 0 {
		e.add("-ac", strconv.Itoa(in.Channels))
	}
	if in.SampleFormat != "" {
		e.add("-sample_fmt", in.SampleFormat)
	}
	if in.PatternType != "" {
		e.add("-pattern_type", in.PatternType)
	}
	if in.SubtitleCharenc != "" {
		e.add("-sub_charenc", in.SubtitleCharenc)
	}
	if len(in.ProtocolWhitelist) > 0 {
		e.add("-protocol_whitelist", strings.Join(in.ProtocolWhitelist, ","))
	}
	if in.ThreadQueueSize > 0 {
		e.add("-thread_queue_size", strconv.Itoa(in.ThreadQueueSize))
	}
	// Timing flags (before -i).
	if v, ok := in.Options["ss"]; ok {
		e.add("-ss", fmt.Sprint(v))
	}
	if in.SeekTimestamp {
		e.add("-seek_timestamp", "1")
	}
	if in.AccurateSeek != nil && !*in.AccurateSeek {
		e.add("-noaccurate_seek")
	}
	if v, ok := in.Options["t"]; ok {
		e.add("-t", fmt.Sprint(v))
	}
	if v, ok := in.Options["to"]; ok {
		e.add("-to", fmt.Sprint(v))
	}
	// -map_metadata is per-output in FFmpeg; it is emitted when building
	// outputs (addOutputFlags), not before -i.
	e.add("-i", in.URL)
}

// ── filter_complex ────────────────────────────────────────────────────────

func hasGraph(cfg *pipeline.Config) bool {
	for _, n := range cfg.Graph.Nodes {
		if n.Type == "filter" || n.Type == "filter_source" || n.Type == "filter_sink" {
			return true
		}
		if n.Type == "go_processor" {
			return true // will be warned; still present in graph
		}
	}
	return false
}

func (e *exporter) buildFilterComplex() {
	spec, unsup := graphToFilterComplex(e.cfg)
	for _, u := range unsup {
		e.warn(u)
	}
	if spec != "" {
		e.add("-filter_complex", spec)
	}
}

// graphToFilterComplex reconstructs the -filter_complex string from
// Config.Graph.Nodes + Config.Graph.Edges using the same labelling
// convention as NormalizeFilterComplex so the round-trip is idempotent.
func graphToFilterComplex(cfg *pipeline.Config) (string, []string) {
	var unsup []string
	nodes := cfg.Graph.Nodes
	edges := cfg.Graph.Edges

	if len(nodes) == 0 {
		return "", nil
	}

	// Build adjacency: which edges feed into each node (by node ID).
	inEdges := map[string][]pipeline.EdgeDef{}
	outEdges := map[string][]pipeline.EdgeDef{}
	for _, edge := range edges {
		fromNode := portNode(edge.From)
		toNode := portNode(edge.To)
		outEdges[fromNode] = append(outEdges[fromNode], edge)
		inEdges[toNode] = append(inEdges[toNode], edge)
	}

	// Assign pad labels to every edge. Edges that come from an input
	// source get the FFmpeg stream-specifier form (e.g. "[0:v]");
	// edges between filter nodes get a synthetic label built from the
	// source node id.
	inputIDs := map[string]int{}
	for i, in := range cfg.Inputs {
		inputIDs[in.ID] = i
	}

	edgeLabel := map[string]string{} // edge.From → label used as output pad
	for _, edge := range edges {
		fromNode := portNode(edge.From)
		if inIdx, ok := inputIDs[fromNode]; ok {
			// Edge originates from an Input.
			typ := portType(edge.From, edge.Type)
			edgeLabel[edge.From] = fmt.Sprintf("%d:%s", inIdx, typ)
		} else {
			// Edge originates from a filter node. Use a synthetic label
			// derived from the node id and port so it's stable & unique.
			safe := sanitizeLabel(edge.From)
			edgeLabel[edge.From] = safe
		}
	}

	// Build each chain: one chain per filter node.
	type chain struct {
		inputs  []string // input pad labels (without [])
		filters string   // "filter=opt=val"
		outputs []string // output pad labels (without [])
	}

	// We topologically order the chains to get a deterministic output.
	// Simple approach: process nodes in declaration order (the GUI
	// preserves insertion order which tends to be topological).
	var chains []chain

	for _, node := range nodes {
		if node.Type == "go_processor" {
			unsup = append(unsup, fmt.Sprintf("Node %q (go_processor %q): Go processors have no FFmpeg CLI equivalent — replace with an equivalent libavfilter filter.", node.ID, node.Processor))
			continue
		}
		if node.Type == "encoder" {
			// Explicit encoder nodes become the codec flag on the
			// output, not a filter_complex entry.
			continue
		}
		if node.Type == "filter_source" || node.Type == "filter_sink" || node.Type == "filter" {
			// Collect input pad labels for this node.
			var ins []string
			for _, edge := range inEdges[node.ID] {
				ins = append(ins, edgeLabel[edge.From])
			}
			// Build the filter expression.
			filterExpr := node.Filter
			if len(node.Params) > 0 {
				var params []string
				for k, v := range node.Params {
					params = append(params, fmt.Sprintf("%s=%v", k, v))
				}
				sort.Strings(params)
				filterExpr += "=" + strings.Join(params, ":")
			}
			if node.ErrorPolicy != nil {
				unsup = append(unsup, fmt.Sprintf("Node %q: ErrorPolicy is a MediaMolder extension with no ffmpeg CLI equivalent.", node.ID))
			}
			// Collect output pad labels.
			var outs []string
			for _, edge := range outEdges[node.ID] {
				outs = append(outs, edgeLabel[edge.From])
			}
			chains = append(chains, chain{
				inputs:  ins,
				filters: filterExpr,
				outputs: outs,
			})
		}
	}

	if len(chains) == 0 {
		return "", unsup
	}

	var parts []string
	for _, c := range chains {
		var b strings.Builder
		for _, lbl := range c.inputs {
			b.WriteByte('[')
			b.WriteString(lbl)
			b.WriteByte(']')
		}
		b.WriteString(c.filters)
		for _, lbl := range c.outputs {
			b.WriteByte('[')
			b.WriteString(lbl)
			b.WriteByte(']')
		}
		parts = append(parts, b.String())
	}
	return strings.Join(parts, ";"), unsup
}

// portNode extracts the node ID from a port ref "nodeID:port".
func portNode(ref string) string {
	if i := strings.LastIndex(ref, ":"); i >= 0 {
		// Refs from inputs use the format "inputID:v:0" or "inputID:a:1"
		// and from filters "nodeID:out:0". The node is always the first
		// colon-delimited segment.
		return ref[:strings.Index(ref, ":")]
	}
	return ref
}

// portType extracts a canonical single-letter stream type from a port ref.
func portType(ref, edgeType string) string {
	// Try to parse the type component from the ref itself first.
	parts := strings.Split(ref, ":")
	if len(parts) >= 2 {
		switch parts[1] {
		case "v", "video":
			return "v"
		case "a", "audio":
			return "a"
		case "s", "subtitle":
			return "s"
		case "d", "data":
			return "d"
		}
	}
	// Fall back to the edge type.
	switch edgeType {
	case "video":
		return "v"
	case "audio":
		return "a"
	case "subtitle":
		return "s"
	case "data":
		return "d"
	}
	return "v"
}

// sanitizeLabel produces a valid FFmpeg filter pad label from a node-port key.
// FFmpeg labels are restricted to [a-zA-Z0-9_]. We replace anything else with
// an underscore and truncate to 63 chars.
func sanitizeLabel(s string) string {
	var b strings.Builder
	for _, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
			b.WriteRune(c)
		} else {
			b.WriteByte('_')
		}
	}
	out := b.String()
	if len(out) > 63 {
		out = out[:63]
	}
	return out
}

// ── outputs ───────────────────────────────────────────────────────────────

func (e *exporter) buildOutputs() {
	for _, out := range e.cfg.Outputs {
		e.buildOutput(out)
	}
}

func (e *exporter) buildOutput(out pipeline.Output) {
	// -map flags (only emit when streams are explicitly selected, i.e.
	// the output's Streams selectors come from an explicitly wired
	// graph rather than the default implicit map).
	for _, sel := range out.Streams {
		// Streams field is for per-stream attributes (disposition,
		// metadata, encoder overrides), not for -map. The actual
		// map selection lives in the input Streams field. We handle
		// dispositions and metadata below.
		_ = sel
	}
	// Emit explicit -map for every output that has input stream selects.
	// In MediaMolder the stream selects live on Input.Streams rather than
	// the Output, so we emit -map args for each output's connected
	// input streams by inspecting the graph edges.
	e.buildMaps(out)

	// Disable flags.
	if out.DisableVideo {
		e.add("-vn")
	}
	if out.DisableAudio {
		e.add("-an")
	}
	if out.DisableSubtitle {
		e.add("-sn")
	}
	if out.DisableData {
		e.add("-dn")
	}

	// Codec selection — sourced via the outputView abstraction
	// (F1.1). The view encapsulates the precedence rule "explicit
	// graph encoder/copy node > Output.Codec* shorthand", so the
	// formatter no longer needs to thread that logic itself. F1.2
	// will let callers source the same view from a normalized
	// graph instead.
	view := resolveOutputViewFromConfig(e.cfg, out)
	if v := view.Video.Codec; v != "" {
		e.add("-c:v", v)
	}
	if v := view.Audio.Codec; v != "" {
		e.add("-c:a", v)
	}
	if v := view.Subtitle.Codec; v != "" {
		e.add("-c:s", v)
	}

	// Codec tags.
	if out.CodecTagVideo != "" {
		e.add("-tag:v", out.CodecTagVideo)
	}
	if out.CodecTagAudio != "" {
		e.add("-tag:a", out.CodecTagAudio)
	}
	if out.CodecTagSubtitle != "" {
		e.add("-tag:s", out.CodecTagSubtitle)
	}

	// Encoder params — flatten to per-type flag strings (sourced
	// via outputView; F1.1 refactor). For codecs in codecToParamsFlag
	// (libx264/libx265/libsvtav1/...) the non-reserved keys are packed
	// into a single "-<codec>-params" flag instead of individual
	// "-<key>:<stream> <val>" pairs.
	e.emitEncoderParams("v", view.Video.Codec, view.Video.Params)
	e.emitEncoderParams("a", view.Audio.Codec, view.Audio.Params)
	e.emitEncoderParams("s", view.Subtitle.Codec, view.Subtitle.Params)

	// Explicit encoder nodes authored in the GUI store codec + AVOptions
	// on the node itself rather than on Output.EncoderParams*.  Emit them
	// here so the CLI round-trip is complete.
	e.buildEncoderNodes(out)

	// Per-stream stream specs (metadata + disposition + encoder overrides).
	for _, ss := range out.Streams {
		spec := fmt.Sprintf("%s:%d", ss.Type, ss.Index)
		if ss.Disposition != "" {
			e.add(fmt.Sprintf("-disposition:s:%s", spec), ss.Disposition)
		}
		for k, v := range ss.Metadata {
			e.add(fmt.Sprintf("-metadata:s:%s", spec), fmt.Sprintf("%s=%s", k, v))
		}
		if ss.Encoder != nil {
			if ss.Encoder.Codec != "" {
				e.add(fmt.Sprintf("-c:%s:%d", ss.Type, ss.Index), ss.Encoder.Codec)
			}
			// Resolve codec for *-params routing: the per-stream
			// override codec wins; otherwise inherit the output-level
			// resolved codec for that stream type.
			streamCodec := ss.Encoder.Codec
			if streamCodec == "" {
				switch ss.Type {
				case "v":
					streamCodec = view.Video.Codec
				case "a":
					streamCodec = view.Audio.Codec
				case "s":
					streamCodec = view.Subtitle.Codec
				}
			}
			e.emitEncoderParams(spec, streamCodec, ss.Encoder.Options)
		}
	}

	// BSF chains.
	if out.BSFVideo != "" {
		e.add("-bsf:v", out.BSFVideo)
	}
	if out.BSFAudio != "" {
		e.add("-bsf:a", out.BSFAudio)
	}
	if out.BSFSubtitle != "" {
		e.add("-bsf:s", out.BSFSubtitle)
	}

	// Frame count caps.
	if out.MaxFramesVideo > 0 {
		e.add("-frames:v", strconv.Itoa(out.MaxFramesVideo))
	}
	if out.MaxFramesAudio > 0 {
		e.add("-frames:a", strconv.Itoa(out.MaxFramesAudio))
	}

	// FPS mode (sourced via outputView; F1.1 refactor).
	if view.Video.FPSMode != "" {
		e.add("-fps_mode", view.Video.FPSMode)
	}

	// Audio sync (sourced via outputView; F1.1 refactor).
	if view.AudioSync != 0 {
		e.add("-async", strconv.Itoa(view.AudioSync))
	}

	// Misc output flags.
	if out.Shortest {
		e.add("-shortest")
	}
	if out.MaxFileSize > 0 {
		e.add("-fs", strconv.FormatInt(out.MaxFileSize, 10))
	}
	if out.MuxDelay != 0 {
		e.add("-muxdelay", formatFloat(out.MuxDelay))
	}
	if out.MuxPreload != 0 {
		e.add("-muxpreload", formatFloat(out.MuxPreload))
	}
	if out.AvoidNegativeTS != "" {
		e.add("-avoid_negative_ts", out.AvoidNegativeTS)
	}

	// Two-pass encoding (sourced via outputView; F1.1 refactor).
	if view.Video.Pass != 0 {
		e.add("-pass", strconv.Itoa(view.Video.Pass))
	}
	if view.Video.PassLogFile != "" {
		e.add("-passlogfile", view.Video.PassLogFile)
	}

	// LoudnormPass has no ffmpeg CLI equivalent (it's a mediamolder
	// orchestration primitive).
	if out.LoudnormPass != 0 {
		e.warn(fmt.Sprintf("Output %q: LoudnormPass=%d is a MediaMolder orchestration feature with no single ffmpeg CLI equivalent — run two separate ffmpeg invocations (analysis + apply) with appropriate -af loudnorm options.", out.ID, out.LoudnormPass))
	}

	// Container-level metadata.
	for k, v := range out.Metadata {
		e.add("-metadata", fmt.Sprintf("%s=%s", k, v))
	}

	// map_metadata / map_chapters from inputs.
	for idx, in := range e.cfg.Inputs {
		if in.MapMetadata {
			e.add("-map_metadata", strconv.Itoa(idx))
		}
		if in.MapChapters {
			e.add("-map_chapters", strconv.Itoa(idx))
		}
	}

	// Explicit chapter table.
	if len(out.Chapters) > 0 {
		e.warn(fmt.Sprintf("Output %q: explicit Chapters table cannot be expressed directly in a plain ffmpeg command line — use the ffmetadata muxer or a sidecar file.", out.ID))
	}

	// Attachments (-attach FILE).
	for _, att := range out.Attachments {
		e.add("-attach", att.Path)
		if att.MimeType != "" {
			// -metadata:s:t:<idx> mimetype=... follows the -attach;
			// we approximate with the global metadata flag for
			// simplicity (exact index would require counting attachments).
			e.add("-metadata:s:t", fmt.Sprintf("mimetype=%s", att.MimeType))
		}
		if att.Filename != "" && att.Filename != attachBasename(att.Path) {
			e.add("-metadata:s:t", fmt.Sprintf("filename=%s", att.Filename))
		}
	}

	// HLS options.
	if out.HLS != nil {
		e.add("-f", "hls")
		h := out.HLS
		if h.Time != 0 {
			e.add("-hls_time", formatFloat(h.Time))
		}
		if h.InitTime != 0 {
			e.add("-hls_init_time", formatFloat(h.InitTime))
		}
		if h.ListSize != 0 {
			e.add("-hls_list_size", strconv.Itoa(h.ListSize))
		}
		if h.PlaylistType != "" {
			e.add("-hls_playlist_type", h.PlaylistType)
		}
		if h.SegmentType != "" {
			e.add("-hls_segment_type", h.SegmentType)
		}
		if h.SegmentFilename != "" {
			e.add("-hls_segment_filename", h.SegmentFilename)
		}
		if h.FMP4InitFilename != "" {
			e.add("-hls_fmp4_init_filename", h.FMP4InitFilename)
		}
		if h.StartNumber != 0 {
			e.add("-start_number", strconv.Itoa(h.StartNumber))
		}
		if h.MasterPlName != "" {
			e.add("-master_pl_name", h.MasterPlName)
		}
		if h.VarStreamMap != "" {
			e.add("-var_stream_map", h.VarStreamMap)
		}
		if len(h.Flags) > 0 {
			e.add("-hls_flags", strings.Join(h.Flags, "+"))
		}
	} else if out.DASH != nil {
		e.add("-f", "dash")
		d := out.DASH
		if d.SegDuration != 0 {
			e.add("-seg_duration", formatFloat(d.SegDuration))
		}
		if d.FragDuration != 0 {
			e.add("-frag_duration", formatFloat(d.FragDuration))
		}
		if d.WindowSize != 0 {
			e.add("-window_size", strconv.Itoa(d.WindowSize))
		}
		if d.ExtraWindowSize != 0 {
			e.add("-extra_window_size", strconv.Itoa(d.ExtraWindowSize))
		}
		if d.InitSegName != "" {
			e.add("-init_seg_name", d.InitSegName)
		}
		if d.MediaSegName != "" {
			e.add("-media_seg_name", d.MediaSegName)
		}
		if d.SingleFile {
			e.add("-single_file", "1")
		}
		if d.UseTemplate != nil {
			if *d.UseTemplate {
				e.add("-use_template", "1")
			} else {
				e.add("-use_template", "0")
			}
		}
		if d.UseTimeline != nil {
			if *d.UseTimeline {
				e.add("-use_timeline", "1")
			} else {
				e.add("-use_timeline", "0")
			}
		}
		if d.Streaming {
			e.add("-streaming", "1")
		}
		if d.AdaptationSets != "" {
			e.add("-adaptation_sets", d.AdaptationSets)
		}
		if d.HLSPlaylist {
			e.add("-hls_playlist", "1")
		}
		if d.LDash {
			e.add("-ldash", "1")
		}
		if len(d.Flags) > 0 {
			e.add("-dash_flags", strings.Join(d.Flags, "+"))
		}
	} else if out.Format != "" {
		e.add("-f", out.Format)
	}

	// Tee output.
	if out.Kind == "tee" {
		e.add("-f", "tee")
		e.add(buildTeeURL(out.Targets))
		return
	}

	// Output URL.
	if out.URL != "" {
		e.add(out.URL)
	}
}

// buildMaps emits -map flags for the output.
//
// In MediaMolder the stream-mapping information is stored in
// Input.Streams (StreamSelect) keyed by input index.  When the parser
// sees no -map args it fills every input's Streams with Optional=true
// defaults; when it sees explicit -map args it replaces the defaults
// with non-Optional entries.
//
// For single-output configs we emit the explicit selects (Optional=false)
// as -map args.  For multi-output configs the per-output routing cannot
// be recovered from the Config (the data structure stores mappings
// globally, not per output), so we skip -map args and rely on FFmpeg's
// implicit stream selection, noting the limitation once.
//
// When the config contains a processing graph the routing is derived by
// walking graph edges backward from the output node; this is more
// accurate than Input.Streams selects because the graph edges encode
// exactly which stream type of which input feeds the output.
func (e *exporter) buildMaps(out pipeline.Output) {
	// Only emit maps for single-output configs; multi-output mapping is
	// not recoverable from the Config.
	if len(e.cfg.Outputs) > 1 {
		// Warn once (keyed off the first output).
		if e.cfg.Outputs[0].ID == out.ID {
			// Collect all non-Optional selects; if any exist, note the limitation.
			hasExplicit := false
			for _, in := range e.cfg.Inputs {
				for _, sel := range in.Streams {
					if !sel.Optional {
						hasExplicit = true
						break
					}
				}
				if hasExplicit {
					break
				}
			}
			if hasExplicit {
				e.warn("Multi-output stream mapping (-map) cannot be reconstructed from the Config — the exported command uses FFmpeg's default implicit stream selection. Verify that each output receives the expected streams.")
			}
		}
		return
	}
	// Prefer graph-derived maps when the config has a processing graph.
	if args := e.graphMaps(out.ID); args != nil {
		for _, arg := range args {
			e.add("-map", arg)
		}
		return
	}
	// Single output: emit explicit selects only (Optional=false means
	// they came from explicit -map args, not the parser defaults).
	for iIdx, in := range e.cfg.Inputs {
		for _, sel := range in.Streams {
			if sel.Optional {
				continue // parser default — skip
			}
			prefix := ""
			if sel.Negate {
				prefix = "-"
			}
			opt := ""
			typLetter := streamTypeLetter(sel.Type)
			var arg string
			if sel.All {
				arg = fmt.Sprintf("%s%d:%s%s", prefix, iIdx, typLetter, opt)
			} else {
				arg = fmt.Sprintf("%s%d:%s:%d%s", prefix, iIdx, typLetter, sel.Track, opt)
			}
			e.add("-map", arg)
		}
	}
}

// graphMaps walks the processing graph backward from outID and returns the
// -map argument strings (e.g. "0:v:0", "1:a:0") for every input stream that
// actually feeds the output.  The returned slice is sorted for deterministic
// output (by input index, then stream type letter, then track index).
// Returns nil when no graph edges are present so the caller can fall back to
// the Input.Streams-based path.
func (e *exporter) graphMaps(outID string) []string {
	if len(e.cfg.Graph.Edges) == 0 {
		return nil
	}
	// Build input ID → index map.
	inputIdx := make(map[string]int, len(e.cfg.Inputs))
	for i, in := range e.cfg.Inputs {
		inputIdx[in.ID] = i
	}
	// Build reverse adjacency: nodeID → edges whose To-node is that ID.
	reverse := make(map[string][]pipeline.EdgeDef, len(e.cfg.Graph.Edges))
	for _, edge := range e.cfg.Graph.Edges {
		toNode := portNode(edge.To)
		reverse[toNode] = append(reverse[toNode], edge)
	}
	// BFS backward from the output node.
	visited := make(map[string]bool)
	queue := []string{outID}
	seen := make(map[string]bool)
	var args []string
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if visited[cur] {
			continue
		}
		visited[cur] = true
		for _, edge := range reverse[cur] {
			fromNode := portNode(edge.From)
			if idx, isInput := inputIdx[fromNode]; isInput {
				// From format: "inputID:typeLetter:trackIndex"
				parts := strings.SplitN(edge.From, ":", 3)
				if len(parts) != 3 {
					continue
				}
				arg := fmt.Sprintf("%d:%s:%s", idx, parts[1], parts[2])
				if !seen[arg] {
					seen[arg] = true
					args = append(args, arg)
				}
			} else if !visited[fromNode] {
				queue = append(queue, fromNode)
			}
		}
	}
	if len(args) == 0 {
		return nil
	}
	sort.Strings(args)
	return args
}

// buildEncoderParams was the legacy per-key emitter; F1.2 routes all
// encoder param emission through (*exporter).emitEncoderParams in
// encoder_view.go so that codec-specific "-<codec>-params" packing
// applies uniformly to output-shorthand, per-stream override, and
// explicit-encoder-node sources.

// (graphCodecs lifted to the package-level graphCodecsForOutput helper
// in encoder_view.go; the buildOutput codec block now sources codecs
// through the outputView abstraction. F1.1.)

// buildEncoderNodes emits AVOption flags sourced from explicit encoder nodes
// that are wired into out's sink in the graph.  Codec flags are handled
// separately by graphCodecs / the codec-selection block in buildOutput.
// Copy nodes have no params to emit, so they are skipped here.
func (e *exporter) buildEncoderNodes(out pipeline.Output) {
	if len(e.cfg.Graph.Nodes) == 0 {
		return
	}
	nodeByID := make(map[string]pipeline.NodeDef, len(e.cfg.Graph.Nodes))
	for _, n := range e.cfg.Graph.Nodes {
		nodeByID[n.ID] = n
	}
	for _, edge := range e.cfg.Graph.Edges {
		if portNode(edge.To) != out.ID {
			continue
		}
		n, ok := nodeByID[portNode(edge.From)]
		if !ok || n.Type != "encoder" {
			continue
		}
		typ := portType(edge.To, edge.Type)
		codec, _ := n.Params["codec"].(string)
		e.emitEncoderParams(typ, codec, n.Params)
	}
}

// ── tee URL ────────────────────────────────────────────────────────────────

func buildTeeURL(targets []pipeline.TeeTarget) string {
	parts := make([]string, 0, len(targets))
	for _, t := range targets {
		var opts []string
		if t.Format != "" {
			opts = append(opts, "f="+teeSafeVal(t.Format))
		}
		if t.Select != "" {
			opts = append(opts, "select="+teeSafeVal(t.Select))
		}
		if t.BSFs != "" {
			opts = append(opts, "bsfs="+teeSafeVal(t.BSFs))
		}
		if t.OnFail != "" {
			opts = append(opts, "onfail="+teeSafeVal(t.OnFail))
		}
		if t.UseFifo {
			opts = append(opts, "use_fifo=1")
		}
		if t.FifoOptions != "" {
			opts = append(opts, "fifo_options="+teeSafeVal(t.FifoOptions))
		}
		for k, v := range t.Options {
			opts = append(opts, k+"="+teeSafeVal(fmt.Sprint(v)))
		}
		url := teeEscapeURL(t.URL)
		if len(opts) > 0 {
			parts = append(parts, "["+strings.Join(opts, ":")+"]"+url)
		} else {
			parts = append(parts, url)
		}
	}
	return strings.Join(parts, "|")
}

// teeSafeVal escapes a value for inclusion in a tee option block.
func teeSafeVal(s string) string {
	return strings.ReplaceAll(s, "\\", "\\\\")
}

// teeEscapeURL escapes characters that are significant in the tee slaves
// grammar (| : ] \) with a leading backslash.
func teeEscapeURL(url string) string {
	var b strings.Builder
	for _, c := range url {
		if c == '|' || c == ':' || c == ']' || c == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// attachBasename extracts the last path component of a path (mimics
// filepath.Base but without importing "path/filepath" here).
func attachBasename(p string) string {
	if i := strings.LastIndexAny(p, "/\\"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// streamTypeLetter converts a StreamSelect type string to the single-letter
// form used in -map arguments.
func streamTypeLetter(t string) string {
	switch t {
	case "video":
		return "v"
	case "audio":
		return "a"
	case "subtitle":
		return "s"
	case "data":
		return "d"
	}
	return t
}

// formatFloat formats a float64 dropping unnecessary trailing zeros.
func formatFloat(f float64) string {
	s := strconv.FormatFloat(f, 'f', 6, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s
}

// ── result ────────────────────────────────────────────────────────────────

func (e *exporter) result() ExportResult {
	args := append([]string{"ffmpeg"}, e.args...)
	// Build Lines: group args into "ffmpeg \\\n  arg arg \\\n  arg arg..."
	// For readability each flag+value pair goes on its own line.
	lines := buildLines(args)
	return ExportResult{
		Command:     strings.Join(args, " "),
		Lines:       lines,
		Unsupported: e.unsup,
	}
}

// buildLines groups the arg list into display lines. Each flag+value pair
// (a flag followed by a non-flag) occupies one line; standalone flags and
// positional args (URLs, filter_complex strings) occupy their own line.
func buildLines(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	var lines []string
	i := 0
	for i < len(args) {
		arg := args[i]
		if strings.HasPrefix(arg, "-") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			// Flag + value.
			lines = append(lines, arg+" "+shellQuote(args[i+1]))
			i += 2
		} else {
			lines = append(lines, shellQuote(arg))
			i++
		}
	}
	return lines
}

// shellQuote wraps a string in single quotes if it contains characters that
// would be interpreted by a shell, and leaves it bare otherwise.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '/' || c == ':' || c == '=' || c == '+' ||
			c == ',' || c == '@' || c == '%') {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	// Single-quote with embedded ' escaped as '\''.
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
