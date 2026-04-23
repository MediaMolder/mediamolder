// Infer technical stream attributes (pix_fmt, width, sample_rate, ...) for an
// edge by walking upstream from the edge's source node.
//
// Each node's params can establish attribute values for the streams it
// produces. When the immediate upstream node does not constrain an attribute,
// we recursively look further upstream until we either find one or run out of
// upstream nodes. Attributes therefore propagate through pass-through chains
// (e.g. setpts, drawtext) until something explicitly overrides them.
//
// The result is a list of `{ key, value, source }` entries that the custom
// edge component renders as a chip on each connection.

import type { FlowEdge, FlowNode } from './jsonAdapter';
import type { Input, NodeDef, Output, StreamType } from './jobTypes';

export interface EdgeAttribute {
  /** Canonical key, e.g. "pix_fmt", "width", "sample_rate". */
  key: string;
  /** Human-readable value. */
  value: string;
  /** Label of the node that established this value (for tooltips). */
  source: string;
}

/** Canonical attribute keys we know how to display, in preferred display order. */
const VIDEO_KEYS = [
  'width', 'height', 'pix_fmt', 'frame_rate',
  'color_space', 'color_range', 'sar', 'codec', 'bit_rate',
] as const;
const AUDIO_KEYS = [
  'sample_rate', 'channels', 'channel_layout', 'sample_fmt',
  'codec', 'bit_rate',
] as const;
const SUBTITLE_KEYS = ['codec'] as const;
const DATA_KEYS = ['codec'] as const;

function keysFor(type: StreamType): readonly string[] {
  switch (type) {
    case 'video': return VIDEO_KEYS;
    case 'audio': return AUDIO_KEYS;
    case 'subtitle': return SUBTITLE_KEYS;
    case 'data': return DATA_KEYS;
  }
}

/** Friendly label for a key (used in chips/tooltips). */
export function attrLabel(key: string): string {
  switch (key) {
    case 'pix_fmt': return 'pix';
    case 'sample_rate': return 'sr';
    case 'sample_fmt': return 'fmt';
    case 'channel_layout': return 'layout';
    case 'frame_rate': return 'fps';
    case 'bit_rate': return 'br';
    case 'color_space': return 'colorspace';
    case 'color_range': return 'range';
    default: return key;
  }
}

function asString(v: unknown): string | undefined {
  if (v === undefined || v === null) return undefined;
  if (typeof v === 'string') return v.trim() || undefined;
  if (typeof v === 'number' || typeof v === 'boolean') return String(v);
  return undefined;
}

/**
 * Extract attributes that a single graph node establishes for the given
 * stream type. Returns a partial map; absent keys mean "not set by this node".
 */
function attrsFromGraphNode(node: NodeDef, type: StreamType): Record<string, string> {
  const out: Record<string, string> = {};
  const p = node.params ?? {};
  const get = (k: string) => asString(p[k]);

  // Generic: any param matching a canonical key wins for the relevant stream.
  for (const k of keysFor(type)) {
    const v = get(k);
    if (v !== undefined) out[k] = v;
  }

  // Filter-specific shortcuts. Only the canonical (most common) parameters are
  // recognised; obscure aliases are intentionally ignored to avoid lying.
  if (node.type === 'filter') {
    const f = node.filter ?? '';
    if (type === 'video') {
      switch (f) {
        case 'scale':
        case 'scale_cuda':
        case 'scale_vaapi':
        case 'scale_qsv': {
          const w = get('w') ?? get('width');
          const h = get('h') ?? get('height');
          if (w) out['width'] = w;
          if (h) out['height'] = h;
          const pf = get('format') ?? get('pix_fmt');
          if (pf) out['pix_fmt'] = pf;
          break;
        }
        case 'pad':
        case 'crop': {
          const w = get('w') ?? get('width');
          const h = get('h') ?? get('height');
          if (w) out['width'] = w;
          if (h) out['height'] = h;
          break;
        }
        case 'format': {
          const pf = get('pix_fmts') ?? get('pix_fmt');
          if (pf) out['pix_fmt'] = pf.split('|')[0];
          break;
        }
        case 'fps':
        case 'framerate': {
          const fps = get('fps') ?? get('framerate');
          if (fps) out['frame_rate'] = fps;
          break;
        }
        case 'setsar':
        case 'setdar': {
          const r = get('r') ?? get('ratio') ?? get('sar') ?? get('dar');
          if (r) out['sar'] = r;
          break;
        }
      }
    } else if (type === 'audio') {
      switch (f) {
        case 'aresample': {
          // Positional value or osr=
          const sr = get('osr') ?? get('sample_rate');
          if (sr) out['sample_rate'] = sr;
          break;
        }
        case 'aformat': {
          const sr = get('sample_rates');
          if (sr) out['sample_rate'] = sr.split('|')[0];
          const sf = get('sample_fmts');
          if (sf) out['sample_fmt'] = sf.split('|')[0];
          const cl = get('channel_layouts');
          if (cl) out['channel_layout'] = cl.split('|')[0];
          break;
        }
        case 'asetrate': {
          const sr = get('sample_rate') ?? get('r');
          if (sr) out['sample_rate'] = sr;
          break;
        }
        case 'pan': {
          const cl = get('channel_layout') ?? get('args');
          if (cl) out['channel_layout'] = cl;
          break;
        }
        case 'channelmap':
        case 'channelsplit': {
          const cl = get('channel_layout');
          if (cl) out['channel_layout'] = cl;
          break;
        }
      }
    }
  }

  // Encoder nodes: declare the codec.
  if (node.type === 'encoder') {
    const codec = node.filter ?? get('codec');
    if (codec) out['codec'] = codec;
    const br = get('b') ?? get('bitrate') ?? get('bit_rate');
    if (br) out['bit_rate'] = br;
  }

  return out;
}

/** Attributes contributed by an Input on the given stream type. */
function attrsFromInput(inp: Input, _type: StreamType): Record<string, string> {
  // Input streams' technical details are determined by the source media at
  // open-time, not by the JSON. Surface only explicitly user-set demuxer
  // options that look like canonical attributes.
  const out: Record<string, string> = {};
  const opts = (inp.options ?? {}) as Record<string, unknown>;
  for (const k of ['pix_fmt', 'sample_rate', 'channels', 'frame_rate']) {
    const v = asString(opts[k]);
    if (v) out[k] = v;
  }
  return out;
}

/** Attributes contributed by an Output sink (codec choice for the stream). */
function attrsFromOutput(out: Output, type: StreamType): Record<string, string> {
  const m: Record<string, string> = {};
  if (type === 'video' && out.codec_video) m['codec'] = out.codec_video;
  if (type === 'audio' && out.codec_audio) m['codec'] = out.codec_audio;
  if (type === 'subtitle' && out.codec_subtitle) m['codec'] = out.codec_subtitle;
  return m;
}

function nodeAttrs(node: FlowNode, type: StreamType): Record<string, string> {
  const ref = node.data.ref;
  switch (ref.kind) {
    case 'input': return attrsFromInput(ref.def, type);
    case 'output': return attrsFromOutput(ref.def, type);
    case 'node': return attrsFromGraphNode(ref.def, type);
  }
}

/**
 * Infer attributes for an edge by walking upstream from its source node.
 * The closest upstream node that sets a key wins.
 */
export function inferEdgeAttributes(
  nodes: FlowNode[],
  edges: FlowEdge[],
  edge: FlowEdge,
): EdgeAttribute[] {
  const type = (edge.data?.streamType ?? (edge.sourceHandle as StreamType) ?? 'video') as StreamType;
  const nodeById = new Map(nodes.map((n) => [n.id, n]));
  // Build incoming-edges map (per stream type) for upstream traversal.
  const incomingByNode = new Map<string, FlowEdge[]>();
  for (const e of edges) {
    if (((e.data?.streamType ?? e.sourceHandle) as StreamType) !== type) continue;
    const list = incomingByNode.get(e.target) ?? [];
    list.push(e);
    incomingByNode.set(e.target, list);
  }

  const result: Record<string, EdgeAttribute> = {};
  const visited = new Set<string>();
  const queue: string[] = [edge.source];

  while (queue.length) {
    const nid = queue.shift()!;
    if (visited.has(nid)) continue;
    visited.add(nid);
    const n = nodeById.get(nid);
    if (!n) continue;
    const attrs = nodeAttrs(n, type);
    for (const [k, v] of Object.entries(attrs)) {
      if (!(k in result)) {
        result[k] = { key: k, value: v, source: n.data.label };
      }
    }
    // Stop when we have everything we display for this stream type.
    if (keysFor(type).every((k) => k in result)) break;
    // Otherwise keep walking upstream.
    const incoming = incomingByNode.get(nid) ?? [];
    for (const inc of incoming) queue.push(inc.source);
  }

  // Preserve the canonical display order.
  return keysFor(type).filter((k) => k in result).map((k) => result[k]);
}

/** Convenience: only the keys safe to use as a tiny inline label. */
export function summariseAttributes(attrs: EdgeAttribute[]): string {
  if (!attrs.length) return '';
  const parts: string[] = [];
  const byKey = new Map(attrs.map((a) => [a.key, a]));
  const w = byKey.get('width')?.value;
  const h = byKey.get('height')?.value;
  if (w && h) parts.push(`${w}×${h}`);
  const pf = byKey.get('pix_fmt')?.value;
  if (pf) parts.push(pf);
  const fr = byKey.get('frame_rate')?.value;
  if (fr) parts.push(`${fr}fps`);
  const sr = byKey.get('sample_rate')?.value;
  if (sr) parts.push(`${sr}Hz`);
  const ch = byKey.get('channels')?.value ?? byKey.get('channel_layout')?.value;
  if (ch) parts.push(ch);
  const codec = byKey.get('codec')?.value;
  if (codec) parts.push(codec);
  // Anything else not yet shown.
  for (const a of attrs) {
    if (['width', 'height', 'pix_fmt', 'frame_rate', 'sample_rate', 'channels', 'channel_layout', 'codec'].includes(a.key)) continue;
    parts.push(`${attrLabel(a.key)}=${a.value}`);
  }
  return parts.join(' · ');
}
