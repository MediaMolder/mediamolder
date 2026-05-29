// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"context"
	"fmt"
	"math"
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
	decoders    map[int]av.FrameDecoder            // keyed by stream index; sw or hw decoder
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
	// ownedHWDev is the hardware device context opened by openSource
	// for per-input hwaccel when cfg.HWAccelDevice is empty (i.e. no
	// pre-declared hardware_devices entry was matched). When non-nil,
	// Close() frees it after all decoders have been closed.
	// (Wave 10 #59)
	ownedHWDev *av.HWDeviceContext
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
	if s.ownedHWDev != nil {
		s.ownedHWDev.Close()
		s.ownedHWDev = nil
	}
	if s.concatCleanup != nil {
		s.concatCleanup()
		s.concatCleanup = nil
	}
}

// muxWriter is the subset of *av.OutputFormatContext used by sinkWriter
// and handleSink. The interface allows a fake muxer to be injected in
// table-driven tests without requiring CGO.
type muxWriter interface {
	WritePacket(pkt *av.Packet) error
	WriteTrailer() error
	BytesWritten() int64
	StreamTimeBase(idx int) [2]int
	Abort()
	Close() error
}

// sinkResources holds a muxer and the encoder(s) feeding it.
type sinkResources struct {
	muxer muxWriter
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

	// preroll holds the Phase 7 per-output pre-roll buffer when the
	// pipeline is in real-time mode and prebuffer_duration_seconds > 0.
	// nil when prerolling is disabled; the sink handler then writes
	// straight through to the muxer as before.
	preroll *OutputBuffer

	// pendingCut is non-nil when the output has SegmentOnMetadata set.
	// Registered in r.segmentCuts during openSink (before goroutines start).
	// The go_processor handler stores the cut frame's source-PTS (in
	// microseconds) when a matching Custom key is truthy; handleSink rotates
	// the segment at the first video keyframe whose PTS is at or past that
	// value. The gate also synchronises non-video sink writers (e.g. audio
	// stream-copy) so they wait for the rotation before writing packets
	// that belong to the new segment.
	pendingCut *cutGate
}

// cutGate carries a queue of per-output segment-cut barriers between the
// go_processor that detects scene changes and the sink writer goroutines.
// The video goroutine performs the actual muxer rotation when it encounters
// the keyframe at or after the front cut PTS; non-video goroutines (e.g.
// audio copy) run ahead of the encoder's lookahead delay so they may see
// several queued cuts before the video reaches the first one.
type cutGate struct {
	mu   sync.Mutex
	cond *sync.Cond
	cuts []int64 // FIFO of pending cut PTS in microseconds (ascending)
	// progressUS is the latest source-PTS in microseconds that the
	// go_processor has finished processing. Non-video sink goroutines
	// (e.g. audio stream-copy) hold packets whose PTS exceeds this value
	// because a future cut at that PTS may not have been signalled yet.
	// math.MaxInt64 means the go_processor has finished and no further
	// cuts will arrive.
	progressUS int64
}

func newCutGate() *cutGate {
	g := &cutGate{}
	g.cond = sync.NewCond(&g.mu)
	return g
}

// signal appends cutUS to the queue (deduplicated). Cuts are expected to
// arrive in source-PTS order from the go_processor.
func (g *cutGate) signal(cutUS int64) {
	if g == nil {
		return
	}
	g.mu.Lock()
	if n := len(g.cuts); n == 0 || g.cuts[n-1] != cutUS {
		g.cuts = append(g.cuts, cutUS)
	}
	g.mu.Unlock()
}

// frontCut returns the next pending cut PTS in microseconds, or -1 if
// the queue is empty.
func (g *cutGate) frontCut() int64 {
	if g == nil {
		return -1
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.cuts) == 0 {
		return -1
	}
	return g.cuts[0]
}

// nextCutAfter returns the first queued cut strictly greater than after,
// or -1 if no such cut exists. Used by non-video writers to decide which
// held packets belong to the just-rotated segment vs later segments.
func (g *cutGate) nextCutAfter(after int64) int64 {
	if g == nil {
		return -1
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, c := range g.cuts {
		if c > after {
			return c
		}
	}
	return -1
}

// popFront removes the head of the queue after a successful rotation and
// broadcasts so any waiters wake.
func (g *cutGate) popFront() {
	if g == nil {
		return
	}
	g.mu.Lock()
	if len(g.cuts) > 0 {
		g.cuts = g.cuts[1:]
	}
	g.cond.Broadcast()
	g.mu.Unlock()
}

// clearAll drops every queued cut and wakes waiters. Used when the video
// goroutine exits without rotating (e.g. EOS with pending cuts).
func (g *cutGate) clearAll() {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.cuts = nil
	g.cond.Broadcast()
	g.mu.Unlock()
}

// advanceProgress records that the go_processor has finished processing
// the source frame with PTS == us microseconds. Audio writers use this to
// know that no further cut < us can ever arrive.
func (g *cutGate) advanceProgress(us int64) {
	if g == nil {
		return
	}
	g.mu.Lock()
	if us > g.progressUS {
		g.progressUS = us
	}
	g.mu.Unlock()
}

// finishProgress marks the go_processor stream complete; no further cuts
// will be signalled, so audio writers can flush any held packets.
func (g *cutGate) finishProgress() {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.progressUS = math.MaxInt64
	g.mu.Unlock()
}

// progress returns the latest go_processor frame PTS in microseconds.
func (g *cutGate) progress() int64 {
	if g == nil {
		return math.MaxInt64
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.progressUS
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
	trackers     map[string]*NodePerfTracker
	goProcessors map[string]processors.Processor
	// eventsSinks is keyed by source go_processor node ID. Each entry
	// contains the file sinks created by the engine for "events" edges
	// that flow from that node to metadata_file_writer nodes.
	// These sinks are independent of the go_processor lifecycle and are
	// closed by graphRunner.close().
	eventsSinks map[string][]*processors.EventSink
	// encoderOpts stores the EncoderOptions used to open each encoder,
	// keyed by encoder node ID. Populated by createEncoder alongside
	// encoders; used by handleEncoder to reopen the encoder with the
	// first frame's actual coded dimensions when the container metadata
	// disagrees with the real bitstream (e.g. anamorphic AVI).
	encoderOpts map[string]av.EncoderOptions
	// passLogFiles holds open pass-1 statistics files for video
	// encoders that consume `Output.Pass` / `Output.PassLogFile`
	// via the generic AVCodecContext.stats_out path (i.e. not
	// libx264 / libx265 / libvvenc, which manage their own stats
	// files via the codec's `stats` AVOption). Keyed by encoder
	// node ID. Populated by createEncoder and closed in close().
	passLogFiles map[string]*os.File
	// hwDevices holds opened hardware-acceleration device contexts keyed
	// by the symbolic name declared in Config.HardwareDevices. Populated
	// by runGraph before any nodes are opened; closed in close().
	// (Wave 10 #56)
	hwDevices map[string]*av.HWDeviceContext
	// segmentCuts maps a Metadata.Custom key to the set of pending-cut-PTS
	// flags registered by segment_sink outputs that watch that key.
	// When a go_processor emits ProcessorMetadata with Custom[key] truthy,
	// the handler signals every matching gate with the cut PTS in microseconds.
	// Each gate is owned by a sink goroutine (via handleSink) and read under
	// its internal mutex.
	segmentCuts map[string][]*cutGate
	// goProcessorInputTB maps a go_processor node ID to the time-base of its
	// (sole) input edge. Pre-resolved during engine setup so handleGoProcessor
	// can convert frame PTS values to microseconds when signalling cutGates,
	// allowing cross-stream (video → audio) PTS comparisons at the sink.
	goProcessorInputTB map[string][2]int
	// segmentConsumers maps a sink node ID (the SOURCE of an "events"
	// edge) to the set of go_processors implementing
	// processors.SegmentEventConsumer that should be notified when the
	// sink finishes writing a segment file.
	segmentConsumers map[string][]processors.SegmentEventConsumer
	// pureEventSinkNodes is the set of go_processor node IDs that act
	// solely as events-wiring sinks (metadata_file_writer without an
	// inner_processor param). They are not in goProcessors and have no
	// AV frame loop; handleGoProcessor returns nil for them.
	pureEventSinkNodes map[string]struct{}
	// eventDrivenGoProcessors is the set of go_processor node IDs that
	// are driven by inbound "events" edges (e.g. twelvelabs_indexer with
	// an events edge from an input or a sink) rather than AV frame
	// streams. When such a node has no AV inputs, handleGoProcessor
	// returns nil because the work is dispatched via OnSegmentCompleted
	// or the metadata emitter, not via the AV scheduler.
	eventDrivenGoProcessors map[string]struct{}
	// goProcessorCloseOrder lists go_processor IDs in the order close()
	// must close them. Event producers (upstream in events edges) come
	// before their consumers so that a producer's Close() can block until
	// its upload goroutines fire OnSegmentCompleted on the consumer before
	// the consumer is closed. Populated by the events-wiring loop.
	// Any processor not in this list is closed afterwards in map order.
	goProcessorCloseOrder []string
}

func newGraphRunner(cfg *Config, pipe *Pipeline) *graphRunner {
	return &graphRunner{
		cfg:                     cfg,
		pipe:                    pipe,
		sources:                 make(map[string]*sourceResources),
		filters:                 make(map[string]*av.FilterGraph),
		encoders:                make(map[string]*av.EncoderContext),
		encoderOpts:             make(map[string]av.EncoderOptions),
		sinks:                   make(map[string]*sinkResources),
		trackers:                make(map[string]*NodePerfTracker),
		goProcessors:            make(map[string]processors.Processor),
		eventsSinks:             make(map[string][]*processors.EventSink),
		pureEventSinkNodes:      make(map[string]struct{}),
		eventDrivenGoProcessors: make(map[string]struct{}),
		passLogFiles:            make(map[string]*os.File),
		hwDevices:               make(map[string]*av.HWDeviceContext),
			segmentCuts:             make(map[string][]*cutGate),
		goProcessorInputTB:      make(map[string][2]int),
		segmentConsumers:        make(map[string][]processors.SegmentEventConsumer),
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
	// Close go_processors in topological order (event producers first) so that
	// a producer's Close() — which blocks until its upload goroutines finish
	// and fire OnSegmentCompleted on downstream consumers — completes before
	// the consumer's Close() waits on its own WaitGroup.
	closedProc := make(map[string]bool, len(r.goProcessors))
	for _, id := range r.goProcessorCloseOrder {
		if p, ok := r.goProcessors[id]; ok {
			p.Close()
			closedProc[id] = true
		}
	}
	for id, p := range r.goProcessors {
		if !closedProc[id] {
			p.Close()
		}
	}
	for _, sinks := range r.eventsSinks {
		for _, s := range sinks {
			_ = s.Close()
		}
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
		s.preroll.Close()
	}
	for _, d := range r.hwDevices {
		_ = d.Close()
	}
	// Sinks are finalized by the caller (muxer.Close for atomic rename).
}

// handle dispatches to the appropriate per-kind handler.
// It implements runtime.NodeHandler.
func (r *graphRunner) handle(ctx context.Context, node *graph.Node, ins []<-chan any, outs []chan<- any) error {
	if t := r.trackers[node.ID]; t != nil {
		ctx = withPerfTracker(ctx, t)
	}
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
