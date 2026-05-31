// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"context"
	"testing"
	"time"

	"github.com/MediaMolder/MediaMolder/av"
)

// newTestPkt allocates a small AV packet with the given PTS and stream
// index, expressed in microseconds (so the {1,1_000_000} time-base used
// by NewOutputPreroll lets us reason in real time).
func newTestPkt(t *testing.T, ptsUS int64, streamIdx int) *av.Packet {
	t.Helper()
	pkt, err := av.AllocPacket()
	if err != nil {
		t.Fatalf("AllocPacket: %v", err)
	}
	pkt.SetPTS(ptsUS)
	pkt.SetDTS(ptsUS)
	pkt.SetStreamIndex(streamIdx)
	return pkt
}

// timeBaseUS is a single-channel preroll time base of microseconds
// (1/1_000_000) so PTS values in newTestPkt directly translate to µs.
var timeBaseUS = [][2]int{{1, 1_000_000}}

func TestOutputPreroll_FillToReadyClosesChannel(t *testing.T) {
	p := NewOutputBuffer("vout", 100*time.Millisecond, 0, timeBaseUS)
	if got := p.State(); got != BufferStateFilling {
		t.Fatalf("initial state = %s, want FILLING", got)
	}

	for i := int64(0); i < 11; i++ {
		pass, _ := p.AddOrPass(0, newTestPkt(t, i*10_000, 0)) // 0..100 ms
		if pass {
			t.Fatalf("AddOrPass returned pass=true while filling at i=%d", i)
		}
	}

	if !p.IsReady() {
		t.Fatalf("state = %s, want READY", p.State())
	}
	select {
	case <-p.Ready():
	default:
		t.Fatal("Ready() channel did not close after fill target")
	}
	if p.ReadyAt().IsZero() {
		t.Fatal("ReadyAt() was not set after transition to READY")
	}
	if got := p.BufferedDuration(); got < 100*time.Millisecond {
		t.Fatalf("BufferedDuration() = %v, want >= 100ms", got)
	}
}

func TestOutputPreroll_EOSBeforeTargetMarksReadyPartial(t *testing.T) {
	p := NewOutputBuffer("vout", 500*time.Millisecond, 0, timeBaseUS)
	pass, full := p.AddOrPass(0, newTestPkt(t, 0, 0))
	if pass || full {
		t.Fatalf("AddOrPass: pass=%v full=%v, want false,false", pass, full)
	}

	p.MarkReadyPartial()
	if got := p.State(); got != BufferStateReadyPartial {
		t.Fatalf("state = %s, want READY_PARTIAL", got)
	}
	select {
	case <-p.Ready():
	default:
		t.Fatal("Ready() channel did not close after MarkReadyPartial")
	}
}

func TestOutputPreroll_EvictsOldestPastMaxDur(t *testing.T) {
	// target 50 ms, max 100 ms.
	p := NewOutputBuffer("vout", 50*time.Millisecond, 100*time.Millisecond, timeBaseUS)
	for i := int64(0); i < 30; i++ {
		_, _ = p.AddOrPass(0, newTestPkt(t, i*10_000, 0)) // 300 ms span unbounded
	}
	if p.Evictions() == 0 {
		t.Fatalf("expected evictions > 0, got 0")
	}
	if got := p.BufferedDuration(); got > 100*time.Millisecond {
		t.Fatalf("BufferedDuration() = %v, want <= 100ms (cap enforced)", got)
	}
}

func TestOutputPreroll_DrainTransitionsToStreaming(t *testing.T) {
	p := NewOutputBuffer("vout", 30*time.Millisecond, 0, timeBaseUS)
	for i := int64(0); i < 5; i++ {
		_, _ = p.AddOrPass(0, newTestPkt(t, i*10_000, 0)) // 40 ms span
	}
	if !p.IsReady() {
		t.Fatalf("state = %s, want READY before Drain", p.State())
	}
	drained := p.Drain()
	if len(drained) != 5 {
		t.Fatalf("Drain returned %d packets, want 5", len(drained))
	}
	for _, bp := range drained {
		_ = bp.pkt.Close()
	}
	if got := p.State(); got != BufferStateStreaming {
		t.Fatalf("state after Drain = %s, want STREAMING", got)
	}
	pass, _ := p.AddOrPass(0, newTestPkt(t, 100_000, 0))
	if !pass {
		t.Fatal("AddOrPass after Drain returned pass=false, want true")
	}
}

func TestGraphReady_ANDCombinesOutputs(t *testing.T) {
	g := newGraphReady()
	a := NewOutputBuffer("a", 30*time.Millisecond, 0, timeBaseUS)
	b := NewOutputBuffer("b", 30*time.Millisecond, 0, timeBaseUS)
	g.Add(a)
	g.Add(b)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.run(ctx, nil)

	// Fill only A.
	for i := int64(0); i < 5; i++ {
		_, _ = a.AddOrPass(0, newTestPkt(t, i*10_000, 0))
	}

	select {
	case <-g.Ready():
		t.Fatal("graph Ready() fired with only one output ready")
	case <-time.After(50 * time.Millisecond):
	}

	for i := int64(0); i < 5; i++ {
		_, _ = b.AddOrPass(0, newTestPkt(t, i*10_000, 0))
	}

	select {
	case <-g.Ready():
	case <-time.After(time.Second):
		t.Fatal("graph Ready() did not fire after both outputs filled")
	}

	ready, _, outs := g.State()
	if !ready {
		t.Fatalf("graphReady.State().ready = false after both outputs ready")
	}
	if len(outs) != 2 {
		t.Fatalf("graphReady.State().outputs = %d, want 2", len(outs))
	}
}
