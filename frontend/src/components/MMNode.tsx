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

  // Each stream type gets a fixed vertical slot (video=0, audio=1, …) so that
  // the same media type lines up across every node, even when a node only
  // exposes a subset of the four handles. Without this, a single-stream
  // node (e.g. an audio-only encoder) would draw its audio handle in
  // slot 0, but a multi-stream demuxer would draw audio in slot 1, and the
  // edge between them would render as a slanted line that misses both
  // endpoint dots.
  const slotTop = (t: StreamHandle) => 16 + STREAM_HANDLES.indexOf(t) * 12;

  return (
    <div className={classes}>
      {!isInput &&
        supported.map((t) => (
          <Handle
            key={`tgt-${t}`}
            type="target"
            position={Position.Left}
            id={t}
            className={`handle-${t}`}
            style={{ top: slotTop(t) }}
          />
        ))}

      <div className="mm-node-type">{describeKind(data.kind, supported)}</div>
      <div className="mm-node-title">{data.label}</div>
      {data.sublabel && <div className="mm-node-sub">{data.sublabel}</div>}
      {run && (run.frames !== undefined || run.errors !== undefined) && (
        <div className="mm-node-run">
          {run.frames !== undefined && <span>{run.frames} pkt</span>}
          {run.fps !== undefined && run.fps > 0 && <span>{run.fps.toFixed(1)} fps</span>}
          {(run.errors ?? 0) > 0 && <span className="badge-err">{run.errors} err</span>}
        </div>
      )}

      {!isOutput &&
        supported.map((t) => (
          <Handle
            key={`src-${t}`}
            type="source"
            position={Position.Right}
            id={t}
            className={`handle-${t}`}
            style={{ top: slotTop(t) }}
          />
        ))}
    </div>
  );
}

/**
 * Human-friendly heading shown at the top of every node, in place of
 * the bare runtime kind. Disambiguates by media type when the node is
 * single-stream (e.g. an encoder wired only for audio renders as
 * "Audio encoder" rather than the generic "ENCODER" tag).
 */
export function describeKind(kind: string, supported: readonly string[]): string {
  const single = supported.length === 1 ? supported[0] : null;
  const cap = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);
  switch (kind) {
    case 'input':
      return 'File read / Demux';
    case 'output':
      return 'Mux / File write';
    case 'demuxer':
      return 'Demuxer';
    case 'muxer':
      return 'Muxer';
    case 'decoder':
      return single ? `${cap(single)} decoder` : 'Decoder';
    case 'encoder':
      return single ? `${cap(single)} encoder` : 'Encoder';
    case 'filter':
      return single ? `${cap(single)} filter` : 'Filter';
    case 'go_processor':
      return 'Processor';
    case 'copy':
      return single ? `${cap(single)} stream copy` : 'Stream copy';
    default:
      return kind;
  }
}
