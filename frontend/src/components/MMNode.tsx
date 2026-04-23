import { Handle, Position, type NodeProps } from '@xyflow/react';
import type { FlowNodeData } from '../lib/jsonAdapter';

const STREAM_HANDLES = ['video', 'audio', 'subtitle', 'data'] as const;

export function MMNode({ data, selected }: NodeProps & { data: FlowNodeData }) {
  const isInput = data.kind === 'input';
  const isOutput = data.kind === 'output';

  return (
    <div className={`mm-node${selected ? ' selected' : ''}`}>
      {!isInput &&
        STREAM_HANDLES.map((t, i) => (
          <Handle
            key={`tgt-${t}`}
            type="target"
            position={Position.Left}
            id={t}
            className={`handle-${t}`}
            style={{ top: 16 + i * 12 }}
          />
        ))}

      <div className="mm-node-type">{data.kind}</div>
      <div className="mm-node-title">{data.label}</div>
      {data.sublabel && <div className="mm-node-sub">{data.sublabel}</div>}

      {!isOutput &&
        STREAM_HANDLES.map((t, i) => (
          <Handle
            key={`src-${t}`}
            type="source"
            position={Position.Right}
            id={t}
            className={`handle-${t}`}
            style={{ top: 16 + i * 12 }}
          />
        ))}
    </div>
  );
}
