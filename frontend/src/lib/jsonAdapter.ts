// Bidirectional adapter between MediaMolder JobConfig and React Flow nodes/edges.
//
// Job graph references use "node:port" syntax for edges:
//   - Inputs:  "in0:v:0", "in0:a:0" (input id, stream type letter, track index)
//   - Outputs: "out0:v", "out0:a"   (output id, stream type letter)
//   - Nodes:   "scale0", "scale0:0" (node id, optional output port index)
//
// The adapter creates one synthetic React Flow node per Input and per Output so the
// canvas shows the full pipeline including sources/sinks. Internal IDs are namespaced
// to avoid collisions with user-defined node IDs:
//   - input nodes:  "__in__<inputId>"
//   - output nodes: "__out__<outputId>"
//   - graph nodes:  "<nodeId>" (verbatim)

import type { Edge, Node } from '@xyflow/react';
import type { EdgeDef, JobConfig, NodeDef, StreamType } from './jobTypes';

export const INPUT_PREFIX = '__in__';
export const OUTPUT_PREFIX = '__out__';

export interface FlowNodeData extends Record<string, unknown> {
  /** "input" | "output" | original NodeDef.type */
  kind: string;
  label: string;
  sublabel?: string;
  /** Original definition for round-trip (one of input/output/node). */
  ref:
    | { kind: 'input'; def: JobConfig['inputs'][number] }
    | { kind: 'output'; def: JobConfig['outputs'][number] }
    | { kind: 'node'; def: NodeDef };
}

export interface FlowEdgeData extends Record<string, unknown> {
  streamType: StreamType;
  rawFrom: string;
  rawTo: string;
}

export type FlowNode = Node<FlowNodeData>;
export type FlowEdge = Edge<FlowEdgeData>;

interface ConvertOptions {
  /** When true (default), apply a simple deterministic layout if no positions provided. */
  autoLayout?: boolean;
}

/** Resolve the React Flow node ID that an edge endpoint string refers to. */
function endpointNodeId(endpoint: string): string {
  // "in0:v:0" -> input node "__in__in0"
  // "out0:v"  -> output node "__out__out0"
  // "scale0"  -> "scale0"
  // "scale0:0" -> "scale0"
  const head = endpoint.split(':')[0];
  // We can't tell input vs output from the string alone; the caller provides context.
  return head;
}

/** Convert a JobConfig to React Flow nodes + edges. */
export function configToFlow(cfg: JobConfig, opts: ConvertOptions = {}): {
  nodes: FlowNode[];
  edges: FlowEdge[];
} {
  const { autoLayout = true } = opts;

  const inputIds = new Set(cfg.inputs.map((i) => i.id));
  const outputIds = new Set(cfg.outputs.map((o) => o.id));

  const nodes: FlowNode[] = [];

  cfg.inputs.forEach((inp) => {
    nodes.push({
      id: INPUT_PREFIX + inp.id,
      type: 'mmNode',
      position: { x: 0, y: 0 },
      data: {
        kind: 'input',
        label: inp.id,
        sublabel: inp.url,
        ref: { kind: 'input', def: inp },
      },
    });
  });

  cfg.graph.nodes.forEach((n) => {
    nodes.push({
      id: n.id,
      type: 'mmNode',
      position: { x: 0, y: 0 },
      data: {
        kind: n.type,
        label: n.id,
        sublabel: n.filter || n.processor || n.type,
        ref: { kind: 'node', def: n },
      },
    });
  });

  cfg.outputs.forEach((out) => {
    nodes.push({
      id: OUTPUT_PREFIX + out.id,
      type: 'mmNode',
      position: { x: 0, y: 0 },
      data: {
        kind: 'output',
        label: out.id,
        sublabel: out.url,
        ref: { kind: 'output', def: out },
      },
    });
  });

  const edges: FlowEdge[] = cfg.graph.edges.map((e, idx) => {
    const fromHead = endpointNodeId(e.from);
    const toHead = endpointNodeId(e.to);
    const sourceId = inputIds.has(fromHead) ? INPUT_PREFIX + fromHead : fromHead;
    const targetId = outputIds.has(toHead) ? OUTPUT_PREFIX + toHead : toHead;
    return {
      id: `e${idx}`,
      source: sourceId,
      target: targetId,
      sourceHandle: e.type, // handle id encodes stream type
      targetHandle: e.type,
      className: `edge-${e.type}`,
      data: {
        streamType: e.type,
        rawFrom: e.from,
        rawTo: e.to,
      },
    };
  });

  if (autoLayout) {
    layoutByColumn(nodes, edges);
  }

  return { nodes, edges };
}

/** Convert React Flow nodes + edges back to a JobConfig (preserving original refs). */
export function flowToConfig(
  baseSchemaVersion: string,
  nodes: FlowNode[],
  edges: FlowEdge[],
  description?: string,
  globalOptions?: JobConfig['global_options'],
): JobConfig {
  const inputs: JobConfig['inputs'] = [];
  const outputs: JobConfig['outputs'] = [];
  const graphNodes: NodeDef[] = [];

  for (const n of nodes) {
    const ref = n.data.ref;
    if (ref.kind === 'input') inputs.push(ref.def);
    else if (ref.kind === 'output') outputs.push(ref.def);
    else graphNodes.push(ref.def);
  }

  const graphEdges: EdgeDef[] = edges.map((e) => ({
    from: e.data?.rawFrom ?? '',
    to: e.data?.rawTo ?? '',
    type: (e.data?.streamType ?? 'video') as StreamType,
  }));

  return {
    schema_version: baseSchemaVersion,
    description,
    inputs,
    graph: { nodes: graphNodes, edges: graphEdges },
    outputs,
    global_options: globalOptions,
  };
}

/**
 * Naive column-based layout: place sources at left, sinks at right, intermediate
 * nodes in between based on longest-path distance from any source. Good enough as
 * a placeholder until dagre/ELK is wired in (Phase 2).
 */
function layoutByColumn(nodes: FlowNode[], edges: FlowEdge[]): void {
  const COL_W = 220;
  const ROW_H = 90;

  const adj = new Map<string, string[]>();
  const indeg = new Map<string, number>();
  nodes.forEach((n) => {
    adj.set(n.id, []);
    indeg.set(n.id, 0);
  });
  edges.forEach((e) => {
    adj.get(e.source)?.push(e.target);
    indeg.set(e.target, (indeg.get(e.target) ?? 0) + 1);
  });

  // Kahn-style BFS to assign column = longest path from any source.
  const col = new Map<string, number>();
  const queue: string[] = [];
  nodes.forEach((n) => {
    if ((indeg.get(n.id) ?? 0) === 0) {
      col.set(n.id, 0);
      queue.push(n.id);
    }
  });
  while (queue.length) {
    const id = queue.shift()!;
    const c = col.get(id) ?? 0;
    for (const next of adj.get(id) ?? []) {
      col.set(next, Math.max(col.get(next) ?? 0, c + 1));
      const d = (indeg.get(next) ?? 0) - 1;
      indeg.set(next, d);
      if (d === 0) queue.push(next);
    }
  }
  // Force inputs to col 0 and outputs to max col (visual clarity).
  let maxCol = 0;
  col.forEach((v) => (maxCol = Math.max(maxCol, v)));
  nodes.forEach((n) => {
    if (n.data.kind === 'input') col.set(n.id, 0);
    if (n.data.kind === 'output') col.set(n.id, maxCol + 1);
  });
  maxCol = Math.max(maxCol, ...Array.from(col.values()));

  // Distribute rows within each column.
  const byCol = new Map<number, string[]>();
  nodes.forEach((n) => {
    const c = col.get(n.id) ?? 0;
    if (!byCol.has(c)) byCol.set(c, []);
    byCol.get(c)!.push(n.id);
  });
  byCol.forEach((ids, c) => {
    ids.forEach((id, i) => {
      const node = nodes.find((n) => n.id === id);
      if (node) node.position = { x: c * COL_W, y: i * ROW_H };
    });
  });
}
