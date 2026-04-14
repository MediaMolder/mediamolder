// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package graph

import (
	"fmt"
	"sort"
	"strings"
)

// PortType identifies the media type flowing through a port.
type PortType string

const (
	PortVideo    PortType = "video"
	PortAudio    PortType = "audio"
	PortSubtitle PortType = "subtitle"
	PortData     PortType = "data"
)

// NodeKind classifies a node in the processing graph.
type NodeKind int

const (
	KindSource  NodeKind = iota // demux + decode
	KindFilter                  // libavfilter
	KindEncoder                 // encode
	KindSink                    // mux
)

func (k NodeKind) String() string {
	switch k {
	case KindSource:
		return "source"
	case KindFilter:
		return "filter"
	case KindEncoder:
		return "encoder"
	case KindSink:
		return "sink"
	default:
		return fmt.Sprintf("NodeKind(%d)", int(k))
	}
}

// ---------- Definition types (input to Build) ----------

// Def is the declarative description used to construct a Graph.
// Callers populate it from pipeline.Config or build it programmatically.
type Def struct {
	Inputs  []InputDef
	Nodes   []NodeDef
	Outputs []OutputDef
	Edges   []EdgeDef
}

// InputDef describes a source input.
type InputDef struct {
	ID string
}

// NodeDef describes a processing node in the graph.
type NodeDef struct {
	ID     string
	Type   string         // "filter", "encoder", "source", "sink"
	Filter string         // filter name (for filter nodes)
	Params map[string]any // filter/encoder parameters
}

// OutputDef describes an output sink.
type OutputDef struct {
	ID string
}

// EdgeDef describes a directed edge between two endpoints.
type EdgeDef struct {
	From string // "nodeID", "nodeID:port", or "nodeID:type:track"
	To   string
	Type string // "video", "audio", "subtitle", "data"
}

// ---------- Resolved graph types ----------

// Node is a vertex in the resolved processing graph.
type Node struct {
	ID     string
	Kind   NodeKind
	Filter string         // filter name (KindFilter only)
	Params map[string]any // filter/encoder parameters

	Inbound  []*Edge
	Outbound []*Edge
}

// Edge is a directed connection between two nodes.
type Edge struct {
	From     *Node
	FromPort string // port key on source node (e.g. "v:0", "default")
	To       *Node
	ToPort   string // port key on destination node
	Type     PortType
}

// Graph is a validated directed acyclic graph ready for scheduling.
type Graph struct {
	Nodes   map[string]*Node
	Edges   []*Edge
	Order   []*Node // topological order (sources first)
	Sources []*Node // nodes with Kind == KindSource
	Sinks   []*Node // nodes with Kind == KindSink
}

// Build constructs and validates a Graph from a Def.
// It creates source/sink nodes from Inputs/Outputs, processing nodes from
// Nodes, resolves all edges, validates type compatibility, and performs
// cycle detection via topological sort.
func Build(def *Def) (*Graph, error) {
	g := &Graph{
		Nodes: make(map[string]*Node),
	}

	// 1. Create source nodes from inputs.
	for _, inp := range def.Inputs {
		if inp.ID == "" {
			return nil, fmt.Errorf("input missing id")
		}
		if err := g.addNode(inp.ID, KindSource, "", nil); err != nil {
			return nil, err
		}
	}

	// 2. Create processing nodes.
	for _, nd := range def.Nodes {
		kind, err := parseNodeKind(nd.Type)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", nd.ID, err)
		}
		if nd.ID == "" {
			return nil, fmt.Errorf("graph node missing id")
		}
		if err := g.addNode(nd.ID, kind, nd.Filter, nd.Params); err != nil {
			return nil, err
		}
	}

	// 3. Create sink nodes from outputs.
	for _, out := range def.Outputs {
		if out.ID == "" {
			return nil, fmt.Errorf("output missing id")
		}
		if err := g.addNode(out.ID, KindSink, "", nil); err != nil {
			return nil, err
		}
	}

	// 4. Resolve and validate edges.
	for i, ed := range def.Edges {
		if err := g.addEdge(i, ed); err != nil {
			return nil, err
		}
	}

	// 5. Classify sources and sinks from node kinds.
	for _, n := range g.Nodes {
		switch n.Kind {
		case KindSource:
			g.Sources = append(g.Sources, n)
		case KindSink:
			g.Sinks = append(g.Sinks, n)
		}
	}
	sortNodes(g.Sources)
	sortNodes(g.Sinks)

	// 6. Topological sort with cycle detection.
	order, err := g.topoSort()
	if err != nil {
		return nil, err
	}
	g.Order = order

	return g, nil
}

// NodeByID returns the node with the given ID, or nil.
func (g *Graph) NodeByID(id string) *Node {
	return g.Nodes[id]
}

// Predecessors returns all nodes that feed into the given node.
func (n *Node) Predecessors() []*Node {
	seen := make(map[string]bool, len(n.Inbound))
	var out []*Node
	for _, e := range n.Inbound {
		if !seen[e.From.ID] {
			seen[e.From.ID] = true
			out = append(out, e.From)
		}
	}
	return out
}

// Successors returns all nodes that this node feeds into.
func (n *Node) Successors() []*Node {
	seen := make(map[string]bool, len(n.Outbound))
	var out []*Node
	for _, e := range n.Outbound {
		if !seen[e.To.ID] {
			seen[e.To.ID] = true
			out = append(out, e.To)
		}
	}
	return out
}

// ---------- internal helpers ----------

func (g *Graph) addNode(id string, kind NodeKind, filter string, params map[string]any) error {
	if _, exists := g.Nodes[id]; exists {
		return fmt.Errorf("duplicate node id %q", id)
	}
	g.Nodes[id] = &Node{
		ID:     id,
		Kind:   kind,
		Filter: filter,
		Params: params,
	}
	return nil
}

func (g *Graph) addEdge(index int, ed EdgeDef) error {
	fromID, fromPort, err := parseRef(ed.From)
	if err != nil {
		return fmt.Errorf("edge[%d] from: %w", index, err)
	}
	toID, toPort, err := parseRef(ed.To)
	if err != nil {
		return fmt.Errorf("edge[%d] to: %w", index, err)
	}

	fromNode, ok := g.Nodes[fromID]
	if !ok {
		return fmt.Errorf("edge[%d]: unknown source node %q", index, fromID)
	}
	toNode, ok := g.Nodes[toID]
	if !ok {
		return fmt.Errorf("edge[%d]: unknown destination node %q", index, toID)
	}

	if fromID == toID {
		return fmt.Errorf("edge[%d]: self-loop on node %q", index, fromID)
	}

	pt := PortType(ed.Type)

	edge := &Edge{
		From:     fromNode,
		FromPort: fromPort,
		To:       toNode,
		ToPort:   toPort,
		Type:     pt,
	}
	fromNode.Outbound = append(fromNode.Outbound, edge)
	toNode.Inbound = append(toNode.Inbound, edge)
	g.Edges = append(g.Edges, edge)
	return nil
}

// parseRef parses an edge endpoint reference string.
//
// Formats:
//
//	"nodeID"            → (nodeID, "default")
//	"nodeID:port"       → (nodeID, "port")
//	"nodeID:type:track" → (nodeID, "type:track")  e.g. "main:v:0"
func parseRef(ref string) (nodeID, portKey string, err error) {
	if ref == "" {
		return "", "", fmt.Errorf("empty reference")
	}
	parts := strings.SplitN(ref, ":", 3)
	switch len(parts) {
	case 1:
		return parts[0], "default", nil
	case 2:
		if parts[1] == "" {
			return "", "", fmt.Errorf("invalid reference %q: empty port", ref)
		}
		return parts[0], parts[1], nil
	case 3:
		if parts[1] == "" || parts[2] == "" {
			return "", "", fmt.Errorf("invalid reference %q: empty segment", ref)
		}
		return parts[0], parts[1] + ":" + parts[2], nil
	default:
		return "", "", fmt.Errorf("invalid reference %q", ref)
	}
}

func parseNodeKind(s string) (NodeKind, error) {
	switch s {
	case "filter":
		return KindFilter, nil
	case "encoder":
		return KindEncoder, nil
	case "source":
		return KindSource, nil
	case "sink":
		return KindSink, nil
	default:
		return 0, fmt.Errorf("unknown node type %q", s)
	}
}

// topoSort returns a topological ordering of the graph using Kahn's algorithm.
// Returns an error if the graph contains a cycle.
func (g *Graph) topoSort() ([]*Node, error) {
	inDegree := make(map[string]int, len(g.Nodes))
	for id := range g.Nodes {
		inDegree[id] = 0
	}
	for _, e := range g.Edges {
		inDegree[e.To.ID]++
	}

	// Seed with zero-indegree nodes.
	var queue []*Node
	for id, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, g.Nodes[id])
		}
	}
	sortNodes(queue)

	var order []*Node
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		order = append(order, n)

		var ready []*Node
		for _, e := range n.Outbound {
			inDegree[e.To.ID]--
			if inDegree[e.To.ID] == 0 {
				ready = append(ready, e.To)
			}
		}
		sortNodes(ready)
		queue = append(queue, ready...)
	}

	if len(order) != len(g.Nodes) {
		var cycleNodes []string
		for id, deg := range inDegree {
			if deg > 0 {
				cycleNodes = append(cycleNodes, id)
			}
		}
		sort.Strings(cycleNodes)
		return nil, fmt.Errorf("graph contains a cycle involving nodes: %s",
			strings.Join(cycleNodes, ", "))
	}

	return order, nil
}

func sortNodes(nodes []*Node) {
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].ID < nodes[j].ID
	})
}
