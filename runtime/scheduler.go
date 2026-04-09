package runtime

import (
	"context"
	"sync"

	"github.com/MediaMolder/MediaMolder/graph"
	"golang.org/x/sync/errgroup"
)

// NodeHandler runs a single graph node.
// ins: input channels (one per inbound edge, in inbound-edge order).
// outs: output channels (one per outbound edge, in outbound-edge order).
// The handler must NOT close output channels; the scheduler does that.
type NodeHandler func(ctx context.Context, node *graph.Node, ins []<-chan any, outs []chan<- any) error

// Scheduler runs a graph.Graph as concurrent goroutines linked by channels.
type Scheduler struct {
	BufSize int // channel buffer size; 0 uses default of 8
}

// Run launches one goroutine per node, wires them with channels, and blocks
// until all finish or ctx is cancelled. Any node error cancels all nodes.
func (s *Scheduler) Run(ctx context.Context, g *graph.Graph, handler NodeHandler) error {
	bufSize := s.BufSize
	if bufSize <= 0 {
		bufSize = 8
	}

	// Create a channel per edge.
	edgeCh := make(map[*graph.Edge]chan any, len(g.Edges))
	for _, e := range g.Edges {
		edgeCh[e] = make(chan any, bufSize)
	}

	eg, ctx := errgroup.WithContext(ctx)

	for _, node := range g.Order {
		node := node

		ins := make([]<-chan any, len(node.Inbound))
		for i, e := range node.Inbound {
			ins[i] = edgeCh[e]
		}

		// Collect raw output channels for closing after handler returns.
		outsRaw := make([]chan any, len(node.Outbound))
		outs := make([]chan<- any, len(node.Outbound))
		for i, e := range node.Outbound {
			outsRaw[i] = edgeCh[e]
			outs[i] = edgeCh[e]
		}

		eg.Go(func() error {
			err := handler(ctx, node, ins, outs)
			for _, ch := range outsRaw {
				close(ch)
			}
			return err
		})
	}

	return eg.Wait()
}

// FanOut reads values from src and broadcasts each value to all dsts.
// Each destination gets its own copier goroutine backed by an independent
// buffer, so a slow destination does not stall faster ones (up to buffer
// capacity). FanOut blocks until src is drained or ctx is cancelled.
func FanOut(ctx context.Context, src <-chan any, dsts []chan<- any) error {
	switch len(dsts) {
	case 0:
		for range src {
		}
		return nil
	case 1:
		for v := range src {
			select {
			case dsts[0] <- v:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		close(dsts[0])
		return nil
	}

	// Multiple destinations: per-destination buffered intermediate channels.
	intermediates := make([]chan any, len(dsts))
	for i := range dsts {
		bufCap := cap(dsts[i])
		if bufCap <= 0 {
			bufCap = 8
		}
		intermediates[i] = make(chan any, bufCap)
	}

	var eg errgroup.Group

	// Copier goroutines: intermediate → destination. Closes dst when done.
	for i, dst := range dsts {
		i, dst := i, dst
		eg.Go(func() error {
			defer close(dst)
			for v := range intermediates[i] {
				select {
				case dst <- v:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		})
	}

	// Broadcast: src → all intermediates.
	var broadcastErr error
	for v := range src {
		for _, ch := range intermediates {
			select {
			case ch <- v:
			case <-ctx.Done():
				broadcastErr = ctx.Err()
				goto done
			}
		}
	}
done:
	for _, ch := range intermediates {
		close(ch)
	}

	waitErr := eg.Wait()
	if broadcastErr != nil {
		return broadcastErr
	}
	return waitErr
}

// Merge reads from multiple source channels and sends all values to dst.
// dst is closed when all sources are drained or ctx is cancelled.
// Useful for fan-in (multiple producers → one consumer).
func Merge(ctx context.Context, srcs []<-chan any, dst chan<- any) error {
	var wg sync.WaitGroup
	wg.Add(len(srcs))

	var mu sync.Mutex
	var firstErr error
	setErr := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
	}

	for _, src := range srcs {
		src := src
		go func() {
			defer wg.Done()
			for v := range src {
				select {
				case dst <- v:
				case <-ctx.Done():
					setErr(ctx.Err())
					return
				}
			}
		}()
	}

	wg.Wait()
	close(dst)
	return firstErr
}
