package pipeline

import (
	"context"
	"fmt"
	"sync"

	"github.com/MediaMolder/MediaMolder/av"
	"golang.org/x/sync/errgroup"
)

// Engine executes a linear single-input -> filter -> single-output pipeline.
type Engine struct {
	cfg *Config

	mu     sync.Mutex
	state  State
	cancel context.CancelFunc
	eg     *errgroup.Group
}

// NewEngine creates an Engine from a validated Config.
func NewEngine(cfg *Config) (*Engine, error) {
	if err := av.CheckVersion(); err != nil {
		return nil, err
	}
	return &Engine{cfg: cfg, state: StateNull}, nil
}

// State returns the current pipeline state.
func (e *Engine) State() State {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state
}

// Run executes the pipeline to completion and blocks until done.
func (e *Engine) Run(ctx context.Context) error {
	e.mu.Lock()
	if e.state != StateNull {
		e.mu.Unlock()
		return fmt.Errorf("Run called on non-NULL pipeline (state=%s)", e.state)
	}
	ctx, cancel := context.WithCancel(ctx)
	e.cancel = cancel
	g, ctx := errgroup.WithContext(ctx)
	e.eg = g
	e.state = StatePlaying
	e.mu.Unlock()

	defer func() {
		e.mu.Lock()
		e.state = StateNull
		e.mu.Unlock()
		cancel()
	}()
	return e.runLinear(ctx, g)
}

// Close cancels a running pipeline and waits for goroutines to exit.
func (e *Engine) Close() error {
	e.mu.Lock()
	cancel := e.cancel
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if e.eg != nil {
		return e.eg.Wait()
	}
	return nil
}

func (e *Engine) runLinear(ctx context.Context, g *errgroup.Group) error {
	cfg := e.cfg
	inCfg := cfg.Inputs[0]
	outCfg := cfg.Outputs[0]

	input, err := av.OpenInput(inCfg.URL, nil)
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
		PixFmt:     0,
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

	enc, err := av.OpenEncoder(av.EncoderOptions{
		CodecName:    outCfg.CodecVideo,
		Width:        si.Width,
		Height:       si.Height,
		FrameRate:    [2]int{25, 1},
		GlobalHeader: true,
		ExtraOpts:    map[string]string{"preset": "medium"},
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
	spec := node.Filter
	first := true
	for k, v := range node.Params {
		if first {
			spec += "="
			first = false
		} else {
			spec += ":"
		}
		spec += fmt.Sprintf("%s=%v", k, v)
	}
	return spec
}
