import { Handle, Position, type NodeProps } from '@xyflow/react';
import type { FlowNodeData } from '../lib/jsonAdapter';

const STREAM_HANDLES = ['video', 'audio', 'subtitle', 'data'] as const;
type StreamHandle = (typeof STREAM_HANDLES)[number];

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

  // Inputs and outputs are media-type-agnostic by design (the user picks
  // which streams an input exposes, and a sink accepts whatever wiring the
  // graph hands it). For everything else, restrict the handle set to the
  // media types the catalog reported as supported. An empty/missing
  // streams list means "unknown" — fall back to all four so the user can
  // still wire the node manually.
  const supported: readonly StreamHandle[] =
    isInput || isOutput || !data.streams || data.streams.length === 0
      ? STREAM_HANDLES
      : STREAM_HANDLES.filter((t) => data.streams!.includes(t));

  const classes = [
    'mm-node',
    selected ? 'selected' : '',
    errored ? 'errored' : '',
    data.implicit ? 'implicit' : '',
  ]
    .filter(Boolean)
    .join(' ');

  return (
    <div className={classes}>
      {!isInput &&
        supported.map((t, i) => (
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
        supported.map((t, i) => (
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
