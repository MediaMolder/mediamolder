// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"errors"
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
)

func quietGate(exitOnError bool) *decodeErrorGate {
	g := newDecodeErrorGate("in0", 1, exitOnError)
	g.logf = func(string, ...any) {}
	return g
}

// The default policy is ffmpeg's: an undecodable packet is skipped and
// counted, the run goes on.
func TestDecodeErrorGateSkipsAndCounts(t *testing.T) {
	g := quietGate(false)
	cause := &av.Err{Code: -0x1030c0a, Message: "sync"}
	for i := 0; i < 5; i++ {
		if err := g.onError(cause); err != nil {
			t.Fatalf("error %d must be skipped, got %v", i, err)
		}
	}
	if g.total != 5 || g.consecutive != 5 {
		t.Fatalf("total/consecutive = %d/%d, want 5/5", g.total, g.consecutive)
	}
	if s := g.summary(); !strings.Contains(s, "skipped 5") {
		t.Fatalf("summary = %q", s)
	}
	if quietGate(false).summary() != "" {
		t.Fatal("a clean stream must have no summary line")
	}
}

// A decoded frame ends the consecutive run; the total keeps counting.
func TestDecodeErrorGateFrameResetsConsecutive(t *testing.T) {
	g := quietGate(false)
	g.maxConsecutive = 3
	cause := errors.New("bad packet")
	for i := 0; i < 2; i++ {
		if err := g.onError(cause); err != nil {
			t.Fatal(err)
		}
	}
	g.onFrame()
	for i := 0; i < 2; i++ {
		if err := g.onError(cause); err != nil {
			t.Fatalf("after a frame the run restarts; got %v", err)
		}
	}
	if g.total != 4 || g.consecutive != 2 {
		t.Fatalf("total/consecutive = %d/%d, want 4/2", g.total, g.consecutive)
	}
}

// A stream that never decodes fails at the consecutive cap, with the cause
// still reachable through errors.As.
func TestDecodeErrorGateCapIsFatal(t *testing.T) {
	g := quietGate(false)
	g.maxConsecutive = 3
	cause := &av.Err{Code: -0x1030c0a, Message: "sync"}
	var err error
	for i := 0; i < 3 && err == nil; i++ {
		err = g.onError(cause)
	}
	if err == nil {
		t.Fatal("the third consecutive error must be fatal")
	}
	var ae *av.Err
	if !errors.As(err, &ae) || ae.Code != cause.Code {
		t.Fatalf("cause not wrapped: %v", err)
	}
	if !strings.Contains(err.Error(), "3 consecutive") {
		t.Fatalf("message = %q", err)
	}
}

// exit_on_error is ffmpeg's -xerror: the first error ends the run.
func TestDecodeErrorGateExitOnError(t *testing.T) {
	g := quietGate(true)
	if err := g.onError(errors.New("bad packet")); err == nil {
		t.Fatal("exit_on_error must fail on the first error")
	}
	if err := quietGate(true).onCorrupt(); err == nil {
		t.Fatal("exit_on_error must refuse a corrupt-flagged packet")
	}
	if err := quietGate(false).onCorrupt(); err != nil {
		t.Fatalf("a corrupt flag is a warning by default, got %v", err)
	}
}

// The default cap is the documented constant.
func TestDecodeErrorGateDefaultCap(t *testing.T) {
	g := quietGate(false)
	cause := errors.New("bad packet")
	for i := 0; i < maxConsecutiveDecodeErrors-1; i++ {
		if err := g.onError(cause); err != nil {
			t.Fatalf("error %d fatal before the cap: %v", i, err)
		}
	}
	if err := g.onError(cause); err == nil {
		t.Fatalf("error %d must hit the cap", maxConsecutiveDecodeErrors)
	}
}
