// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestHandleSink_OneStreamErrorDoesNotHangSiblings is a regression test for the
// multi-stream sink deadlock. When one output stream's muxer write fails (e.g.
// the muxer rejects a non-monotonic DTS), the sibling stream consumers — which
// are otherwise blocked on their input channels waiting for packets that will
// never arrive — must be cancelled so handleSink returns the error promptly.
//
// Before the fix, handleSink built its per-stream consumer group with
// `errgroup.WithContext(ctx)` but discarded the derived (cancellable) context
// and looped with a bare `for v := range in`, so a single stream's error left
// every sibling blocked forever: eg.Wait() never returned and the whole
// pipeline hung until the job timed out. This test deadlocks (10 s timeout)
// against that code and passes against the fix.
func TestHandleSink_OneStreamErrorDoesNotHangSiblings(t *testing.T) {
	sentinel := errors.New("muxer rejected packet (non-monotonic dts)")
	fm := &fakeMuxer{tb: tb1k, writeErr: sentinel}
	defer fm.closeAll()

	// Two inbound edges => handleSink takes the multi-stream interleave path.
	node := makeVideoNode("out", 2)

	r := &graphRunner{
		pipe: minimalPipeline(),
		sinks: map[string]*sinkResources{
			"out": {
				muxer:         fm,
				streamRescale: []*sinkRescale{nil, nil},
				timing:        outputTiming{recordingUS: noLimitUS},
				shortestPTSus: noLimitUS,
			},
		},
	}

	chErr := make(chan any, 1)  // stream 0: one packet, rejected by the muxer
	chBlocked := make(chan any) // stream 1: never fed, never closed
	ins := []<-chan any{chErr, chBlocked}

	chErr <- makePkt(t, 0)

	done := make(chan error, 1)
	go func() { done <- r.handleSink(context.Background(), node, ins) }()

	select {
	case err := <-done:
		if !errors.Is(err, sentinel) {
			t.Fatalf("handleSink returned %v, want %v", err, sentinel)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("handleSink hung: a stream error did not cancel sibling consumers (deadlock regression)")
	}
}
