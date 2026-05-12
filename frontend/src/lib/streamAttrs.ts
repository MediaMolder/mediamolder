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
import type { Input, NodeDef, Output, ProbedStream, StreamType } from './jobTypes';
import { findOption, getEncoderInfoSync, primaryMeta, rolesFor } from './encoderSchema';

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
  'bit_depth', 'color_space', 'color_range', 'color_primaries', 'color_transfer',
  'sar', 'field_order',
  'codec', 'profile', 'level', 'bit_rate', 'rate_control',
] as const;
const AUDIO_KEYS = [
  'sample_rate', 'channels', 'channel_layout', 'sample_fmt', 'bit_depth',
  'codec', 'profile', 'bit_rate',
] as const;
const SUBTITLE_KEYS = ['codec'] as const;
const DATA_KEYS = ['codec'] as const;

function keysFor(type: StreamType): readonly string[] {
  switch (type) {
    case 'video': return VIDEO_KEYS;
    case 'audio': return AUDIO_KEYS;
    case 'subtitle': return SUBTITLE_KEYS;
    case 'data': return DATA_KEYS;
    case 'metadata': return [];
    case 'attachment': return [];
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
    case 'bit_depth': return 'depth';
    case 'color_space': return 'colorspace';
    case 'color_range': return 'range';
    case 'color_primaries': return 'primaries';
    case 'color_transfer': return 'trc';
    case 'field_order': return 'field';
    case 'rate_control': return 'rate control';
    default: return key;
  }
}

function asString(v: unknown): string | undefined {
  if (v === undefined || v === null) return undefined;
  if (typeof v === 'string') return v.trim() || undefined;
  if (typeof v === 'number' || typeof v === 'boolean') return String(v);
  return undefined;
}

function formatBitRate(bps: number): string {
  if (bps >= 1_000_000) return `${(bps / 1_000_000).toFixed(2)} Mbps`;
  if (bps >= 1_000) return `${(bps / 1_000).toFixed(0)} kbps`;
  return `${bps} bps`;
}

/**
 * Parse a bit-rate string as accepted by FFmpeg's AVOptions: a plain
 * integer ("5000000"), or a decimal with a SI/binary suffix ("5M",
 * "5000k", "1.5M", "1500K"). Returns the value in bits/second, or
 * undefined if the input doesn't look like a bit rate.
 */
export function parseBitRate(s: string): number | undefined {
  const m = s.trim().match(/^(\d+(?:\.\d+)?)\s*([kKmMgG])?$/);
  if (!m) return undefined;
  const n = parseFloat(m[1]);
  if (!Number.isFinite(n)) return undefined;
  switch (m[2]) {
    case 'k': case 'K': return Math.round(n * 1_000);
    case 'm': case 'M': return Math.round(n * 1_000_000);
    case 'g': case 'G': return Math.round(n * 1_000_000_000);
    default: return Math.round(n);
  }
}

/** Format a raw param string (e.g. "5M", "5000000") as a bit rate. */
export function formatBitRateString(s: string): string {
  const bps = parseBitRate(s);
  return bps === undefined ? s : formatBitRate(bps);
}

/**
 * Derive the per-component bit depth from a pixel-format name.
 * Handles high-bit-depth planar formats (yuv420p10le → 10, p010le → 10)
 * and packed RGB exceptions (rgb48le → 16 per component). Returns 8 for
 * all standard 8-bit formats (yuv420p, rgb24, nv12, rgba, …).
 */
function pixFmtBitDepth(pf: string): number {
  // Packed RGB/RGBA where the suffix is total bits, not per-component.
  const packedExceptions: Record<string, number> = {
    rgb48le: 16, rgb48be: 16, bgr48le: 16, bgr48be: 16,
    rgba64le: 16, rgba64be: 16, bgra64le: 16, bgra64be: 16,
  };
  if (pf in packedExceptions) return packedExceptions[pf];
  // High-bit planar/semi-planar names end with NNle or NNbe (NN ≥ 10).
  const m = pf.match(/(\d{2,})(le|be)$/);
  if (m) return parseInt(m[1], 10);
  // Everything else is 8-bit.
  return 8;
}

/**
 * Uncompressed bits-per-pixel for a pixel format.
 * Based on chroma subsampling ratios and component depth.
 * Returns undefined for unknown/exotic formats.
 */
function pixFmtBitsPerPixel(pf: string): number | undefined {
  const known: Record<string, number> = {
    // 4:2:0  8-bit (12 bpp)
    yuv420p: 12, yuvj420p: 12, nv12: 12, nv21: 12,
    // 4:2:0 10-bit (15 bpp)
    yuv420p10le: 15, yuv420p10be: 15, p010le: 15, p010be: 15,
    // 4:2:0 12-bit (18 bpp)
    yuv420p12le: 18, yuv420p12be: 18,
    // 4:2:2  8-bit (16 bpp)
    yuv422p: 16, yuvj422p: 16, uyvy422: 16, yuyv422: 16, yvyu422: 16,
    // 4:2:2 10-bit (20 bpp)
    yuv422p10le: 20, yuv422p10be: 20, p210le: 20, p210be: 20,
    // 4:4:4  8-bit (24 bpp)
    yuv444p: 24, yuvj444p: 24, gbrp: 24,
    // 4:4:4 10-bit (30 bpp)
    yuv444p10le: 30, yuv444p10be: 30, gbrp10le: 30, gbrp10be: 30,
    // 4:1:1  8-bit (9 bpp)
    yuv411p: 9, yuvj411p: 9,
    // Grayscale
    gray: 8, gray8: 8, gray10le: 10, gray12le: 12, gray16le: 16, gray16be: 16,
    // Packed RGB
    rgb24: 24, bgr24: 24,
    rgba: 32, argb: 32, bgra: 32, abgr: 32,
    rgb48le: 48, rgb48be: 48, bgr48le: 48, bgr48be: 48,
    rgba64le: 64, rgba64be: 64, bgra64le: 64, bgra64be: 64,
  };
  return known[pf];
}

/**
 * Parse a frame-rate string that may be a decimal ("23.976") or a
 * rational ("24000/1001"). Returns NaN for unparseable input.
 */
function parseFrameRate(s: string): number {
  const slash = s.indexOf('/');
  if (slash !== -1) {
    const num = parseFloat(s.slice(0, slash));
    const den = parseFloat(s.slice(slash + 1));
    return den > 0 ? num / den : NaN;
  }
  return parseFloat(s);
}

/**
 * Extract attributes that a single graph node establishes for the given
 * stream type. Returns a partial map; absent keys mean "not set by this node".
 */
function attrsFromGraphNode(node: NodeDef, type: StreamType): Record<string, string | null> {
  const out: Record<string, string | null> = {};
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
          if (pf) {
            out['pix_fmt'] = pf;
            out['bit_depth'] = `${pixFmtBitDepth(pf)} bit`;
          }
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
          if (pf) {
            const resolved = pf.split('|')[0];
            out['pix_fmt'] = resolved;
            out['bit_depth'] = `${pixFmtBitDepth(resolved)} bit`;
          }
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
    // Decoded frames flowing through filter nodes have no codec identity
    // and no compressed bit rate — block all of these from propagating
    // upstream so the input's ProRes/h264/bitrate doesn't bleed through.
    for (const k of ['codec', 'profile', 'level', 'bit_rate'] as const) {
      if (!(k in out)) out[k] = null;
    }
  }

  // Encoder nodes: declare the codec, rate control mode, and bitrate.
  if (node.type === 'encoder') {
    const codec = node.filter ?? get('codec');
    if (codec) out['codec'] = codec;

    // Quality/constant-rate-factor modes: CRF (libx264/libx265/libsvtav1/…),
    // CQ (nvenc), ICQ/global_quality (qsv), QP.
    const crfExplicit = get('crf') ?? get('cq') ?? get('global_quality');
    const qpExplicit  = get('qp')  ?? get('qp_i');
    const br          = get('b')   ?? get('bitrate') ?? get('bit_rate');
    const rcMode      = get('rc_mode') ?? get('rc');

    if (crfExplicit !== undefined) {
      // CRF/CQ/ICQ encode — output bitrate is unknowable until encoding.
      const label = (get('crf') !== undefined ? 'CRF'
                  : get('cq')  !== undefined ? 'CQ'
                  : 'ICQ');
      out['rate_control'] = `${label} ${crfExplicit}`;
      out['bit_rate'] = null;
    } else if (qpExplicit !== undefined) {
      out['rate_control'] = `QP ${qpExplicit}`;
      out['bit_rate'] = null;
    } else if (br) {
      out['bit_rate'] = formatBitRateString(br);
      if (rcMode) out['rate_control'] = rcMode.toUpperCase();
    } else {
      // No explicit rate params. Always null bit_rate so the upstream
      // decoded rate never leaks onto an encoder output edge.
      out['bit_rate'] = null;
      if (codec) {
        // If the schema is already cached, determine the default RC mode
        // and show it (e.g. libx264 defaults to CRF 23).
        const info = getEncoderInfoSync(codec);
        if (info) {
          const roles = rolesFor(codec, info.options);
          const defaultRc = roles.default_rc ?? (roles.crf ? 'crf' : roles.qp ? 'qp' : 'bitrate');
          if ((defaultRc === 'crf') && roles.crf) {
            const crfOpt = findOption(info.options, roles.crf);
            const label  = roles.crf === 'cq' ? 'CQ' : roles.crf === 'global_quality' ? 'ICQ' : 'CRF';
            if (crfOpt) {
              const meta = primaryMeta(codec, crfOpt);
              out['rate_control'] = meta.default
                ? `${label} ${meta.default} (default)`
                : `${label} (default)`;
            } else {
              out['rate_control'] = `${label} (default)`;
            }
          } else if (defaultRc === 'qp' && roles.qp) {
            const qpOpt = findOption(info.options, roles.qp);
            if (qpOpt) {
              const meta = primaryMeta(codec, qpOpt);
              out['rate_control'] = meta.default
                ? `QP ${meta.default} (default)`
                : 'QP (default)';
            } else {
              out['rate_control'] = 'QP (default)';
            }
          } else if (defaultRc === 'bitrate' && roles.bit_rate) {
            const brOpt = findOption(info.options, roles.bit_rate);
            const defVal = brOpt?.default?.int;
            if (defVal !== undefined && defVal > 0) {
              out['bit_rate'] = formatBitRate(defVal) + ' (default)';
            }
          }
        }
      }
    }
    if (rcMode && !('rate_control' in out)) out['rate_control'] = rcMode.toUpperCase();
  }

  return out;
}

/** Attributes contributed by an Input on the given stream type. */
function attrsFromInput(inp: Input, type: StreamType, probed?: ProbedStream[]): Record<string, string> {
  const out: Record<string, string> = {};

  // Probed values take precedence over user-set demuxer options because they
  // describe the actual decoded stream. Pick the first probed stream of the
  // requested type — the common case is one video + one audio per input.
  // Track-aware selection (parsing "in0:v:1") can be added later if needed.
  if (probed && probed.length) {
    const ps = probed.find((p) => p.type === type);
    if (ps) {
      const set = (k: string, v: unknown) => {
        const s = asString(v);
        if (s !== undefined) out[k] = s;
      };
      set('codec', ps.codec);
      if (ps.profile) set('profile', ps.profile);
      if (ps.level) set('level', ps.level);
      if (ps.bit_rate) set('bit_rate', formatBitRate(ps.bit_rate));
      if (ps.bit_depth) set('bit_depth', `${ps.bit_depth} bit`);
      if (type === 'video') {
        set('width', ps.width);
        set('height', ps.height);
        set('pix_fmt', ps.pix_fmt);
        set('frame_rate', ps.frame_rate);
        if (ps.sar && ps.sar !== '1:1') set('sar', ps.sar);
        if (ps.field_order && ps.field_order !== 'progressive') set('field_order', ps.field_order);
        set('color_space', ps.color_space);
        set('color_range', ps.color_range);
        set('color_primaries', ps.color_primaries);
        set('color_transfer', ps.color_transfer);
      } else if (type === 'audio') {
        set('sample_rate', ps.sample_rate);
        set('sample_fmt', ps.sample_fmt);
        set('channels', ps.channels);
        set('channel_layout', ps.channel_layout);
      }
    }
  }

  // User-set demuxer options layer on top only where probed didn't fill in.
  const opts = (inp.options ?? {}) as Record<string, unknown>;
  for (const k of ['pix_fmt', 'sample_rate', 'channels', 'frame_rate']) {
    if (k in out) continue;
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

function nodeAttrs(node: FlowNode, type: StreamType): Record<string, string | null> {
  const ref = node.data.ref;
  switch (ref.kind) {
    case 'input': return attrsFromInput(ref.def, type, node.data.probed);
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
  // Keys that a node explicitly cleared (null sentinel) — stop propagating
  // them from further upstream nodes.
  const blocked = new Set<string>();
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
      if (blocked.has(k)) continue;
      if (v === null) {
        blocked.add(k);
        continue;
      }
      if (!(k in result)) {
        result[k] = { key: k, value: v, source: n.data.label };
      }
    }
    // Stop when we have everything we display for this stream type.
    if (keysFor(type).every((k) => k in result || blocked.has(k))) break;
    // Keep walking upstream so spatial attrs (width/height/fps/pix_fmt/…)
    // propagate through encoder nodes. Compressed bitrate and codec identity
    // are already blocked by the null sentinels emitted by filter nodes or
    // by quality-mode encoder nodes above.
    const incoming = incomingByNode.get(nid) ?? [];
    for (const inc of incoming) queue.push(inc.source);
  }

  // For decoded video streams all compressed-bitrate attrs have been
  // blocked by the null sentinel. Compute the uncompressed rate from
  // first principles: w × h × fps × bits_per_pixel.
  if (type === 'video' && !('bit_rate' in result) && !blocked.has('bit_rate')) {
    const w = result['width'] ? parseInt(result['width'].value, 10) : NaN;
    const h = result['height'] ? parseInt(result['height'].value, 10) : NaN;
    const fpsStr = result['frame_rate']?.value;
    const fps = fpsStr ? parseFrameRate(fpsStr) : NaN;
    const pf = result['pix_fmt']?.value;
    if (!isNaN(w) && !isNaN(h) && !isNaN(fps) && pf) {
      const bpp = pixFmtBitsPerPixel(pf);
      if (bpp !== undefined) {
        const bps = w * h * fps * bpp;
        result['bit_rate'] = { key: 'bit_rate', value: formatBitRate(bps) + ' (decoded)', source: 'calculated' };
      }
    }
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
