// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"errors"
	"sync"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/graph"
)

// ---------- fakeMuxer ----------

// fakeMuxer implements muxWriter for tests. It records every packet written
// and allows the test to control BytesWritten() to exercise max_file_size.
type fakeMuxer struct {
	mu       sync.Mutex
	packets  []*av.Packet
	written  int64 // value returned by BytesWritten
	writeErr error // injected WritePacket error
	tb       [2]int
}

func (f *fakeMuxer) WritePacket(pkt *av.Packet) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeErr != nil {
		return f.writeErr
	}
	c, err := av.ClonePacket(pkt)
	if err != nil {
		return err
	}
	f.packets = append(f.packets, c)
	return nil
}
func (f *fakeMuxer) WriteTrailer() error       { return nil }
func (f *fakeMuxer) BytesWritten() int64       { f.mu.Lock(); defer f.mu.Unlock(); return f.written }
func (f *fakeMuxer) StreamTimeBase(int) [2]int { return f.tb }
func (f *fakeMuxer) Abort()                    {}
func (f *fakeMuxer) Close() error              { return nil }

func (f *fakeMuxer) closeAll() {
	for _, p := range f.packets {
		p.Close()
	}
}

// ---------- helpers ----------

// minimalPipeline returns a *Pipeline with only the metrics registry set,
// sufficient for the metrics calls in sinkWriter.writeOne.
func minimalPipeline() *Pipeline {
	return &Pipeline{metrics: NewMetricsRegistry()}
}

// makeVideoNode builds a graph.Node with n inbound video edges (nil From nodes)
// so limitForChan returns the video cap for channel 0.
func makeVideoNode(id string, n int) *graph.Node {
	node := &graph.Node{ID: id}
	for i := 0; i < n; i++ {
		node.Inbound = append(node.Inbound, &graph.Edge{Type: graph.PortVideo})
	}
	return node
}

// makePkt allocates a packet and sets its PTS to the given value.
func makePkt(t *testing.T, pts int64) *av.Packet {
	t.Helper()
	pkt, err := av.AllocPacket()
	if err != nil {
		t.Fatalf("AllocPacket: %v", err)
	}
	pkt.SetPTS(pts)
	pkt.SetDTS(pts)
	return pkt
}

// ---------- processOne table tests ----------

// tb1k is a convenient 1/1000 time base (1 tick = 1 ms = 1000 µs).
var tb1k = [2]int{1, 1000}

// TestSinkWriter_ProcessOne covers the trim, max-frames, shortest, and
// write-success paths of sinkWriter.processOne without requiring a real muxer
// or live media files.
func TestSinkWriter_ProcessOne(t *testing.T) {
	const nodeID = "sink0"

	// Helper: build a sinkWriter with a fakeMuxer.
	makeSink := func(
		fm *fakeMuxer,
		startUS, stopUS, shiftUS int64,
		shortest bool, shortestPTSus int64,
		maxFrames int, maxFileSize int64,
	) *sinkWriter {
		cfg := Output{MaxFramesVideo: maxFrames}
		sr := &sinkResources{
			muxer:         fm,
			cfg:           cfg,
			shortest:      shortest,
			shortestPTSus: shortestPTSus,
			maxFileSize:   maxFileSize,
		}
		if shortest {
			sr.shortestPTSus = shortestPTSus
		}
		return &sinkWriter{
			sink:    sr,
			node:    makeVideoNode(nodeID, 1),
			startUS: startUS,
			stopUS:  stopUS,
			shiftUS: shiftUS,
			pipe:    minimalPipeline(),
		}
	}

	cases := []struct {
		name        string
		ptsInTB     int64 // packet PTS in 1/1000 units
		startUS     int64
		stopUS      int64
		shiftUS     int64
		shortest    bool
		shortestUS  int64 // active shortest bound (noLimitUS = none set)
		maxFrames   int   // 0 = unlimited
		preWritten  int   // st.written before the call
		maxFileSize int64 // 0 = unlimited
		fmWritten   int64 // BytesWritten() reported by fakeMuxer
		wantWrote   bool
		wantStopAll bool
		wantErr     bool
	}{
		// ── trim start (output-side -ss) ─────────────────────────────────────
		{
			name:        "trim start: below startUS drops",
			ptsInTB:     3000, // 3_000_000 µs = 3 s
			startUS:     5_000_000,
			stopUS:      noLimitUS,
			wantWrote:   false,
			wantStopAll: false,
		},
		{
			name:        "trim start: at startUS passes",
			ptsInTB:     5000, // 5_000_000 µs = exactly startUS
			startUS:     5_000_000,
			stopUS:      noLimitUS,
			wantWrote:   true,
			wantStopAll: false,
		},
		// ── trim stop (output-side -t/-to) ───────────────────────────────────
		{
			name:        "trim stop: at stopUS triggers stopAll",
			ptsInTB:     10000, // 10_000_000 µs = 10 s
			startUS:     int64(av.NoPTSValue),
			stopUS:      10_000_000,
			wantWrote:   false,
			wantStopAll: true,
		},
		{
			name:        "trim stop: just below stopUS writes",
			ptsInTB:     9999, // 9_999_000 µs < 10_000_000
			startUS:     int64(av.NoPTSValue),
			stopUS:      10_000_000,
			wantWrote:   true,
			wantStopAll: false,
		},
		// ── max-frames (-frames:v) ────────────────────────────────────────────
		{
			name:        "max-frames: at limit drops",
			ptsInTB:     1000,
			startUS:     int64(av.NoPTSValue),
			stopUS:      noLimitUS,
			maxFrames:   5,
			preWritten:  5, // already hit the cap
			wantWrote:   false,
			wantStopAll: false,
		},
		{
			name:        "max-frames: below limit writes",
			ptsInTB:     1000,
			startUS:     int64(av.NoPTSValue),
			stopUS:      noLimitUS,
			maxFrames:   5,
			preWritten:  4,
			wantWrote:   true,
			wantStopAll: false,
		},
		// ── -shortest ────────────────────────────────────────────────────────
		{
			name:        "shortest: ptsUS >= bound drops",
			ptsInTB:     10000, // 10 s
			startUS:     int64(av.NoPTSValue),
			stopUS:      noLimitUS,
			shortest:    true,
			shortestUS:  9_000_000, // bound set by a channel that closed at 9 s
			wantWrote:   false,
			wantStopAll: false,
		},
		{
			name:        "shortest: ptsUS < bound passes",
			ptsInTB:     5000, // 5 s
			startUS:     int64(av.NoPTSValue),
			stopUS:      noLimitUS,
			shortest:    true,
			shortestUS:  9_000_000,
			wantWrote:   true,
			wantStopAll: false,
		},
		{
			name:        "shortest: no bound set (noLimitUS) passes",
			ptsInTB:     5000,
			startUS:     int64(av.NoPTSValue),
			stopUS:      noLimitUS,
			shortest:    true,
			shortestUS:  noLimitUS, // no channel has closed yet
			wantWrote:   true,
			wantStopAll: false,
		},
		// ── max_file_size (-fs) ───────────────────────────────────────────────
		{
			name:        "max_file_size: limit reached triggers stopAll",
			ptsInTB:     1000,
			startUS:     int64(av.NoPTSValue),
			stopUS:      noLimitUS,
			maxFileSize: 1 << 20, // 1 MiB
			fmWritten:   1 << 20, // already at the limit
			wantWrote:   false,
			wantStopAll: true,
		},
		{
			name:        "max_file_size: under limit writes",
			ptsInTB:     1000,
			startUS:     int64(av.NoPTSValue),
			stopUS:      noLimitUS,
			maxFileSize: 1 << 20,
			fmWritten:   (1 << 20) - 1,
			wantWrote:   true,
			wantStopAll: false,
		},
		// ── happy path ───────────────────────────────────────────────────────
		{
			name:        "no constraints: packet written",
			ptsInTB:     2500,
			startUS:     int64(av.NoPTSValue),
			stopUS:      noLimitUS,
			wantWrote:   true,
			wantStopAll: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fm := &fakeMuxer{tb: tb1k, written: tc.fmWritten}
			defer fm.closeAll()

			shortestBound := noLimitUS
			if tc.shortest {
				shortestBound = tc.shortestUS
			}
			w := makeSink(fm, tc.startUS, tc.stopUS, 0, tc.shortest, shortestBound, tc.maxFrames, tc.maxFileSize)

			pkt := makePkt(t, tc.ptsInTB)
			defer pkt.Close()

			st := &chanState{written: tc.preWritten}
			wrote, stopAll, err := w.processOne(0, pkt, tb1k, nil, st)

			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if wrote != tc.wantWrote {
				t.Errorf("wrote = %v, want %v", wrote, tc.wantWrote)
			}
			if stopAll != tc.wantStopAll {
				t.Errorf("stopAll = %v, want %v", stopAll, tc.wantStopAll)
			}
		})
	}
}

// TestSinkWriter_WriteOne_Error verifies that a WritePacket error from the
// muxer is propagated through processOne.
func TestSinkWriter_WriteOne_Error(t *testing.T) {
	sentinel := errors.New("disk full")
	fm := &fakeMuxer{tb: tb1k, writeErr: sentinel}
	defer fm.closeAll()

	w := &sinkWriter{
		sink: &sinkResources{
			muxer:         fm,
			shortestPTSus: noLimitUS,
		},
		node:    makeVideoNode("sink0", 0),
		startUS: int64(av.NoPTSValue),
		stopUS:  noLimitUS,
		pipe:    minimalPipeline(),
	}

	pkt := makePkt(t, 1000)
	defer pkt.Close()

	st := &chanState{}
	_, _, err := w.processOne(0, pkt, tb1k, nil, st)
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
}

// TestSinkWriter_RecordShortest checks that recordShortest updates
// shortestPTSus when a channel closes with a smaller PTS.
func TestSinkWriter_RecordShortest(t *testing.T) {
	fm := &fakeMuxer{tb: tb1k}
	defer fm.closeAll()

	sr := &sinkResources{
		muxer:         fm,
		shortest:      true,
		shortestPTSus: noLimitUS,
	}
	w := &sinkWriter{sink: sr, node: makeVideoNode("sink0", 0)}

	// First channel closes at 5 s.
	w.recordShortest(5_000_000, true)
	sr.shortestMu.Lock()
	got := sr.shortestPTSus
	sr.shortestMu.Unlock()
	if got != 5_000_000 {
		t.Errorf("shortestPTSus = %d, want 5_000_000", got)
	}

	// Second channel closes at 8 s — should not increase the bound.
	w.recordShortest(8_000_000, true)
	sr.shortestMu.Lock()
	got = sr.shortestPTSus
	sr.shortestMu.Unlock()
	if got != 5_000_000 {
		t.Errorf("shortestPTSus = %d after later close, want 5_000_000 (unchanged)", got)
	}

	// Third channel closes at 3 s — should lower the bound.
	w.recordShortest(3_000_000, true)
	sr.shortestMu.Lock()
	got = sr.shortestPTSus
	sr.shortestMu.Unlock()
	if got != 3_000_000 {
		t.Errorf("shortestPTSus = %d after earlier close, want 3_000_000", got)
	}
}
