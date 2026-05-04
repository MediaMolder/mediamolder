// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/graph"
	"github.com/MediaMolder/MediaMolder/processors"
)

// configToGraphDef converts a pipeline Config into a graph.Def suitable for
// graph.Build. Inputs become source nodes, outputs become sink nodes.
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
	case graph.KindFilterSource:
		return r.handleFilterSource(ctx, node, outs)
	case graph.KindFilterSink:
		return r.handleFilterSink(ctx, node, ins)
	default:
		return fmt.Errorf("unknown node kind %v for node %q", node.Kind, node.ID)
	}
}
