// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"fmt"
	"log"
)

// maxConsecutiveDecodeErrors is how many undecodable packets in a row a
// stream may produce before the source gives up on it. At AC-3's 32 ms per
// frame that is ~32 s of continuous damage — enough to ride out a real tape
// dropout — while a stream that never decodes at all still fails within a
// fraction of a second of wall time.
const maxConsecutiveDecodeErrors = 1000

// decodeErrorLogVerbose is how many decode errors per stream are logged one
// by one before the log falls back to every decodeErrorLogEvery-th.
const (
	decodeErrorLogVerbose = 10
	decodeErrorLogEvery   = 100
)

// decodeErrorGate is the per-stream policy for decoder failures inside the
// source node.
//
// libav reports damage at packet granularity — a corrupt AC-3 frame, a slice
// with a bad header — and ffmpeg's own decode loop (fftools/ffmpeg_dec.c)
// logs such an error, counts it and moves on to the next packet unless
// `-xerror` was given. Before this gate the source node returned the first
// such error and the whole run died: a 40-minute tape capture with two bad
// audio frames produced nothing at all, where ffmpeg would have produced 40
// minutes minus 64 ms.
//
// Every error is counted (the node's Errors metric carries the total), the
// run continues, and only a stream that stops decoding altogether
// (maxConsecutive in a row) or an explicit exit_on_error turns a bad packet
// into a failed run. The gate is pure Go so the policy is testable without
// libav.
type decodeErrorGate struct {
	source         string // node id, for messages
	stream         int    // stream index, for messages
	exitOnError    bool   // global_options.exit_on_error: fail on the first error (ffmpeg -xerror)
	maxConsecutive int    // 0 = maxConsecutiveDecodeErrors

	consecutive int
	total       int
	logf        func(format string, args ...any) // nil = log.Printf
}

func newDecodeErrorGate(source string, stream int, exitOnError bool) *decodeErrorGate {
	return &decodeErrorGate{source: source, stream: stream, exitOnError: exitOnError}
}

func (g *decodeErrorGate) printf(format string, args ...any) {
	if g.logf != nil {
		g.logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

// onError records one undecodable packet. It returns a non-nil error — which
// the source must return, ending the run — when the stream has exceeded the
// consecutive cap or exit_on_error is set; nil means "skip the packet and
// carry on". The cause is wrapped, so errors.As against *av.Err still works.
func (g *decodeErrorGate) onError(cause error) error {
	g.consecutive++
	g.total++
	if g.exitOnError {
		return fmt.Errorf("source %q stream %d: decode error (exit_on_error): %w", g.source, g.stream, cause)
	}
	limit := g.maxConsecutive
	if limit <= 0 {
		limit = maxConsecutiveDecodeErrors
	}
	if g.consecutive >= limit {
		return fmt.Errorf("source %q stream %d: %d consecutive decode errors (total %d), last: %w",
			g.source, g.stream, g.consecutive, g.total, cause)
	}
	if g.total <= decodeErrorLogVerbose || g.total%decodeErrorLogEvery == 0 {
		g.printf("source %q stream %d: skipping undecodable packet (%d so far): %v",
			g.source, g.stream, g.total, cause)
	}
	return nil
}

// onCorrupt notes a packet the demuxer flagged AV_PKT_FLAG_CORRUPT. As in
// ffmpeg, the packet still goes to the decoder (many decoders conceal partial
// damage; refusing it outright would lose the good half of a video frame) —
// the flag is a warning unless exit_on_error makes it fatal.
func (g *decodeErrorGate) onCorrupt() error {
	if g.exitOnError {
		return fmt.Errorf("source %q stream %d: corrupt input packet (exit_on_error)", g.source, g.stream)
	}
	if g.total < decodeErrorLogVerbose {
		g.printf("source %q stream %d: corrupt input packet", g.source, g.stream)
	}
	return nil
}

// onFrame records a successfully decoded frame, which ends any run of
// consecutive failures.
func (g *decodeErrorGate) onFrame() { g.consecutive = 0 }

// summary is the one-line end-of-stream report, or "" when nothing was
// skipped.
func (g *decodeErrorGate) summary() string {
	if g.total == 0 {
		return ""
	}
	return fmt.Sprintf("source %q stream %d: skipped %d undecodable packet(s)", g.source, g.stream, g.total)
}
