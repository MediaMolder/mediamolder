// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/graph"
	"github.com/MediaMolder/MediaMolder/processors"
	"golang.org/x/sync/errgroup"
)

// configToGraphDef converts a pipeline Config into a graph.Def suitable for
// graph.Build. Inputs become source nodes, outputs become sink nodes.
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
		def.Nodes = append(def.Nodes, graph.NodeDef{
			ID:        node.ID,
			Type:      node.Type,
			Filter:    node.Filter,
			Processor: node.Processor,
			Params:    node.Params,
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
		// Skip metadata-routing edges; they are runtime-only.
		if e.Type == "metadata" {
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
	expandImplicitEncoders(cfg, def)
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
		// Skip if the source is already a filter or processor: the
		// user (or another splice pass) has set up the format chain.
		if srcNode, ok := nodeByID[head(e.From)]; ok {
			if srcNode.Type == "filter" || srcNode.Type == "go_processor" {
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
		// Stash FPSMode on the synthetic encoder node for video edges so
		// handleEncoder's per-frame renumberer can consume it. The double-
		// underscore prefix matches the `__enc__` convention and keeps the
		// key out of the AVDictionary path via encoderReservedParams.
		if e.Type == "video" && out.FPSMode != "" {
			nodeParams["__fps_mode"] = out.FPSMode
		}
		// Two-pass video encoding (FFmpeg `-pass N -passlogfile P`).
		// Honoured only on video edges; the global stream index is
		// computed below after all synthetic encoders are added so it
		// matches the `<prefix>-<idx>.log` numbering FFmpeg uses
		// (one entry per video encoder in declaration order).
		if e.Type == "video" && out.Pass != 0 {
			nodeParams["__pass"] = out.Pass
			if out.PassLogFile != "" {
				nodeParams["__passlogfile"] = out.PassLogFile
			}
		}
		// Force-keyframe spec (FFmpeg `-force_key_frames`). Honoured
		// only on video edges; the encoder handler builds a
		// forceKeyFramesMatcher and stamps frame.pict_type =
		// AV_PICTURE_TYPE_I on each matching frame before SendFrame.
		if e.Type == "video" && out.ForceKeyFrames != "" {
			nodeParams["__force_key_frames"] = out.ForceKeyFrames
		}
		// SAR / DAR shorthand (FFmpeg `-aspect` / `setsar` / `setdar`).
		// Resolved to a numeric SAR in createEncoder once the encoder's
		// width/height are known. Honoured only on video edges.
		if e.Type == "video" && out.SAR != "" {
			nodeParams["__sar"] = out.SAR
		}
		if e.Type == "video" && out.DAR != "" {
			nodeParams["__dar"] = out.DAR
		}
		if codec == "copy" {
			nodeType = "copy"
			nodeParams = nil
		}
		encNode := graph.NodeDef{
			ID:     encID,
			Type:   nodeType,
			Params: nodeParams,
		}
		def.Nodes = append(def.Nodes, encNode)
		nodeByID[encID] = encNode
		added = append(added, graph.EdgeDef{From: encID, To: e.To, Type: e.Type})
		e.To = encID
	}
	def.Edges = append(def.Edges, added...)

	// Assign sequential `__pass_index` to each video encoder that
	// requested two-pass mode, in node-declaration order. The index
	// drives the `<prefix>-<idx>.log` naming so multiple two-pass
	// video streams in one run (e.g. ABR ladder) get unique stats
	// files \u2014 mirrors FFmpeg's `ost_idx` computation in
	// fftools/ffmpeg_mux_init.c.
	passIdx := 0
	for i := range def.Nodes {
		n := &def.Nodes[i]
		if n.Type != "encoder" || n.Params == nil {
			continue
		}
		if _, ok := n.Params["__pass"]; !ok {
			continue
		}
		n.Params["__pass_index"] = passIdx
		passIdx++
	}
}

// ---------- Pre-opened resource containers ----------

// sourceResources holds a demuxer and its per-stream decoders.
type sourceResources struct {
	input       *av.InputFormatContext
	decoders    map[int]*av.DecoderContext         // keyed by stream index
	subDecoders map[int]*av.SubtitleDecoderContext // keyed by stream index
	streams     map[int]av.StreamInfo              // keyed by stream index
	cfg         Input
	// mediaDuration is the longest declared duration across the
	// selected input streams (0 for live / unknown). Cached at
	// open-time so handleSource can publish it via the metrics
	// registry without re-reading container metadata.
	mediaDuration time.Duration
	// stopPTSus is the absolute packet PTS (in AV_TIME_BASE units,
	// i.e. microseconds) at or after which the demux loop should
	// stop emitting packets, mirroring the recording_time check in
	// fftools/ffmpeg_demux.c::input_packet_process(). noLimitUS means
	// "no limit" (no `-t` / `-to` was set). The seek for `-ss` and
	// the conflict resolution between `-t` and `-to` happen at
	// open-time in openSource(); only the per-packet stop check
	// remains in handleSource().
	stopPTSus int64

	// tsOffsetUS is the timestamp offset (in AV_TIME_BASE units)
	// applied to every demuxed packet's PTS/DTS, mirroring
	// fftools/ffmpeg_demux.c's `ifile->ts_offset` in non-`copy_ts`
	// mode: it equals `-timestamp` where `timestamp` is the value
	// passed to avformat_seek_file. The effect is that downstream
	// nodes (encoders, muxers) see packets whose timestamps start at
	// 0 even when `-ss` seeked into the middle of the source. Without
	// this shift, a stream-copy of e.g. `-ss 450 -t 10` would write
	// 10s of packets but tag them with PTS in the [450s, 460s] range,
	// causing the muxer to report a 460s-long file.
	tsOffsetUS int64

	// streamLoopRemaining counts how many additional EOF→rewind
	// cycles the demux loop should still perform. -1 = infinite.
	// Decremented by handleSource each time the runtime seeks back
	// to start. Mirrors `Demuxer.loop` in
	// fftools/ffmpeg_demux.c::seek_to_start.
	streamLoopRemaining int
	// loopOffsetUS is the cumulative media duration of completed
	// loop iterations, in AV_TIME_BASE units. Added to every
	// post-rewind packet's PTS/DTS so timestamps remain monotone
	// across iterations. Mirrors `Demuxer.duration.ts` rescaled to
	// AV_TIME_BASE_Q in fftools/ffmpeg_demux.c::ts_fixup. Updated
	// after each successful seek by adding the current iteration's
	// `(maxPTSus - minPTSus)`.
	loopOffsetUS int64
	// pacer enforces FFmpeg's `-readrate` / `-re` /
	// `-readrate_initial_burst` / `-readrate_catchup` semantics on
	// the demux loop. Nil when no pacing is configured. See
	// readRatePacer for the algorithm details.
	pacer *readRatePacer
	// concatCleanup removes the temp listfile materialised from
	// Input.ConcatList when Kind="concat". Nil for every other
	// input kind. Invoked from Close() after the demuxer has
	// shut down so libavformat is no longer reading the file.
	concatCleanup func()
}

func (s *sourceResources) Close() {
	for _, d := range s.decoders {
		d.Close()
	}
	for _, d := range s.subDecoders {
		d.Close()
	}
	if s.input != nil {
		s.input.Close()
	}
	if s.concatCleanup != nil {
		s.concatCleanup()
		s.concatCleanup = nil
	}
}

// sinkResources holds a muxer and the encoder(s) feeding it.
type sinkResources struct {
	muxer *av.OutputFormatContext
	cfg   Output
	// streamRescale[i] describes the timestamp rescaling to apply to
	// packets arriving on input channel i before WritePacket. Always
	// non-nil: stream-copy edges rescale demuxer time_base → muxer
	// time_base; encoder edges rescale encoder time_base → muxer
	// time_base. The encoder rescale is required because some muxers
	// (notably MP4) overwrite the stream's time_base in WriteHeader,
	// so the encoder's PTS values would otherwise be interpreted in
	// the wrong units and play back at the wrong rate.
	streamRescale []*sinkRescale

	// streamBSF[i] is the per-channel bitstream-filter chain (parsed
	// via av_bsf_list_parse_str from the FFmpeg `-bsf` chain syntax
	// `f1[=k=v[:k=v]][,f2]`) applied between rescale and WritePacket.
	// nil when no BSF is configured for that channel's media type.
	// Mirrors fftools/ffmpeg_mux.c::bsf_init.
	streamBSF []*av.BitstreamFilter

	// timing holds the resolved output-side -ss/-t/-to window
	// (mirrors fftools/ffmpeg_mux_init.c's per-OutputFile
	// `start_time` / `recording_time`). Empty when no output trim
	// is configured.
	timing outputTiming
	// copyTS is the global Config.CopyTS flag, latched onto the sink
	// for fast access. When false, kept packets are shifted back so
	// the output anchors at PTS 0 (mirroring of_streamcopy's
	// `pts -= ts_offset`); when true, original timestamps survive.
	copyTS bool
	// maxFileSize is the configured `-fs` limit in bytes (0 = unlimited).
	maxFileSize int64
	// shortest, when true, stops every stream of this output as soon
	// as the shortest input stream closes (mirrors `-shortest`).
	shortest bool

	// shortestMu guards shortestPTSus. Only used when shortest is true.
	shortestMu sync.Mutex
	// shortestPTSus is the smallest "last muxed PTS" (in AV_TIME_BASE
	// units) across every input channel of this output that has
	// closed. Initialised to noLimitUS; once any channel closes it
	// drops to that channel's last-emitted PTS, and the remaining
	// channels stop muxing packets whose PTS reaches that bound.
	shortestPTSus int64
	// stopAll, when set, signals every channel of this output to
	// stop muxing further packets (drain-and-drop). Used by the
	// -fs (max_file_size) and output-side -t/-to enforcement to
	// halt every stream consistently after the first hit.
	stopAll atomic.Bool
}

type sinkRescale struct {
	srcTB [2]int
	dstTB [2]int
}

// graphRunner pre-opens all AV resources and provides the runtime.NodeHandler
// callback used by the Scheduler.
type graphRunner struct {
	cfg  *Config
	pipe *Pipeline

	sources      map[string]*sourceResources
	filters      map[string]*av.FilterGraph
	encoders     map[string]*av.EncoderContext
	sinks        map[string]*sinkResources
	goProcessors map[string]processors.Processor
	// passLogFiles holds open pass-1 statistics files for video
	// encoders that consume `Output.Pass` / `Output.PassLogFile`
	// via the generic AVCodecContext.stats_out path (i.e. not
	// libx264 / libx265 / libvvenc, which manage their own stats
	// files via the codec's `stats` AVOption). Keyed by encoder
	// node ID. Populated by createEncoder and closed in close().
	passLogFiles map[string]*os.File
}

func newGraphRunner(cfg *Config, pipe *Pipeline) *graphRunner {
	return &graphRunner{
		cfg:          cfg,
		pipe:         pipe,
		sources:      make(map[string]*sourceResources),
		filters:      make(map[string]*av.FilterGraph),
		encoders:     make(map[string]*av.EncoderContext),
		sinks:        make(map[string]*sinkResources),
		goProcessors: make(map[string]processors.Processor),
		passLogFiles: make(map[string]*os.File),
	}
}

// resolveThreadCount returns the thread count for a node using the hierarchy:
// per-node params.threads > global_options.threads > 0 (FFmpeg auto).
// If maxThreads > 0, the result is clamped.
func (r *graphRunner) resolveThreadCount(node *graph.Node) int {
	threads := 0
	if v := paramInt(node.Params, "threads"); v > 0 {
		threads = v
	} else if r.cfg.GlobalOptions.Threads > 0 {
		threads = r.cfg.GlobalOptions.Threads
	}
	if r.pipe.maxThreads > 0 && threads > r.pipe.maxThreads {
		threads = r.pipe.maxThreads
	}
	return threads
}

// resolveThreadType returns the thread type for a node using the hierarchy:
// per-node params.thread_type > global_options.thread_type > "" (auto).
func (r *graphRunner) resolveThreadType(node *graph.Node) string {
	if v := paramString(node.Params, "thread_type"); v != "" {
		return v
	}
	return r.cfg.GlobalOptions.ThreadType
}

func (r *graphRunner) close() {
	for _, s := range r.sources {
		s.Close()
	}
	for _, fg := range r.filters {
		fg.Close()
	}
	for _, enc := range r.encoders {
		enc.Close()
	}
	for _, p := range r.goProcessors {
		p.Close()
	}
	for _, f := range r.passLogFiles {
		_ = f.Close()
	}
	for _, s := range r.sinks {
		for _, b := range s.streamBSF {
			if b != nil {
				_ = b.Close()
			}
		}
	}
	// Sinks are finalized by the caller (muxer.Close for atomic rename).
}

// handle dispatches to the appropriate per-kind handler.
// It implements runtime.NodeHandler.
func (r *graphRunner) handle(ctx context.Context, node *graph.Node, ins []<-chan any, outs []chan<- any) error {
	switch node.Kind {
	case graph.KindSource:
		return r.handleSource(ctx, node, outs)
	case graph.KindFilter:
		return r.handleFilter(ctx, node, ins, outs)
	case graph.KindEncoder:
		return r.handleEncoder(ctx, node, ins, outs)
	case graph.KindSink:
		return r.handleSink(ctx, node, ins)
	case graph.KindGoProcessor:
		return r.handleGoProcessor(ctx, node, ins, outs)
	case graph.KindCopy:
		return r.handleCopy(ctx, node, ins, outs)
	default:
		return fmt.Errorf("unknown node kind %v for node %q", node.Kind, node.ID)
	}
}

// ---------- Source handler ----------

func (r *graphRunner) handleSource(ctx context.Context, node *graph.Node, outs []chan<- any) error {
	src := r.sources[node.ID]
	if src == nil {
		return fmt.Errorf("source handler: no resources for node %q", node.ID)
	}
	// Publish the input's known duration once so the GUI can compute
	// percent-complete / ETA. Stays 0 for live or unknown-duration
	// inputs; the GUI hides the progress bar in that case and shows
	// only elapsed-time and processed-media-time.
	r.pipe.Metrics().Node(node.ID).SetMediaDuration(src.mediaDuration)

	// Map edge type → output channel indices, split between "frame"
	// outs (decode → av.Frame) and "copy" outs (raw demuxer packets
	// forwarded to a downstream copy node).
	type typeOuts struct{ frame, copy []int }
	byType := map[graph.PortType]*typeOuts{
		graph.PortVideo:    {},
		graph.PortAudio:    {},
		graph.PortSubtitle: {},
		graph.PortData:     {},
	}
	for i, e := range node.Outbound {
		bucket := byType[e.Type]
		if bucket == nil {
			continue
		}
		if e.To != nil && e.To.Kind == graph.KindCopy {
			bucket.copy = append(bucket.copy, i)
		} else {
			bucket.frame = append(bucket.frame, i)
		}
	}

	sendFrame := func(f *av.Frame, indices []int) error {
		for _, idx := range indices {
			select {
			case outs[idx] <- f:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}

	// sendPacketCopies clones the demuxer packet once per copy-out and
	// forwards each clone. Cloning is necessary because the source
	// reuses a single AVPacket across the demux loop (Unref before
	// each ReadPacket); without a clone the downstream copy node would
	// see freed buffers.
	sendPacketCopies := func(pkt *av.Packet, indices []int) error {
		for _, idx := range indices {
			c, err := av.ClonePacket(pkt)
			if err != nil {
				return err
			}
			select {
			case outs[idx] <- c:
			case <-ctx.Done():
				c.Close()
				return ctx.Err()
			}
		}
		return nil
	}

	// rescaleAudioPTS converts an audio frame's pts from the input stream's
	// time_base to (1, sample_rate) units. The downstream audio filter
	// graph (abuffer source) is configured at (1, sample_rate) granularity
	// to keep sample-accurate timing through filters like asetnsamples.
	// Many container/codec combinations (e.g. MP3 in AVI, where the stream
	// time_base is 1/(sample_rate/1152)) deliver decoded frames whose pts
	// would otherwise be misinterpreted as one sample apart.
	rescaleAudioPTS := func(f *av.Frame, si av.StreamInfo) {
		pts := f.PTS()
		if pts == math.MinInt64 || si.TimeBase[1] <= 0 || si.SampleRate <= 0 {
			return
		}
		// new_pts = pts * tb_num * sample_rate / tb_den
		// Use big-int-style ordering to minimise overflow risk.
		f.SetPTS(pts * int64(si.TimeBase[0]) * int64(si.SampleRate) / int64(si.TimeBase[1]))
	}

	receiveAll := func(dec *av.DecoderContext, si av.StreamInfo) error {
		var indices []int
		switch si.Type {
		case av.MediaTypeVideo:
			indices = byType[graph.PortVideo].frame
		case av.MediaTypeAudio:
			indices = byType[graph.PortAudio].frame
		}
		if len(indices) == 0 {
			return nil
		}
		for {
			f, err := av.AllocFrame()
			if err != nil {
				return err
			}
			if err := dec.ReceiveFrame(f); err != nil {
				f.Close()
				if av.IsEAgain(err) || av.IsEOF(err) {
					return nil
				}
				return err
			}
			if si.Type == av.MediaTypeAudio {
				rescaleAudioPTS(f, si)
			}
			if err := sendFrame(f, indices); err != nil {
				f.Close()
				return err
			}
		}
	}

	// Demux + decode loop.
	pkt, err := av.AllocPacket()
	if err != nil {
		return err
	}
	defer pkt.Close()

	// Per-loop-iteration min/max packet PTS (in AV_TIME_BASE
	// microseconds) — used by the `-stream_loop` rewind path to
	// compute the cycle's media duration so post-rewind packets can
	// be PTS-shifted by the right amount. Mirrors `Demuxer.min_pts`
	// / `Demuxer.max_pts` in fftools/ffmpeg_demux.c::ts_fixup.
	// Reset at every successful seek_to_start.
	var iterMinPTSus, iterMaxPTSus int64 = math.MaxInt64, math.MinInt64

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		pkt.Unref()
		frameStart := time.Now()
		if err := src.input.ReadPacket(pkt); err != nil {
			if av.IsEOF(err) {
				// `-stream_loop` semantics. Mirrors
				// fftools/ffmpeg_demux.c::seek_to_start:
				// rewind, accumulate the cycle's media
				// duration into loopOffsetUS, decrement the
				// remaining-loops counter (unless it is -1,
				// which means infinite), and continue
				// reading. On seek failure we fall through
				// to the normal EOF break (matches FFmpeg's
				// behaviour: any failure inside seek_to_start
				// terminates the input).
				if src.streamLoopRemaining != 0 && iterMaxPTSus > math.MinInt64 {
					seekTo := src.input.StartTime()
					if seekTo == av.NoPTSValue {
						seekTo = 0
					}
					if serr := src.input.SeekFile(seekTo); serr == nil {
						minPTSus := iterMinPTSus
						if minPTSus == math.MaxInt64 {
							minPTSus = 0
						}
						src.loopOffsetUS += iterMaxPTSus - minPTSus
						iterMinPTSus = math.MaxInt64
						iterMaxPTSus = math.MinInt64
						if src.streamLoopRemaining > 0 {
							src.streamLoopRemaining--
						}
						continue
					}
				}
				break
			}
			return err
		}
		dec := src.decoders[pkt.StreamIndex()]
		subDec := src.subDecoders[pkt.StreamIndex()]
		si, known := src.streams[pkt.StreamIndex()]
		if dec == nil && subDec == nil && !known {
			continue
		}

		// Honour per-input -t / -to: stop demuxing once any selected
		// stream's packet PTS reaches the absolute stop point computed
		// from the input's recording_time. Mirrors
		// fftools/ffmpeg_demux.c::input_packet_process()'s `dts >=
		// recording_time + start_time` check (with FFmpeg's default
		// non-copy_ts start_time of 0; we apply the seek timestamp via
		// stopPTSus instead of via per-packet ts_offset). Skips packets
		// without a valid PTS so we don't bail out on a probe-only
		// header. The check runs against the raw PTS (before ts_offset)
		// so the comparison stays in source coordinates.
		if src.stopPTSus != noLimitUS {
			if pts := pkt.PTS(); pts != math.MinInt64 && si.TimeBase[1] > 0 {
				// Convert pkt PTS (in stream time_base units) to
				// AV_TIME_BASE units (microseconds).
				ptsUS := pts * 1_000_000 * int64(si.TimeBase[0]) / int64(si.TimeBase[1])
				if ptsUS >= src.stopPTSus {
					break
				}
			}
		}

		// Apply ts_offset: rebase every packet's PTS/DTS so the
		// pipeline starts at 0 even when -ss seeked into the middle of
		// the source. Mirrors fftools/ffmpeg_demux.c::ts_fixup() in
		// non-copy_ts mode. Rescales the AV_TIME_BASE-unit ts_offset
		// into the packet's stream time_base before adding.
		if src.tsOffsetUS != 0 && si.TimeBase[1] > 0 {
			offsetTB := src.tsOffsetUS * int64(si.TimeBase[1]) /
				(1_000_000 * int64(si.TimeBase[0]))
			pkt.ShiftTS(offsetTB)
		}

		// Apply the per-iteration loop offset for `-stream_loop`.
		// loopOffsetUS is the cumulative duration (in AV_TIME_BASE
		// microseconds) of all completed iterations; adding it
		// keeps post-rewind PTS monotone. Mirrors `pkt->pts +=
		// duration` in fftools/ffmpeg_demux.c::ts_fixup. Tracked
		// separately from tsOffsetUS so the cycle-duration
		// arithmetic doesn't entangle with the `-ss` /
		// `-itsoffset` shift.
		if src.loopOffsetUS != 0 && si.TimeBase[1] > 0 {
			loopTB := src.loopOffsetUS * int64(si.TimeBase[1]) /
				(1_000_000 * int64(si.TimeBase[0]))
			pkt.ShiftTS(loopTB)
		}

		// Track per-iteration min/max packet PTS in AV_TIME_BASE
		// microseconds so the loop rewind path (above) can compute
		// `max - min` as the cycle's media duration. Done in
		// post-shift coordinates, exactly as
		// fftools/ffmpeg_demux.c::ts_fixup updates `Demuxer.min_pts`
		// / `Demuxer.max_pts` after the offset additions.
		if src.streamLoopRemaining != 0 {
			if pts := pkt.PTS(); pts != math.MinInt64 && si.TimeBase[1] > 0 {
				ptsUSshifted := pts * 1_000_000 * int64(si.TimeBase[0]) / int64(si.TimeBase[1])
				if ptsUSshifted < iterMinPTSus {
					iterMinPTSus = ptsUSshifted
				}
				dur := pkt.Duration()
				endUS := ptsUSshifted
				if dur > 0 {
					endUS += dur * 1_000_000 * int64(si.TimeBase[0]) / int64(si.TimeBase[1])
				}
				if endUS > iterMaxPTSus {
					iterMaxPTSus = endUS
				}
			}
		}

		// Pace the demux loop when the user requested
		// `-readrate` / `-re`. Done after PTS shifts so the
		// pacer compares wallclock-elapsed against the
		// post-offset packet PTS — matches
		// fftools/ffmpeg_demux.c::readrate_sleep, which uses
		// `ds->dts` *after* `ts_fixup` has finished.
		if src.pacer != nil {
			if pts := pkt.PTS(); pts != math.MinInt64 && si.TimeBase[1] > 0 {
				ptsUS := pts * 1_000_000 * int64(si.TimeBase[0]) / int64(si.TimeBase[1])
				src.pacer.maybeSleep(ctx, ptsUS)
			}
		}

		// Publish media-time progress so the GUI can compute
		// percent-complete / ETA. Skip packets without a valid PTS
		// (AV_NOPTS_VALUE == math.MinInt64) and streams without a
		// known timebase.
		if pts := pkt.PTS(); pts != math.MinInt64 && si.TimeBase[1] > 0 {
			ptsNs := time.Duration(pts) * time.Second *
				time.Duration(si.TimeBase[0]) / time.Duration(si.TimeBase[1])
			r.pipe.Metrics().Node(node.ID).AdvanceMediaPTS(ptsNs)
		}

		// Route to copy outs first (per stream type).
		var portType graph.PortType
		switch si.Type {
		case av.MediaTypeVideo:
			portType = graph.PortVideo
		case av.MediaTypeAudio:
			portType = graph.PortAudio
		case av.MediaTypeSubtitle:
			portType = graph.PortSubtitle
		case av.MediaTypeData:
			portType = graph.PortData
		}
		if bucket := byType[portType]; bucket != nil && len(bucket.copy) > 0 {
			if err := sendPacketCopies(pkt, bucket.copy); err != nil {
				return err
			}
		}

		// Handle subtitle streams via subtitle decoder.
		if subDec != nil && len(byType[graph.PortSubtitle].frame) > 0 {
			sub, got, err := subDec.Decode(pkt)
			if err != nil {
				return err
			}
			if got {
				for _, idx := range byType[graph.PortSubtitle].frame {
					select {
					case outs[idx] <- sub:
					case <-ctx.Done():
						sub.Close()
						return ctx.Err()
					}
				}
			}
			r.pipe.Metrics().Node(node.ID).RecordLatency(time.Since(frameStart))
			continue
		}

		if dec == nil {
			// Copy-only or data-only stream: nothing to decode.
			r.pipe.Metrics().Node(node.ID).RecordLatency(time.Since(frameStart))
			continue
		}
		// If no downstream node consumes decoded frames of this
		// stream type, skip the decoder entirely. Otherwise packets
		// pile up in the decoder's internal queue until SendPacket
		// returns EAGAIN and the source aborts with averror(-35).
		// (A copy-only consumer was already serviced above via
		// sendPacketCopies.)
		if bucket := byType[portType]; bucket == nil || len(bucket.frame) == 0 {
			r.pipe.Metrics().Node(node.ID).RecordLatency(time.Since(frameStart))
			continue
		}
		if err := dec.SendPacket(pkt); err != nil {
			return err
		}
		if err := receiveAll(dec, si); err != nil {
			return err
		}
		r.pipe.Metrics().Node(node.ID).RecordLatency(time.Since(frameStart))
	}

	// Flush every decoder that actually had packets pushed through it
	// (i.e. has at least one downstream frame consumer).
	for idx, dec := range src.decoders {
		si := src.streams[idx]
		var portType graph.PortType
		switch si.Type {
		case av.MediaTypeVideo:
			portType = graph.PortVideo
		case av.MediaTypeAudio:
			portType = graph.PortAudio
		case av.MediaTypeSubtitle:
			portType = graph.PortSubtitle
		case av.MediaTypeData:
			portType = graph.PortData
		}
		if bucket := byType[portType]; bucket == nil || len(bucket.frame) == 0 {
			continue
		}
		if err := dec.Flush(); err != nil && !av.IsEOF(err) && !av.IsEAgain(err) {
			return err
		}
		// Drain remaining decoded frames.
		for {
			f, err := av.AllocFrame()
			if err != nil {
				return err
			}
			if err := dec.ReceiveFrame(f); err != nil {
				f.Close()
				if av.IsEOF(err) || av.IsEAgain(err) {
					break
				}
				return err
			}
			switch si.Type {
			case av.MediaTypeVideo:
				if err := sendFrame(f, byType[graph.PortVideo].frame); err != nil {
					f.Close()
					return err
				}
			case av.MediaTypeAudio:
				rescaleAudioPTS(f, si)
				if err := sendFrame(f, byType[graph.PortAudio].frame); err != nil {
					f.Close()
					return err
				}
			default:
				f.Close()
			}
		}
	}
	return nil
}

// ---------- Copy handler ----------
//
// A copy node is a verbatim demuxer-packet-to-muxer pipeline: it neither
// decodes nor encodes. The source emits raw AVPackets in the input
// stream's time_base; the sink rescales to the output stream's time_base
// at write time. handleCopy is therefore a thin passthrough; it exists as
// a distinct kind so the graph can express stream-copy intent and so the
// muxer can be configured via AddStreamFromInput rather than from an
// encoder context.
func (r *graphRunner) handleCopy(ctx context.Context, node *graph.Node, ins []<-chan any, outs []chan<- any) error {
	if len(ins) != 1 || len(outs) < 1 {
		return fmt.Errorf("copy node %q: expected 1 input / >=1 output, got %d/%d", node.ID, len(ins), len(outs))
	}
	in := ins[0]
	for v := range in {
		pkt, ok := v.(*av.Packet)
		if !ok {
			return fmt.Errorf("copy node %q: expected *av.Packet, got %T", node.ID, v)
		}
		frameStart := time.Now()
		// Fan out to each downstream channel (clone for all but the last
		// so each muxer owns an independent ref and can rescale/set the
		// stream index without racing the others).
		var sendErr error
		for i, out := range outs {
			var p *av.Packet
			if i == len(outs)-1 {
				p = pkt
			} else {
				c, err := av.ClonePacket(pkt)
				if err != nil {
					pkt.Close()
					sendErr = err
					break
				}
				p = c
			}
			select {
			case out <- p:
			case <-ctx.Done():
				p.Close()
				sendErr = ctx.Err()
			}
			if sendErr != nil {
				break
			}
		}
		if sendErr != nil {
			return sendErr
		}
		r.pipe.Metrics().Node(node.ID).RecordLatency(time.Since(frameStart))
	}
	return nil
}

// ---------- Filter handler ----------

func (r *graphRunner) handleFilter(ctx context.Context, node *graph.Node, ins []<-chan any, outs []chan<- any) error {
	fg := r.filters[node.ID]
	if fg == nil {
		return fmt.Errorf("filter handler: no filter graph for node %q", node.ID)
	}

	// Simple 1→1 fast-path.
	if len(ins) == 1 && len(outs) == 1 {
		return r.handleSimpleFilter(ctx, node, fg, ins[0], outs[0])
	}

	// Multi-input / multi-output: serialise all filter-graph operations
	// through a single goroutine to satisfy FFmpeg's thread-safety contract.
	type filterMsg struct {
		padIdx int
		frame  *av.Frame // nil = this input is exhausted
	}

	msgCh := make(chan filterMsg, 8*len(ins))

	var wg sync.WaitGroup
	for i, in := range ins {
		i, in := i, in
		wg.Add(1)
		go func() {
			defer wg.Done()
			for v := range in {
				msgCh <- filterMsg{padIdx: i, frame: v.(*av.Frame)}
			}
			msgCh <- filterMsg{padIdx: i, frame: nil}
		}()
	}
	go func() {
		wg.Wait()
		close(msgCh)
	}()

	pullOutputs := func() error {
		for oi := range outs {
			for {
				f, err := av.AllocFrame()
				if err != nil {
					return err
				}
				if err := fg.PullFrameAt(oi, f); err != nil {
					f.Close()
					if av.IsEAgain(err) || av.IsEOF(err) {
						break
					}
					return err
				}
				select {
				case outs[oi] <- f:
				case <-ctx.Done():
					f.Close()
					return ctx.Err()
				}
			}
		}
		return nil
	}

	for msg := range msgCh {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if msg.frame == nil {
			// Flush this input pad.
			if err := fg.FlushAt(msg.padIdx); err != nil && !av.IsEOF(err) {
				return err
			}
		} else {
			if err := fg.PushFrameAt(msg.padIdx, msg.frame); err != nil {
				msg.frame.Close()
				// EOF from a buffersrc means the filter has decided it
				// no longer wants frames on this pad (e.g. xfade after
				// the transition window has fully consumed an input,
				// or any "trim"-style filter past its endpoint). Mirror
				// fftools/ffmpeg_filter.c's behaviour: treat the pad as
				// drained and continue pulling outputs / feeding other
				// pads. EAGAIN is also benign — the buffersrc is full
				// and downstream needs to pull first; we'll get a
				// pullOutputs() pass below.
				if !av.IsEOF(err) && !av.IsEAgain(err) {
					return err
				}
			} else {
				msg.frame.Close()
			}
		}
		if err := pullOutputs(); err != nil {
			return err
		}
	}

	// Final drain of all outputs.
	for oi := range outs {
		for {
			f, err := av.AllocFrame()
			if err != nil {
				return err
			}
			if err := fg.PullFrameAt(oi, f); err != nil {
				f.Close()
				if av.IsEOF(err) || av.IsEAgain(err) {
					break
				}
				return err
			}
			select {
			case outs[oi] <- f:
			case <-ctx.Done():
				f.Close()
				return ctx.Err()
			}
		}
	}
	return nil
}

// handleSimpleFilter processes a single-input single-output filter chain.
func (r *graphRunner) handleSimpleFilter(ctx context.Context, node *graph.Node, fg *av.FilterGraph, in <-chan any, out chan<- any) error {
	pull := func() error {
		for {
			f, err := av.AllocFrame()
			if err != nil {
				return err
			}
			if err := fg.PullFrame(f); err != nil {
				f.Close()
				if av.IsEAgain(err) || av.IsEOF(err) {
					return nil
				}
				return err
			}
			select {
			case out <- f:
			case <-ctx.Done():
				f.Close()
				return ctx.Err()
			}
		}
	}

	for v := range in {
		f := v.(*av.Frame)
		frameStart := time.Now()
		if err := fg.PushFrame(f); err != nil {
			f.Close()
			return err
		}
		f.Close()
		if err := pull(); err != nil {
			return err
		}
		r.pipe.Metrics().Node(node.ID).RecordLatency(time.Since(frameStart))
	}

	// Flush and drain.
	if err := fg.Flush(); err != nil && !av.IsEOF(err) {
		return err
	}
	return pull()
}

// ---------- Encoder handler ----------

func (r *graphRunner) handleEncoder(ctx context.Context, node *graph.Node, ins []<-chan any, outs []chan<- any) error {
	enc := r.encoders[node.ID]
	if enc == nil {
		return fmt.Errorf("encoder handler: no encoder for node %q", node.ID)
	}
	if len(ins) != 1 || len(outs) < 1 {
		return fmt.Errorf("encoder node %q: expected 1 input / >=1 output, got %d/%d", node.ID, len(ins), len(outs))
	}

	in := ins[0]

	// Per-output frame-rate enforcement (Output.FPSMode → __fps_mode on the
	// synthetic encoder node by expandImplicitEncoders). Only video frames
	// flow through the rewriter; audio/subtitle encoders see an empty mode
	// which degrades to passthrough.
	var fpsRW *fpsRewriter
	if enc.MediaType() == av.MediaTypeVideo {
		mode := paramString(node.Params, "__fps_mode")
		if mode != "" && mode != "passthrough" {
			fpsRW = newFPSRewriter(mode, computeFrameDurationTB(enc.FrameRate(), enc.TimeBase()))
		}
	}

	// Per-output forced-keyframe spec (Output.ForceKeyFrames →
	// __force_key_frames on the synthetic encoder node). Only video
	// frames are eligible; audio/subtitle encoders see no marker.
	// The matcher is consulted exactly once per frame (after any
	// fpsRewriter rewrite resolves the final PTS) so its `n` /
	// `n_forced` / `prev_forced_*` counters mirror the post-rewrite
	// PTS stream the encoder actually sees.
	var forceKF *forceKeyFramesMatcher
	if enc.MediaType() == av.MediaTypeVideo {
		if specStr := paramString(node.Params, "__force_key_frames"); specStr != "" {
			spec, err := parseForceKeyFrames(specStr)
			if err != nil {
				return fmt.Errorf("encoder %q: %w", node.ID, err)
			}
			tb := enc.TimeBase()
			m, err := newForceKeyFramesMatcher(spec, tb[0], tb[1])
			if err != nil {
				return fmt.Errorf("encoder %q: %w", node.ID, err)
			}
			forceKF = m
			defer forceKF.Close()
		}
	}

	// Fan out each encoded packet to every downstream channel. With one
	// output the packet is forwarded as-is; with N outputs the packet is
	// av_packet_clone'd N-1 times so each consumer (muxer) owns an
	// independent reference and can mutate stream_index / time_base
	// without racing the others.
	sendPacket := func(p *av.Packet) error {
		for i, out := range outs {
			var pkt *av.Packet
			if i == len(outs)-1 {
				pkt = p
			} else {
				c, err := av.ClonePacket(p)
				if err != nil {
					p.Close()
					return err
				}
				pkt = c
			}
			select {
			case out <- pkt:
			case <-ctx.Done():
				pkt.Close()
				return ctx.Err()
			}
		}
		return nil
	}

	// Pass-1 stats sink for the generic codec path (mpeg2video,
	// mpeg4, libxvid, ...). libx264 / libvvenc / libx265 manage
	// their own stats file via the codec's `stats` AVOption and
	// leave AVCodecContext.stats_out empty, so the writer below
	// stays a no-op for them.
	passLog := r.passLogFiles[node.ID]

	drainEncoder := func() error {
		for {
			p, err := av.AllocPacket()
			if err != nil {
				return err
			}
			if err := enc.ReceivePacket(p); err != nil {
				p.Close()
				if av.IsEAgain(err) || av.IsEOF(err) {
					return nil
				}
				return err
			}
			if passLog != nil {
				if s := enc.StatsOut(); s != "" {
					if _, werr := passLog.WriteString(s); werr != nil {
						p.Close()
						return fmt.Errorf("encoder %q: write pass-1 stats: %w", node.ID, werr)
					}
				}
			}
			if err := sendPacket(p); err != nil {
				return err
			}
		}
	}

	// sendOne pushes a single frame through the encoder and drains any
	// resulting packets. Used by both the passthrough path and the CFR
	// duplication loop.
	sendOne := func(f *av.Frame) error {
		// Forced-keyframe stamp: must happen on the exact frame
		// instance handed to libavcodec (cloned duplicates from the
		// CFR fill path each get their own check, mirroring FFmpeg's
		// per-frame `forced_kf_apply` invocation in
		// fftools/ffmpeg_enc.c::frame_encode line 798).
		if forceKF != nil {
			if forceKF.shouldForce(f.PTS(), f.PictType()) {
				f.SetPictType(av.PictureTypeI)
			}
		}
		if err := enc.SendFrame(f); err != nil {
			return err
		}
		return drainEncoder()
	}

	for v := range in {
		f := v.(*av.Frame)
		frameStart := time.Now()

		if fpsRW != nil {
			emit, basePTS, drop := fpsRW.rewrite(f.PTS())
			if drop || emit == 0 {
				f.Close()
				r.pipe.Metrics().Node(node.ID).RecordLatency(time.Since(frameStart))
				continue
			}
			// Fast path: single emission, no clone.
			if emit == 1 {
				f.SetPTS(basePTS)
				if err := sendOne(f); err != nil {
					f.Close()
					return err
				}
				f.Close()
				r.pipe.Metrics().Node(node.ID).RecordLatency(time.Since(frameStart))
				continue
			}
			// CFR forward-gap fill: emit `emit` copies at basePTS,
			// basePTS+dur, basePTS+2*dur, ... The final copy reuses f
			// (and is closed at the end); intermediate copies are
			// av_frame_clone'd.
			dur := fpsRW.frameDurTB
			for i := 0; i < emit-1; i++ {
				dup, err := f.Clone()
				if err != nil {
					f.Close()
					return err
				}
				dup.SetPTS(basePTS + int64(i)*dur)
				if err := sendOne(dup); err != nil {
					dup.Close()
					f.Close()
					return err
				}
				dup.Close()
			}
			f.SetPTS(basePTS + int64(emit-1)*dur)
			if err := sendOne(f); err != nil {
				f.Close()
				return err
			}
			f.Close()
			r.pipe.Metrics().Node(node.ID).RecordLatency(time.Since(frameStart))
			continue
		}

		if err := sendOne(f); err != nil {
			f.Close()
			return err
		}
		f.Close()
		r.pipe.Metrics().Node(node.ID).RecordLatency(time.Since(frameStart))
	}

	// Flush.
	if err := enc.Flush(); err != nil && !av.IsEOF(err) && !av.IsEAgain(err) {
		return err
	}
	return drainEncoder()
}

// ---------- Sink handler ----------

func (r *graphRunner) handleSink(ctx context.Context, node *graph.Node, ins []<-chan any) error {
	sink := r.sinks[node.ID]
	if sink == nil {
		return fmt.Errorf("sink handler: no resources for node %q", node.ID)
	}

	// Per-channel max-frames limit derived from Output.MaxFramesVideo /
	// MaxFramesAudio and the inbound edge's media type. 0 = unlimited.
	// Once written >= limit, subsequent packets on that channel are
	// drained-and-dropped so upstream encoders/copy nodes never block;
	// the muxer trailer is written when every channel closes naturally.
	limitForChan := func(i int) int {
		if i >= len(node.Inbound) {
			return 0
		}
		switch node.Inbound[i].Type {
		case graph.PortVideo:
			return sink.cfg.MaxFramesVideo
		case graph.PortAudio:
			return sink.cfg.MaxFramesAudio
		}
		return 0
	}

	// ptsToMicros converts pkt PTS (in dstTB units) to AV_TIME_BASE
	// (microseconds) for cross-stream PTS comparison, returning
	// (us, true) on success or (0, false) when the PTS is unset or
	// the time_base is invalid.
	ptsToMicros := func(pts int64, dstTB [2]int) (int64, bool) {
		if pts == math.MinInt64 || dstTB[0] <= 0 || dstTB[1] <= 0 {
			return 0, false
		}
		return pts * 1_000_000 * int64(dstTB[0]) / int64(dstTB[1]), true
	}

	// shiftPTSus converts a microsecond offset into dstTB units and
	// subtracts it from pkt's PTS/DTS. Mirrors of_streamcopy's
	// `pkt->pts -= ts_offset` after rebasing the output to start at 0.
	shiftPTSus := func(pkt *av.Packet, deltaUS int64, dstTB [2]int) {
		if deltaUS == 0 || dstTB[0] <= 0 || dstTB[1] <= 0 {
			return
		}
		off := deltaUS * int64(dstTB[1]) / (1_000_000 * int64(dstTB[0]))
		if off != 0 {
			pkt.ShiftTS(-off)
		}
	}

	// Output-side trim window in AV_TIME_BASE units. NoPTSValue means
	// "no -ss"; noLimitUS means "no -t/-to".
	startUS := sink.timing.startTimestampUS()
	stopUS := sink.timing.stopTimestampUS(sink.copyTS)

	// shiftDownUS is the offset subtracted from kept packets so the
	// muxed file anchors at 0 (mirrors of_streamcopy's ts_offset).
	// Suppressed under -copyts.
	var shiftDownUS int64
	if !sink.copyTS && startUS != int64(av.NoPTSValue) {
		shiftDownUS = startUS
	}

	// recordShortest is called when a per-channel goroutine exits
	// naturally (channel closed). Updates the shared shortestPTSus
	// to min(current, last_pts) so other channels can stop.
	recordShortest := func(lastPTSus int64, ok bool) {
		if !sink.shortest || !ok {
			return
		}
		sink.shortestMu.Lock()
		if lastPTSus < sink.shortestPTSus {
			sink.shortestPTSus = lastPTSus
		}
		sink.shortestMu.Unlock()
	}

	// shortestReached returns true when the shortest cap has been
	// reached for `ptsUS` (only consulted when shortest is true).
	shortestReached := func(ptsUS int64, ok bool) bool {
		if !sink.shortest || !ok {
			return false
		}
		sink.shortestMu.Lock()
		bound := sink.shortestPTSus
		sink.shortestMu.Unlock()
		return bound != noLimitUS && ptsUS >= bound
	}

	// processOne runs the full per-packet pipeline for input channel
	// i: max-frames cap, output-side trim drop / stop, ts shift,
	// rescale, max-file-size cap, shortest cap, then WritePacket.
	// Returns (wrote, stopAll, err) where stopAll signals every
	// channel of this output to drain-and-drop.
	type chanState struct {
		written   int
		lastPTSus int64
		lastPTSok bool
	}
	processOne := func(i int, pkt *av.Packet, dstTB [2]int, rs *sinkRescale, st *chanState, mu *sync.Mutex) (bool, bool, error) {
		// Per-stream max frames (counts post-encoder packets, mirrors
		// FFmpeg's `-frames:v` / `-frames:a`).
		if lim := limitForChan(i); lim > 0 && st.written >= lim {
			return false, false, nil
		}
		pkt.SetStreamIndex(i)
		if rs != nil {
			pkt.Rescale(rs.srcTB, rs.dstTB)
		}
		// Compute pts in AV_TIME_BASE units against the muxer's
		// time_base for trim / shortest comparisons.
		ptsUS, hasPTS := ptsToMicros(pkt.PTS(), dstTB)

		// Output-side `-ss`: drop packets whose PTS is below the
		// configured start. Mirrors of_streamcopy's
		// `if (dts < of->start_time) return EAGAIN`.
		if startUS != int64(av.NoPTSValue) && hasPTS && ptsUS < startUS {
			return false, false, nil
		}

		// Output-side `-t` / `-to`: stop the entire output when any
		// kept packet's PTS reaches the configured end. Mirrors
		// `check_recording_time`'s `av_compare_ts >= 0` => stop.
		if stopUS != noLimitUS && hasPTS && ptsUS >= stopUS {
			return false, true, nil
		}

		// `-shortest`: stop this packet (and everything else on this
		// output) once any other channel has finished and its end
		// PTS is reached. Mirrors the per-output sync-queue cap in
		// fftools/ffmpeg_mux_init.c.
		if shortestReached(ptsUS, hasPTS) {
			return false, false, nil
		}

		// Shift kept packets back by startUS so the file anchors at
		// PTS 0 (suppressed under -copyts).
		shiftPTSus(pkt, shiftDownUS, dstTB)

		// writeOne handles a single muxer-bound packet: max_file_size
		// check, WritePacket, and per-channel bookkeeping. Returns
		// (wrote, stopAll, err).
		writeOne := func(p *av.Packet) (bool, bool, error) {
			frameStart := time.Now()
			var wErr error
			if mu != nil {
				mu.Lock()
			}
			stopAllNow := false
			if sink.maxFileSize > 0 {
				if cur := sink.muxer.BytesWritten(); cur >= 0 && cur >= sink.maxFileSize {
					stopAllNow = true
				}
			}
			if !stopAllNow {
				wErr = sink.muxer.WritePacket(p)
			}
			if mu != nil {
				mu.Unlock()
			}
			if stopAllNow {
				return false, true, nil
			}
			if wErr != nil {
				return false, false, wErr
			}
			st.written++
			if pPTS, hasP := ptsToMicros(p.PTS(), dstTB); hasP {
				st.lastPTSus = pPTS
				st.lastPTSok = true
				ptsNs := time.Duration(p.PTS()) * time.Second *
					time.Duration(dstTB[0]) / time.Duration(dstTB[1])
				r.pipe.Metrics().Node(node.ID).AdvanceOutputPTS(ptsNs)
			}
			r.pipe.Metrics().Node(node.ID).RecordLatency(time.Since(frameStart))
			return true, false, nil
		}

		// BSF chain (if any): drive the input packet through
		// av_bsf_send_packet / av_bsf_receive_packet and call
		// writeOne for each output packet. Mirrors
		// fftools/ffmpeg_mux.c::write_packet's BSF loop.
		var bsf *av.BitstreamFilter
		if i < len(sink.streamBSF) {
			bsf = sink.streamBSF[i]
		}
		if bsf != nil {
			outs, err := bsf.FilterPacket(pkt)
			if err != nil {
				return false, false, fmt.Errorf("bsf filter: %w", err)
			}
			var wroteAny bool
			for _, op := range outs {
				op.SetStreamIndex(i)
				wrote, stopAll, werr := writeOne(op)
				op.Close()
				wroteAny = wroteAny || wrote
				if stopAll || werr != nil {
					return wroteAny, stopAll, werr
				}
			}
			return wroteAny, false, nil
		}

		return writeOne(pkt)
	}

	// flushBSF drains any residual packets buffered inside the BSF
	// chain at end-of-stream by sending a null packet (EOF signal),
	// then writing the drained output packets through the same
	// per-channel write path. Mirrors fftools/ffmpeg_mux.c::mux_thread
	// flushing the BSF before WriteTrailer.
	flushBSF := func(i int, dstTB [2]int, st *chanState, mu *sync.Mutex) error {
		var bsf *av.BitstreamFilter
		if i < len(sink.streamBSF) {
			bsf = sink.streamBSF[i]
		}
		if bsf == nil {
			return nil
		}
		outs, err := bsf.Flush()
		if err != nil {
			return fmt.Errorf("bsf flush: %w", err)
		}
		for _, op := range outs {
			op.SetStreamIndex(i)
			if mu != nil {
				mu.Lock()
			}
			werr := sink.muxer.WritePacket(op)
			if mu != nil {
				mu.Unlock()
			}
			op.Close()
			if werr != nil {
				return werr
			}
			st.written++
		}
		return nil
	}

	if len(ins) == 1 {
		var rs *sinkRescale
		if len(sink.streamRescale) > 0 {
			rs = sink.streamRescale[0]
		}
		// Use rs.dstTB (which equals the muxer's pre-WriteHeader
		// stream TB for BSF streams, post-header otherwise) so PTS
		// comparisons stay in the same units packets arrive in
		// after rescale.
		dstTB := sink.muxer.StreamTimeBase(0)
		if rs != nil {
			dstTB = rs.dstTB
		}
		st := &chanState{}
		for v := range ins[0] {
			pkt := v.(*av.Packet)
			if sink.stopAll.Load() {
				pkt.Close()
				continue
			}
			_, stopAll, err := processOne(0, pkt, dstTB, rs, st, nil)
			if stopAll {
				sink.stopAll.Store(true)
			}
			pkt.Close()
			if err != nil {
				return err
			}
		}
		recordShortest(st.lastPTSus, st.lastPTSok)
		if err := flushBSF(0, dstTB, st, nil); err != nil {
			return err
		}
		return sink.muxer.WriteTrailer()
	}

	// Multiple input streams: interleave with per-stream goroutines.
	eg, _ := errgroup.WithContext(ctx)
	var mu sync.Mutex

	for i, in := range ins {
		i, in := i, in
		var rs *sinkRescale
		if i < len(sink.streamRescale) {
			rs = sink.streamRescale[i]
		}
		dstTB := sink.muxer.StreamTimeBase(i)
		if rs != nil {
			dstTB = rs.dstTB
		}
		st := &chanState{}
		eg.Go(func() error {
			defer recordShortest(st.lastPTSus, st.lastPTSok)
			for v := range in {
				pkt := v.(*av.Packet)
				if sink.stopAll.Load() {
					pkt.Close()
					continue
				}
				_, stopAll, err := processOne(i, pkt, dstTB, rs, st, &mu)
				if stopAll {
					sink.stopAll.Store(true)
				}
				pkt.Close()
				if err != nil {
					return err
				}
			}
			return flushBSF(i, dstTB, st, &mu)
		})
	}

	if err := eg.Wait(); err != nil {
		return err
	}
	return sink.muxer.WriteTrailer()
}

// ---------- Go Processor handler ----------

func (r *graphRunner) handleGoProcessor(ctx context.Context, node *graph.Node, ins []<-chan any, outs []chan<- any) error {
	proc := r.goProcessors[node.ID]
	if proc == nil {
		return fmt.Errorf("go_processor handler: no processor for node %q", node.ID)
	}
	if len(ins) != 1 {
		return fmt.Errorf("go_processor node %q: expected 1 input, got %d", node.ID, len(ins))
	}

	// Determine the media type from the inbound edge.
	var mediaType av.MediaType
	if len(node.Inbound) > 0 {
		mediaType = portTypeToAVMediaType(node.Inbound[0].Type)
	}

	var frameIndex uint64
	for v := range ins[0] {
		f := v.(*av.Frame)
		frameStart := time.Now()

		pctx := processors.ProcessorContext{
			StreamID:   node.ID,
			MediaType:  mediaType,
			PTS:        f.PTS(),
			FrameIndex: frameIndex,
			Context:    ctx,
		}

		out, md, err := proc.Process(f, pctx)
		if err != nil {
			f.Close()
			return fmt.Errorf("go_processor %q: %w", node.ID, err)
		}

		// Emit metadata on the event bus if provided.
		if md != nil && r.pipe != nil {
			r.pipe.events.Post(ProcessorMetadata{
				NodeID:     node.ID,
				FrameIndex: frameIndex,
				PTS:        f.PTS(),
				Metadata:   md,
			})
		}

		// If processor returned a different frame, close the original.
		if out != nil && out != f {
			f.Close()
			f = out
		}

		frameIndex++
		r.pipe.Metrics().Node(node.ID).RecordLatency(time.Since(frameStart))

		// nil output means the processor consumed (dropped) the frame.
		if out == nil {
			f.Close()
			continue
		}

		// Send output to all downstream channels.
		for _, ch := range outs {
			select {
			case ch <- f:
			case <-ctx.Done():
				f.Close()
				return ctx.Err()
			}
		}
	}
	return nil
}

// ---------- Resource pre-opening ----------

func (r *graphRunner) openSource(cfg Input, srcNode *graph.Node, decOpts av.DecoderOptions) (*sourceResources, error) {
	var inputOpts map[string]string
	if len(cfg.Options) > 0 {
		inputOpts = make(map[string]string, len(cfg.Options))
		for k, v := range cfg.Options {
			inputOpts[k] = fmt.Sprintf("%v", v)
		}
	}

	// Per-input timing flags (FFmpeg's `-ss` / `-t` / `-to`). These
	// are not AVOptions consumed by avformat_open_input — the runtime
	// enforces them itself, mirroring the logic in
	// fftools/ffmpeg_demux.c::ist_add_input_file(). Strip them from
	// the dictionary so libav doesn't see leftover unknown options.
	timing, err := resolveInputTiming(cfg.Options, func(format string, args ...any) {
		log.Printf("input %q: "+format, append([]any{cfg.URL}, args...)...)
	})
	if err != nil {
		return nil, fmt.Errorf("input %q timing: %w", cfg.URL, err)
	}
	delete(inputOpts, "ss")
	delete(inputOpts, "t")
	delete(inputOpts, "to")

	// Map Input.Kind onto a libavformat input-format name. Empty Kind (or
	// "file") falls through to OpenInput's URL-probing behaviour. "lavfi"
	// routes through libavformat's lavfi virtual demuxer so the URL is
	// interpreted as a filtergraph spec (anullsrc, color, sine, testsrc, …).
	var formatName string
	switch cfg.Kind {
	case "", "file":
		// default file probing; honour explicit Format override
		formatName = cfg.Format
	case "lavfi":
		formatName = "lavfi"
	case "raw":
		// kind="raw" requires Format to name the rawvideo / PCM
		// demuxer (validated upstream by validateInputDemuxerFields).
		formatName = cfg.Format
	case "concat":
		// kind="concat" pins the demuxer; if ConcatList was supplied
		// the runtime serialises it to a temp listfile (handled
		// below); otherwise URL points at an existing listfile.
		formatName = "concat"
	default:
		return nil, fmt.Errorf("input %q: unsupported kind %q (want \"file\", \"lavfi\", \"raw\" or \"concat\")", cfg.ID, cfg.Kind)
	}

	// Promote the typed demuxer fields (Wave 5 #23-#28) into the
	// AVDictionary the demuxer actually consumes. Each field maps to
	// the canonical AVOption name documented on the libavformat
	// demuxer that recognises it; collisions with cfg.Options leave
	// the typed field as the winner so the schema layer is the
	// source of truth.
	openURL := cfg.URL
	var concatCleanup func()
	defer func() {
		if concatCleanup != nil {
			// On failure between materialisation and successful return.
			concatCleanup()
		}
	}()
	if cfg.Kind == "concat" && len(cfg.ConcatList) > 0 {
		path, cleanup, err := materialiseConcatList(cfg.ConcatList)
		if err != nil {
			return nil, fmt.Errorf("input %q: materialise concat list: %w", cfg.ID, err)
		}
		openURL = path
		concatCleanup = cleanup
	}
	if inputOpts == nil {
		inputOpts = map[string]string{}
	}
	if cfg.FrameRate > 0 {
		inputOpts["framerate"] = strconv.FormatFloat(cfg.FrameRate, 'f', -1, 64)
	}
	if cfg.PixelFormat != "" {
		inputOpts["pixel_format"] = cfg.PixelFormat
	}
	if cfg.VideoSize != "" {
		inputOpts["video_size"] = cfg.VideoSize
	}
	if cfg.SampleRate > 0 {
		inputOpts["sample_rate"] = strconv.Itoa(cfg.SampleRate)
	}
	if cfg.Channels > 0 {
		inputOpts["channels"] = strconv.Itoa(cfg.Channels)
	}
	if cfg.SampleFormat != "" {
		inputOpts["sample_fmt"] = cfg.SampleFormat
	}
	if cfg.ThreadQueueSize > 0 {
		inputOpts["thread_queue_size"] = strconv.Itoa(cfg.ThreadQueueSize)
	}
	if len(cfg.ProtocolWhitelist) > 0 {
		inputOpts["protocol_whitelist"] = strings.Join(cfg.ProtocolWhitelist, ",")
	}
	if cfg.PatternType != "" {
		inputOpts["pattern_type"] = cfg.PatternType
	}
	if cfg.SeekTimestamp {
		inputOpts["seek_timestamp"] = "1"
	}
	// AccurateSeek: only emit when explicitly false (FFmpeg default
	// is true). Mapped to "noaccurate_seek" in the runtime via
	// dropping the +start_time addition below; we still pass
	// through so any future libavformat consumer sees it.
	if cfg.AccurateSeek != nil && !*cfg.AccurateSeek {
		inputOpts["accurate_seek"] = "0"
	}
	if len(inputOpts) == 0 {
		inputOpts = nil
	}

	input, err := av.OpenInputWithFormat(openURL, formatName, inputOpts)
	if err != nil {
		return nil, fmt.Errorf("open input %q: %w", cfg.URL, err)
	}

	// Mirror fftools/ffmpeg_demux.c: compute the seek target and apply
	// avformat_seek_file. Uses AV_TIME_BASE units (microseconds). The
	// container's reported start_time is added in to align with FFmpeg
	// for formats whose first PTS is non-zero (e.g. MPEG-TS). Skipped
	// for lavfi inputs — virtual sources don't support seeking and
	// always start at zero, so any -ss value is converted to the
	// per-packet stop check via timing's recording_time path.
	if timing.haveStart && formatName != "lavfi" {
		targetUS := timing.seekTimestampUS(input.StartTime())
		if err := input.SeekFile(targetUS); err != nil {
			input.Close()
			return nil, fmt.Errorf("seek input %q to %d us: %w", cfg.URL, targetUS, err)
		}
	}

	allStreams, err := input.AllStreams()
	if err != nil {
		input.Close()
		return nil, fmt.Errorf("enumerate streams %q: %w", cfg.URL, err)
	}

	// Determine which stream types feed *only* copy nodes (no decoder
	// needed) versus types that have at least one decoded consumer.
	// Streams whose type appears only on copy edges skip decoder open.
	copyOnly := map[string]bool{}
	if srcNode != nil {
		seenAny := map[string]bool{}
		seenNonCopy := map[string]bool{}
		for _, e := range srcNode.Outbound {
			t := string(e.Type)
			seenAny[t] = true
			if e.To == nil || e.To.Kind != graph.KindCopy {
				seenNonCopy[t] = true
			}
		}
		for t := range seenAny {
			if !seenNonCopy[t] {
				copyOnly[t] = true
			}
		}
	}

	decoders := make(map[int]*av.DecoderContext)
	subDecoders := make(map[int]*av.SubtitleDecoderContext)
	streams := make(map[int]av.StreamInfo)

	// Resolve the selector list (handles All / Optional / Negate /
	// Program — Wave 2 #9 + #10) into the concrete set of input
	// stream indices to demux. Selectors are walked in declaration
	// order; Negate selectors subtract from the running set; missing
	// non-Optional matches fail fast (the previous silent-skip
	// behaviour produced confusing downstream graph errors).
	selectedIdx, err := resolveStreamSelection(cfg.Streams, allStreams, input.Programs())
	if err != nil {
		input.Close()
		return nil, fmt.Errorf("input %q: %w", cfg.URL, err)
	}
	streamByIdx := make(map[int]av.StreamInfo, len(allStreams))
	for _, si := range allStreams {
		streamByIdx[si.Index] = si
	}
	for _, idx := range selectedIdx {
		si := streamByIdx[idx]
		typ := si.Type.String()
		switch {
		case copyOnly[typ]:
			// Stream-copy only: don't open a decoder.
		case typ == "subtitle":
			subDec, err := av.OpenSubtitleDecoderWithOptions(input, si.Index, av.SubtitleDecoderOptions{
				Charenc: cfg.SubtitleCharenc,
			})
			if err != nil {
				for _, d := range decoders {
					d.Close()
				}
				for _, d := range subDecoders {
					d.Close()
				}
				input.Close()
				return nil, fmt.Errorf("open subtitle decoder for stream %d: %w", si.Index, err)
			}
			subDecoders[si.Index] = subDec
		default:
			dec, err := av.OpenDecoderWithOptions(input, si.Index, decOpts)
			if err != nil {
				for _, d := range decoders {
					d.Close()
				}
				for _, d := range subDecoders {
					d.Close()
				}
				input.Close()
				return nil, fmt.Errorf("open decoder for %s stream %d: %w", typ, si.Index, err)
			}
			decoders[si.Index] = dec
		}
		streams[si.Index] = si
	}

	// Compute longest selected stream duration for progress reporting.
	// Skips streams the user didn't pick (e.g. unselected audio
	// tracks), so a video-only job reports against the video duration
	// rather than a longer audio stream.
	var mediaDuration time.Duration
	for _, si := range streams {
		if si.Duration <= 0 || si.TimeBase[1] <= 0 {
			continue
		}
		d := time.Duration(si.Duration) * time.Second *
			time.Duration(si.TimeBase[0]) / time.Duration(si.TimeBase[1])
		if d > mediaDuration {
			mediaDuration = d
		}
	}

	// Compute ts_offset only when -ss actually triggered a seek.
	// FFmpeg additionally compensates for the container's reported
	// start_time even without -ss, but doing so unconditionally would
	// alter PTS for every existing job (e.g. MPEG-TS captures whose
	// first PTS is non-zero); restricting it to seeked jobs preserves
	// backward compatibility while still mirroring FFmpeg for the
	// trim use case the user is exercising.
	//
	// When Config.CopyTS is true the shift is suppressed so the
	// original demuxer PTS reach downstream nodes intact — mirrors
	// FFmpeg's global `-copyts` flag, which sets `ifile->ts_offset`
	// to 0 (or to `input_ts_offset`, which we don't model) instead
	// of `-timestamp` in fftools/ffmpeg_demux.c.
	//
	// `Config.StartAtZero` re-enables the shift even under CopyTS so
	// the first kept packet still anchors at PTS 0 (mirrors the
	// `start_at_zero ? 0 : f->start_time_effective` branch at
	// fftools/ffmpeg_demux.c L486 — `-start_at_zero` overrides the
	// `-copyts` suppression).
	var tsOffsetUS int64
	if timing.haveStart && (!(r.cfg != nil && r.cfg.CopyTS) || (r.cfg != nil && r.cfg.StartAtZero)) {
		tsOffsetUS = -timing.seekTimestampUS(input.StartTime())
	}

	// Compose `-itsoffset` additively with the seek compensation.
	// FFmpeg's fftools/ffmpeg_demux.c does the same:
	//   `f->ts_offset = o->input_ts_offset - timestamp;`
	// (where `timestamp` is the value passed to avformat_seek_file).
	// `Input.ITSOffset` is in seconds; convert to AV_TIME_BASE
	// microseconds before adding.
	if cfg.ITSOffset != 0 {
		tsOffsetUS += int64(cfg.ITSOffset * 1_000_000)
	}

	// Build the read-rate pacer when the user enabled pacing.
	// Mirrors fftools/ffmpeg_demux.c's `Demuxer.readrate` /
	// `readrate_initial_burst` / `readrate_catchup` defaults: when
	// burst is unset it falls back to 0.5s, and catchup falls back
	// to readrate × 1.05.
	var pacer *readRatePacer
	if cfg.ReadRate > 0 {
		burst := cfg.ReadRateInitialBurst
		if burst == 0 {
			burst = 0.5
		}
		catchup := cfg.ReadRateCatchup
		if catchup == 0 {
			catchup = cfg.ReadRate * 1.05
		}
		pacer = newReadRatePacer(cfg.ReadRate, burst, catchup)
	}

	res := &sourceResources{
		input:               input,
		decoders:            decoders,
		subDecoders:         subDecoders,
		streams:             streams,
		cfg:                 cfg,
		mediaDuration:       mediaDuration,
		stopPTSus:           timing.stopTimestampUS(input.StartTime()),
		tsOffsetUS:          tsOffsetUS,
		streamLoopRemaining: cfg.StreamLoop,
		pacer:               pacer,
		concatCleanup:       concatCleanup,
	}
	concatCleanup = nil // ownership transferred to res.Close()
	return res, nil
}

func (r *graphRunner) createFilter(dag *graph.Graph, node *graph.Node) (*av.FilterGraph, error) {
	params := node.Params
	// Loudnorm pass-2 shuttle: read the JSON stats file written by
	// pass 1 and inject measured_I / measured_TP / measured_LRA /
	// measured_thresh / offset into the loudnorm node's params before
	// the filter graph is instantiated. See pipeline/loudnorm.go.
	if node.Filter == "loudnorm" && params != nil {
		if pv, ok := params["__loudnorm_pass"]; ok {
			pi, _ := pv.(int)
			if pi == 2 {
				statsPath, _ := params["__loudnorm_stats"].(string)
				measured, err := loadLoudnormMeasurements(statsPath)
				if err != nil {
					return nil, fmt.Errorf("filter %q: %w", node.ID, err)
				}
				merged := make(map[string]any, len(params)+len(measured))
				for k, v := range params {
					merged[k] = v
				}
				for k, v := range measured {
					merged[k] = v
				}
				params = merged
			}
		}
	}
	filterSpec := buildFilterSpec(NodeDef{
		Filter: node.Filter,
		Params: params,
	})

	// Simple 1→1 filter.
	if len(node.Inbound) == 1 && len(node.Outbound) == 1 {
		si, err := r.resolveStreamInfo(dag, node)
		if err != nil {
			return nil, fmt.Errorf("filter %q: %w", node.ID, err)
		}
		if node.Inbound[0].Type == graph.PortVideo {
			return av.NewVideoFilterGraph(av.VideoFilterGraphConfig{
				Width:      si.Width,
				Height:     si.Height,
				PixFmt:     si.PixFmt,
				TBNum:      si.TimeBase[0],
				TBDen:      si.TimeBase[1],
				SARNum:     1,
				SARDen:     1,
				FRNum:      si.FrameRate[0],
				FRDen:      si.FrameRate[1],
				FilterSpec: filterSpec,
			})
		}
		return av.NewAudioFilterGraph(av.AudioFilterGraphConfig{
			SampleFmt:  si.SampleFmt,
			SampleRate: si.SampleRate,
			Channels:   si.Channels,
			FilterSpec: filterSpec,
		})
	}

	// Multi-input / multi-output: use complex filter graph.
	inputs := make([]av.FilterPadConfig, len(node.Inbound))
	for i, e := range node.Inbound {
		si, err := r.resolveEdgeStreamInfo(dag, e)
		if err != nil {
			return nil, fmt.Errorf("filter %q input %d: %w", node.ID, i, err)
		}
		inputs[i] = av.FilterPadConfig{
			Label:      fmt.Sprintf("in%d", i),
			MediaType:  portTypeToAVMediaType(e.Type),
			Width:      si.Width,
			Height:     si.Height,
			PixFmt:     si.PixFmt,
			TBNum:      si.TimeBase[0],
			TBDen:      si.TimeBase[1],
			SARNum:     1,
			SARDen:     1,
			FRNum:      si.FrameRate[0],
			FRDen:      si.FrameRate[1],
			SampleFmt:  si.SampleFmt,
			SampleRate: si.SampleRate,
			Channels:   si.Channels,
		}
	}

	outputs := make([]av.FilterOutputConfig, len(node.Outbound))
	for i, e := range node.Outbound {
		outputs[i] = av.FilterOutputConfig{
			Label:     fmt.Sprintf("out%d", i),
			MediaType: portTypeToAVMediaType(e.Type),
		}
	}

	spec := buildComplexFilterSpec(filterSpec, len(inputs), len(outputs))

	return av.NewComplexFilterGraph(av.ComplexFilterGraphConfig{
		Inputs:     inputs,
		Outputs:    outputs,
		FilterSpec: spec,
	})
}

func (r *graphRunner) createEncoder(dag *graph.Graph, node *graph.Node) (*av.EncoderContext, error) {
	// Determine codec: first from node params, then from downstream output config.
	codecName := paramString(node.Params, "codec")
	if codecName == "" {
		for _, e := range node.Outbound {
			if e.To.Kind == graph.KindSink {
				out := r.findOutputConfig(e.To.ID)
				if out != nil {
					switch e.Type {
					case graph.PortVideo:
						codecName = out.CodecVideo
					case graph.PortAudio:
						codecName = out.CodecAudio
					}
				}
			}
		}
	}
	if codecName == "" {
		return nil, fmt.Errorf("encoder node %q: no codec specified", node.ID)
	}

	si, err := r.resolveStreamInfo(dag, node)
	if err != nil {
		return nil, fmt.Errorf("encoder node %q: %w", node.ID, err)
	}

	opts := av.EncoderOptions{
		CodecName:    codecName,
		GlobalHeader: true,
		ThreadCount:  r.resolveThreadCount(node),
		ThreadType:   r.resolveThreadType(node),
	}

	switch edgeType := node.Inbound[0].Type; edgeType {
	case graph.PortVideo:
		// Check if upstream is a filter; if so, use the filter's output dimensions.
		if fg := r.upstreamFilterGraph(dag, node); fg != nil {
			opts.Width = fg.OutputWidth(0)
			opts.Height = fg.OutputHeight(0)
			if pf := fg.OutputPixFmt(0); pf >= 0 {
				opts.PixFmt = pf
			}
			// Frames emerge from the buffersink with PTS in the sink's
			// time_base. The encoder must use the same TB or libavcodec will
			// reinterpret the PTS in 1/framerate units, blowing up the
			// container duration (e.g. demuxer TB 1/12288 fed into a
			// 24 fps encoder produces ~512x oversized timestamps).
			if tbn, tbd := fg.OutputTimeBase(0); tbn > 0 && tbd > 0 {
				opts.TimeBase = [2]int{tbn, tbd}
			}
		} else {
			opts.Width = si.Width
			opts.Height = si.Height
		}
		frameRate := si.FrameRate
		if frameRate[0] <= 0 || frameRate[1] <= 0 {
			frameRate = [2]int{25, 1}
		}
		opts.FrameRate = frameRate
	case graph.PortAudio:
		opts.SampleFmt = si.SampleFmt
		opts.SampleRate = si.SampleRate
		opts.Channels = si.Channels
	}

	// Allow explicit param overrides.
	if v := paramInt(node.Params, "width"); v > 0 {
		opts.Width = v
	}
	if v := paramInt(node.Params, "height"); v > 0 {
		opts.Height = v
	}
	if v := paramInt64(node.Params, "bitrate"); v > 0 {
		opts.BitRate = v
	}
	// `b` is the FFmpeg AVOption name for bit rate; the GUI's encoder form
	// writes it under that key. Honour it as an alias for `bitrate` so the
	// muxer sees the configured rate on the encoder context.
	if opts.BitRate == 0 {
		if v := paramInt64(node.Params, "b"); v > 0 {
			opts.BitRate = v
		}
	}
	// `g` is the FFmpeg AVOption name for keyframe interval / GOP size.
	if v := paramInt(node.Params, "g"); v > 0 {
		opts.GOPSize = v
	}

	// SAR / DAR shorthand (FFmpeg `-aspect` / `setsar` / `setdar`).
	// Resolve against the encoder's just-decided Width/Height so DAR
	// can be converted into a SAR fraction.
	if sar := paramString(node.Params, "__sar"); sar != "" {
		n, d, err := resolveSAR(sar, "", opts.Width, opts.Height)
		if err != nil {
			return nil, fmt.Errorf("encoder node %q: %w", node.ID, err)
		}
		opts.SampleAspectRatio = [2]int{n, d}
	} else if dar := paramString(node.Params, "__dar"); dar != "" {
		n, d, err := resolveSAR("", dar, opts.Width, opts.Height)
		if err != nil {
			return nil, fmt.Errorf("encoder node %q: %w", node.ID, err)
		}
		opts.SampleAspectRatio = [2]int{n, d}
	}

	// Pass every remaining param through as an AVDictionary entry so codec-
	// specific options written by the GUI (preset, crf, maxrate, bufsize,
	// x264-params, ...) actually reach the encoder.
	opts.ExtraOpts = collectEncoderExtraOpts(node.Params)

	// Two-pass video encoding. Mirrors fftools/ffmpeg_mux_init.c:705 et seq.
	if pass := paramInt(node.Params, "__pass"); pass != 0 {
		opts.Pass = pass
		prefix := paramString(node.Params, "__passlogfile")
		if prefix == "" {
			prefix = "ffmpeg2pass"
		}
		idx := paramInt(node.Params, "__pass_index")
		logfile := fmt.Sprintf("%s-%d.log", prefix, idx)
		switch codecName {
		case "libx264", "libvvenc":
			if opts.ExtraOpts == nil {
				opts.ExtraOpts = make(map[string]string)
			}
			if _, set := opts.ExtraOpts["stats"]; !set {
				opts.ExtraOpts["stats"] = logfile
			}
		case "libx265":
			if opts.ExtraOpts == nil {
				opts.ExtraOpts = make(map[string]string)
			}
			if _, set := opts.ExtraOpts["x265-stats"]; !set {
				opts.ExtraOpts["x265-stats"] = logfile
			}
		default:
			if pass&2 != 0 {
				buf, ferr := os.ReadFile(logfile)
				if ferr != nil {
					return nil, fmt.Errorf("encoder node %q: read pass-2 stats %q: %w", node.ID, logfile, ferr)
				}
				opts.StatsIn = buf
			}
			if pass&1 != 0 {
				f, ferr := os.Create(logfile)
				if ferr != nil {
					return nil, fmt.Errorf("encoder node %q: open pass-1 stats %q: %w", node.ID, logfile, ferr)
				}
				r.passLogFiles[node.ID] = f
			}
		}
	}

	return av.OpenEncoder(opts)
}

// encoderReservedParams lists the param keys consumed directly by createEncoder
// (or used to address the node itself). They must not be forwarded as
// AVDictionary options because some are not codec AVOptions ("codec", "width",
// "height") and the rest are already applied to EncoderOptions explicitly.
var encoderReservedParams = map[string]bool{
	"codec":       true,
	"width":       true,
	"height":      true,
	"bitrate":     true,
	"threads":     true,
	"thread_type": true,
	// `__fps_mode` is consumed by handleEncoder's per-frame renumberer,
	// not by libavcodec. Keep it out of the AVDictionary forwarded to
	// avcodec_open2 so the encoder doesn't reject the unknown option.
	"__fps_mode": true,
	// `__pass`, `__passlogfile`, `__pass_index` are consumed by
	// createEncoder to drive two-pass video encoding. They never
	// reach avcodec_open2 directly \u2014 createEncoder either sets the
	// codec-specific stats AVOption (libx264 / libvvenc / libx265)
	// or wires AVCodecContext.stats_in / opens a log file for
	// stats_out (generic codecs).
	"__pass":        true,
	"__passlogfile": true,
	"__pass_index":  true,
	"__sar":         true,
	"__dar":         true,
}

// collectEncoderExtraOpts returns a map of AVDictionary options to forward to
// avcodec_open2 from a node's user-supplied params. Reserved keys are skipped;
// empty/nil values are skipped (so the encoder uses its built-in default).
func collectEncoderExtraOpts(params map[string]any) map[string]string {
	if len(params) == 0 {
		return nil
	}
	var out map[string]string
	for k, v := range params {
		if encoderReservedParams[k] {
			continue
		}
		if v == nil {
			continue
		}
		s := fmt.Sprintf("%v", v)
		if s == "" {
			continue
		}
		if out == nil {
			out = make(map[string]string, len(params))
		}
		out[k] = s
	}
	return out
}

func (r *graphRunner) openSink(_ *graph.Graph, node *graph.Node) (*sinkResources, error) {
	out := r.findOutputConfig(node.ID)
	if out == nil {
		return nil, fmt.Errorf("sink node %q: no matching output config", node.ID)
	}

	// Per-output timing flags (FFmpeg's output-side `-ss` / `-t` /
	// `-to`). Stripped from the AVDict before any libav consumer sees
	// them; enforced by handleSink against per-packet PTS, mirroring
	// `fftools/ffmpeg_mux.c::of_streamcopy`.
	outTiming, err := resolveOutputTiming(out.Options, func(format string, args ...any) {
		log.Printf("output %q: "+format, append([]any{out.URL}, args...)...)
	})
	if err != nil {
		return nil, fmt.Errorf("output %q timing: %w", out.URL, err)
	}

	var muxer *av.OutputFormatContext
	switch out.Kind {
	case "tee":
		slavesURL, terr := buildTeeSlavesURL(out.Targets)
		if terr != nil {
			return nil, fmt.Errorf("output %q: %w", out.ID, terr)
		}
		m, oerr := av.OpenTeeOutput(slavesURL)
		if oerr != nil {
			return nil, fmt.Errorf("open tee output %q: %w", out.ID, oerr)
		}
		muxer = m
	default:
		m, oerr := av.OpenOutputWithFormat(out.URL, out.Format)
		if oerr != nil {
			return nil, fmt.Errorf("open output %q: %w", out.URL, oerr)
		}
		muxer = m
	}

	rescales := make([]*sinkRescale, len(node.Inbound))

	// streamsByType records, for each media-type letter ("v"/"a"/"s"/
	// "d"), the absolute output stream indices of the streams of that
	// type in the order they were added to the muxer. This is the same
	// counting convention FFmpeg's `check_stream_specifier` uses for
	// `s:<type>:<idx>`, so `Output.Streams[k] = {Type:"a", Index:1}`
	// resolves to the second audio stream of this output. Built up
	// inside the AddStream loop below.
	streamsByType := map[string][]int{}
	typeLetterFor := func(t graph.PortType) string {
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
		return ""
	}

	// codecTagFor returns the configured FourCC codec_tag override for
	// the given edge's stream kind, or "" if none is configured.
	codecTagFor := func(t graph.PortType) string {
		switch t {
		case "video":
			return out.CodecTagVideo
		case "audio":
			return out.CodecTagAudio
		case "subtitle":
			return out.CodecTagSubtitle
		}
		return ""
	}

	// Add one stream per inbound edge. Encoder predecessors register
	// the stream from the encoder context; stream-copy predecessors
	// copy the input codecpar directly so the muxer never sees an
	// encoder for that stream. Topological order guarantees both kinds
	// of predecessor are already prepared.
	for i, e := range node.Inbound {
		from := e.From
		var outIdx int
		switch from.Kind {
		case graph.KindEncoder:
			enc := r.encoders[from.ID]
			if enc == nil {
				muxer.Abort()
				return nil, fmt.Errorf("sink %q: inbound from %q has no encoder", node.ID, from.ID)
			}
			idx, err := muxer.AddStream(enc)
			if err != nil {
				muxer.Abort()
				return nil, fmt.Errorf("sink %q add stream: %w", node.ID, err)
			}
			outIdx = idx
			// Capture the encoder's time_base. AddStream copies it onto
			// the output stream, but some muxers (notably MP4) overwrite
			// the stream's time_base in WriteHeader, leaving encoder
			// packets (whose PTS is in encoder TB) misinterpreted by the
			// muxer in the new TB. We rescale per-packet to compensate.
			rescales[i] = &sinkRescale{srcTB: enc.TimeBase(), dstTB: muxer.StreamTimeBase(outIdx)}
		case graph.KindCopy:
			srcInput, srcIdx, srcTB, err := r.copySourceFor(from)
			if err != nil {
				muxer.Abort()
				return nil, fmt.Errorf("sink %q copy from %q: %w", node.ID, from.ID, err)
			}
			idx, err := muxer.AddStreamFromInput(srcInput, srcIdx)
			if err != nil {
				muxer.Abort()
				return nil, fmt.Errorf("sink %q add copy stream: %w", node.ID, err)
			}
			outIdx = idx
			rescales[i] = &sinkRescale{srcTB: srcTB, dstTB: muxer.StreamTimeBase(outIdx)}
		default:
			muxer.Abort()
			return nil, fmt.Errorf("sink %q: inbound from %q (kind=%v) is not an encoder or copy node", node.ID, from.ID, from.Kind)
		}

		// Apply optional codec_tag override (e.g. -tag:v hvc1).
		if tag := codecTagFor(e.Type); tag != "" {
			if err := muxer.SetStreamCodecTag(outIdx, tag); err != nil {
				muxer.Abort()
				return nil, fmt.Errorf("sink %q set codec_tag for %s stream: %w", node.ID, e.Type, err)
			}
		}

		// Record this stream's absolute index under its media-type
		// letter for FFmpeg-style `s:<type>:<idx>` resolution below.
		if letter := typeLetterFor(e.Type); letter != "" {
			streamsByType[letter] = append(streamsByType[letter], outIdx)
		}
	}

	// Apply per-stream metadata + disposition (`Output.Streams`).
	// Mirrors FFmpeg's `-metadata:s:<type>:<idx>` and
	// `-disposition:s:<type>:<idx>`. Resolution counts streams of the
	// requested media type in muxer-add order, so a job with one video
	// + two audio streams resolves `{Type:"a", Index:1}` to the
	// second audio AVStream regardless of the order video / audio
	// edges were declared on the sink.
	for j, ss := range out.Streams {
		idxs, ok := streamsByType[ss.Type]
		if !ok || ss.Index < 0 || ss.Index >= len(idxs) {
			muxer.Abort()
			return nil, fmt.Errorf("sink %q streams[%d]: no stream matches %s:%d (have %d %s stream(s))",
				node.ID, j, ss.Type, ss.Index, len(idxs), ss.Type)
		}
		streamIdx := idxs[ss.Index]
		if len(ss.Metadata) > 0 {
			if err := muxer.SetStreamMetadata(streamIdx, ss.Metadata); err != nil {
				muxer.Abort()
				return nil, fmt.Errorf("sink %q streams[%d] set metadata: %w", node.ID, j, err)
			}
		}
		if ss.Disposition != "" {
			if err := muxer.SetStreamDisposition(streamIdx, ss.Disposition); err != nil {
				muxer.Abort()
				return nil, fmt.Errorf("sink %q streams[%d] set disposition: %w", node.ID, j, err)
			}
		}
	}

	// Per-stream bitstream-filter chains (Output.BSFVideo /
	// BSFAudio / BSFSubtitle, FFmpeg `-bsf:v` / `-bsf:a` / `-bsf:s`).
	// Each value is the FFmpeg chain spec `f1[=k=v[:k=v]][,f2]` parsed
	// by libavcodec's av_bsf_list_parse_str. Attached after stream
	// creation and before WriteHeader so the chain's par_out replaces
	// the muxer's stream codecpar — exactly the order
	// fftools/ffmpeg_mux.c::bsf_init follows. Stored per inbound
	// channel for processOne to drive packets through.
	bsfSpecFor := func(t graph.PortType) string {
		switch t {
		case graph.PortVideo:
			return out.BSFVideo
		case graph.PortAudio:
			return out.BSFAudio
		case graph.PortSubtitle:
			return out.BSFSubtitle
		}
		return ""
	}
	streamBSF := make([]*av.BitstreamFilter, len(node.Inbound))
	for i, e := range node.Inbound {
		spec := bsfSpecFor(e.Type)
		if spec == "" {
			continue
		}
		bsf, err := muxer.AttachStreamBSF(i, spec)
		if err != nil {
			for _, b := range streamBSF {
				if b != nil {
					_ = b.Close()
				}
			}
			muxer.Abort()
			return nil, fmt.Errorf("sink %q attach %s bsf chain %q: %w", node.ID, e.Type, spec, err)
		}
		streamBSF[i] = bsf
	}

	// Container metadata + chapters. Resolution rules:
	//   - Output.Metadata, when non-nil, fully replaces any
	//     metadata mapped from inputs (mirrors FFmpeg's behaviour
	//     when both `-metadata` and `-map_metadata` are given:
	//     `-metadata` is applied last and wins).
	//   - When Output.Metadata is nil, every input with
	//     MapMetadata=true contributes its container-level
	//     metadata in declaration order; the last writer wins
	//     per key.
	//   - Chapters follow the same precedence: an explicit
	//     Output.Chapters wins; otherwise the first input with
	//     MapChapters=true contributes its chapter table.
	if err := r.applyOutputMetadata(muxer, out); err != nil {
		muxer.Abort()
		return nil, fmt.Errorf("sink %q apply metadata: %w", node.ID, err)
	}
	if err := r.applyOutputChapters(muxer, out); err != nil {
		muxer.Abort()
		return nil, fmt.Errorf("sink %q apply chapters: %w", node.ID, err)
	}

	// Per-stream color metadata + HDR10 (Output.Color / Output.HDR,
	// FFmpeg `-color_*` / `-mastering_display_metadata` /
	// `-content_light_level`). Applied to every inbound video edge's
	// stream codecpar (and codecpar.coded_side_data for HDR side
	// data) before WriteHeader so the muxer writes the corresponding
	// container boxes / SEI passthrough. Audio + subtitle edges are
	// skipped — color metadata is meaningless for them.
	if out.Color != nil || out.HDR != nil {
		for i, e := range node.Inbound {
			if e.Type != graph.PortVideo {
				continue
			}
			if out.Color != nil {
				if err := muxer.SetStreamColor(i, av.ColorParams{
					Range:          out.Color.Range,
					Primaries:      out.Color.Primaries,
					Transfer:       out.Color.Transfer,
					Space:          out.Color.Space,
					ChromaLocation: out.Color.ChromaLocation,
				}); err != nil {
					for _, b := range streamBSF {
						if b != nil {
							_ = b.Close()
						}
					}
					muxer.Abort()
					return nil, fmt.Errorf("sink %q set color: %w", node.ID, err)
				}
			}
			if out.HDR != nil {
				if md := out.HDR.MasteringDisplay; md != nil {
					hasPrim := md.DisplayPrimariesRX != 0 || md.WhitePointX != 0
					hasLum := md.MaxLuminance != 0
					if hasPrim || hasLum {
						if err := muxer.SetStreamMasteringDisplay(i, av.MasteringDisplay{
							HasPrimaries: hasPrim,
							DisplayPrim: [6]int{
								md.DisplayPrimariesRX, md.DisplayPrimariesRY,
								md.DisplayPrimariesGX, md.DisplayPrimariesGY,
								md.DisplayPrimariesBX, md.DisplayPrimariesBY,
							},
							WhitePoint:   [2]int{md.WhitePointX, md.WhitePointY},
							HasLuminance: hasLum,
							MinLuminance: md.MinLuminance,
							MaxLuminance: md.MaxLuminance,
						}); err != nil {
							for _, b := range streamBSF {
								if b != nil {
									_ = b.Close()
								}
							}
							muxer.Abort()
							return nil, fmt.Errorf("sink %q set mastering display: %w", node.ID, err)
						}
					}
				}
				if cll := out.HDR.ContentLightLevel; cll != nil && (cll.MaxCLL != 0 || cll.MaxFALL != 0) {
					if err := muxer.SetStreamContentLightLevel(i, av.ContentLightLevel{
						MaxCLL:  cll.MaxCLL,
						MaxFALL: cll.MaxFALL,
					}); err != nil {
						for _, b := range streamBSF {
							if b != nil {
								_ = b.Close()
							}
						}
						muxer.Abort()
						return nil, fmt.Errorf("sink %q set content light level: %w", node.ID, err)
					}
				}
			}
		}
	}

	if err := muxer.WriteHeaderWithOptions(buildMuxerOptions(out)); err != nil {
		muxer.Abort()
		return nil, fmt.Errorf("sink %q write header: %w", node.ID, err)
	}

	// Some muxers adjust stream time_base in WriteHeader; refresh the
	// post-header value so per-packet rescaling targets the actual
	// container timebase. BSF-attached streams keep the pre-header
	// rescale target (= bsf.time_base_in), since the BSF chain was
	// initialised against that TB and av_interleaved_write_frame
	// rescales the BSF's output PTS to the post-header stream TB.
	for i, rs := range rescales {
		if rs == nil {
			continue
		}
		if i < len(streamBSF) && streamBSF[i] != nil {
			continue
		}
		rs.dstTB = muxer.StreamTimeBase(i)
	}

	return &sinkResources{
		muxer:         muxer,
		cfg:           *out,
		streamRescale: rescales,
		streamBSF:     streamBSF,
		timing:        outTiming,
		copyTS:        r.cfg != nil && r.cfg.CopyTS,
		maxFileSize:   out.MaxFileSize,
		shortest:      out.Shortest,
		shortestPTSus: noLimitUS,
	}, nil
}

// copySourceFor resolves a stream-copy node back to the input it reads
// from, returning the InputFormatContext, the demuxer stream index, and
// the input stream's time_base. The copy node is required to have
// exactly one inbound edge from a KindSource.
func (r *graphRunner) copySourceFor(copyNode *graph.Node) (*av.InputFormatContext, int, [2]int, error) {
	if len(copyNode.Inbound) != 1 {
		return nil, 0, [2]int{}, fmt.Errorf("copy node %q must have exactly 1 inbound edge, got %d", copyNode.ID, len(copyNode.Inbound))
	}
	in := copyNode.Inbound[0]
	from := in.From
	if from.Kind != graph.KindSource {
		return nil, 0, [2]int{}, fmt.Errorf("copy node %q: upstream %q (kind=%v) must be a source", copyNode.ID, from.ID, from.Kind)
	}
	src := r.sources[from.ID]
	if src == nil {
		return nil, 0, [2]int{}, fmt.Errorf("copy node %q: source %q has no resources", copyNode.ID, from.ID)
	}
	mt := portTypeToAVMediaType(in.Type)
	for idx, si := range src.streams {
		if si.Type == mt {
			return src.input, idx, si.TimeBase, nil
		}
	}
	return nil, 0, [2]int{}, fmt.Errorf("copy node %q: source %q has no %v stream", copyNode.ID, from.ID, in.Type)
}

// ---------- Helpers ----------

func (r *graphRunner) findOutputConfig(id string) *Output {
	for i := range r.cfg.Outputs {
		if r.cfg.Outputs[i].ID == id {
			return &r.cfg.Outputs[i]
		}
	}
	return nil
}

// resolveStreamInfo returns the upstream StreamInfo for a node by walking
// inbound edges back to a source.
func (r *graphRunner) resolveStreamInfo(dag *graph.Graph, node *graph.Node) (av.StreamInfo, error) {
	if len(node.Inbound) == 0 {
		return av.StreamInfo{}, fmt.Errorf("node %q has no inbound edges", node.ID)
	}
	return r.resolveEdgeStreamInfo(dag, node.Inbound[0])
}

func (r *graphRunner) resolveEdgeStreamInfo(dag *graph.Graph, e *graph.Edge) (av.StreamInfo, error) {
	from := e.From
	switch from.Kind {
	case graph.KindSource:
		src := r.sources[from.ID]
		if src == nil {
			return av.StreamInfo{}, fmt.Errorf("no source resources for node %q", from.ID)
		}
		mt := portTypeToAVMediaType(e.Type)
		for _, si := range src.streams {
			if si.Type == mt {
				return si, nil
			}
		}
		return av.StreamInfo{}, fmt.Errorf("source %q has no %v stream", from.ID, e.Type)
	case graph.KindFilter:
		// If the upstream filter graph is already built (topological order
		// guarantees this), query its actual output dimensions rather than
		// tracing all the way back to the source. This is critical for
		// chained filters (e.g. scale → fps) where the downstream node must
		// be initialised with the scaled, not the source, dimensions.
		if fg := r.filters[from.ID]; fg != nil {
			padIdx := 0
			if e.FromPort != "default" {
				if n, err := strconv.Atoi(e.FromPort); err == nil {
					padIdx = n
				}
			}
			si, err := r.resolveStreamInfo(dag, from)
			if err != nil {
				return av.StreamInfo{}, err
			}
			switch e.Type {
			case graph.PortVideo:
				if w := fg.OutputWidth(padIdx); w > 0 {
					si.Width = w
				}
				if h := fg.OutputHeight(padIdx); h > 0 {
					si.Height = h
				}
				if pf := fg.OutputPixFmt(padIdx); pf >= 0 {
					si.PixFmt = pf
				}
				// Frame-rate metadata advertised by the upstream filter (e.g.
				// fps, framerate, minterpolate, settb) overrides the source's
				// guessed rate. Required so downstream filters that demand a
				// constant frame rate (xfade, framerate, minterpolate) can
				// configure their buffersrc with the correct value.
				if frn, frd := fg.OutputFrameRate(padIdx); frn > 0 && frd > 0 {
					si.FrameRate = [2]int{frn, frd}
				}
				if tbn, tbd := fg.OutputTimeBase(padIdx); tbn > 0 && tbd > 0 {
					si.TimeBase = [2]int{tbn, tbd}
				}
			case graph.PortAudio:
				if sr := fg.OutputSampleRate(padIdx); sr > 0 {
					si.SampleRate = sr
				}
				if ch := fg.OutputChannels(padIdx); ch > 0 {
					si.Channels = ch
				}
				if sf := fg.OutputSampleFmt(padIdx); sf >= 0 {
					si.SampleFmt = sf
				}
				if tbn, tbd := fg.OutputTimeBase(padIdx); tbn > 0 && tbd > 0 {
					si.TimeBase = [2]int{tbn, tbd}
				}
			}
			return si, nil
		}
		return r.resolveStreamInfo(dag, from)
	case graph.KindGoProcessor:
		// Pass through to the node's upstream source.
		return r.resolveStreamInfo(dag, from)
	default:
		return av.StreamInfo{}, fmt.Errorf("cannot resolve stream info from node %q (kind=%v)", from.ID, from.Kind)
	}
}

// upstreamFilterGraph returns the av.FilterGraph for the immediate upstream
// filter, if any. Used to query output dimensions after scaling/padding.
func (r *graphRunner) upstreamFilterGraph(_ *graph.Graph, node *graph.Node) *av.FilterGraph {
	if len(node.Inbound) == 0 {
		return nil
	}
	from := node.Inbound[0].From
	if from.Kind == graph.KindFilter {
		return r.filters[from.ID]
	}
	return nil
}

// buildComplexFilterSpec wraps a base filter spec with input/output pad labels.
// For overlay: "[in0][in1]overlay[out0]"
// For split:   "[in0]split[out0][out1]"
func buildComplexFilterSpec(baseSpec string, numIn, numOut int) string {
	var sb strings.Builder
	for i := 0; i < numIn; i++ {
		fmt.Fprintf(&sb, "[in%d]", i)
	}
	sb.WriteString(baseSpec)
	for i := 0; i < numOut; i++ {
		fmt.Fprintf(&sb, "[out%d]", i)
	}
	return sb.String()
}

func portTypeToAVMediaType(pt graph.PortType) av.MediaType {
	switch pt {
	case graph.PortVideo:
		return av.MediaTypeVideo
	case graph.PortAudio:
		return av.MediaTypeAudio
	case graph.PortSubtitle:
		return av.MediaTypeSubtitle
	default:
		return av.MediaTypeUnknown
	}
}

func paramString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	// JSON numbers unmarshal into map[string]any as float64. Format them as
	// integers (no decimal/scientific notation) so that Sscanf %d parses them
	// correctly (e.g. 7000000.0 → "7000000", not "7e+06").
	if f, ok := v.(float64); ok {
		return strconv.FormatInt(int64(f), 10)
	}
	return fmt.Sprintf("%v", v)
}

func paramInt(m map[string]any, key string) int {
	s := paramString(m, key)
	if s == "" {
		return 0
	}
	var n int
	_, _ = fmt.Sscanf(s, "%d", &n)
	return n
}

func paramInt64(m map[string]any, key string) int64 {
	s := paramString(m, key)
	if s == "" {
		return 0
	}
	var n int64
	_, _ = fmt.Sscanf(s, "%d", &n)
	return n
}

// applyOutputMetadata writes container-level metadata onto muxer
// according to the precedence rules documented at the call site:
// Output.Metadata wins outright; otherwise an explicit
// metadata_writer/reader graph-node pair wins (Wave 2 #11);
// otherwise every input with Input.MapMetadata=true contributes in
// declaration order.
func (r *graphRunner) applyOutputMetadata(muxer *av.OutputFormatContext, out *Output) error {
	if out.Metadata != nil {
		return muxer.SetMetadata(out.Metadata)
	}
	// Wave 2 #11: metadata_writer node targeting this output wins
	// over the input-wide MapMetadata fallback.
	if md, ok := r.routedContainerMetadata(out.ID); ok {
		return muxer.SetMetadata(md)
	}
	var merged map[string]string
	for _, in := range r.cfg.Inputs {
		if !in.MapMetadata {
			continue
		}
		src := r.sources[in.ID]
		if src == nil || src.input == nil {
			continue
		}
		for k, v := range src.input.Metadata() {
			if merged == nil {
				merged = make(map[string]string)
			}
			merged[k] = v
		}
	}
	if merged == nil {
		return nil
	}
	return muxer.SetMetadata(merged)
}

// applyOutputChapters writes chapters onto muxer with the same
// precedence as applyOutputMetadata, except chapters are not merged
// across inputs (FFmpeg's `-map_chapters` is single-source); the first
// input with MapChapters=true wins.
func (r *graphRunner) applyOutputChapters(muxer *av.OutputFormatContext, out *Output) error {
	if len(out.Chapters) > 0 {
		for _, ch := range out.Chapters {
			if err := muxer.AddChapter(ch.ID, ch.Start, ch.End, ch.Title, ch.Metadata); err != nil {
				return err
			}
		}
		return nil
	}
	// Wave 2 #11: metadata_writer node with section=chapters
	// targeting this output wins.
	if chs, ok := r.routedChapters(out.ID); ok {
		for _, ch := range chs {
			if err := muxer.AddChapter(ch.ID, ch.Start, ch.End, ch.Title, ch.Metadata); err != nil {
				return err
			}
		}
		return nil
	}
	for _, in := range r.cfg.Inputs {
		if !in.MapChapters {
			continue
		}
		src := r.sources[in.ID]
		if src == nil || src.input == nil {
			continue
		}
		for _, ch := range src.input.Chapters() {
			if err := muxer.AddChapter(ch.ID, ch.Start, ch.End, ch.Title, ch.Metadata); err != nil {
				return err
			}
		}
		return nil
	}
	return nil
}

// routedContainerMetadata resolves a metadata_writer node targeting
// outputID with section!=chapters and walks back along its inbound
// metadata edges to find a metadata_reader node, returning the source
// input's container metadata. Returns ok=false when no such routing
// exists.
func (r *graphRunner) routedContainerMetadata(outputID string) (map[string]string, bool) {
	reader := r.findMetadataReaderFor(outputID, false)
	if reader == nil {
		return nil, false
	}
	src := paramString(reader.Params, "source")
	in := r.sources[src]
	if in == nil || in.input == nil {
		return nil, false
	}
	md := in.input.Metadata()
	if md == nil {
		md = map[string]string{}
	}
	return md, true
}

// routedChapters mirrors routedContainerMetadata for the chapter
// section.
func (r *graphRunner) routedChapters(outputID string) ([]av.ChapterInfo, bool) {
	reader := r.findMetadataReaderFor(outputID, true)
	if reader == nil {
		return nil, false
	}
	src := paramString(reader.Params, "source")
	in := r.sources[src]
	if in == nil || in.input == nil {
		return nil, false
	}
	return in.input.Chapters(), true
}

// findMetadataReaderFor locates the metadata_reader connected by a
// metadata edge to a metadata_writer targeting outputID. When
// wantChapters is true the writer/reader pair must opt into
// section=chapters; otherwise both must be the default (global)
// section.
func (r *graphRunner) findMetadataReaderFor(outputID string, wantChapters bool) *NodeDef {
	wantSection := "global"
	if wantChapters {
		wantSection = "chapters"
	}
	nodesByID := make(map[string]*NodeDef, len(r.cfg.Graph.Nodes))
	for i := range r.cfg.Graph.Nodes {
		n := &r.cfg.Graph.Nodes[i]
		nodesByID[n.ID] = n
	}
	for i := range r.cfg.Graph.Nodes {
		w := &r.cfg.Graph.Nodes[i]
		if w.Type != "metadata_writer" {
			continue
		}
		if paramString(w.Params, "target") != outputID {
			continue
		}
		section := paramString(w.Params, "section")
		if section == "" {
			section = "global"
		}
		if section != wantSection {
			continue
		}
		// Find metadata edge feeding this writer.
		for _, e := range r.cfg.Graph.Edges {
			if e.Type != "metadata" || edgeNodeID(e.To) != w.ID {
				continue
			}
			reader := nodesByID[edgeNodeID(e.From)]
			if reader == nil || reader.Type != "metadata_reader" {
				continue
			}
			rsec := paramString(reader.Params, "section")
			if rsec == "" {
				rsec = "global"
			}
			if rsec != wantSection {
				continue
			}
			return reader
		}
	}
	return nil
}

// edgeNodeID strips the optional ":port" / ":type:track" suffix from
// an edge endpoint reference.
func edgeNodeID(ref string) string {
	if i := strings.IndexByte(ref, ':'); i >= 0 {
		return ref[:i]
	}
	return ref
}
