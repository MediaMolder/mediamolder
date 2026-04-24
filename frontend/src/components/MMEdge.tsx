// Custom React Flow edge.
//
// The path itself carries no text label (it would clutter dense graphs).
// Instead, hovering the edge — or clicking it — opens a popover at the
// midpoint that lists every known technical attribute for the stream
// (pix_fmt, width×height, color_space, bit_rate, ...). The full attribute
// list (with the node that established each value) is also available in
// the native `title` tooltip on the popover.

import { useState } from 'react';
import { BaseEdge, EdgeLabelRenderer, getBezierPath, type EdgeProps } from '@xyflow/react';
import type { EdgeAttribute } from '../lib/streamAttrs';
import { attrLabel } from '../lib/streamAttrs';

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

  const attrs = ed.attrs ?? [];
  const [hover, setHover] = useState(false);
  const [pinned, setPinned] = useState(false);
  const open = (hover || pinned) && attrs.length > 0;

  const tooltip = attrs.length
    ? attrs.map((a) => `${a.key}: ${a.value}  (from ${a.source})`).join('\n')
    : 'No technical attributes known for this connection.';

  return (
    <>
      <BaseEdge id={id} path={edgePath} markerEnd={markerEnd} style={style} />
      {/* Wide invisible hit area so hover/click works reliably on thin edges. */}
      <path
        d={edgePath}
        fill="none"
        stroke="transparent"
        strokeWidth={20}
        style={{ cursor: attrs.length ? 'pointer' : 'default' }}
        onMouseEnter={() => setHover(true)}
        onMouseLeave={() => setHover(false)}
        onClick={(e) => {
          e.stopPropagation();
          setPinned((p) => !p);
        }}
      />
      {open && (
        <EdgeLabelRenderer>
          <div
            className="mm-edge-popover nodrag nopan"
            title={tooltip}
            style={{ transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)` }}
            onMouseEnter={() => setHover(true)}
            onMouseLeave={() => setHover(false)}
          >
            <dl className="mm-edge-popover-attrs">
              {attrs.map((a) => (
                <div key={a.key} className="mm-edge-popover-row">
                  <dt>{attrLabel(a.key)}</dt>
                  <dd>{a.value}</dd>
                </div>
              ))}
            </dl>
            {pinned && (
              <button
                className="mm-edge-popover-close"
                onClick={(e) => {
                  e.stopPropagation();
                  setPinned(false);
                }}
                title="Close"
              >
                ×
              </button>
            )}
          </div>
        </EdgeLabelRenderer>
      )}
    </>
  );
}
