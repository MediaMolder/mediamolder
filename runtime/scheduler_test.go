// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package runtime

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"

	"github.com/MediaMolder/MediaMolder/graph"
)

func TestSchedulerLinear(t *testing.T) {
	// src → filter → sink
	def := &graph.Def{
		Inputs:  []graph.InputDef{{ID: "src"}},
		Outputs: []graph.OutputDef{{ID: "out"}},
		Nodes:   []graph.NodeDef{{ID: "f", Type: "filter", Filter: "null"}},
		Edges: []graph.EdgeDef{
			{From: "src:v:0", To: "f:default", Type: "video"},
			{From: "f:default", To: "out:v", Type: "video"},
		},
	}
	g, err := graph.Build(def)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	var mu sync.Mutex
	ran := map[string]bool{}

	handler := func(ctx context.Context, node *graph.Node, ins []<-chan any, outs []chan<- any) error {
		mu.Lock()
		ran[node.ID] = true
		mu.Unlock()

		switch node.Kind {
		case graph.KindSource:
			for i := 0; i < 5; i++ {
				select {
				case outs[0] <- i:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		case graph.KindFilter:
			for v := range ins[0] {
				select {
				case outs[0] <- v:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		case graph.KindSink:
			count := 0
			for range ins[0] {
				count++
			}
			if count != 5 {
				return fmt.Errorf("sink got %d items, want 5", count)
			}
		}
		return nil
	}

	s := &Scheduler{BufSize: 4}
	if err := s.Run(context.Background(), g, handler); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, id := range []string{"src", "f", "out"} {
		if !ran[id] {
			t.Errorf("node %q did not run", id)
		}
	}
}

func TestSchedulerMultiOutput(t *testing.T) {
	// src → split → hls, dash
	def := &graph.Def{
		Inputs:  []graph.InputDef{{ID: "src"}},
		Outputs: []graph.OutputDef{{ID: "hls"}, {ID: "dash"}},
		Nodes:   []graph.NodeDef{{ID: "split", Type: "filter", Filter: "split"}},
		Edges: []graph.EdgeDef{
			{From: "src:v:0", To: "split:default", Type: "video"},
			{From: "split:0", To: "hls:v", Type: "video"},
			{From: "split:1", To: "dash:v", Type: "video"},
		},
	}
	g, err := graph.Build(def)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	var mu sync.Mutex
	sinkCounts := map[string]int{}

	handler := func(ctx context.Context, node *graph.Node, ins []<-chan any, outs []chan<- any) error {
		switch node.Kind {
		case graph.KindSource:
			for i := 0; i < 10; i++ {
				outs[0] <- i
			}
		case graph.KindFilter:
			// Split: send each value to all outputs.
			for v := range ins[0] {
				for _, out := range outs {
					select {
					case out <- v:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
			}
		case graph.KindSink:
			count := 0
			for range ins[0] {
				count++
			}
			mu.Lock()
			sinkCounts[node.ID] = count
			mu.Unlock()
		}
		return nil
	}

	s := &Scheduler{BufSize: 4}
	if err := s.Run(context.Background(), g, handler); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, id := range []string{"hls", "dash"} {
		if sinkCounts[id] != 10 {
			t.Errorf("sink %q got %d items, want 10", id, sinkCounts[id])
		}
	}
}

func TestSchedulerMultiInput(t *testing.T) {
	// bg, fg → overlay → out
	def := &graph.Def{
		Inputs:  []graph.InputDef{{ID: "bg"}, {ID: "fg"}},
		Outputs: []graph.OutputDef{{ID: "out"}},
		Nodes:   []graph.NodeDef{{ID: "overlay", Type: "filter", Filter: "overlay"}},
		Edges: []graph.EdgeDef{
			{From: "bg:v:0", To: "overlay:0", Type: "video"},
			{From: "fg:v:0", To: "overlay:1", Type: "video"},
			{From: "overlay:default", To: "out:v", Type: "video"},
		},
	}
	g, err := graph.Build(def)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	result := make(chan []int, 1)

	handler := func(ctx context.Context, node *graph.Node, ins []<-chan any, outs []chan<- any) error {
		switch node.Kind {
		case graph.KindSource:
			for i := 0; i < 3; i++ {
				outs[0] <- i
			}
		case graph.KindFilter:
			// Overlay: read from both inputs alternately.
			for v0 := range ins[0] {
				v1, ok := <-ins[1]
				if !ok {
					break
				}
				combined := v0.(int)*10 + v1.(int)
				outs[0] <- combined
			}
		case graph.KindSink:
			var vals []int
			for v := range ins[0] {
				vals = append(vals, v.(int))
			}
			result <- vals
		}
		return nil
	}

	s := &Scheduler{BufSize: 4}
	if err := s.Run(context.Background(), g, handler); err != nil {
		t.Fatalf("Run: %v", err)
	}

	vals := <-result
	if len(vals) != 3 {
		t.Fatalf("got %d values, want 3", len(vals))
	}
}

func TestSchedulerContextCancel(t *testing.T) {
	def := &graph.Def{
		Inputs:  []graph.InputDef{{ID: "src"}},
		Outputs: []graph.OutputDef{{ID: "out"}},
		Edges: []graph.EdgeDef{
			{From: "src:v:0", To: "out:v", Type: "video"},
		},
	}
	g, err := graph.Build(def)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	handler := func(ctx context.Context, node *graph.Node, ins []<-chan any, outs []chan<- any) error {
		if node.Kind == graph.KindSource {
			// Send items in a loop until cancelled.
			for i := 0; ; i++ {
				if i == 2 {
					cancel()
				}
				select {
				case outs[0] <- i:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
		// Sink: drain input.
		for range ins[0] {
		}
		return nil
	}

	s := &Scheduler{BufSize: 1}
	err = s.Run(ctx, g, handler)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestFanOut(t *testing.T) {
	src := make(chan any, 10)
	dst1 := make(chan any, 10)
	dst2 := make(chan any, 10)

	for i := 0; i < 5; i++ {
		src <- i
	}
	close(src)

	err := FanOut(context.Background(), src, []chan<- any{dst1, dst2})
	if err != nil {
		t.Fatalf("FanOut: %v", err)
	}

	var got1, got2 []int
	for v := range dst1 {
		got1 = append(got1, v.(int))
	}
	for v := range dst2 {
		got2 = append(got2, v.(int))
	}

	if len(got1) != 5 || len(got2) != 5 {
		t.Fatalf("got1=%d got2=%d, want 5 each", len(got1), len(got2))
	}
}

func TestFanOutSingle(t *testing.T) {
	src := make(chan any, 5)
	dst := make(chan any, 5)
	for i := 0; i < 3; i++ {
		src <- i
	}
	close(src)

	err := FanOut(context.Background(), src, []chan<- any{dst})
	if err != nil {
		t.Fatalf("FanOut: %v", err)
	}

	count := 0
	for range dst {
		count++
	}
	if count != 3 {
		t.Errorf("got %d, want 3", count)
	}
}

func TestMerge(t *testing.T) {
	src1 := make(chan any, 5)
	src2 := make(chan any, 5)
	dst := make(chan any, 10)

	for i := 0; i < 3; i++ {
		src1 <- i
	}
	close(src1)
	for i := 10; i < 15; i++ {
		src2 <- i
	}
	close(src2)

	err := Merge(context.Background(), []<-chan any{src1, src2}, dst)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	var got []int
	for v := range dst {
		got = append(got, v.(int))
	}
	sort.Ints(got)
	if len(got) != 8 {
		t.Fatalf("got %d values, want 8", len(got))
	}
}
