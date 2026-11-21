// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"context"
	"errors"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/graph"
	"github.com/MediaMolder/MediaMolder/processors"
)

func TestGoProcessor_FanoutFrameReferences(t *testing.T) {
	r := newGraphRunner(&Config{}, minimalPipeline())
	r.goProcessors["p"] = &processors.Null{}
	node := &graph.Node{ID: "p", Kind: graph.KindGoProcessor}
	node.Inbound = []*graph.Edge{{To: node, Type: graph.PortVideo}}

	f := av.NewTestFrame(t, 64, 48, 0)
	f.SetPTS(42)
	av.FillTestFrameYFlat(f, 200)
	in := make(chan any, 1)
	first := make(chan any, 1)
	second := make(chan any, 1)
	in <- f
	close(in)

	if err := r.handleGoProcessor(t.Context(), node, []<-chan any{in}, []chan<- any{first, second}); err != nil {
		t.Fatalf("handleGoProcessor: %v", err)
	}
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("outputs received %d and %d frames, want one each", len(first), len(second))
	}
	a, b := (<-first).(*av.Frame), (<-second).(*av.Frame)
	defer a.Close()
	defer b.Close()
	if a == b {
		t.Fatal("outputs received the same frame reference")
	}
	a.SetPTS(100)
	a.Close()
	if got := b.PTS(); got != 42 {
		t.Errorf("second frame PTS = %d, want 42", got)
	}
	if got := b.Plane(0)[0]; got != 200 {
		t.Errorf("second frame luma = %d, want 200", got)
	}
}

func TestGoProcessor_CancelFanoutPreservesSentFrame(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f := av.NewTestFrame(t, 64, 48, 0)
	defer f.Close()
	first := make(chan any)
	done := make(chan error, 1)
	go func() {
		done <- sendFrame(ctx, f, []chan<- any{first, make(chan any)})
	}()
	a := (<-first).(*av.Frame)
	defer a.Close()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("sendFrame: got %v, want context.Canceled", err)
	}
	if a == f {
		t.Fatal("first output received the original frame reference")
	}
	if clone, err := f.Clone(); err == nil {
		clone.Close()
		t.Error("unsent original frame was not closed")
	}
	if got := a.Width(); got != 64 {
		t.Errorf("sent frame width = %d, want 64", got)
	}
}
