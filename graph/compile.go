// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package graph

import "fmt"

// Compile analyzes a validated Graph and produces an ExecutionPlan.
//
// The compilation phase sits between Build (which validates structure and
// produces a topologically sorted DAG) and the runtime scheduler (which
// executes nodes as goroutines). It performs analysis passes that would be
// too expensive or out-of-scope for Build, but whose results are useful
// before committing to execution.
//
// Current passes:
//  1. Stage grouping — assigns each node a topological depth and groups
//     nodes at the same depth into stages that can run concurrently.
//  2. Dead-branch detection — walks backward from sinks to find nodes
//     whose outputs are never consumed. These nodes run but waste work.
//  3. Disconnected-source detection — flags source nodes with no outbound
//     edges, which would be started by the scheduler but produce nothing.
//  4. Buffer size hints — assigns per-edge channel buffer sizes based on
//     the kinds of the connected nodes (e.g., larger buffers after sources
//     to absorb demux bursts).
//
// Compile never modifies the input Graph.
func Compile(g *Graph) (*ExecutionPlan, error) {
	if g == nil {
		return nil, fmt.Errorf("compile: nil graph")
	}
	if len(g.Order) == 0 {
		return nil, fmt.Errorf("compile: graph has no nodes")
	}

	plan := &ExecutionPlan{Graph: g}

	// Pass 1: compute topological depth and group nodes into stages.
	plan.Stages = computeStages(g)

	// Pass 2: detect dead branches (nodes unreachable from any sink).
	detectDeadBranches(g, plan)

	// Pass 3: detect disconnected sources.
	detectDisconnectedSources(g, plan)

	// Pass 4: compute per-edge buffer size hints.
	computeBufferHints(g, plan)

	return plan, nil
}

// computeStages assigns each node a topological depth and groups nodes at the
// same depth into stages.
//
// Depth is defined as:
//   - 0 for nodes with no inbound edges (sources)
//   - max(depth of predecessors) + 1 for all other nodes
//
// This produces the earliest possible execution stage for each node. Nodes
// within the same stage have no data dependencies on each other.
func computeStages(g *Graph) []Stage {
	depth := make(map[string]int, len(g.Nodes))

	// Walk in topological order so all predecessors are processed first.
	for _, node := range g.Order {
		d := 0
		for _, e := range node.Inbound {
			predDepth := depth[e.From.ID]
			if predDepth+1 > d {
				d = predDepth + 1
			}
		}
		depth[node.ID] = d
	}

	// Find the maximum depth to size the stages slice.
	maxDepth := 0
	for _, d := range depth {
		if d > maxDepth {
			maxDepth = d
		}
	}

	// Group nodes by depth.
	stages := make([]Stage, maxDepth+1)
	for i := range stages {
		stages[i].Depth = i
	}
	for _, node := range g.Order {
		d := depth[node.ID]
		stages[d].Nodes = append(stages[d].Nodes, node)
	}

	// Nodes within each stage are already sorted because g.Order is
	// deterministically sorted (Kahn's algorithm with alphabetical tie-breaking).
	return stages
}

// detectDeadBranches walks backward from all sink nodes and marks any node
// that cannot reach a sink as a dead branch.
func detectDeadBranches(g *Graph, plan *ExecutionPlan) {
	// Walk backward from sinks to find all "live" nodes.
	live := make(map[string]bool, len(g.Nodes))
	var walk func(n *Node)
	walk = func(n *Node) {
		if live[n.ID] {
			return
		}
		live[n.ID] = true
		for _, e := range n.Inbound {
			walk(e.From)
		}
	}
	for _, sink := range g.Sinks {
		walk(sink)
	}

	// Any node not in the live set is a dead branch.
	for _, node := range g.Order {
		if !live[node.ID] {
			plan.Warnings = append(plan.Warnings, Warning{
				NodeID:  node.ID,
				Code:    WarnDeadNode,
				Message: fmt.Sprintf("node %q is not connected to any output and its results will be discarded", node.ID),
			})
		}
	}
}

// detectDisconnectedSources flags source nodes that have no outbound edges.
func detectDisconnectedSources(g *Graph, plan *ExecutionPlan) {
	for _, src := range g.Sources {
		if len(src.Outbound) == 0 {
			plan.Warnings = append(plan.Warnings, Warning{
				NodeID:  src.ID,
				Code:    WarnDisconnectedSource,
				Message: fmt.Sprintf("source %q has no outbound edges and will not contribute to any output", src.ID),
			})
		}
	}
}

// computeBufferHints assigns per-edge buffer sizes based on the kinds of the
// upstream and downstream nodes. The heuristics account for common patterns:
//
//   - Sources produce bursts (e.g., B-frame reordering), so edges leaving a
//     source get a larger buffer.
//   - Encoders are typically slower than filters, so the edge feeding an
//     encoder gets a larger buffer to absorb speed differences.
//   - Sinks do mostly I/O, which is fast, so encoder→sink edges use a smaller
//     buffer.
//   - Filter→filter and GoProcessor edges use the default buffer size.
func computeBufferHints(g *Graph, plan *ExecutionPlan) {
	const defaultBuf = 8

	plan.EdgeBufSizes = make(map[*Edge]int, len(g.Edges))
	for _, e := range g.Edges {
		plan.EdgeBufSizes[e] = edgeBufSize(e.From.Kind, e.To.Kind, defaultBuf)
	}
}

// edgeBufSize returns the recommended buffer size for an edge between two
// node kinds.
func edgeBufSize(from, to NodeKind, defaultBuf int) int {
	switch {
	case from == KindSource:
		return 16 // demux produces bursts (B-frame reordering)
	case to == KindEncoder:
		return 16 // encoder is typically the slowest stage
	case from == KindEncoder && to == KindSink:
		return 4 // packets are small, muxer I/O is fast
	default:
		return defaultBuf
	}
}
