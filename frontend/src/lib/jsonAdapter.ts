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
import type { EdgeDef, JobConfig, NodeDef, StreamType, UIPosition } from './jobTypes';

export const INPUT_PREFIX = '__in__';
export const OUTPUT_PREFIX = '__out__';

const STREAM_LETTER: Record<StreamType, string> = {
  video: 'v',
  audio: 'a',
  subtitle: 's',
  data: 'd',
};

const LETTER_STREAM: Record<string, StreamType> = {
  v: 'video',
  a: 'audio',
  s: 'subtitle',
  d: 'data',
};

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
  /** When true (default), apply column auto-layout if no positions are stored. */
  autoLayout?: boolean;
}

/** Get the React Flow node id for a job-side input/output/node id. */
export function flowIdFor(kind: 'input' | 'output' | 'node', id: string): string {
  if (kind === 'input') return INPUT_PREFIX + id;
  if (kind === 'output') return OUTPUT_PREFIX + id;
  return id;
}

/** Reverse of flowIdFor: split the React Flow node id into kind + raw id. */
export function jobIdFromFlow(flowId: string): { kind: 'input' | 'output' | 'node'; id: string } {
  if (flowId.startsWith(INPUT_PREFIX)) return { kind: 'input', id: flowId.slice(INPUT_PREFIX.length) };
  if (flowId.startsWith(OUTPUT_PREFIX)) return { kind: 'output', id: flowId.slice(OUTPUT_PREFIX.length) };
  return { kind: 'node', id: flowId };
}

/** Resolve the head id of an edge endpoint string ("scale0:0" -> "scale0"). */
function endpointHead(endpoint: string): string {
  return endpoint.split(':')[0];
}

/** Convert a JobConfig to React Flow nodes + edges. */
export function configToFlow(cfg: JobConfig, opts: ConvertOptions = {}): {
  nodes: FlowNode[];
  edges: FlowEdge[];
} {
  const { autoLayout = true } = opts;
  const positions = cfg.graph.ui?.positions ?? {};

  const inputIds = new Set(cfg.inputs.map((i) => i.id));
  const outputIds = new Set(cfg.outputs.map((o) => o.id));

  const nodes: FlowNode[] = [];

  cfg.inputs.forEach((inp) => {
    const id = INPUT_PREFIX + inp.id;
    nodes.push({
      id,
      type: 'mmNode',
      position: positions[id] ?? { x: 0, y: 0 },
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
      position: positions[n.id] ?? { x: 0, y: 0 },
      data: {
        kind: n.type,
        label: n.id,
        sublabel: n.filter || n.processor || n.type,
        ref: { kind: 'node', def: n },
      },
    });
  });

  cfg.outputs.forEach((out) => {
    const id = OUTPUT_PREFIX + out.id;
    nodes.push({
      id,
      type: 'mmNode',
      position: positions[id] ?? { x: 0, y: 0 },
      data: {
        kind: 'output',
        label: out.id,
        sublabel: out.url,
        ref: { kind: 'output', def: out },
      },
    });
  });

  const edges: FlowEdge[] = cfg.graph.edges.map((e, idx) => {
    const fromHead = endpointHead(e.from);
    const toHead = endpointHead(e.to);
    const sourceId = inputIds.has(fromHead) ? INPUT_PREFIX + fromHead : fromHead;
    const targetId = outputIds.has(toHead) ? OUTPUT_PREFIX + toHead : toHead;
    return {
      id: `e${idx}-${sourceId}-${targetId}-${e.type}`,
      source: sourceId,
      target: targetId,
      sourceHandle: e.type,
      targetHandle: e.type,
      className: `edge-${e.type}`,
      data: {
        streamType: e.type,
        rawFrom: e.from,
        rawTo: e.to,
      },
    };
  });

  // Apply layout fallback only if no positions were stored.
  const hasAnyPosition = Object.keys(positions).length > 0;
  if (autoLayout && !hasAnyPosition) {
    layoutByColumn(nodes, edges);
  }

  return { nodes, edges };
}

/** Convert React Flow nodes + edges back to a JobConfig (preserving original refs and positions). */
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
  const positions: Record<string, UIPosition> = {};

  for (const n of nodes) {
    positions[n.id] = { x: Math.round(n.position.x), y: Math.round(n.position.y) };
    const ref = n.data.ref;
    if (ref.kind === 'input') inputs.push(ref.def);
    else if (ref.kind === 'output') outputs.push(ref.def);
    else graphNodes.push(ref.def);
  }

  const graphEdges: EdgeDef[] = edges.map((e) => ({
    from: e.data?.rawFrom || deriveEndpoint(nodes, e.source, e.sourceHandle, 'source'),
    to: e.data?.rawTo || deriveEndpoint(nodes, e.target, e.targetHandle, 'target'),
    type: (e.data?.streamType ?? (e.sourceHandle as StreamType) ?? 'video') as StreamType,
  }));

  return {
    schema_version: baseSchemaVersion,
    description,
    inputs,
    graph: { nodes: graphNodes, edges: graphEdges, ui: { positions } },
    outputs,
    global_options: globalOptions,
  };
}

/**
 * Build a graph endpoint string for an edge that was created in the editor (no
 * pre-existing rawFrom/rawTo). Inputs synthesise "<id>:<v|a|s|d>:0",
 * outputs synthesise "<id>:<v|a|s|d>", regular nodes use the bare id.
 */
function deriveEndpoint(
  nodes: FlowNode[],
  flowId: string | null | undefined,
  handle: string | null | undefined,
  side: 'source' | 'target',
): string {
  if (!flowId) return '';
  const node = nodes.find((n) => n.id === flowId);
  const stream = (handle as StreamType) || 'video';
  const letter = STREAM_LETTER[stream] ?? 'v';
  if (node?.data.kind === 'input' && side === 'source') {
    return `${node.data.label}:${letter}:0`;
  }
  if (node?.data.kind === 'output' && side === 'target') {
    return `${node.data.label}:${letter}`;
  }
  return node?.data.label ?? flowId;
}

/** Parse the stream type from a raw endpoint, if present. */
export function streamTypeFromEndpoint(ep: string): StreamType | null {
  const parts = ep.split(':');
  if (parts.length >= 2 && LETTER_STREAM[parts[1]]) return LETTER_STREAM[parts[1]];
  return null;
}

/**
 * Naive column-based fallback layout used when a job loads with no UI
 * positions. The toolbar's "Auto layout" action uses dagre instead.
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
  let maxCol = 0;
  col.forEach((v) => (maxCol = Math.max(maxCol, v)));
  nodes.forEach((n) => {
    if (n.data.kind === 'input') col.set(n.id, 0);
    if (n.data.kind === 'output') col.set(n.id, maxCol + 1);
  });
  maxCol = Math.max(maxCol, ...Array.from(col.values()));

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
