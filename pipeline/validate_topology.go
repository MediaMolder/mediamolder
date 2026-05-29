// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"fmt"
	"strings"

	"github.com/MediaMolder/MediaMolder/graph"
)

// validateTopology builds the processing graph from cfg and runs all TOPO_*
// checks. Returns the built graph, or nil if graph construction failed.
func validateTopology(cfg *Config, r *ValidationReport) *graph.Graph {
	def := configToGraphDef(cfg)
	g, err := graph.Build(def)
	if err != nil {
		code, msg := classifyBuildError(err)
		r.add(ValidationIssue{
			Severity: SeverityError,
			Code:     code,
			Message:  msg,
		})
		return nil
	}

	checkDanglingNodes(cfg, g, r)
	checkEdgeViolations(g, r)
	checkMultiEdgeSamePort(g, r)
	checkUnreachableNodes(g, r)
	return g
}

// classifyBuildError maps a graph.Build error string to the most specific TOPO_* code.
func classifyBuildError(err error) (code, msg string) {
	s := err.Error()
	switch {
	case strings.Contains(s, "cycle"):
		return "TOPO_CYCLE", s
	case strings.Contains(s, "duplicate node id"):
		return "TOPO_DUPLICATE_NODE_ID", s
	case strings.Contains(s, "self-loop"):
		return "TOPO_SELF_LOOP", s
	case strings.Contains(s, "unknown source node"), strings.Contains(s, "unknown destination node"):
		return "TOPO_UNKNOWN_NODE_REF", s
	default:
		return "TOPO_CYCLE", s
	}
}

// sourceIDsWithEventsEdges returns the set of node IDs that have at least one
// outgoing events edge in cfg. Events edges are stripped from the AV graph but
// are valid connections, so inputs that feed only a go_processor events chain
// must not be flagged as dangling sources.
func sourceIDsWithEventsEdges(cfg *Config) map[string]bool {
	m := make(map[string]bool)
	for _, e := range cfg.Graph.Edges {
		if e.Type == "events" || e.Type == "file" {
			base := e.From
			if i := strings.Index(base, ":"); i >= 0 {
				base = base[:i]
			}
			m[base] = true
		}
	}
	return m
}

func checkDanglingNodes(cfg *Config, g *graph.Graph, r *ValidationReport) {
	evSources := sourceIDsWithEventsEdges(cfg)
	for _, n := range g.Sources {
		if len(n.Outbound) == 0 && !evSources[n.ID] {
			r.add(ValidationIssue{
				Severity:   SeverityError,
				Code:       "TOPO_DANGLING_SOURCE",
				Location:   "node:" + n.ID,
				Message:    fmt.Sprintf("source %q has no outbound edges", n.ID),
				Suggestion: "add an edge from this input to a processing node or output",
			})
		}
	}
	for _, n := range g.Sinks {
		if len(n.Inbound) == 0 {
			r.add(ValidationIssue{
				Severity:   SeverityError,
				Code:       "TOPO_DANGLING_SINK",
				Location:   "node:" + n.ID,
				Message:    fmt.Sprintf("output %q has no inbound edges", n.ID),
				Suggestion: "add an edge from a processing node or input to this output",
			})
		}
	}
}

func checkEdgeViolations(g *graph.Graph, r *ValidationReport) {
	for _, e := range g.Edges {
		if e.To.Kind == graph.KindSource {
			r.add(ValidationIssue{
				Severity:   SeverityError,
				Code:       "TOPO_EDGE_INTO_SOURCE",
				Location:   fmt.Sprintf("edge:%s→%s", e.From.ID, e.To.ID),
				Message:    fmt.Sprintf("edge from %q targets source node %q; sources have no input pads", e.From.ID, e.To.ID),
				Suggestion: "remove this edge; source nodes only produce frames",
			})
		}
		if e.From.Kind == graph.KindSink {
			r.add(ValidationIssue{
				Severity:   SeverityError,
				Code:       "TOPO_EDGE_FROM_SINK",
				Location:   fmt.Sprintf("edge:%s→%s", e.From.ID, e.To.ID),
				Message:    fmt.Sprintf("edge originates from sink node %q; sinks have no output pads", e.From.ID),
				Suggestion: "remove this edge; output nodes only consume frames",
			})
		}
	}
}

func checkMultiEdgeSamePort(g *graph.Graph, r *ValidationReport) {
	for _, n := range g.Nodes {
		portCount := make(map[string]int)
		for _, e := range n.Inbound {
			portCount[e.ToPort]++
		}
		for port, count := range portCount {
			if count > 1 {
				// "default" means the edge spec omitted a port selector,
				// which in FFmpeg's filtergraph model assigns pads
				// sequentially (pad 0, pad 1, …). Multiple edges at
				// "default" is the normal way to wire multi-input filters
				// (overlay, xfade, amix, acrossfade, …). Only flag
				// collisions on explicitly named ports.
				if port == "default" {
					continue
				}
				r.add(ValidationIssue{
					Severity:   SeverityError,
					Code:       "TOPO_MULTI_EDGE_SAME_INPUT_PORT",
					Location:   "node:" + n.ID,
					Message:    fmt.Sprintf("node %q has %d edges at input port %q; only one edge per pad is allowed", n.ID, count, port),
					Suggestion: "use a split/asplit node to fan out from a single source, or fix the port assignments",
				})
			}
		}
	}
}

// checkUnreachableNodes flags filter/encoder nodes with no path to any sink.
func checkUnreachableNodes(g *graph.Graph, r *ValidationReport) {
	reachable := make(map[string]bool)
	for _, sink := range g.Sinks {
		markReachableBackward(sink, reachable)
	}
	for id, n := range g.Nodes {
		if n.Kind != graph.KindFilter && n.Kind != graph.KindEncoder {
			continue
		}
		if !reachable[id] {
			r.add(ValidationIssue{
				Severity:   SeverityWarning,
				Code:       "TOPO_UNREACHABLE_NODE",
				Location:   "node:" + id,
				Message:    fmt.Sprintf("processing node %q has no path to any output", id),
				Suggestion: "connect this node to an output, or remove it if unused",
			})
		}
	}
}

func markReachableBackward(n *graph.Node, visited map[string]bool) {
	if visited[n.ID] {
		return
	}
	visited[n.ID] = true
	for _, e := range n.Inbound {
		markReachableBackward(e.From, visited)
	}
}
