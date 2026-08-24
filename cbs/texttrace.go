// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package cbs

import (
	"fmt"
	"io"
)

// TextTracer renders the trace event stream in the exact text format of
// `ffmpeg -bsf:v trace_headers` (without the "[trace_headers @ 0x...] "
// prefix). Diagnostics above the info level (verbose, debug) are dropped,
// matching ffmpeg's default log level for the golden captures.
type TextTracer struct {
	w   io.Writer
	err error
	// MaxLevel is the most verbose Level printed; defaults to LevelInfo.
	MaxLevel Level
}

// NewTextTracer returns a Tracer writing FFmpeg-format trace lines to w.
func NewTextTracer(w io.Writer) *TextTracer {
	return &TextTracer{w: w, MaxLevel: LevelInfo}
}

// Line writes a raw line (the driver-level "Packet: ..." / "Extradata"
// lines from trace_headers.c, which are not Tracer events in FFmpeg
// either).
func (t *TextTracer) Line(s string) {
	if t.err == nil {
		_, t.err = fmt.Fprintln(t.w, s)
	}
}

func (t *TextTracer) Header(name string) { t.Line(name) }

func (t *TextTracer) Element(e Element) { t.Line(e.FFmpegLine()) }

func (t *TextTracer) Diag(level Level, msg string) {
	if level <= t.MaxLevel {
		t.Line(msg)
	}
}

// Err returns the first write error, if any.
func (t *TextTracer) Err() error { return t.err }
