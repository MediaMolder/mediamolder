import { Handle, Position, type NodeProps } from '@xyflow/react';
import type { FlowNodeData } from '../lib/jsonAdapter';

const STREAM_HANDLES = ['video', 'audio', 'subtitle', 'data'] as const;

export interface MMNodeRunData {
  frames?: number;
  fps?: number;
  errors?: number;
  hasError?: boolean;
}

export function MMNode({ data, selected }: NodeProps & { data: FlowNodeData & { run?: MMNodeRunData } }) {
  const isInput = data.kind === 'input';
  const isOutput = data.kind === 'output';
  const run = data.run;
  const errored = !!run?.hasError || (run?.errors ?? 0) > 0;

  const classes = [
    'mm-node',
    selected ? 'selected' : '',
    errored ? 'errored' : '',
  ]
    .filter(Boolean)
    .join(' ');

  return (
    <div className={classes}>
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
      {run && (run.frames !== undefined || run.errors !== undefined) && (
        <div className="mm-node-run">
          {run.frames !== undefined && <span>{run.frames} fr</span>}
          {run.fps !== undefined && run.fps > 0 && <span>{run.fps.toFixed(1)} fps</span>}
          {(run.errors ?? 0) > 0 && <span className="badge-err">{run.errors} err</span>}
        </div>
      )}

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
