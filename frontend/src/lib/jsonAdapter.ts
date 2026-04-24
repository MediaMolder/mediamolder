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
}

/**
 * Placeholder shown in the canvas sublabel of an input/output node
 * when the user has not yet chosen a file URL. Visually distinct so
 * it's obvious the node is incomplete and the user needs to act.
 */
export const EMPTY_URL_PLACEHOLDER = '‹choose file›';

/** Render `url` for the canvas sublabel, falling back to a placeholder when blank. */
export function displayUrl(url: string | undefined): string {
  return url && url.trim() ? url : EMPTY_URL_PLACEHOLDER;
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

/**
 * Default codec name used when the user has wired an input directly
 * to an output for the given stream type and the output declares no
 * `codec_*` of its own. Mirrors the runtime defaults applied by
 * `pipeline.expandImplicitEncoders`.
 */
function defaultEncoderCodec(type: StreamType): string {
  switch (type) {
    case 'video':
      return 'libx264';
    case 'audio':
      return 'aac';
    case 'subtitle':
      return 'mov_text';
    default:
      return '';
  }
}

/**
 * Materialise implicit encoders into real `graph.nodes[]` entries so
 * the GUI can display them as editable nodes (rate-control, presets,
 * bitrate, etc) rather than read-only ghosts. Mirrors the runtime
 * pass `pipeline.expandImplicitEncoders`.
 *
 * Rule: for every edge whose source is an input id and target is an
 * output id, insert a new encoder node between them. The encoder's
 * `codec` param comes from the output's `codec_video` / `codec_audio`
 * if non-empty, otherwise the type-specific default
 * (libx264 / aac / mov_text).
 *
 * Returns a new JobConfig — the caller-supplied object is not
 * mutated. Edges are rewritten so the original source→sink wire now
 * goes source→encoder→sink. Synthetic encoder ids use the
 * `auto_enc_<output>_<type>_<n>` pattern so they are clearly
 * user-visible (and editable like any other encoder node).
 */
export function materializeImplicitEncoders(cfg: JobConfig): JobConfig {
  const inputIds = new Set(cfg.inputs.map((i) => i.id));
  const outputIds = new Set(cfg.outputs.map((o) => o.id));
  const outputById = new Map(cfg.outputs.map((o) => [o.id, o]));

  const nodes: NodeDef[] = [...cfg.graph.nodes];
  const edges: EdgeDef[] = [];
  const usedIds = new Set(nodes.map((n) => n.id));

  let inserted = 0;
  for (const e of cfg.graph.edges) {
    const fromHead = endpointHead(e.from);
    const toHead = endpointHead(e.to);
    if (!inputIds.has(fromHead) || !outputIds.has(toHead)) {
      edges.push(e);
      continue;
    }
    const out = outputById.get(toHead)!;
    const declared =
      e.type === 'video' ? out.codec_video
      : e.type === 'audio' ? out.codec_audio
      : e.type === 'subtitle' ? out.codec_subtitle
      : '';
    const codec = (declared && declared.length > 0) ? declared : defaultEncoderCodec(e.type);
    if (!codec) {
      edges.push(e);
      continue;
    }

    let encId = `auto_enc_${toHead}_${e.type}`;
    let suffix = 1;
    while (usedIds.has(encId)) {
      encId = `auto_enc_${toHead}_${e.type}_${suffix++}`;
    }
    usedIds.add(encId);

    nodes.push({
      id: encId,
      type: 'encoder',
      params: { codec },
    });
    edges.push({ from: e.from, to: encId, type: e.type });
    edges.push({ from: encId, to: e.to, type: e.type });
    inserted++;
  }

  if (inserted === 0) return cfg;

  return {
    ...cfg,
    graph: {
      ...cfg.graph,
      nodes,
      edges,
    },
  };
}

/**
 * Pick the bold heading shown on a graph NodeDef in the canvas. For
 * encoders, filters and go_processors the user-meaningful identity is
 * the codec / filter / processor name (e.g. "libx264", "scale",
 * "yolov8"), not the synthetic node id (e.g. "auto_enc_out0_video").
 * Falls back to the node id when no specialised name is set.
 */
export function nodeDisplayLabel(n: NodeDef): string {
  if (n.type === 'encoder') {
    const codec = (n.params?.codec as string | undefined)?.trim();
    if (codec) return codec;
  }
  if (n.type === 'filter' && n.filter) return n.filter;
  if (n.type === 'go_processor' && n.processor) return n.processor;
  return n.id;
}

/** Secondary line shown beneath the heading — typically the node id. */
export function nodeDisplaySublabel(n: NodeDef): string {
  return n.id;
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
        sublabel: displayUrl(inp.url),
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
        label: nodeDisplayLabel(n),
        sublabel: nodeDisplaySublabel(n),
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
        sublabel: displayUrl(out.url),
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

/**
 * Pure ghost-expansion pass. Returns a new {nodes, edges} pair with
 * the implicit demuxer / per-stream decoder / encoder / muxer stages
 * spliced in between every input→… and …→output edge. The originating
 * user-facing edges are returned `hidden` so React Flow doesn't draw
 * them, but they remain in the array so flowToConfig (which only
 * strips items flagged `data.implicit`) round-trips losslessly.
 *
 * Inputs/outputs are identified by `node.data.ref.kind === 'input' |
 * 'output'`; their JobConfig defs are read from `node.data.ref.def`,
 * so this function is free of any reference to the original
 * JobConfig and can be re-run on every state change.
 */
export function expandImplicitNodes(
  inNodes: FlowNode[],
  inEdges: FlowEdge[],
): { nodes: FlowNode[]; edges: FlowEdge[] } {
  const nodes: FlowNode[] = [...inNodes];
  const edges: FlowEdge[] = inEdges.map((e) => ({ ...e }));

  const inputById = new Map<string, JobConfig['inputs'][number]>();
  const outputById = new Map<string, JobConfig['outputs'][number]>();
  for (const n of inNodes) {
    if (n.data.ref.kind === 'input') inputById.set(n.data.ref.def.id, n.data.ref.def);
    else if (n.data.ref.kind === 'output') outputById.set(n.data.ref.def.id, n.data.ref.def);
  }
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
      position: { x: 0, y: 0 },
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

    // ---- Sink side: muxer → output ----
    // Encoders are no longer ghosts — they are materialised into the
    // graph as real editable nodes by `materializeImplicitEncoders` at
    // load time, so the only sink-side ghost left is the muxer.
    if (outputDef) {
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

  // Position freshly added ghosts at the centroid of their non-ghost
  // neighbours. Real nodes keep whatever positions they already had.
  if (addedNodeIds.size > 0) {
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

  return { nodes, edges };
}
