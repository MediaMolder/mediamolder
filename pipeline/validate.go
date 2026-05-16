// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/MediaMolder/MediaMolder/graph"
)

// Severity indicates how serious a validation issue is.
type Severity string

const (
	SeverityError   Severity = "ERROR"
	SeverityWarning Severity = "WARNING"
	SeverityInfo    Severity = "INFO"
)

// ValidationIssue describes a single detected problem in a pipeline config.
type ValidationIssue struct {
	Severity   Severity `json:"severity"`
	Code       string   `json:"code"`
	Location   string   `json:"location,omitempty"`
	Message    string   `json:"message"`
	Suggestion string   `json:"suggestion,omitempty"`
}

// ValidationReport is the result of a full validation run.
type ValidationReport struct {
	Issues      []ValidationIssue `json:"issues"`
	HasErrors   bool              `json:"has_errors"`
	HasWarnings bool              `json:"has_warnings"`
}

func (r *ValidationReport) add(issue ValidationIssue) {
	r.Issues = append(r.Issues, issue)
	switch issue.Severity {
	case SeverityError:
		r.HasErrors = true
	case SeverityWarning:
		r.HasWarnings = true
	}
}

// Error returns a human-readable summary of all ERROR-level issues.
func (r *ValidationReport) Error() string {
	if !r.HasErrors {
		return ""
	}
	var b strings.Builder
	for _, iss := range r.Issues {
		if iss.Severity != SeverityError {
			continue
		}
		if iss.Location != "" {
			fmt.Fprintf(&b, "ERROR [%s] %s: %s\n", iss.Location, iss.Code, iss.Message)
		} else {
			fmt.Fprintf(&b, "ERROR %s: %s\n", iss.Code, iss.Message)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// ValidateConfigStatic runs all Phase A static validation checks against cfg.
//
// It performs topology analysis, codec/container compatibility, filter arity,
// two-pass consistency, hardware availability, and optional security checks.
// No file I/O is performed (Phase B probe-assisted checks are separate).
// sec may be nil; when provided, SEC_* security checks are also run.
func ValidateConfigStatic(cfg *Config, sec *SecurityConfig) *ValidationReport {
	r := &ValidationReport{}

	// Build the graph and check topology. Returns nil if the graph is malformed.
	g := validateTopology(cfg, r)

	// Per-node video and audio static checks.
	validateVideo(cfg, g, r)
	validateAudio(cfg, g, r)

	// Codec/container compatibility matrix.
	validateCodecContainer(cfg, r)

	// Two-pass encoder consistency.
	validateTwoPass(cfg, r)

	// Filter arity and media-type checks.
	validateFilters(cfg, g, r)

	// Hardware encoder availability and platform compatibility.
	validateHardware(cfg, r)

	// Security / resource limits.
	if sec != nil {
		validateSecurity(cfg, sec, r)
	}

	return r
}

// ---------- graph path helpers ----------

// pathContainsFilter returns true if any node on any path from from (exclusive)
// to to (exclusive) is a KindFilter node whose Filter field is in names.
// Returns false if g, from, or to is nil.
func pathContainsFilter(g *graph.Graph, from, to *graph.Node, names map[string]bool) bool {
	if g == nil || from == nil || to == nil {
		return false
	}
	visited := make(map[string]bool)
	return dfsFilter(from, to, names, visited)
}

func dfsFilter(cur, target *graph.Node, names map[string]bool, visited map[string]bool) bool {
	if visited[cur.ID] {
		return false
	}
	visited[cur.ID] = true
	for _, e := range cur.Outbound {
		next := e.To
		if next.ID == target.ID {
			continue // reached target without finding filter
		}
		if next.Kind == graph.KindFilter && names[next.Filter] {
			return true
		}
		if dfsFilter(next, target, names, visited) {
			return true
		}
	}
	return false
}

// ---------- shared param helpers ----------

// paramToInt converts a map[string]any value to int.
// Returns 0 for zero, -1 if the value cannot be parsed.
func paramToInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(x))
		if err != nil {
			return -1
		}
		return n
	}
	return -1
}

// nodeParamString returns the string value of a node param, or "".
func nodeParamString(nd NodeDef, key string) string {
	v, ok := nd.Params[key]
	if !ok {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", v))
}

// isZeroRateParam returns true if a param value represents a zero frame rate.
func isZeroRateParam(v any) bool {
	s := strings.TrimSpace(fmt.Sprintf("%v", v))
	return s == "0" || s == "0/1" || s == "0/0"
}

// nodeOrNil returns the graph node for the given ID, or nil if g is nil.
func nodeOrNil(g *graph.Graph, id string) *graph.Node {
	if g == nil {
		return nil
	}
	return g.Nodes[id]
}

// containsStr reports whether list contains s.
func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// containsInt reports whether list contains n.
func containsInt(list []int, n int) bool {
	for _, v := range list {
		if v == n {
			return true
		}
	}
	return false
}
