// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package graph

// ExecutionPlan is the output of the Compile step. It takes a validated Graph
// and produces a plan that groups nodes into concurrent stages, detects
// unreachable subgraphs, and collects warnings about potential issues.
//
// The pipeline currently runs one goroutine per node regardless of stage
// grouping. The stage information is exposed so that future schedulers,
// profilers, and dry-run tools can reason about the graph's structure without
// re-analyzing it.
type ExecutionPlan struct {
	// Graph is the validated graph this plan was compiled from.
	Graph *Graph

	// Stages groups nodes by topological depth. Nodes within the same stage
	// have no data dependencies on each other and can run concurrently.
	// Stage 0 contains all source nodes (no inbound edges), stage 1 contains
	// nodes whose inputs all come from stage 0, and so on.
	Stages []Stage

	// Warnings are non-fatal issues detected during compilation, such as
	// nodes that are unreachable from any sink (dead branches).
	Warnings []Warning

	// EdgeBufSizes maps each edge to a recommended channel buffer size.
	// The scheduler uses these hints instead of a uniform buffer size.
	// Sizes are determined by static heuristics based on node kinds.
	EdgeBufSizes map[*Edge]int
}

// Stage is a set of nodes at the same topological depth. All nodes in a stage
// can execute concurrently because none depends on another within the same
// stage.
type Stage struct {
	// Depth is the zero-based topological depth of this stage.
	// Sources are at depth 0.
	Depth int

	// Nodes are the graph nodes at this depth, sorted by ID for determinism.
	Nodes []*Node
}

// Warning describes a non-fatal issue found during graph compilation.
type Warning struct {
	// NodeID identifies the node the warning relates to, if any.
	NodeID string

	// Code is a machine-readable warning category.
	Code WarningCode

	// Message is a human-readable description of the issue.
	Message string
}

// WarningCode classifies compilation warnings.
type WarningCode string

const (
	// WarnDeadNode means a node's output is never consumed by any sink.
	// The node will run but its results are discarded.
	WarnDeadNode WarningCode = "dead_node"

	// WarnDisconnectedSource means a source node has no outbound edges.
	// It will be started by the scheduler but produce no useful output.
	WarnDisconnectedSource WarningCode = "disconnected_source"
)
