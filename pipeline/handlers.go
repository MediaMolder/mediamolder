// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
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
	for _, e := range cfg.Graph.Edges {
		def.Edges = append(def.Edges, graph.EdgeDef{
			From: e.From,
			To:   e.To,
			Type: e.Type,
		})
	}
	expandImplicitEncoders(cfg, def)
	return def
}

// expandImplicitEncoders rewrites edges that connect a source directly to a
// sink, splicing in a synthetic encoder node that uses the sink's
// codec_video / codec_audio (defaulting to libx264 / aac / mov_text when
// the field is empty). This lets compact JobConfigs (one input, one
// output, two stream-routing edges) run end-to-end without the user
// having to declare an encoder node by hand.
//
// The GUI mirrors this pass in `materializeImplicitEncoders` so the
// implicit encoder appears as a real editable node in the canvas; this
// runtime fallback is what makes the JSON also work when fed directly
// to `mediamolder run` without going through the GUI.
//
// Synthetic encoder nodes use the "__enc__" prefix to avoid colliding
// with user-supplied node IDs.
func expandImplicitEncoders(cfg *Config, def *graph.Def) {
	inputIDs := make(map[string]bool, len(cfg.Inputs))
	for _, inp := range cfg.Inputs {
		inputIDs[inp.ID] = true
	}
	outputs := make(map[string]Output, len(cfg.Outputs))
	for _, out := range cfg.Outputs {
		outputs[out.ID] = out
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
		if !inputIDs[fromID] {
			continue
		}
		out, ok := outputs[toID]
		if !ok {
			continue
		}
		var codec string
		switch e.Type {
		case "video":
			codec = out.CodecVideo
			if codec == "" {
				codec = "libx264"
			}
		case "audio":
			codec = out.CodecAudio
			if codec == "" {
				codec = "aac"
			}
		case "subtitle":
			codec = out.CodecSubtitle
			if codec == "" {
				codec = "mov_text"
			}
		}
		if codec == "" {
			continue
		}
		encID := fmt.Sprintf("__enc__%s_%s_%d", toID, e.Type, i)
		def.Nodes = append(def.Nodes, graph.NodeDef{
			ID:     encID,
			Type:   "encoder",
			Params: map[string]any{"codec": codec},
		})
		added = append(added, graph.EdgeDef{From: encID, To: e.To, Type: e.Type})
		e.To = encID
	}
	def.Edges = append(def.Edges, added...)
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
}

// sinkResources holds a muxer and the encoder(s) feeding it.
type sinkResources struct {
	muxer *av.OutputFormatContext
	cfg   Output
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

	// Map edge type → output channel indices.
	videoOuts := make([]int, 0, 1)
	audioOuts := make([]int, 0, 1)
	subtitleOuts := make([]int, 0, 1)
	for i, e := range node.Outbound {
		switch e.Type {
		case graph.PortVideo:
			videoOuts = append(videoOuts, i)
		case graph.PortAudio:
			audioOuts = append(audioOuts, i)
		case graph.PortSubtitle:
			subtitleOuts = append(subtitleOuts, i)
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

	receiveAll := func(dec *av.DecoderContext, mt av.MediaType) error {
		var indices []int
		switch mt {
		case av.MediaTypeVideo:
			indices = videoOuts
		case av.MediaTypeAudio:
			indices = audioOuts
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

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		pkt.Unref()
		frameStart := time.Now()
		if err := src.input.ReadPacket(pkt); err != nil {
			if av.IsEOF(err) {
				break
			}
			return err
		}
		dec := src.decoders[pkt.StreamIndex()]
		subDec := src.subDecoders[pkt.StreamIndex()]
		if dec == nil && subDec == nil {
			continue
		}
		si := src.streams[pkt.StreamIndex()]

		// Publish media-time progress so the GUI can compute
		// percent-complete / ETA. Skip packets without a valid PTS
		// (AV_NOPTS_VALUE == math.MinInt64) and streams without a
		// known timebase.
		if pts := pkt.PTS(); pts != math.MinInt64 && si.TimeBase[1] > 0 {
			ptsNs := time.Duration(pts) * time.Second *
				time.Duration(si.TimeBase[0]) / time.Duration(si.TimeBase[1])
			r.pipe.Metrics().Node(node.ID).AdvanceMediaPTS(ptsNs)
		}

		// Handle subtitle streams via subtitle decoder.
		if subDec != nil && len(subtitleOuts) > 0 {
			sub, got, err := subDec.Decode(pkt)
			if err != nil {
				return err
			}
			if got {
				for _, idx := range subtitleOuts {
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
			continue
		}
		if err := dec.SendPacket(pkt); err != nil {
			return err
		}
		if err := receiveAll(dec, si.Type); err != nil {
			return err
		}
		r.pipe.Metrics().Node(node.ID).RecordLatency(time.Since(frameStart))
	}

	// Flush every decoder.
	for idx, dec := range src.decoders {
		if err := dec.Flush(); err != nil && !av.IsEOF(err) {
			return err
		}
		si := src.streams[idx]
		// Drain remaining decoded frames.
		for {
			f, err := av.AllocFrame()
			if err != nil {
				return err
			}
			if err := dec.ReceiveFrame(f); err != nil {
				f.Close()
				if av.IsEOF(err) {
					break
				}
				return err
			}
			switch si.Type {
			case av.MediaTypeVideo:
				if err := sendFrame(f, videoOuts); err != nil {
					f.Close()
					return err
				}
			case av.MediaTypeAudio:
				if err := sendFrame(f, audioOuts); err != nil {
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
				return err
			}
			msg.frame.Close()
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
	if len(ins) != 1 || len(outs) != 1 {
		return fmt.Errorf("encoder node %q: expected 1 input / 1 output, got %d/%d", node.ID, len(ins), len(outs))
	}

	in, out := ins[0], outs[0]

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
			select {
			case out <- p:
			case <-ctx.Done():
				p.Close()
				return ctx.Err()
			}
		}
	}

	for v := range in {
		f := v.(*av.Frame)
		frameStart := time.Now()
		if err := enc.SendFrame(f); err != nil {
			f.Close()
			return err
		}
		f.Close()
		if err := drainEncoder(); err != nil {
			return err
		}
		r.pipe.Metrics().Node(node.ID).RecordLatency(time.Since(frameStart))
	}

	// Flush.
	if err := enc.Flush(); err != nil && !av.IsEOF(err) {
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

	if len(ins) == 1 {
		for v := range ins[0] {
			pkt := v.(*av.Packet)
			pkt.SetStreamIndex(0)
			frameStart := time.Now()
			if err := sink.muxer.WritePacket(pkt); err != nil {
				pkt.Close()
				return err
			}
			pkt.Close()
			r.pipe.Metrics().Node(node.ID).RecordLatency(time.Since(frameStart))
		}
		return sink.muxer.WriteTrailer()
	}

	// Multiple input streams: interleave with per-stream goroutines.
	eg, _ := errgroup.WithContext(ctx)
	var mu sync.Mutex

	for i, in := range ins {
		i, in := i, in
		eg.Go(func() error {
			for v := range in {
				pkt := v.(*av.Packet)
				pkt.SetStreamIndex(i)
				frameStart := time.Now()
				mu.Lock()
				err := sink.muxer.WritePacket(pkt)
				mu.Unlock()
				pkt.Close()
				r.pipe.Metrics().Node(node.ID).RecordLatency(time.Since(frameStart))
				if err != nil {
					return err
				}
			}
			return nil
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

func (r *graphRunner) openSource(cfg Input, decOpts av.DecoderOptions) (*sourceResources, error) {
	var inputOpts map[string]string
	if len(cfg.Options) > 0 {
		inputOpts = make(map[string]string, len(cfg.Options))
		for k, v := range cfg.Options {
			inputOpts[k] = fmt.Sprintf("%v", v)
		}
	}

	input, err := av.OpenInput(cfg.URL, inputOpts)
	if err != nil {
		return nil, fmt.Errorf("open input %q: %w", cfg.URL, err)
	}

	allStreams, err := input.AllStreams()
	if err != nil {
		input.Close()
		return nil, fmt.Errorf("enumerate streams %q: %w", cfg.URL, err)
	}

	decoders := make(map[int]*av.DecoderContext)
	subDecoders := make(map[int]*av.SubtitleDecoderContext)
	streams := make(map[int]av.StreamInfo)

	for _, sel := range cfg.Streams {
		count := 0
		for _, si := range allStreams {
			if si.Type.String() == sel.Type {
				if count == sel.Track {
					if sel.Type == "subtitle" {
						subDec, err := av.OpenSubtitleDecoder(input, si.Index)
						if err != nil {
							for _, d := range decoders {
								d.Close()
							}
							for _, d := range subDecoders {
								d.Close()
							}
							input.Close()
							return nil, fmt.Errorf("open subtitle decoder for track %d: %w", sel.Track, err)
						}
						subDecoders[si.Index] = subDec
					} else {
						dec, err := av.OpenDecoderWithOptions(input, si.Index, decOpts)
						if err != nil {
							for _, d := range decoders {
								d.Close()
							}
							for _, d := range subDecoders {
								d.Close()
							}
							input.Close()
							return nil, fmt.Errorf("open decoder for %s track %d: %w", sel.Type, sel.Track, err)
						}
						decoders[si.Index] = dec
					}
					streams[si.Index] = si
					break
				}
				count++
			}
		}
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

	return &sourceResources{
		input:         input,
		decoders:      decoders,
		subDecoders:   subDecoders,
		streams:       streams,
		cfg:           cfg,
		mediaDuration: mediaDuration,
	}, nil
}

func (r *graphRunner) createFilter(dag *graph.Graph, node *graph.Node) (*av.FilterGraph, error) {
	filterSpec := buildFilterSpec(NodeDef{
		Filter: node.Filter,
		Params: node.Params,
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

	return av.OpenEncoder(opts)
}

func (r *graphRunner) openSink(_ *graph.Graph, node *graph.Node) (*sinkResources, error) {
	out := r.findOutputConfig(node.ID)
	if out == nil {
		return nil, fmt.Errorf("sink node %q: no matching output config", node.ID)
	}

	muxer, err := av.OpenOutput(out.URL)
	if err != nil {
		return nil, fmt.Errorf("open output %q: %w", out.URL, err)
	}

	// Add one stream per inbound edge. The encoder for each stream has already
	// been opened (topological order guarantees this).
	for _, e := range node.Inbound {
		from := e.From
		enc := r.encoders[from.ID]
		if enc == nil {
			muxer.Abort()
			return nil, fmt.Errorf("sink %q: inbound from %q has no encoder", node.ID, from.ID)
		}
		if _, err := muxer.AddStream(enc); err != nil {
			muxer.Abort()
			return nil, fmt.Errorf("sink %q add stream: %w", node.ID, err)
		}
	}

	if err := muxer.WriteHeader(); err != nil {
		muxer.Abort()
		return nil, fmt.Errorf("sink %q write header: %w", node.ID, err)
	}

	return &sinkResources{muxer: muxer, cfg: *out}, nil
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
	case graph.KindFilter, graph.KindGoProcessor:
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
