// Build new NodeDef / Input / Output instances when the user drops a palette
// item onto the canvas, and assign collision-free IDs.

import type { FlowNode } from './jsonAdapter';
import { INPUT_PREFIX, OUTPUT_PREFIX } from './jsonAdapter';
import type { Input, NodeDef, Output } from './jobTypes';

export interface PaletteEntry {
  category: string;
  type: string; // schema NodeDef.type or "input" / "output"
  name: string;
  description?: string;
  streams?: string[];
  num_inputs?: number;
  num_outputs?: number;
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
  return `${base}_${Date.now()}`;
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
        data: { kind: 'input', label: id, sublabel: '(no url)', ref: { kind: 'input', def } },
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
        data: { kind: 'output', label: id, sublabel: '(no url)', ref: { kind: 'output', def } },
      },
    };
  }

  // Filter / encoder / processor → graph NodeDef
  const baseId = sanitiseId(entry.name);
  const id = uniqueId(baseId, existingIds);
  const def: NodeDef = { id, type: entry.type };
  if (entry.type === 'filter') def.filter = entry.name;
  else if (entry.type === 'encoder') def.filter = entry.name; // codec name lives in params normally; placeholder
  else if (entry.type === 'go_processor') def.processor = entry.name;

  return {
    flowNode: {
      id,
      type: 'mmNode',
      position,
      data: {
        kind: entry.type,
        label: id,
        sublabel: entry.name,
        ref: { kind: 'node', def },
      },
    },
  };
}

function sanitiseId(s: string): string {
  return s.toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_+|_+$/g, '') || 'node';
}
