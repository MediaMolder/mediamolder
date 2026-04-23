// Dagre-backed auto-layout for the editor canvas. Runs entirely client-side.

import dagre from '@dagrejs/dagre';
import type { FlowEdge, FlowNode } from './jsonAdapter';

const NODE_W = 160;
const NODE_H = 56;

export function autoLayout(nodes: FlowNode[], edges: FlowEdge[]): FlowNode[] {
  const g = new dagre.graphlib.Graph();
  g.setDefaultEdgeLabel(() => ({}));
  g.setGraph({ rankdir: 'LR', nodesep: 30, ranksep: 80, marginx: 10, marginy: 10 });

  nodes.forEach((n) => g.setNode(n.id, { width: NODE_W, height: NODE_H }));
  edges.forEach((e) => g.setEdge(e.source, e.target));

  dagre.layout(g);

  return nodes.map((n) => {
    const pos = g.node(n.id);
    if (!pos) return n;
    return {
      ...n,
      position: {
        x: Math.round(pos.x - NODE_W / 2),
        y: Math.round(pos.y - NODE_H / 2),
      },
    };
  });
}
