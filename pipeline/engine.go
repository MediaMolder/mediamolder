package pipeline

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/graph"
	"github.com/MediaMolder/MediaMolder/runtime"
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

	metrics     *MetricsRegistry
	reconf      *reconfigurable // live filter parameter changes (graph mode only)
	graphRunner *graphRunner    // running graph resources (graph mode only)
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
	snap := p.metrics.Snapshot()
	snap.State = p.State().String()
	return snap
}

// Metrics returns the underlying MetricsRegistry for direct node updates.
func (p *Pipeline) Metrics() *MetricsRegistry {
	return p.metrics
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
		if len(p.cfg.Inputs) == 0 || len(p.cfg.Outputs) == 0 {
			return fmt.Errorf("config has no inputs or outputs")
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
		p.eg.Wait() // error already captured in runDone
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

// waitIfPaused blocks until the pipeline is unpaused or ctx is cancelled.
func (p *Pipeline) waitIfPaused(ctx context.Context) error {
	p.mu.Lock()
	ch := p.pauseCh
	p.mu.Unlock()
	if ch == nil {
		return nil
	}
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

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

	dec, err := av.OpenDecoder(input, vidIdx)
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

func buildFilterSpec(node NodeDef) string {
	if node.Filter == "" {
		return "null"
	}
	if len(node.Params) == 0 {
		return node.Filter
	}
	// Sort keys for deterministic output.
	keys := make([]string, 0, len(node.Params))
	for k := range node.Params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	spec := node.Filter
	first := true
	for _, k := range keys {
		if first {
			spec += "="
			first = false
		} else {
			spec += ":"
		}
		spec += fmt.Sprintf("%s=%v", k, node.Params[k])
	}
	return spec
}

// runGraph executes the pipeline using the graph builder + scheduler for
// configs with explicit edges (multi-input / multi-output support).
func (p *Pipeline) runGraph(ctx context.Context) error {
	cfg := p.cfg

	// 1. Convert pipeline config → graph definition → validated DAG.
	def := configToGraphDef(cfg)
	dag, err := graph.Build(def)
	if err != nil {
		return fmt.Errorf("build graph: %w", err)
	}

	// 2. Pre-open all AV resources in topological order.
	runner := newGraphRunner(cfg, p)
	defer func() {
		runner.close()
		p.mu.Lock()
		p.graphRunner = nil
		p.reconf = nil
		p.mu.Unlock()
	}()

	for _, inp := range cfg.Inputs {
		src, err := runner.openSource(inp)
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

	// 3. Run the scheduler — one goroutine per node, channels per edge.
	sched := &runtime.Scheduler{BufSize: 8}
	if err := sched.Run(ctx, dag, runner.handle); err != nil {
		// Abort all outputs on error.
		for _, s := range runner.sinks {
			s.muxer.Abort()
		}
		return err
	}

	// 4. Finalize outputs (atomic rename from .tmp).
	for _, s := range runner.sinks {
		if err := s.muxer.Close(); err != nil {
			return err
		}
	}
	return nil
}
