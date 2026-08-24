// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later
//
// Port of the CBS trace callbacks: ff_cbs_trace_header and
// ff_cbs_trace_read_log (libavcodec/cbs.c).

package cbs

import (
	"fmt"
	"strings"
)

// Level mirrors the av_log levels CBS emits diagnostics at.
type Level int

const (
	LevelError Level = iota
	LevelWarning
	LevelInfo
	LevelVerbose
	LevelDebug
)

// Element is one traced syntax element, as delivered to
// ff_cbs_trace_read_log.
type Element struct {
	// Position is the bit offset of the element within the unit's RBSP
	// (get_bits_count at CBS_TRACE_READ_START).
	Position int
	// Length is the number of bits consumed; 0 for VALUE_ONLY composite
	// elements (e.g. AV1 subexp).
	Length int
	// Name is the exact C identifier from the syntax template, with any
	// bracket groups unsubstituted (e.g. "offset_for_ref_frame[i]").
	Name string
	// Subscripts are the values substituted into the bracket groups, left
	// to right; nil when the template passed no subscripts.
	Subscripts []int
	// Bits is the element rendered as '0'/'1' characters; empty when
	// Length == 0.
	Bits string
	// Value is the decoded value (C asserts INT_MIN <= v <= UINT32_MAX).
	Value int64
}

// DisplayName substitutes Subscripts into the bracket groups of Name,
// mirroring cbs.c trace_read_log: the first len(Subscripts) groups become
// "[%d]"; remaining groups are copied verbatim.
func (e Element) DisplayName() string {
	if len(e.Subscripts) == 0 && !strings.ContainsRune(e.Name, '[') {
		return e.Name
	}
	var b strings.Builder
	n, subs := 0, len(e.Subscripts)
	s := e.Name
	for i := 0; i < len(s); {
		if s[i] == '[' && n < subs {
			fmt.Fprintf(&b, "[%d", e.Subscripts[n])
			n++
			for i++; i < len(s) && s[i] != ']'; i++ {
			}
			// the ']' itself is copied by the normal path below
		} else if s[i] == '[' {
			for i < len(s) && s[i] != ']' {
				b.WriteByte(s[i])
				i++
			}
		} else {
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String()
}

// FFmpegLine renders the element exactly as cbs.c prints it:
//
//	"%-10d  %s%*s = %"PRId64
//
// with pad = bits_len+2 when name_len+bits_len > 60, else 61-name_len.
func (e Element) FFmpegLine() string {
	name := e.DisplayName()
	var pad int
	if len(name)+len(e.Bits) > 60 {
		pad = len(e.Bits) + 2
	} else {
		pad = 61 - len(name)
	}
	return fmt.Sprintf("%-10d  %s%s%s = %d",
		e.Position, name, strings.Repeat(" ", pad-len(e.Bits)), e.Bits, e.Value)
}

// Tracer receives the parse event stream. Header corresponds to
// ff_cbs_trace_header (a section title such as "Sequence Parameter Set"),
// Element to ff_cbs_trace_read_log, and Diag to the av_log diagnostics CBS
// emits while parsing (errors, warnings such as
// "4 bytes left at end of AVCC header.").
//
// A nil Tracer disables tracing (trace_enable == 0); parsing still fills
// the content structs.
type Tracer interface {
	Header(name string)
	Element(e Element)
	Diag(level Level, msg string)
}
