// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package runtime

import (
	"context"
	"testing"
	"time"
)

func TestEdgeStatsPartialFill(t *testing.T) {
	reg := NewEdgeStatsRegistry()
	ch := make(chan any, 10)
	es := reg.Register("a→b:video", "a", "b", "video", ch)

	// Fill 3 of 10 slots.
	for i := 0; i < 3; i++ {
		ch <- i
	}
	reg.Sample()

	if got := es.Fill(); got < 0.29 || got > 0.31 {
		t.Errorf("Fill() = %f, want ~0.3", got)
	}
	if es.Stalls() != 0 {
		t.Errorf("Stalls() = %d, want 0", es.Stalls())
	}
}

func TestEdgeStatsFullChannel(t *testing.T) {
	reg := NewEdgeStatsRegistry()
	ch := make(chan any, 4)
	es := reg.Register("a→b:video", "a", "b", "video", ch)

	for i := 0; i < 4; i++ {
		ch <- i
	}
	reg.Sample()

	if got := es.Fill(); got != 1.0 {
		t.Errorf("Fill() = %f, want 1.0", got)
	}
	if es.Stalls() != 1 {
		t.Errorf("Stalls() = %d, want 1", es.Stalls())
	}

	// Sample again — stalls should increment.
	reg.Sample()
	if es.Stalls() != 2 {
		t.Errorf("Stalls() = %d, want 2", es.Stalls())
	}
}

func TestEdgeStatsPeakFill(t *testing.T) {
	reg := NewEdgeStatsRegistry()
	ch := make(chan any, 10)
	es := reg.Register("a→b:video", "a", "b", "video", ch)

	// Fill to 80%.
	for i := 0; i < 8; i++ {
		ch <- i
	}
	reg.Sample()
	peak1 := es.PeakFill()
	if peak1 < 0.79 || peak1 > 0.81 {
		t.Fatalf("PeakFill() = %f, want ~0.8", peak1)
	}

	// Drain to 20%.
	for i := 0; i < 6; i++ {
		<-ch
	}
	reg.Sample()

	// Current fill should be ~0.2, but peak should still be ~0.8.
	if got := es.Fill(); got < 0.19 || got > 0.21 {
		t.Errorf("Fill() = %f, want ~0.2", got)
	}
	if got := es.PeakFill(); got < 0.79 || got > 0.81 {
		t.Errorf("PeakFill() = %f, want ~0.8 (unchanged)", got)
	}
}

func TestEdgeStatsEmptyChannel(t *testing.T) {
	reg := NewEdgeStatsRegistry()
	ch := make(chan any, 8)
	es := reg.Register("a→b:audio", "a", "b", "audio", ch)

	reg.Sample()
	if es.Fill() != 0.0 {
		t.Errorf("Fill() = %f, want 0.0", es.Fill())
	}
	if es.PeakFill() != 0.0 {
		t.Errorf("PeakFill() = %f, want 0.0", es.PeakFill())
	}
}

func TestEdgeStatsSnapshot(t *testing.T) {
	reg := NewEdgeStatsRegistry()
	ch := make(chan any, 4)
	ch <- 1
	ch <- 2
	reg.Register("x→y:video", "x", "y", "video", ch)
	reg.Sample()

	snaps := reg.Snapshot()
	if len(snaps) != 1 {
		t.Fatalf("len(Snapshot()) = %d, want 1", len(snaps))
	}
	s := snaps[0]
	if s.FromNode != "x" || s.ToNode != "y" {
		t.Errorf("FromNode=%q ToNode=%q", s.FromNode, s.ToNode)
	}
	if s.Fill < 0.49 || s.Fill > 0.51 {
		t.Errorf("Fill = %f, want ~0.5", s.Fill)
	}
}

func TestEdgeStatsSamplerStops(t *testing.T) {
	reg := NewEdgeStatsRegistry()
	ch := make(chan any, 4)
	reg.Register("a→b:video", "a", "b", "video", ch)

	ctx, cancel := context.WithCancel(context.Background())
	reg.StartSampler(ctx, 10*time.Millisecond)

	ch <- 1
	time.Sleep(50 * time.Millisecond)
	cancel()

	// Sampler should have run at least once.
	snap := reg.Snapshot()
	if snap[0].Fill == 0 {
		t.Error("sampler did not run")
	}
}

func TestEdgeStatsZeroCapChannel(t *testing.T) {
	reg := NewEdgeStatsRegistry()
	ch := make(chan any) // unbuffered
	reg.Register("a→b:video", "a", "b", "video", ch)

	// Should not panic on zero-cap channel.
	reg.Sample()
}
