// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later
//
// Violations: a first-class list of what is wrong with the stream.
//
// Layer 0 (always on): every unit parse failure the cbs templates
// already detect — out-of-range syntax elements, truncation, missing
// parameter-set references — becomes a kind="syntax" violation.
//
// Layer 1 (opt-in via Options.Checks): stream-structure checks over the
// decode-order unit walk (see checks.go), kind="structure".
//
// This is header-level validation only: semantics that need a decoder
// (DPB conformance, level limits, HRD, residuals) are out of scope.

package report

import (
	"strings"

	"github.com/MediaMolder/MediaMolder/cbs"
)

// Violation is one report entry. Packet is -1 for extradata units and
// split-phase failures; Unit is -1 for packet-level findings.
type Violation struct {
	Severity string `json:"severity"`        // "error" | "warning"
	Kind     string `json:"kind"`            // "syntax" | "structure"
	Check    string `json:"check,omitempty"` // structure-check id
	Spec     string `json:"spec,omitempty"`  // "H.264" | "H.265" | "AV1"
	Packet   int64  `json:"packet"`
	Unit     int    `json:"unit"`
	Message  string `json:"message"`
}

func specName(codec string) string {
	switch codec {
	case "h264":
		return "H.264"
	case "hevc", "h265":
		return "H.265"
	case "av1":
		return "AV1"
	}
	return ""
}

// violationLog accumulates violations and counts error severities.
type violationLog struct {
	list   []Violation
	errors int
}

func (v *violationLog) add(vi Violation) {
	v.list = append(v.list, vi)
	if vi.Severity == "error" {
		v.errors++
	}
}

// addUnitError records the layer-0 violation for a failed unit. msg may
// carry the parse diagnostic text ("PPS id 3 not available."); when
// empty the unit error string is used.
func (v *violationLog) addUnitError(codec string, packet int64, unit int, u *cbs.Unit, msg string) {
	if u.Err == nil {
		return
	}
	if msg == "" {
		msg = u.Err.Error()
	}
	v.add(Violation{
		Severity: "error",
		Kind:     "syntax",
		Spec:     specName(codec),
		Packet:   packet,
		Unit:     unit,
		Message:  msg,
	})
}

// firstErrorDiag extracts the first error-level diagnostic recorded for
// a unit by the collecting tracer ("error: PPS id 3 not available.").
func firstErrorDiag(diags []string) string {
	for _, d := range diags {
		if m, ok := strings.CutPrefix(d, "error: "); ok {
			return m
		}
	}
	return ""
}
