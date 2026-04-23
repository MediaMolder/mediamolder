// Custom React Flow edge that draws the standard bezier path AND a small chip
// near the midpoint listing all known technical attributes for the stream
// (pix_fmt, width×height, sample_rate, ...). The full attribute list (with
// the node that established each value) appears in the chip's tooltip.

import { BaseEdge, EdgeLabelRenderer, getBezierPath, type EdgeProps } from '@xyflow/react';
import type { EdgeAttribute } from '../lib/streamAttrs';

export interface MMEdgeData extends Record<string, unknown> {
  streamType?: string;
  rawFrom?: string;
  rawTo?: string;
  attrs?: EdgeAttribute[];
  attrSummary?: string;
}

export function MMEdge(props: EdgeProps) {
  const {
    id,
    sourceX, sourceY, targetX, targetY,
    sourcePosition, targetPosition,
    markerEnd, style, data,
  } = props;
  const ed = (data ?? {}) as MMEdgeData;

  const [edgePath, labelX, labelY] = getBezierPath({
    sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition,
  });

  const summary = ed.attrSummary ?? '';
  const attrs = ed.attrs ?? [];
  const tooltip = attrs.length
    ? attrs.map((a) => `${a.key}: ${a.value}  (from ${a.source})`).join('\n')
    : 'No technical attributes known for this connection.';

  return (
    <>
      <BaseEdge id={id} path={edgePath} markerEnd={markerEnd} style={style} />
      {summary && (
        <EdgeLabelRenderer>
          <div
            className="mm-edge-chip nodrag nopan"
            title={tooltip}
            style={{ transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)` }}
          >
            {summary}
          </div>
        </EdgeLabelRenderer>
      )}
    </>
  );
}
