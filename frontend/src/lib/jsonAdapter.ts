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
import type { EdgeDef, JobConfig, NodeDef, ProbedStream, StreamType, UIPosition } from './jobTypes';

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
  /**
   * Probed stream metadata (input nodes only). Populated by clicking
   * "Get properties" in the Inspector, which calls `POST /api/probe`.
   * Editor-only — never serialised back into the JobConfig.
   */
  probed?: ProbedStream[];
  /**
   * Media types this node supports on its handles (e.g. ["video"] for a
   * video-only encoder, ["audio"] for an audio filter). Drives which
   * pins MMNode renders. Editor-only — populated from the catalog when
   * the node is spawned or when JSON is loaded. Empty/undefined ⇒
   * media-type-agnostic (render all four pins; used for inputs,
   * outputs, dynamic-pad filters, and unknown go_processors).
   */
  streams?: string[];
  /**
   * Set on synthetic "ghost" nodes inserted by the GUI to visualise the
   * implicit demuxer / decoder / encoder / muxer stages that the runtime
   * actually instantiates but that have no representation in the
   * JobConfig (see expandImplicitNodes). Ghost nodes are dropped on
   * export and rendered read-only in the Inspector.
   */
  implicit?: boolean;
}

export interface FlowEdgeData extends Record<string, unknown> {
  streamType: StreamType;
  rawFrom: string;
  rawTo: string;
  /** Set on synthetic ghost-chain edges. Dropped on export. */
  implicit?: boolean;
}

export type FlowNode = Node<FlowNodeData>;
export type FlowEdge = Edge<FlowEdgeData>;

interface ConvertOptions {
  /** When true (default), apply column auto-layout if no positions are stored. */
  autoLayout?: boolean;
  /**
   * When true (default), insert read-only "ghost" nodes for the implicit
   * demuxer / decoder / encoder / muxer stages that the runtime actually
   * instantiates. The originating user-facing edges are kept but marked
   * `hidden` so React Flow doesn't render them; ghost nodes/edges are
   * stripped on export.
   */
  expandImplicit?: boolean;
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
  const { autoLayout = true, expandImplicit = true } = opts;
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
      type: 'mmEdge',
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

  if (expandImplicit) {
    expandImplicitNodes(cfg, nodes, edges, positions);
    if (autoLayout && !hasAnyPosition) {
      // Re-layout so the ghost chain is positioned alongside the
      // originals; skip edges we just hid so they don't short-circuit
      // the column ordering.
      layoutByColumn(nodes, edges.filter((e) => !e.hidden));
    }
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
  // Drop ghost nodes / ghost edges synthesised by expandImplicitNodes —
  // they have no place in the persisted JobConfig. The originating
  // user-facing edges remain in `edges` (marked hidden) so the round-trip
  // preserves the user's wiring.
  nodes = nodes.filter((n) => !n.data.implicit);
  edges = edges.filter((e) => !e.data?.implicit);

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

/* ------------------------------------------------------------------ *
 * Implicit-stage ("ghost") visualisation
 *
 * The runtime fuses demuxing + decoding into KindSource and muxing +
 * writing into KindSink, and lazily inserts encoders for direct
 * source→sink edges (see pipeline/handlers.go expandImplicitEncoders).
 * The JobConfig therefore omits these stages entirely. To give the user
 * a faithful picture of what actually runs, we splice synthetic ghost
 * nodes into the React Flow graph between each input and the first
 * "real" downstream node, and between each output and the last real
 * upstream node. Ghost nodes are flagged `data.implicit = true`,
 * rendered read-only, dropped on export, and the originating edges are
 * hidden (not deleted) so the persisted graph round-trips losslessly.
 * ------------------------------------------------------------------ */

const GHOST_DEMUX_PREFIX = '__ghost__demux__';
const GHOST_DECODE_PREFIX = '__ghost__decode__';
const GHOST_ENCODE_PREFIX = '__ghost__encode__';
const GHOST_MUX_PREFIX = '__ghost__mux__';

/** Best-effort container guess from a URL/file extension. Returns "" if unknown. */
function guessContainer(url: string): string {
  const m = url.match(/\.([a-zA-Z0-9]+)(?:\?.*)?$/);
  if (!m) return '';
  return m[1].toLowerCase();
}

/** Best-effort decoder name for an input stream from probed metadata. */
function decoderNameFor(
  probed: ProbedStream[] | undefined,
  type: StreamType,
  track: number,
): string {
  if (!probed) return type;
  const ofType = probed.filter((s) => s.type === type);
  const stream = ofType[track] ?? ofType[0];
  return stream?.codec || type;
}

function expandImplicitNodes(
  cfg: JobConfig,
  nodes: FlowNode[],
  edges: FlowEdge[],
  positions: Record<string, UIPosition>,
): void {
  const inputById = new Map(cfg.inputs.map((i) => [i.id, i]));
  const outputById = new Map(cfg.outputs.map((o) => [o.id, o]));
  // Index the original nodes (before we start mutating) so ghost lookup
  // ignores any ghost we just added in a previous iteration.
  const nodeById = new Map(nodes.map((n) => [n.id, n]));
  const probedByInputFlowId = new Map<string, ProbedStream[] | undefined>();
  for (const n of nodes) {
    if (n.data.kind === 'input') probedByInputFlowId.set(n.id, n.data.probed);
  }

  const addedNodeIds = new Set<string>();
  const ensureNode = (
    id: string,
    kind: string,
    label: string,
    sublabel: string,
    streams: StreamType[],
  ): FlowNode => {
    const existing = nodeById.get(id);
    if (existing) return existing;
    const ghost: FlowNode = {
      id,
      type: 'mmNode',
      position: positions[id] ?? { x: 0, y: 0 },
      data: {
        kind,
        label,
        sublabel,
        ref: { kind: 'input', def: { id, url: '', streams: [] } }, // placeholder; never serialised
        streams,
        implicit: true,
      },
    };
    nodes.push(ghost);
    nodeById.set(id, ghost);
    addedNodeIds.add(id);
    return ghost;
  };

  let ghostEdgeSeq = 0;
  const addEdge = (
    source: string,
    target: string,
    type: StreamType,
    rawFrom: string,
    rawTo: string,
  ): void => {
    edges.push({
      id: `g${ghostEdgeSeq++}-${source}-${target}-${type}`,
      type: 'mmEdge',
      source,
      target,
      sourceHandle: type,
      targetHandle: type,
      className: `edge-${type} edge-implicit`,
      data: { streamType: type, rawFrom, rawTo, implicit: true },
    });
  };

  // Walk a copy of the original edges; we will add new ghost edges and
  // hide the originals as we go.
  const original = [...edges];
  for (const e of original) {
    if (e.data?.implicit) continue;
    const fromHead = endpointHead(e.data?.rawFrom ?? '');
    const toHead = endpointHead(e.data?.rawTo ?? '');
    const inputDef = inputById.get(fromHead);
    const outputDef = outputById.get(toHead);
    const sourceNode = nodeById.get(e.source);
    const targetNode = nodeById.get(e.target);
    if (!sourceNode || !targetNode) continue;
    const type = (e.data?.streamType ?? 'video') as StreamType;

    let chainHead = e.source;
    let chainHeadRaw = e.data?.rawFrom ?? '';
    let chainTail = e.target;
    let chainTailRaw = e.data?.rawTo ?? '';
    let mutated = false;

    // ---- Source side: input → demuxer → decoder ----
    if (inputDef) {
      const demuxId = GHOST_DEMUX_PREFIX + inputDef.id;
      const container = guessContainer(inputDef.url) || 'demuxer';
      ensureNode(demuxId, 'demuxer', 'demuxer', container, []);
      // input → demuxer (one edge per input/type pair so the typed
      // pipe colour reads sensibly; demuxers are media-agnostic on
      // both sides via empty `streams`).
      const demuxEdgeId = `${demuxId}__from__${e.source}__${type}`;
      if (!edges.some((x) => x.id === demuxEdgeId)) {
        edges.push({
          id: demuxEdgeId,
          type: 'mmEdge',
          source: e.source,
          target: demuxId,
          sourceHandle: type,
          targetHandle: type,
          className: `edge-${type} edge-implicit`,
          data: {
            streamType: type,
            rawFrom: chainHeadRaw,
            rawTo: '',
            implicit: true,
          },
        });
      }

      // Per-stream decoder. Endpoint format: "<input>:<letter>:<track>".
      const parts = (e.data?.rawFrom ?? '').split(':');
      const track = parts.length >= 3 ? parts[2] : '0';
      const decId = `${GHOST_DECODE_PREFIX}${inputDef.id}__${type}__${track}`;
      const decLabel = decoderNameFor(
        probedByInputFlowId.get(e.source),
        type,
        Number(track) || 0,
      );
      ensureNode(decId, 'decoder', decLabel, `${type} decoder`, [type]);
      addEdge(demuxId, decId, type, '', '');
      chainHead = decId;
      chainHeadRaw = '';
      mutated = true;
    }

    // ---- Sink side: encoder → muxer → output ----
    if (outputDef) {
      // Choose codec from output's per-type field. If empty, the
      // runtime's expandImplicitEncoders will skip insertion, so we
      // skip the ghost too.
      const codec =
        type === 'video'
          ? outputDef.codec_video
          : type === 'audio'
            ? outputDef.codec_audio
            : type === 'subtitle'
              ? outputDef.codec_subtitle
              : '';

      // Only insert a ghost encoder if the upstream of this edge is
      // not already an encoder (real or ghost) — i.e. the user hasn't
      // wired one explicitly.
      const upstream = nodeById.get(chainHead);
      const upstreamKind = upstream?.data.kind ?? '';
      const needsEncoder =
        !!codec && upstreamKind !== 'encoder' && upstreamKind !== 'demuxer';

      if (needsEncoder) {
        const encId = `${GHOST_ENCODE_PREFIX}${outputDef.id}__${type}`;
        ensureNode(encId, 'encoder', codec!, `${type} encoder`, [type]);
        addEdge(chainHead, encId, type, chainHeadRaw, '');
        chainHead = encId;
        chainHeadRaw = '';
      }

      const muxId = GHOST_MUX_PREFIX + outputDef.id;
      const container = outputDef.format || guessContainer(outputDef.url) || 'muxer';
      ensureNode(muxId, 'muxer', 'muxer', container, []);
      addEdge(chainHead, muxId, type, chainHeadRaw, '');
      chainHead = muxId;
      chainHeadRaw = '';
      chainTail = e.target;
      // Final muxer → output edge (one per output/type so colour reads).
      const muxOutId = `${muxId}__to__${e.target}__${type}`;
      if (!edges.some((x) => x.id === muxOutId)) {
        edges.push({
          id: muxOutId,
          type: 'mmEdge',
          source: muxId,
          target: e.target,
          sourceHandle: type,
          targetHandle: type,
          className: `edge-${type} edge-implicit`,
          data: { streamType: type, rawFrom: '', rawTo: chainTailRaw, implicit: true },
        });
      }
      mutated = true;
    } else if (mutated) {
      // Source-side expansion only: connect decoder → original target.
      addEdge(chainHead, chainTail, type, chainHeadRaw, chainTailRaw);
    }

    // Hide the original edge — keep it for round-trip on export.
    if (mutated) {
      e.hidden = true;
    }
  }

  // Re-layout only the freshly added ghost nodes if the user already
  // had stored positions for the real nodes. Quick offset heuristic:
  // place each new node at (avg(neighbors.x), avg(neighbors.y)) +
  // small jitter. The full layout pass in configToFlow handles the
  // empty-positions case.
  if (addedNodeIds.size > 0 && Object.keys(positions).length > 0) {
    const neighbours = new Map<string, string[]>();
    for (const e of edges) {
      if (!neighbours.has(e.source)) neighbours.set(e.source, []);
      if (!neighbours.has(e.target)) neighbours.set(e.target, []);
      neighbours.get(e.source)!.push(e.target);
      neighbours.get(e.target)!.push(e.source);
    }
    for (const id of addedNodeIds) {
      const ghost = nodeById.get(id);
      if (!ghost) continue;
      const nbrs = (neighbours.get(id) ?? [])
        .map((nid) => nodeById.get(nid))
        .filter((n): n is FlowNode => !!n && !addedNodeIds.has(n.id));
      if (nbrs.length === 0) continue;
      const x = nbrs.reduce((s, n) => s + n.position.x, 0) / nbrs.length;
      const y = nbrs.reduce((s, n) => s + n.position.y, 0) / nbrs.length;
      ghost.position = { x, y };
    }
  }
}
