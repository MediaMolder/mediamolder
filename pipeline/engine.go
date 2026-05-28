// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/graph"
	"github.com/MediaMolder/MediaMolder/observability"
	"github.com/MediaMolder/MediaMolder/pipeline/snap"
	"github.com/MediaMolder/MediaMolder/processors"
	"github.com/MediaMolder/MediaMolder/runtime"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
)

// Pipeline executes a media processing pipeline driven by a Config.
type Pipeline struct {
	cfg *Config

	mu        sync.Mutex
	state     State
	cancel    context.CancelFunc
	eg        *errgroup.Group
	events    *EventBus
	pauseCh   chan struct{} // closed when unpaused; recreated on pause
	runErr    error         // error from the data-flow goroutines
	runDone   chan struct{} // closed when data-flow finishes
	parentCtx context.Context

	seekTarget  int64 // seek target in AV_TIME_BASE units
	seekPending bool  // true when a seek has been requested

	metrics      *MetricsRegistry
	edgeStats    *runtime.EdgeStatsRegistry
	prom         *observability.Metrics  // optional Prometheus metrics; nil = disabled
	obsProvider  *observability.Provider // optional OTel tracing provider; nil = disabled
	maxThreads   int                     // per-codec thread cap from SecurityConfig; 0 = unlimited
	reconf       *reconfigurable         // live filter parameter changes (graph mode only)
	graphRunner  *graphRunner            // running graph resources (graph mode only)
	realtimeCtrl *realtimeController     // Phase 6 adaptive controller; nil when --realtime is off
	ready        *graphReady             // Phase 7 per-output preroll aggregator; nil when realtime off
}

// NewPipeline creates a Pipeline from a validated Config.
// The pipeline starts in StateNull.
func NewPipeline(cfg *Config) (*Pipeline, error) {
	if err := av.CheckVersion(); err != nil {
		return nil, err
	}
	return &Pipeline{
		cfg:     cfg,
		state:   StateNull,
		events:  NewEventBus(256),
		metrics: NewMetricsRegistry(),
	}, nil
}

// Events returns a read-only channel of pipeline events.
func (p *Pipeline) Events() <-chan Event {
	return p.events.Chan()
}

// State returns the current pipeline state.
func (p *Pipeline) State() State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

// SetState requests a state transition. Transitions are sequential:
// NULL→READY→PAUSED→PLAYING. Intermediate states are traversed automatically.
// Any state can transition to NULL (teardown).
func (p *Pipeline) SetState(target State) error {
	p.mu.Lock()
	cur := p.state
	p.mu.Unlock()

	if cur == target {
		return nil
	}

	// Any → NULL is always allowed.
	if target == StateNull {
		return p.transitionToNull()
	}

	// Forward transitions: walk through intermediate states.
	if target > cur {
		for next := cur + 1; next <= target; next++ {
			if err := p.stepForward(next); err != nil {
				return err
			}
		}
		return nil
	}

	// Backward: only PLAYING→PAUSED is allowed (besides →NULL above).
	if cur == StatePlaying && target == StatePaused {
		return p.stepBackward(StatePaused)
	}

	return &ErrInvalidStateTransition{From: cur, To: target}
}

// Start transitions NULL→PLAYING (through READY, PAUSED).
func (p *Pipeline) Start(ctx context.Context) error {
	p.mu.Lock()
	p.parentCtx = ctx
	p.mu.Unlock()
	return p.SetState(StatePlaying)
}

// Pause transitions PLAYING→PAUSED.
func (p *Pipeline) Pause() error {
	return p.SetState(StatePaused)
}

// Resume transitions PAUSED→PLAYING.
func (p *Pipeline) Resume() error {
	return p.SetState(StatePlaying)
}

// SeekTo pauses the pipeline, flushes buffers, seeks all inputs to the target
// timestamp, and leaves the pipeline in PAUSED state. The caller must call
// Resume() to continue processing from the new position.
// target is in AV_TIME_BASE units (microseconds).
func (p *Pipeline) SeekTo(target int64) error {
	p.mu.Lock()
	cur := p.state
	p.mu.Unlock()

	if cur != StatePlaying && cur != StatePaused {
		return fmt.Errorf("cannot seek in state %s", cur)
	}

	// Pause if currently playing.
	if cur == StatePlaying {
		if err := p.Pause(); err != nil {
			return fmt.Errorf("seek pause: %w", err)
		}
	}

	p.mu.Lock()
	p.seekTarget = target
	p.seekPending = true
	p.mu.Unlock()

	p.events.Post(StateChanged{
		From: StatePaused,
		To:   StatePaused,
	})
	return nil
}

// seekState returns any pending seek target and resets the flag.
func (p *Pipeline) seekState() (int64, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.seekPending {
		return 0, false
	}
	target := p.seekTarget
	p.seekPending = false
	return target, true
}

// GetMetrics returns a point-in-time snapshot of pipeline metrics.
func (p *Pipeline) GetMetrics() MetricsSnapshot {
	shot := p.metrics.Snapshot()
	shot.State = p.State().String()
	// Phase 6: attach graph-level realtime summary when the adaptive
	// controller is active.
	p.mu.Lock()
	ctrl := p.realtimeCtrl
	p.mu.Unlock()
	if ctrl != nil {
		rt := snap.RealtimeSnapshot{Enabled: true, Decisions: ctrl.snapshotDecisions()}
		rt.FPSTarget, rt.FPSActual, rt.Satisfied = graphFPS(shot, ctrl.dag)
		// Phase 7: per-output preroll readiness.
		p.mu.Lock()
		ready := p.ready
		p.mu.Unlock()
		if ready != nil {
			r, since, outs := ready.State()
			rt.Ready = r
			rt.ReadyAt = since
			rt.Outputs = make([]snap.OutputBufferSnapshot, 0, len(outs))
			for _, o := range outs {
				rt.Outputs = append(rt.Outputs, snap.OutputBufferSnapshot{
					NodeID:      o.NodeID,
					State:       o.State,
					BufferedDur: o.BufferedDur,
					TargetDur:   o.TargetDur,
					Evictions:   o.Evictions,
				})
			}
		}
		shot.Realtime = rt
	}
	return shot
}

// Metrics returns the underlying MetricsRegistry for direct node updates.
func (p *Pipeline) Metrics() *MetricsRegistry {
	return p.metrics
}

// EdgeStats returns the edge backpressure registry, or nil if not running.
func (p *Pipeline) EdgeStats() *runtime.EdgeStatsRegistry {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.edgeStats
}

// SetPrometheus sets the Prometheus metrics collector for this pipeline.
// Must be called before SetState(StatePlaying).
func (p *Pipeline) SetPrometheus(prom *observability.Metrics) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prom = prom
}

// SetObsProvider sets the OpenTelemetry tracing provider for this pipeline.
// Must be called before SetState(StatePlaying).
func (p *Pipeline) SetObsProvider(provider *observability.Provider) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.obsProvider = provider
}

// SetMaxThreads sets the per-codec thread cap from SecurityConfig.MaxThreads.
// When > 0, each decoder/encoder thread count is clamped to this value.
// Must be called before SetState(StatePlaying).
func (p *Pipeline) SetMaxThreads(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.maxThreads = n
}

// Wait blocks until the pipeline finishes (EOS or error) while in PLAYING state.
// Returns nil on clean EOS, or the pipeline error.
func (p *Pipeline) Wait() error {
	p.mu.Lock()
	done := p.runDone
	p.mu.Unlock()
	if done == nil {
		return nil
	}
	<-done

	p.mu.Lock()
	err := p.runErr
	p.mu.Unlock()
	return err
}

// Close transitions any→NULL and releases all resources.
func (p *Pipeline) Close() error {
	err := p.SetState(StateNull)
	p.events.Close()
	return err
}

// Run is a convenience method that starts the pipeline, waits for completion,
// then tears down. Equivalent to Start + Wait + Close.
func (p *Pipeline) Run(ctx context.Context) error {
	if err := p.Start(ctx); err != nil {
		return err
	}
	err := p.Wait()
	closeErr := p.Close()
	if err != nil {
		return err
	}
	return closeErr
}

// stepForward performs a single upward state transition.
func (p *Pipeline) stepForward(target State) error {
	p.mu.Lock()
	cur := p.state
	p.mu.Unlock()

	start := time.Now()

	switch target {
	case StateReady:
		// Validate config is usable (inputs/outputs present).
		// Actual resource allocation is deferred to PAUSED to keep READY cheap.
		// Wave 7 #36c: a config may have zero Inputs when it stands
		// entirely on filter_source nodes (color, testsrc, sine, …).
		// Wave 7 #36d: a config may have zero Outputs when every sink
		// is a filter_sink (nullsink/anullsink terminating a side-effect
		// chain such as ebur128 or ametadata=mode=print).
		if len(p.cfg.Outputs) == 0 && !configHasFilterSink(p.cfg) && !configHasOnlyGoProcessors(p.cfg) {
			return fmt.Errorf("config has no outputs or filter_sink nodes")
		}
		if len(p.cfg.Inputs) == 0 && !configHasFilterSource(p.cfg) {
			return fmt.Errorf("config has no inputs or filter_source nodes")
		}

	case StatePaused:
		// Create the pause channel (starts paused).
		p.mu.Lock()
		p.pauseCh = make(chan struct{})
		p.mu.Unlock()

	case StatePlaying:
		// If coming from PAUSED and data flow not yet started, start it.
		p.mu.Lock()
		needsStart := p.runDone == nil
		if p.pauseCh != nil {
			// Signal unpause by closing the channel.
			select {
			case <-p.pauseCh:
				// Already unpaused.
			default:
				close(p.pauseCh)
			}
		}
		p.mu.Unlock()

		if needsStart {
			p.startDataFlow()
		}

	default:
		return &ErrInvalidStateTransition{From: cur, To: target}
	}

	p.mu.Lock()
	p.state = target
	p.mu.Unlock()

	p.events.Post(StateChanged{
		From:     cur,
		To:       target,
		Duration: time.Since(start),
	})
	return nil
}

// stepBackward performs PLAYING→PAUSED.
func (p *Pipeline) stepBackward(target State) error {
	p.mu.Lock()
	cur := p.state
	p.mu.Unlock()

	start := time.Now()

	// Recreate the pause channel to suspend data flow.
	p.mu.Lock()
	p.pauseCh = make(chan struct{})
	p.state = StatePaused
	p.mu.Unlock()

	p.events.Post(StateChanged{
		From:     cur,
		To:       target,
		Duration: time.Since(start),
	})
	return nil
}

// transitionToNull tears down everything back to NULL.
func (p *Pipeline) transitionToNull() error {
	p.mu.Lock()
	cur := p.state
	cancel := p.cancel
	// Unpause so goroutines can drain.
	if p.pauseCh != nil {
		select {
		case <-p.pauseCh:
		default:
			close(p.pauseCh)
		}
	}
	p.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	// Wait for data-flow goroutines to finish.
	if p.eg != nil {
		_ = p.eg.Wait() // error already captured in runDone
	}

	start := time.Now()

	p.mu.Lock()
	p.state = StateNull
	p.cancel = nil
	p.eg = nil
	p.runDone = nil
	p.runErr = error(nil)
	p.pauseCh = nil
	p.seekTarget = 0
	p.seekPending = false
	p.mu.Unlock()

	if cur != StateNull {
		p.events.Post(StateChanged{
			From:     cur,
			To:       StateNull,
			Duration: time.Since(start),
		})
	}
	return nil
}

// startDataFlow launches the goroutine-per-stage pipeline.
func (p *Pipeline) startDataFlow() {
	p.mu.Lock()
	ctx := p.parentCtx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	g, gctx := errgroup.WithContext(ctx)
	p.eg = g
	done := make(chan struct{})
	p.runDone = done
	p.mu.Unlock()

	go func() {
		var err error
		if len(p.cfg.Graph.Edges) > 0 {
			err = p.runGraph(gctx)
		} else {
			err = p.runLinear(gctx, g)
		}
		p.mu.Lock()
		p.runErr = err
		p.mu.Unlock()
		if err == nil {
			p.events.Post(EOS{})
		} else {
			p.events.Post(ErrorEvent{Err: err, Time: time.Now()})
		}
		close(done)
	}()
}

// runLinear is the legacy non-graph execution path used when
// Config.Graph is empty. It is the *only* runtime entry that still
// reads authoring shorthand (Output.CodecVideo, GlobalOptions.Threads,
// GlobalOptions.ThreadType) directly from Config rather than from a
// normalized graph node. This intentional exemption is documented in
// docs/field-ownership.md (§ "Linear-mode exemption") and tracked in
// private_local/followups_roadmap.md as F7 — the linear-mode
// retire-or-keep decision. Callers that build a graph go through
// runGraph, which honours the Milestone C invariant: no shorthand
// reads after NormalizeConfig.
func (e *Pipeline) runLinear(ctx context.Context, g *errgroup.Group) error {
	cfg := e.cfg
	inCfg := cfg.Inputs[0]
	outCfg := cfg.Outputs[0]

	// Convert Input.Options (map[string]any) to map[string]string for av.OpenInput.
	var inputOpts map[string]string
	if len(inCfg.Options) > 0 {
		inputOpts = make(map[string]string, len(inCfg.Options))
		for k, v := range inCfg.Options {
			inputOpts[k] = fmt.Sprintf("%v", v)
		}
	}

	input, err := av.OpenInput(inCfg.URL, inputOpts)
	if err != nil {
		return fmt.Errorf("open input %q: %w", inCfg.URL, err)
	}
	defer input.Close()

	// Resolve the first requested video stream.
	vidIdx := -1
	all, _ := input.AllStreams()
	for _, sel := range inCfg.Streams {
		if sel.Type != "video" {
			continue
		}
		count := 0
		for _, si := range all {
			if si.Type == av.MediaTypeVideo {
				if count == sel.Track {
					vidIdx = si.Index
					break
				}
				count++
			}
		}
		if vidIdx >= 0 {
			break
		}
	}
	if vidIdx < 0 {
		return fmt.Errorf("no video stream found in %q", inCfg.URL)
	}

	si, _ := input.StreamInfo(vidIdx)

	// Resolve global thread settings for linear pipeline.
	globalThreads := cfg.GlobalOptions.Threads
	globalThreadType := cfg.GlobalOptions.ThreadType
	if e.maxThreads > 0 && globalThreads > e.maxThreads {
		globalThreads = e.maxThreads
	}

	dec, err := av.OpenDecoderWithOptions(input, vidIdx, av.DecoderOptions{
		ThreadCount: globalThreads,
		ThreadType:  globalThreadType,
	})
	if err != nil {
		return fmt.Errorf("open decoder: %w", err)
	}
	defer dec.Close()

	filterSpec := "null"
	for _, node := range cfg.Graph.Nodes {
		if node.Type == "filter" && node.Filter != "" {
			filterSpec = buildFilterSpec(node)
			break
		}
	}

	fg, err := av.NewVideoFilterGraph(av.VideoFilterGraphConfig{
		Width:      si.Width,
		Height:     si.Height,
		PixFmt:     si.PixFmt,
		TBNum:      si.TimeBase[0],
		TBDen:      si.TimeBase[1],
		SARNum:     1,
		SARDen:     1,
		FilterSpec: filterSpec,
	})
	if err != nil {
		return fmt.Errorf("build filter graph %q: %w", filterSpec, err)
	}
	defer fg.Close()

	// Determine frame rate: use stream info, fall back to 25fps.
	frameRate := si.FrameRate
	if frameRate[0] <= 0 || frameRate[1] <= 0 {
		frameRate = [2]int{25, 1}
	}

	enc, err := av.OpenEncoder(av.EncoderOptions{
		CodecName:    outCfg.CodecVideo,
		Width:        si.Width,
		Height:       si.Height,
		FrameRate:    frameRate,
		GlobalHeader: true,
		ThreadCount:  globalThreads,
		ThreadType:   globalThreadType,
	})
	if err != nil {
		return fmt.Errorf("open encoder %q: %w", outCfg.CodecVideo, err)
	}
	defer enc.Close()

	muxer, err := av.OpenOutput(outCfg.URL)
	if err != nil {
		return fmt.Errorf("open muxer %q: %w", outCfg.URL, err)
	}
	success := false
	defer func() {
		if !success {
			muxer.Abort()
		}
	}()

	if _, err := muxer.AddStream(enc); err != nil {
		return fmt.Errorf("add stream: %w", err)
	}
	if err := muxer.WriteHeader(); err != nil {
		return fmt.Errorf("write header: %w", err)
	}

	pktCh := make(chan *av.Packet, 8)
	decFrameCh := make(chan *av.Frame, 8)
	filtFrameCh := make(chan *av.Frame, 8)
	encPktCh := make(chan *av.Packet, 8)

	// Demux stage.
	g.Go(func() error {
		defer close(pktCh)
		for {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			pkt, err := av.AllocPacket()
			if err != nil {
				return err
			}
			if err := input.ReadPacket(pkt); err != nil {
				pkt.Close()
				if av.IsEOF(err) {
					return nil
				}
				return err
			}
			if pkt.StreamIndex() != vidIdx {
				pkt.Close()
				continue
			}
			select {
			case pktCh <- pkt:
			case <-ctx.Done():
				pkt.Close()
				return ctx.Err()
			}
		}
	})

	// Decode stage.
	g.Go(func() error {
		defer close(decFrameCh)
		drainDecoder := func() error {
			if err := dec.Flush(); err != nil {
				return err
			}
			for {
				f, _ := av.AllocFrame()
				err := dec.ReceiveFrame(f)
				if av.IsEOF(err) {
					f.Close()
					return nil
				}
				if err != nil {
					f.Close()
					return err
				}
				select {
				case decFrameCh <- f:
				case <-ctx.Done():
					f.Close()
					return ctx.Err()
				}
			}
		}
		for pkt := range pktCh {
			if err := dec.SendPacket(pkt); err != nil {
				pkt.Close()
				return err
			}
			pkt.Close()
			for {
				f, _ := av.AllocFrame()
				err := dec.ReceiveFrame(f)
				if av.IsEAgain(err) {
					f.Close()
					break
				}
				if err != nil {
					f.Close()
					return err
				}
				select {
				case decFrameCh <- f:
				case <-ctx.Done():
					f.Close()
					return ctx.Err()
				}
			}
		}
		return drainDecoder()
	})

	// Filter stage.
	g.Go(func() error {
		defer close(filtFrameCh)
		drainFilter := func() error {
			if err := fg.Flush(); err != nil && !av.IsEOF(err) {
				return err
			}
			for {
				f, _ := av.AllocFrame()
				err := fg.PullFrame(f)
				if av.IsEOF(err) || av.IsEAgain(err) {
					f.Close()
					return nil
				}
				if err != nil {
					f.Close()
					return err
				}
				select {
				case filtFrameCh <- f:
				case <-ctx.Done():
					f.Close()
					return ctx.Err()
				}
			}
		}
		for f := range decFrameCh {
			if err := fg.PushFrame(f); err != nil {
				f.Close()
				return err
			}
			f.Close()
			for {
				out, _ := av.AllocFrame()
				err := fg.PullFrame(out)
				if av.IsEAgain(err) {
					out.Close()
					break
				}
				if err != nil {
					out.Close()
					return err
				}
				select {
				case filtFrameCh <- out:
				case <-ctx.Done():
					out.Close()
					return ctx.Err()
				}
			}
		}
		return drainFilter()
	})

	// Encode stage.
	g.Go(func() error {
		defer close(encPktCh)
		drainEncoder := func() error {
			if err := enc.Flush(); err != nil {
				return err
			}
			for {
				p, _ := av.AllocPacket()
				err := enc.ReceivePacket(p)
				if av.IsEOF(err) {
					p.Close()
					return nil
				}
				if err != nil {
					p.Close()
					return err
				}
				select {
				case encPktCh <- p:
				case <-ctx.Done():
					p.Close()
					return ctx.Err()
				}
			}
		}
		for f := range filtFrameCh {
			if err := enc.SendFrame(f); err != nil {
				f.Close()
				return err
			}
			f.Close()
			for {
				p, _ := av.AllocPacket()
				err := enc.ReceivePacket(p)
				if av.IsEAgain(err) {
					p.Close()
					break
				}
				if err != nil {
					p.Close()
					return err
				}
				select {
				case encPktCh <- p:
				case <-ctx.Done():
					p.Close()
					return ctx.Err()
				}
			}
		}
		return drainEncoder()
	})

	// Mux stage.
	g.Go(func() error {
		for pkt := range encPktCh {
			pkt.SetStreamIndex(0)
			if err := muxer.WritePacket(pkt); err != nil {
				pkt.Close()
				return err
			}
			pkt.Close()
		}
		return muxer.WriteTrailer()
	})

	if err := g.Wait(); err != nil {
		return err
	}
	success = true
	return muxer.Close()
}

// configHasFilterSource reports whether cfg.Graph contains at least one
// node of type "filter_source" (Wave 7 #36c). Lets the engine accept a
// pure source-only pipeline (testsrc → encoder → file) without inputs.
func configHasFilterSource(cfg *Config) bool {
	for _, n := range cfg.Graph.Nodes {
		if n.Type == "filter_source" {
			return true
		}
	}
	return false
}

// configHasFilterSink reports whether cfg.Graph contains at least one
// node of type "filter_sink" (Wave 7 #36d). Lets the engine accept a
// pipeline whose terminal nodes are all libavfilter sinks (nullsink,
// anullsink, ebur128 → anullsink, …) with no top-level muxer outputs.
func configHasFilterSink(cfg *Config) bool {
	for _, n := range cfg.Graph.Nodes {
		if n.Type == "filter_sink" {
			return true
		}
	}
	return false
}

// configHasOnlyGoProcessors reports whether every node in cfg.Graph is a
// go_processor. When true the pipeline requires no AV outputs; all work
// is performed by the processors themselves via events edges.
func configHasOnlyGoProcessors(cfg *Config) bool {
	if len(cfg.Graph.Nodes) == 0 {
		return false
	}
	for _, n := range cfg.Graph.Nodes {
		if n.Type != "go_processor" {
			return false
		}
	}
	return true
}

func buildFilterSpec(node NodeDef) string {
	if node.Filter == "" {
		return "null"
	}
	if len(node.Params) == 0 {
		return node.Filter
	}
	// Partition keys into positional ("_posN") and named. Positional keys
	// come from compat/ffcli when an upstream FFmpeg-style filter expression
	// like `scale=320:240` is parsed: each `:`-separated token without an
	// embedded `=` is recorded as `_pos0`, `_pos1`, ... so the original
	// argument order can be recovered. They must reach libavfilter as bare,
	// in-order values (no `_posN=` prefix) and must precede any named args.
	type posArg struct {
		idx int
		val any
	}
	var positional []posArg
	named := make([]string, 0, len(node.Params))
	for k := range node.Params {
		if n, ok := strings.CutPrefix(k, "_pos"); ok {
			if i, err := strconv.Atoi(n); err == nil {
				positional = append(positional, posArg{idx: i, val: node.Params[k]})
				continue
			}
		}
		// Reserved internal markers used by the runtime to thread
		// pipeline-level state into a filter node (e.g. the loudnorm
		// shuttle's `__loudnorm_pass` / `__loudnorm_stats`). They
		// must not reach the libavfilter parser.
		if strings.HasPrefix(k, "__") {
			continue
		}
		named = append(named, k)
	}
	sort.Slice(positional, func(i, j int) bool { return positional[i].idx < positional[j].idx })
	sort.Strings(named)

	quote := func(v any) string {
		s := fmt.Sprintf("%v", v)
		// avfilter_graph_parse_ptr treats ',' and ';' as separators between
		// filter chains. Quote any value that contains those characters (or
		// a literal single-quote) so the expression reaches the filter intact.
		// NOTE: ':' (the option key=value separator) cannot be escaped via
		// avfilter_graph_parse_ptr — the parser splits on all ':' before
		// processing escape sequences. Callers must avoid ':' in option values
		// (e.g. use relative file paths instead of Windows absolute paths).
		if strings.ContainsAny(s, "',;") {
			s = "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
		}
		return s
	}

	spec := node.Filter
	first := true
	for _, p := range positional {
		if first {
			spec += "="
			first = false
		} else {
			spec += ":"
		}
		spec += quote(p.val)
	}
	for _, k := range named {
		if first {
			spec += "="
			first = false
		} else {
			spec += ":"
		}
		spec += k + "=" + quote(node.Params[k])
	}
	return spec
}

// runGraph executes the pipeline using the graph builder + scheduler for
// configs with explicit edges (multi-input / multi-output support).
func (p *Pipeline) runGraph(ctx context.Context) (runErr error) {
	cfg := p.cfg

	// Wrap the entire run in a pipeline-level OTel span if a provider is set.
	p.mu.Lock()
	obs := p.obsProvider
	p.mu.Unlock()
	if obs != nil {
		var pipelineSpan trace.Span
		ctx, pipelineSpan = obs.StartPipelineSpan(ctx, cfg.Description)
		defer func() {
			if runErr != nil {
				observability.EndSpanError(pipelineSpan, runErr)
			} else {
				observability.EndSpanOK(pipelineSpan)
			}
		}()
	}

	// 1a. Resolve "$asset:<name>" references in filter params.
	cfg, err := resolveConfigAssets(cfg)
	if err != nil {
		return fmt.Errorf("resolve assets: %w", err)
	}

	// 1. Normalize: lower the authoring config to an executable
	// graph.Def. NormalizeConfig is the single boundary between
	// FFmpeg-style shorthand on Config (codec_video, audio_sync,
	// pass, force_key_frames, ...) and the node-local executable
	// graph the runtime consumes. See docs/field-ownership.md.
	def, warnings, err := NormalizeConfig(cfg)
	if err != nil {
		return fmt.Errorf("normalize config: %w", err)
	}
	for _, w := range warnings {
		p.events.Post(ErrorEvent{
			Err:  fmt.Errorf("normalize warning [%s] %s: %s", w.Code, w.Path, w.Message),
			Time: time.Now(),
		})
	}
	dag, err := graph.Build(def)
	if err != nil {
		return fmt.Errorf("build graph: %w", err)
	}

	// 2. Compile: analyze the graph for stage grouping and warnings.
	plan, err := graph.Compile(dag)
	if err != nil {
		return fmt.Errorf("compile graph: %w", err)
	}
	for _, w := range plan.Warnings {
		// For go_processor-only pipelines, events edges are stripped from the
		// AV graph before compilation, so dead_node and disconnected_source
		// warnings are expected and should not be surfaced as errors.
		if configHasOnlyGoProcessors(cfg) &&
			(w.Code == graph.WarnDeadNode || w.Code == graph.WarnDisconnectedSource) {
			continue
		}
		p.events.Post(ErrorEvent{
			Err:  fmt.Errorf("graph compilation warning [%s]: %s", w.Code, w.Message),
			Time: time.Now(),
		})
	}

	// Apply EncoderInputBufferFrames override: the default from graph.Compile
	// is 16; override to the configured value for all encoder-input edges.
	if encBuf := cfg.GlobalOptions.EncoderInputBufferFrames; encBuf > 0 {
		for e := range plan.EdgeBufSizes {
			if e.To.Kind == graph.KindEncoder {
				plan.EdgeBufSizes[e] = encBuf
			}
		}
	}

	// 3. Pre-open all AV resources in topological order.
	runner := newGraphRunner(cfg, p)
	defer func() {
		runner.close()
		p.mu.Lock()
		p.graphRunner = nil
		p.reconf = nil
		p.edgeStats = nil
		p.ready = nil
		p.mu.Unlock()
	}()

	// Phase 7: when realtime is on, the per-output preroll aggregator
	// must exist before openSink() runs so each sink can register its
	// preroll into it. The Ready() channel is exposed via Pipeline.Ready
	// and the AND-aggregator goroutine starts once the scheduler is up.
	if cfg.GlobalOptions.Realtime {
		p.mu.Lock()
		p.ready = newGraphReady()
		p.mu.Unlock()
	}

	// 3a. Open named hardware-acceleration device contexts (Wave 10 #56).
	// These are opened before any source/encoder/filter nodes so that
	// they can be looked up by name during resource creation.
	for _, hd := range cfg.HardwareDevices {
		dt := av.ParseHWDeviceType(hd.Type)
		if dt == av.HWDeviceNone {
			return fmt.Errorf("hardware_devices[%q]: unknown device type %q", hd.Name, hd.Type)
		}
		ctx, err := av.OpenHWDevice(dt, hd.Device)
		if err != nil {
			return fmt.Errorf("hardware_devices[%q]: %w", hd.Name, err)
		}
		runner.hwDevices[hd.Name] = ctx
	}

	for _, inp := range cfg.Inputs {
		// Resolve decoder threading from the corresponding source node.
		decOpts := av.DecoderOptions{}
		srcNode := dag.NodeByID(inp.ID)
		if srcNode != nil {
			decOpts.ThreadCount = runner.resolveThreadCount(srcNode)
			decOpts.ThreadType = runner.resolveThreadType(srcNode)
		}
		src, err := runner.openSource(inp, srcNode, decOpts)
		if err != nil {
			return err
		}
		runner.sources[inp.ID] = src
	}

	for _, node := range dag.Order {
		switch node.Kind {
		case graph.KindSource:
			// Already opened above.
		case graph.KindFilter:
			fg, err := runner.createFilter(dag, node)
			if err != nil {
				return fmt.Errorf("create filter %q: %w", node.ID, err)
			}
			runner.filters[node.ID] = fg
		case graph.KindFilterSource:
			fg, err := runner.createFilterSource(node)
			if err != nil {
				return fmt.Errorf("create filter_source %q: %w", node.ID, err)
			}
			runner.filters[node.ID] = fg
		case graph.KindFilterSink:
			fg, err := runner.createFilterSink(dag, node)
			if err != nil {
				return fmt.Errorf("create filter_sink %q: %w", node.ID, err)
			}
			runner.filters[node.ID] = fg
		case graph.KindEncoder:
			enc, err := runner.createEncoder(dag, node)
			if err != nil {
				return fmt.Errorf("create encoder %q: %w", node.ID, err)
			}
			runner.encoders[node.ID] = enc
		case graph.KindSink:
			sink, err := runner.openSink(dag, node)
			if err != nil {
				return err
			}
			runner.sinks[node.ID] = sink
		case graph.KindGoProcessor:
			// Pure-sink metadata_file_writer nodes (no inner_processor param,
			// wired via "events" edges) are handled entirely by the engine's
			// eventsSinks table below. Skip go_processor initialisation for them.
			if node.Processor == "metadata_file_writer" {
				if _, hasInner := node.Params["inner_processor"]; !hasInner {
					runner.pureEventSinkNodes[node.ID] = struct{}{}
					continue
				}
			}
			proc, err := processors.Get(node.Processor)
			if err != nil {
				return fmt.Errorf("go_processor %q: %w", node.ID, err)
			}
			// Auto-inject "frame_rate" from the probed upstream video stream
			// so processors like the scene detectors don't require the user
			// to specify it manually. Only injected when the param is absent
			// and the upstream stream has a valid avg_frame_rate.
			initParams := node.Params
			if _, hasFrameRate := initParams["frame_rate"]; !hasFrameRate {
				if si, siErr := runner.resolveStreamInfo(dag, node); siErr == nil &&
					si.Type == av.MediaTypeVideo &&
					si.FrameRate[0] > 0 && si.FrameRate[1] > 0 {
					initParams = copyParams(initParams)
					initParams["frame_rate"] = float64(si.FrameRate[0]) / float64(si.FrameRate[1])
				}
			}
			if err := proc.Init(initParams); err != nil {
				return fmt.Errorf("go_processor %q init: %w", node.ID, err)
			}
			runner.goProcessors[node.ID] = proc
		case graph.KindCopy:
			// Stream-copy nodes hold no per-node AV resources; the
			// source already emits raw packets and the sink wires the
			// output stream from the input codecpar at openSink time.
		}
	}

	// Build the events routing table from "events" edges.
	// For each edge {from: srcID, to: tgtID, type: "events"} where tgtID
	// is a pure-sink metadata_file_writer (no inner_processor), open an
	// EventSink and register it under the source node ID.
	nodesByID := make(map[string]*NodeDef, len(cfg.Graph.Nodes))
	for i := range cfg.Graph.Nodes {
		nodesByID[cfg.Graph.Nodes[i].ID] = &cfg.Graph.Nodes[i]
	}
	for _, e := range cfg.Graph.Edges {
		if e.Type != "events" && e.Type != "file" {
			continue
		}
		srcID := edgeNodeID(e.From)
		tgtID := edgeNodeID(e.To)
		tgt := nodesByID[tgtID]
		if tgt == nil || tgt.Type != "go_processor" {
			continue
		}

		// metadata_file_writer in "events-wiring" mode: open a file sink
		// and register it under the source node ID.
		if tgt.Processor == "metadata_file_writer" {
			if _, hasInner := tgt.Params["inner_processor"]; hasInner {
				// Wrapper-mode node: events are written by the processor itself.
				continue
			}
			outputFile, _ := tgt.Params["output_file"].(string)
			if outputFile == "" {
				return fmt.Errorf("metadata_file_writer %q: events edge requires output_file param", tgtID)
			}
			outputFormat, _ := tgt.Params["output_format"].(string)
			sink, err := processors.NewEventSink(outputFile, outputFormat)
			if err != nil {
				return fmt.Errorf("metadata_file_writer %q: %w", tgtID, err)
			}
			runner.eventsSinks[srcID] = append(runner.eventsSinks[srcID], sink)
			continue
		}

		// Generic event-driven go_processor (e.g. twelvelabs_indexer).
		// If it implements SegmentEventConsumer, register it under the
		// source node ID so the sink dispatches SegmentCompleted events.
		// If it implements AsyncMetadataProcessor, install a MetadataEmitter
		// that forwards posts to the pipeline event bus and to any
		// downstream events sinks rooted at the processor's own node ID.
		proc := runner.goProcessors[tgtID]
		if proc == nil {
			continue
		}
		if consumer, ok := proc.(processors.SegmentEventConsumer); ok {
			runner.segmentConsumers[srcID] = append(runner.segmentConsumers[srcID], consumer)
			runner.eventDrivenGoProcessors[tgtID] = struct{}{}
		}
		if async, ok := proc.(processors.AsyncMetadataProcessor); ok {
			runner.eventDrivenGoProcessors[tgtID] = struct{}{}
			nodeID := tgtID
			pipeCtx := ctx // capture before inner variable shadowing
			async.SetMetadataEmitter(func(md *processors.Metadata) {
				if md == nil {
					return
				}
				runner.pipe.events.Post(ProcessorMetadata{
					NodeID:   nodeID,
					Metadata: md,
				})
				// Progress events are SSE-only: skip file sinks and downstream
				// consumer chaining so intermediate status updates (uploading,
				// task_created, waiting) do not trigger downstream processors.
				// Failed events are similarly SSE-only: downstream processors
				// must not be triggered when an upstream step has errored.
				if md.Progress || md.Failed {
					return
				}
				pCtx := processors.ProcessorContext{StreamID: nodeID}
				for _, s := range runner.eventsSinks[nodeID] {
					s.Write(pCtx, md)
				}
				// Chain downstream SegmentEventConsumers (processor→processor
				// "events" edges). FilePath is propagated from the metadata so
				// the downstream consumer receives the original source path.
				// Custom is forwarded so downstream processors can use results
				// from upstream (e.g. video_id from TwelveLabsIndexer).
				if downstream := runner.segmentConsumers[nodeID]; len(downstream) > 0 {
					ev := processors.SegmentEvent{OutputID: nodeID, FilePath: md.FilePath, Custom: md.Custom}
					for _, c := range downstream {
						c.OnSegmentCompleted(pipeCtx, ev)
					}
				}
			})
		}
	}

	// Compute go_processor close order: event producers (nodes that are
	// sources of processor→processor "events" edges) before consumers, so
	// producer.Close() blocks until it fires OnSegmentCompleted on consumers
	// before consumer.Close() waits on the consumer's WaitGroup.
	{
		seen := make(map[string]bool)
		for _, e := range cfg.Graph.Edges {
			if e.Type != "events" {
				continue
			}
			srcID := edgeNodeID(e.From)
			if _, isProc := runner.goProcessors[srcID]; isProc && !seen[srcID] {
				seen[srcID] = true
				runner.goProcessorCloseOrder = append(runner.goProcessorCloseOrder, srcID)
			}
		}
		// Append any remaining go_processors (pure consumers) in sorted order.
		var remaining []string
		for id := range runner.goProcessors {
			if !seen[id] {
				remaining = append(remaining, id)
			}
		}
		sort.Strings(remaining)
		runner.goProcessorCloseOrder = append(runner.goProcessorCloseOrder, remaining...)
	}

	// Dispatch "from-input" events synchronously: for each input node that
	// is the source of an "events" edge, fire OnSegmentCompleted immediately
	// so the processor's wg.Add(1) is called before sched.Run starts and
	// runner.close() can correctly wait via wg.Wait().
	{
		inputURLByID := make(map[string]string, len(cfg.Inputs))
		for _, inp := range cfg.Inputs {
			inputURLByID[inp.ID] = inp.URL
		}
		for srcID, consumers := range runner.segmentConsumers {
			url, isInput := inputURLByID[srcID]
			if !isInput {
				continue
			}
			ev := processors.SegmentEvent{OutputID: srcID, FilePath: url, SegmentIndex: 0}
			for _, c := range consumers {
				c.OnSegmentCompleted(ctx, ev)
			}
		}
	}

	// Register reconfigurable filters for live parameter changes.
	reconf := &reconfigurable{filters: make(map[string]*reconfigEntry)}
	for _, node := range cfg.Graph.Nodes {
		if node.Type == "filter" && node.Filter != "" {
			reconf.filters[node.ID] = &reconfigEntry{
				filterName: node.Filter,
				params:     copyParams(node.Params),
			}
		}
	}
	p.mu.Lock()
	p.graphRunner = runner
	p.reconf = reconf
	p.mu.Unlock()

	// 2b. Create per-node performance trackers and register with metrics.
	for _, node := range dag.Order {
		tr := NewNodePerfTracker(node.ID, 0)
		switch node.Kind {
		case graph.KindSource:
			if src := runner.sources[node.ID]; src != nil {
				for _, dec := range src.decoders {
					tr.SetThreadInfo(dec.ThreadCount(), threadModeString(dec.ActiveThreadType()))
					tr.SetThreadBusyFn(dec.ThreadsBusy)
					break
				}
			}
		case graph.KindFilter:
			if fg := runner.filters[node.ID]; fg != nil {
				tr.SetThreadInfo(fg.ThreadCount(), "auto")
				tr.SetThreadBusyFn(fg.ThreadsBusy)
			}
		case graph.KindEncoder:
			if enc := runner.encoders[node.ID]; enc != nil {
				tr.SetThreadInfo(enc.ThreadCount(), threadModeString(enc.ActiveThreadType()))
				tr.SetThreadBusyFn(enc.ThreadsBusy)
				// Phase 6: seed preset metadata for adaptive stepping.
				codecName := enc.CodecName()
				initial := ""
				if opts, ok := runner.encoderOpts[node.ID]; ok && opts.ExtraOpts != nil {
					initial = opts.ExtraOpts["preset"]
				}
				if ladder, ok := LadderFor(codecName); ok {
					if initial == "" {
						initial = ladder.Default()
					}
					tr.SetPresetInfo(codecName, initial, ladder)
				}
			}
		}
		runner.trackers[node.ID] = tr
		p.metrics.RegisterPerfTracker(node.ID, tr)
	}

	// 3. Run the scheduler — one goroutine per node, channels per edge.
	edgeStats := runtime.NewEdgeStatsRegistry()
	p.mu.Lock()
	p.edgeStats = edgeStats
	p.mu.Unlock()

	// 3a. Start the real-time adaptive control loop when enabled.
	// It must start after perf trackers are registered (above) and the
	// scheduler is about to run, so snapshots contain live data.
	if cfg.GlobalOptions.Realtime {
		budget := newThreadBudget(p.maxThreads)
		for _, node := range dag.Order {
			switch node.Kind {
			case graph.KindEncoder:
				if enc := runner.encoders[node.ID]; enc != nil {
					// Nodes with a named hardware device (NVENC, VideoToolbox,
					// VAAPI, QSV, …) consume GPU resources, not CPU threads —
					// exempt them from the CPU ThreadBudget.
					if node.Device != "" {
						budget.SetHWNode(node.ID)
					} else {
						budget.Seed(node.ID, enc.ThreadCount())
					}
				}
			case graph.KindFilter:
				if fg := runner.filters[node.ID]; fg != nil {
					budget.Seed(node.ID, fg.ThreadCount())
				}
			}
		}
		p.mu.Lock()
		prom := p.prom
		p.mu.Unlock()
		ctrl := newRealtimeController(budget, p.metrics, p.events, runner, dag, prom)
		// Phase 6: pass adaptive-preset configuration through.
		ctrl.highestQualityPreset = cfg.GlobalOptions.HighestQualityPreset
		ctrl.targetFPS = cfg.GlobalOptions.TargetFPS
		// Propagate the graph-level FPS target to per-encoder perf trackers.
		// observe() skips nodes with FPSTarget <= 0, so without this the RT
		// controller never evaluates any node and all preset decisions stall.
		if ctrl.targetFPS > 0 {
			for _, node := range dag.Order {
				if node.Kind == graph.KindEncoder {
					if tr := runner.trackers[node.ID]; tr != nil {
						tr.SetFPSTarget(ctrl.targetFPS)
					}
				}
			}
		}
		ctrl.groupStep = true
		if cfg.GlobalOptions.PresetGroupStep != nil {
			ctrl.groupStep = *cfg.GlobalOptions.PresetGroupStep
		}
		ctrl.logPath = cfg.GlobalOptions.RealtimeLogPath
		p.mu.Lock()
		p.realtimeCtrl = ctrl
		p.mu.Unlock()
		go ctrl.run(ctx)
	}

	// Phase 7: start the per-output preroll aggregator. It closes
	// Pipeline.Ready() once every registered preroll signals readiness
	// and posts a single RealTimeReady event on the bus.
	if p.ready != nil {
		go p.ready.run(ctx, p.events)
	}

	// Wrap handler with per-node OTel spans.
	handler := runtime.NodeHandler(runner.handle)
	if obs != nil {
		inner := handler
		handler = func(ctx context.Context, node *graph.Node, ins []<-chan any, outs []chan<- any) error {
			ctx, span := observability.StartNodeSpan(ctx, node.ID, node.Kind.String(), "", "")
			err := inner(ctx, node, ins, outs)
			if err != nil {
				observability.EndSpanError(span, err)
			} else {
				observability.EndSpanOK(span)
			}
			return err
		}
	}

	sched := &runtime.Scheduler{BufSize: 8, EdgeBufSizes: plan.EdgeBufSizes, EdgeStats: edgeStats}
	if err := sched.Run(ctx, dag, handler); err != nil {
		// Abort all outputs on error.
		for _, s := range runner.sinks {
			s.muxer.Abort()
		}
		return err
	}

	// 5. Finalize outputs (atomic rename from .tmp).
	for _, s := range runner.sinks {
		if err := s.muxer.Close(); err != nil {
			return err
		}
	}
	return nil
}

// threadModeString converts an AVCodecContext.active_thread_type bitmask to a
// human-readable label.  Values map to FFmpeg's FF_THREAD_* constants:
//
//	0 = FF_THREAD_NONE  → "none"
//	1 = FF_THREAD_FRAME → "frame"
//	2 = FF_THREAD_SLICE → "slice"
func threadModeString(active int) string {
	switch active {
	case 1:
		return "frame"
	case 2:
		return "slice"
	case 0:
		return "none"
	default:
		return "unknown"
	}
}

// ReadyState is a point-in-time snapshot of the Phase 7 per-output
// preroll aggregator, returned by Pipeline.ReadyState() and emitted in
// the RealTimeReady event.
type ReadyState struct {
	Ready   bool              `json:"ready"`
	Since   time.Time         `json:"since,omitempty"`
	Outputs []OutputReadyView `json:"outputs,omitempty"`
}

// Ready returns a channel that closes once every output's preroll
// buffer has reached its fill target (READY/READY_PARTIAL/STREAMING).
// When real-time mode is off it returns a closed channel so callers
// gating on Ready() do not block.
func (p *Pipeline) Ready() <-chan struct{} {
	p.mu.Lock()
	r := p.ready
	p.mu.Unlock()
	if r == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return r.Ready()
}

// ReadyState returns a snapshot of per-output preroll readiness.
func (p *Pipeline) ReadyState() ReadyState {
	p.mu.Lock()
	r := p.ready
	p.mu.Unlock()
	if r == nil {
		return ReadyState{Ready: false}
	}
	ready, since, outs := r.State()
	return ReadyState{Ready: ready, Since: since, Outputs: outs}
}
