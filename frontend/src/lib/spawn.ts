// Build new NodeDef / Input / Output instances when the user drops a palette
// item onto the canvas, and assign collision-free IDs.

import type { FlowNode } from './jsonAdapter';
import { EMPTY_URL_PLACEHOLDER, INPUT_PREFIX, OUTPUT_PREFIX } from './jsonAdapter';
import type { Input, NodeDef, Output } from './jobTypes';

export interface PaletteEntry {
  category: string;
  subcategory?: string;
  type: string; // schema NodeDef.type or "input" / "output"
  name: string;
  label?: string;
  description?: string;
  streams?: string[];
  num_inputs?: number;
  num_outputs?: number;
  /**
   * Curation metadata mirrored from internal/gui/curation.go. Populated
   * by `GET /api/nodes` when the entry is in the curated registry.
   */
  common?: boolean;
  friendly_name?: string;
  aliases?: string[];
}

export interface SpawnResult {
  flowNode: FlowNode;
}

/** Generate an ID like "<base>", "<base>1", "<base>2", ... unique among nodes. */
export function uniqueId(base: string, existing: Iterable<string>): string {
  const taken = new Set(existing);
  if (!taken.has(base)) return base;
  for (let i = 1; i < 10_000; i++) {
    const candidate = `${base}${i}`;
    if (!taken.has(candidate)) return candidate;
  }
  return `${base}_${crypto.randomUUID()}`;
}

/** Spawn a new FlowNode at the given canvas position from a palette entry. */
export function spawnNodeFrom(
  entry: PaletteEntry,
  position: { x: number; y: number },
  existingNodes: FlowNode[],
): SpawnResult {
  const existingIds = existingNodes.map((n) => n.id);

  if (entry.type === 'input') {
    const baseId = 'in';
    const id = uniqueId(baseId, existingIds.map((i) => i.replace(INPUT_PREFIX, '')));
    const def: Input = { id, url: '', streams: [{ input_index: 0, type: 'video', track: 0 }] };
    return {
      flowNode: {
        id: INPUT_PREFIX + id,
        type: 'mmNode',
        position,
        data: { kind: 'input', label: id, sublabel: EMPTY_URL_PLACEHOLDER, ref: { kind: 'input', def } },
      },
    };
  }

  // Wave 11 #63: Capture-device input. Spawns a top-level Input pre-populated
  // with `format` set to the libavdevice demuxer name (dshow / avfoundation /
  // v4l2 / gdigrab). The Inspector routes device inputs to DeviceInputForm.
  if (entry.type === 'device_input') {
    const baseId = entry.name; // 'dshow', 'v4l2', etc.
    const id = uniqueId(baseId, existingIds.map((i) => i.replace(INPUT_PREFIX, '')));
    const streams = (entry.streams ?? ['video']).map(
      (t, idx): Input['streams'][number] => ({ input_index: idx, type: t as Input['streams'][number]['type'], track: 0 }),
    );
    const def: Input = { id, url: '', format: entry.name, streams };
    return {
      flowNode: {
        id: INPUT_PREFIX + id,
        type: 'mmNode',
        position,
        data: { kind: 'device_input', label: id, sublabel: entry.name, ref: { kind: 'input', def } },
      },
    };
  }

  // Wave 8 #44: Input.Kind="lavfi" shorthand. Spawns a top-level Input
  // pre-populated with kind:"lavfi" so the URL field becomes the
  // libavfilter graph spec (e.g. `anullsrc=r=48000:cl=stereo`).
  if (entry.type === 'lavfi_input') {
    const baseId = 'lavfi';
    const id = uniqueId(baseId, existingIds.map((i) => i.replace(INPUT_PREFIX, '')));
    const def: Input = {
      id,
      url: 'anullsrc=r=48000:cl=stereo',
      kind: 'lavfi',
      streams: [{ input_index: 0, type: 'audio', track: 0 }],
    };
    return {
      flowNode: {
        id: INPUT_PREFIX + id,
        type: 'mmNode',
        position,
        data: { kind: 'input', label: id, sublabel: 'lavfi', ref: { kind: 'input', def } },
      },
    };
  }

  if (entry.type === 'output') {
    const baseId = 'out';
    const id = uniqueId(baseId, existingIds.map((i) => i.replace(OUTPUT_PREFIX, '')));
    const def: Output = { id, url: '' };
    return {
      flowNode: {
        id: OUTPUT_PREFIX + id,
        type: 'mmNode',
        position,
        data: { kind: 'output', label: id, sublabel: EMPTY_URL_PLACEHOLDER, ref: { kind: 'output', def } },
      },
    };
  }

  // Filter / encoder / processor / copy / filter_source / filter_sink → graph NodeDef
  const baseId = sanitiseId(entry.name);
  const id = uniqueId(baseId, existingIds);
  const def: NodeDef = { id, type: entry.type };
  if (entry.type === 'filter') def.filter = entry.name;
  else if (entry.type === 'filter_source' || entry.type === 'filter_sink') {
    // Wave 8 #44: virtual source / sink filters carry a `filter` name
    // identical to the regular filter type. The runtime dispatches on
    // node.type to decide whether to construct a source-only,
    // sink-only, or buffersrc-fronted libavfilter graph.
    def.filter = entry.name;
    if (entry.type === 'filter_source' && entry.name !== 'anullsrc' && entry.name !== 'aevalsrc') {
      // Bound the source so the job actually terminates. Validator
      // (pipeline #36c) requires duration or nb_frames for everything
      // except the silent-audio sources.
      def.params = { duration: '5' };
    }
  } else if (entry.type === 'encoder') def.params = { codec: entry.name };
  else if (entry.type === 'go_processor') def.processor = entry.name;
  // Copy nodes carry no params; the inbound edge type tells the
  // runtime which input stream to forward.

  // Show the codec / filter / processor name as the bold heading; the
  // user-facing id is the secondary line. Mirrors nodeDisplayLabel /
  // nodeDisplaySublabel in jsonAdapter.ts.
  return {
    flowNode: {
      id,
      type: 'mmNode',
      position,
      data: {
        kind: entry.type,
        label: entry.name,
        sublabel: id,
        ref: { kind: 'node', def },
        streams: entry.streams,
        friendlyName: entry.friendly_name,
      },
    },
  };
}

function sanitiseId(s: string): string {
  return s.toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_+|_+$/g, '') || 'node';
}
