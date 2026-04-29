// Dagre-backed auto-layout for the editor canvas. Runs entirely client-side.

import dagre from '@dagrejs/dagre';
import type { FlowEdge, FlowNode } from './jsonAdapter';

// Fallback dimensions used when React Flow hasn't measured a node yet
// (e.g. immediately after loading a JobConfig with no UI positions).
// Real widths supersede these whenever available so wide nodes — the
// usual culprit being a source/sink showing a long file path — get the
// horizontal slot they actually need and don't overlap their neighbours.
const FALLBACK_W = 200;
const FALLBACK_H = 64;

export function autoLayout(nodes: FlowNode[], edges: FlowEdge[]): FlowNode[] {
  const g = new dagre.graphlib.Graph();
  g.setDefaultEdgeLabel(() => ({}));
  g.setGraph({ rankdir: 'LR', nodesep: 40, ranksep: 90, marginx: 10, marginy: 10 });

  // Per-node measured dimensions, indexed by id, for the post-layout
  // re-centering pass below.
  const dims = new Map<string, { w: number; h: number }>();
  nodes.forEach((n) => {
    const w = nodeWidth(n);
    const h = nodeHeight(n);
    dims.set(n.id, { w, h });
    g.setNode(n.id, { width: w, height: h });
  });
  edges.forEach((e) => g.setEdge(e.source, e.target));

  dagre.layout(g);

  return nodes.map((n) => {
    const pos = g.node(n.id);
    if (!pos) return n;
    const { w, h } = dims.get(n.id) ?? { w: FALLBACK_W, h: FALLBACK_H };
    return {
      ...n,
      position: {
        x: Math.round(pos.x - w / 2),
        y: Math.round(pos.y - h / 2),
      },
    };
  });
}

/** Best available width for a node. React Flow v12 stores the measured DOM
 * size on `node.measured`; older positions live on `width`. Anything else
 * falls back to the constant. */
function nodeWidth(n: FlowNode): number {
  return measured(n.measured?.width) ?? measured(n.width) ?? FALLBACK_W;
}

function nodeHeight(n: FlowNode): number {
  return measured(n.measured?.height) ?? measured(n.height) ?? FALLBACK_H;
}

function measured(v: number | null | undefined): number | undefined {
  return typeof v === 'number' && Number.isFinite(v) && v > 0 ? v : undefined;
}
