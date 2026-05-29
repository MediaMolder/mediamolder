// Custom React Flow edge.
//
// The path itself carries no text label (it would clutter dense graphs).
// Instead, hovering the edge — or clicking it — opens a popover at the
// midpoint that lists every known technical attribute for the stream
// (pix_fmt, width×height, color_space, bit_rate, ...).

import { useState } from 'react';
import type React from 'react';
import { BaseEdge, EdgeLabelRenderer, getBezierPath, useReactFlow, useStore, type EdgeProps } from '@xyflow/react';
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
  // Counteract canvas zoom so the popover stays a constant on-screen size.
  const zoom = useStore((s) => s.transform[2]);
  const inv = zoom > 0 ? 1 / zoom : 1;
  const rf = useReactFlow();

  // Drive stroke appearance via inline style so it wins over the xyflow
  // default `.react-flow__edge-path { stroke: var(--xy-edge-stroke-default) }`
  // rule regardless of CSS cascade order.
  const STREAM_STROKE: Record<string, string> = {
    file:     '#ffffff',
    video:    'var(--video)',
    audio:    'var(--audio)',
    subtitle: 'var(--subtitle)',
    data:     'var(--data)',
    events:   'var(--events)',
  };
  const st = ed.streamType ?? 'video';
  const edgeStyle: React.CSSProperties = {
    ...style,
    stroke: STREAM_STROKE[st] ?? STREAM_STROKE.video,
    strokeWidth: 2,
    ...(st === 'events' ? { strokeDasharray: '6 3' } : {}),
  };

  return (
    <>
      <BaseEdge id={id} path={edgePath} markerEnd={markerEnd} style={edgeStyle} />
      {/* Wide invisible hit area so hover/click works reliably on thin edges.
       * We deliberately do NOT stopPropagation so React Flow can still
       * select the edge (which makes Backspace/Delete remove it). */}
      <path
        d={edgePath}
        fill="none"
        stroke="transparent"
        strokeWidth={20}
        style={{ cursor: 'pointer' }}
        onMouseEnter={() => setHover(true)}
        onMouseLeave={() => setHover(false)}
        onClick={() => setPinned((p) => !p)}
      />
      {open && (
        <EdgeLabelRenderer>
          <div
            className="mm-edge-popover nodrag nopan"
            style={{ transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px) scale(${inv})` }}
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
            <div className="mm-edge-popover-actions">
              <button
                className="mm-edge-popover-btn danger"
                onClick={(e) => {
                  e.stopPropagation();
                  rf.deleteElements({ edges: [{ id }] });
                }}
                title="Delete this connection"
              >
                Delete
              </button>
              {pinned && (
                <button
                  className="mm-edge-popover-btn"
                  onClick={(e) => {
                    e.stopPropagation();
                    setPinned(false);
                  }}
                  title="Close"
                >
                  Close
                </button>
              )}
            </div>
          </div>
        </EdgeLabelRenderer>
      )}
    </>
  );
}
