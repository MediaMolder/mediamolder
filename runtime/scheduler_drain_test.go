// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package runtime

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/MediaMolder/MediaMolder/graph"
)

// A value that must be released by whoever ends up holding it — the shape of
// an av.Frame or av.Packet on an edge.
type releasable struct{ closed *atomic.Int32 }

func (r releasable) Close() { r.closed.Add(1) }

// When a node fails, the values its neighbours never consumed are still on
// the edges. The scheduler must release them: a run that aborts mid-stream
// (a decoder giving up, a sink that cannot write) would otherwise strand
// every buffered frame.
func TestRunReleasesValuesStrandedOnEdgesAfterAnError(t *testing.T) {
	src := &graph.Node{ID: "src"}
	sink := &graph.Node{ID: "sink"}
	edge := &graph.Edge{From: src, To: sink, Type: graph.PortAudio}
	src.Outbound = []*graph.Edge{edge}
	sink.Inbound = []*graph.Edge{edge}
	g := &graph.Graph{
		Nodes: map[string]*graph.Node{src.ID: src, sink.ID: sink},
		Edges: []*graph.Edge{edge},
		Order: []*graph.Node{src, sink},
	}

	const produced = 5
	var closed atomic.Int32
	sinkErr := errors.New("sink cannot write")
	handler := func(ctx context.Context, node *graph.Node, ins []<-chan any, outs []chan<- any) error {
		switch node.ID {
		case "src":
			for i := 0; i < produced; i++ {
				select {
				case outs[0] <- releasable{&closed}:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		default:
			// Takes one value, releases it, then fails — the rest stay buffered.
			v := <-ins[0]
			v.(releasable).Close()
			return sinkErr
		}
	}
	s := &Scheduler{BufSize: produced}
	err := s.Run(context.Background(), g, handler)
	if !errors.Is(err, sinkErr) {
		t.Fatalf("Run = %v, want the sink's error", err)
	}
	if got := closed.Load(); got != produced {
		t.Fatalf("released %d of %d values (the stranded ones must be released by the scheduler)", got, produced)
	}
}
