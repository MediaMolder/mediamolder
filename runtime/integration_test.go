// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package runtime

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/MediaMolder/MediaMolder/graph"
)

func TestSchedulerDiamond(t *testing.T) {
	// src1, src2 → merge → sink
	def := &graph.Def{
		Inputs:  []graph.InputDef{{ID: "src1"}, {ID: "src2"}},
		Nodes:   []graph.NodeDef{{ID: "merge", Type: "filter", Filter: "amerge"}},
		Outputs: []graph.OutputDef{{ID: "sink"}},
		Edges: []graph.EdgeDef{
			{From: "src1:v:0", To: "merge:default", Type: "audio"},
			{From: "src2:v:0", To: "merge:default", Type: "audio"},
			{From: "merge:out", To: "sink:a", Type: "audio"},
		},
	}
	g, err := graph.Build(def)
	if err != nil {
		t.Fatal(err)
	}

	var sinkCount int64
	handler := func(ctx context.Context, n *graph.Node, ins []<-chan any, outs []chan<- any) error {
		switch n.Kind {
		case graph.KindSource:
			for i := 0; i < 5; i++ {
				for _, o := range outs {
					o <- i
				}
			}
		case graph.KindFilter:
			// merge: read from all inputs until done
			for _, in := range ins {
				for range in {
					for _, o := range outs {
						o <- 1
					}
				}
			}
		case graph.KindSink:
			for _, in := range ins {
				for range in {
					atomic.AddInt64(&sinkCount, 1)
				}
			}
		}
		return nil
	}

	s := &Scheduler{BufSize: 8}
	if err := s.Run(context.Background(), g, handler); err != nil {
		t.Fatal(err)
	}
	if c := atomic.LoadInt64(&sinkCount); c != 10 {
		t.Errorf("sink received %d items, want 10", c)
	}
}

func TestSchedulerChain5(t *testing.T) {
	// in → f1 → f2 → f3 → f4 → f5 → out
	def := &graph.Def{
		Inputs: []graph.InputDef{{ID: "in"}},
		Nodes: []graph.NodeDef{
			{ID: "f1", Type: "filter"}, {ID: "f2", Type: "filter"},
			{ID: "f3", Type: "filter"}, {ID: "f4", Type: "filter"},
			{ID: "f5", Type: "filter"},
		},
		Outputs: []graph.OutputDef{{ID: "out"}},
		Edges: []graph.EdgeDef{
			{From: "in:v:0", To: "f1:default", Type: "video"},
			{From: "f1:default", To: "f2:default", Type: "video"},
			{From: "f2:default", To: "f3:default", Type: "video"},
			{From: "f3:default", To: "f4:default", Type: "video"},
			{From: "f4:default", To: "f5:default", Type: "video"},
			{From: "f5:default", To: "out:v", Type: "video"},
		},
	}
	g, err := graph.Build(def)
	if err != nil {
		t.Fatal(err)
	}

	var sinkCount int64
	handler := func(ctx context.Context, n *graph.Node, ins []<-chan any, outs []chan<- any) error {
		switch n.Kind {
		case graph.KindSource:
			for i := 0; i < 3; i++ {
				for _, o := range outs {
					o <- i
				}
			}
		case graph.KindFilter:
			for _, in := range ins {
				for v := range in {
					for _, o := range outs {
						o <- v
					}
				}
			}
		case graph.KindSink:
			for _, in := range ins {
				for range in {
					atomic.AddInt64(&sinkCount, 1)
				}
			}
		}
		return nil
	}

	s := &Scheduler{BufSize: 4}
	if err := s.Run(context.Background(), g, handler); err != nil {
		t.Fatal(err)
	}
	if c := atomic.LoadInt64(&sinkCount); c != 3 {
		t.Errorf("sink received %d items, want 3", c)
	}
}

func TestFanOutMultiple(t *testing.T) {
	src := make(chan any, 10)
	dst1 := make(chan any, 10)
	dst2 := make(chan any, 10)
	dst3 := make(chan any, 10)

	for i := 0; i < 5; i++ {
		src <- i
	}
	close(src)

	ctx := context.Background()
	if err := FanOut(ctx, src, []chan<- any{dst1, dst2, dst3}); err != nil {
		t.Fatal(err)
	}

	counts := [3]int{}
	for range dst1 {
		counts[0]++
	}
	for range dst2 {
		counts[1]++
	}
	for range dst3 {
		counts[2]++
	}
	for i, c := range counts {
		if c != 5 {
			t.Errorf("dst%d: got %d, want 5", i, c)
		}
	}
}

func TestMergeMultiple(t *testing.T) {
	src1 := make(chan any, 5)
	src2 := make(chan any, 5)
	src3 := make(chan any, 5)
	dst := make(chan any, 15)

	for i := 0; i < 3; i++ {
		src1 <- i
		src2 <- i + 10
		src3 <- i + 20
	}
	close(src1)
	close(src2)
	close(src3)

	ctx := context.Background()
	if err := Merge(ctx, []<-chan any{src1, src2, src3}, dst); err != nil {
		t.Fatal(err)
	}

	count := 0
	for range dst {
		count++
	}
	if count != 9 {
		t.Errorf("merged %d items, want 9", count)
	}
}
