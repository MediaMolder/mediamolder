import { Fragment } from 'react';
import { Handle, Position, type NodeProps } from '@xyflow/react';
import type { FlowNodeData } from '../lib/jsonAdapter';
import { MULTI_AUDIO_INPUT_FILTERS } from '../lib/jsonAdapter';
import type { NodeDef } from '../lib/jobTypes';
import { displayName, lookupFriendlyName, useNamingMode } from '../lib/friendlyNames';

const STREAM_HANDLES = ['video', 'audio', 'subtitle', 'data'] as const;
type StreamHandle = (typeof STREAM_HANDLES)[number];

export interface MMNodeRunData {
  frames?: number;
  fps?: number;
  errors?: number;
  hasError?: boolean;
}

export const INSPECTOR_OPEN_EVENT = 'mm.inspector.open';
export const URL_BROWSE_EVENT = 'mm.url.browse';

// Vertical spacing constants (px).
const SLOT_PITCH = 12;   // gap between distinct stream types (unchanged)
const TRACK_PITCH = 10;  // gap between per-track handles of the same type
const AUDIO_BASE = 16 + STREAM_HANDLES.indexOf('audio') * SLOT_PITCH; // 28

export function MMNode({ id, data, selected }: NodeProps & { data: FlowNodeData & { run?: MMNodeRunData } }) {
  const naming = useNamingMode();
  const friendly = data.friendlyName ?? lookupFriendlyName(data.label);
  const heading = displayName({ name: data.label, friendly_name: friendly }, naming);
  const isInput = data.kind === 'input' || data.kind === 'filter_source';
  const isOutput = data.kind === 'output' || data.kind === 'filter_sink';
  const run = data.run;
  const errored = !!run?.hasError || (run?.errors ?? 0) > 0;

  // Inputs and outputs are media-type-agnostic by design (the user picks
  // which streams an input exposes, and a sink accepts whatever wiring the
  // graph hands it). For everything else, restrict the handle set to the
  // media types the catalog reported as supported. An empty/missing
  // streams list means "unknown" — fall back to all four so the user can
  // still wire the node manually.
  const supported: readonly StreamHandle[] =
    isOutput || !data.streams || data.streams.length === 0
      ? STREAM_HANDLES
      : STREAM_HANDLES.filter((t) => data.streams!.includes(t));

  // Per-track source handle count for input nodes.
  // Prefer the stored audioTrackCount (set by configToFlow on load and
  // onProbedData after probing) over recomputing from raw probed data.
  const audioSrcCount: number = (() => {
    if (!isInput) return 1;
    if (typeof data.audioTrackCount === 'number' && data.audioTrackCount > 1) return data.audioTrackCount;
    if (Array.isArray(data.probed)) {
      const n = (data.probed as { type?: string }[]).filter((s) => s.type === 'audio').length;
      if (n > 1) return n;
    }
    return 1;
  })();

  // Per-slot input handle count for multi-input filter nodes (amerge etc.).
  const audioTgtCount: number = (() => {
    if (isInput || isOutput) return 1;
    const ref = data.ref;
    if (ref?.kind !== 'node') return 1;
    const def = ref.def as NodeDef;
    if (!MULTI_AUDIO_INPUT_FILTERS.has(def.filter ?? '')) return 1;
    const n = Number(def.params?.inputs ?? def.params?.nb_inputs ?? 2);
    return Number.isFinite(n) && n >= 2 ? n : 2;
  })();

  const maxAudioSlots = Math.max(audioSrcCount, audioTgtCount);

  // Slot top values. Audio expands vertically when multi-track/multi-input;
  // other stream types shift down accordingly so handles stay below audio.
  const slotTop = (t: StreamHandle, trackIdx = 0): number => {
    switch (t) {
      case 'video': return 16;
      case 'audio': return AUDIO_BASE + trackIdx * TRACK_PITCH;
      case 'subtitle': return AUDIO_BASE + maxAudioSlots * TRACK_PITCH;
      case 'data': return AUDIO_BASE + maxAudioSlots * TRACK_PITCH + SLOT_PITCH;
      default: return 16;
    }
  };

  // Ensure the node container is tall enough to contain all handle dots.
  const nodeMinHeight = maxAudioSlots > 1
    ? slotTop('data') + 12
    : undefined;

  const classes = [
    'mm-node',
    selected ? 'selected' : '',
    errored ? 'errored' : '',
    data.implicit ? 'implicit' : '',
    (isInput || isOutput) ? 'mm-node--io' : '',
  ]
    .filter(Boolean)
    .join(' ');

  return (
    <div className={classes} style={nodeMinHeight ? { minHeight: nodeMinHeight } : undefined}>
      {/* Target (left) handles */}
      {!isInput &&
        supported.flatMap((t) => {
          if (t === 'audio' && audioTgtCount > 1) {
            return Array.from({ length: audioTgtCount }, (_, i) => (
              <Fragment key={`tgt-audio-${i}`}>
                <span
                  className="handle-track-label handle-track-label--tgt"
                  style={{ top: slotTop('audio', i) }}
                  aria-hidden="true"
                >
                  {i}
                </span>
                <Handle
                  type="target"
                  position={Position.Left}
                  id={`audio:${i}`}
                  className="handle-audio"
                  style={{ top: slotTop('audio', i) }}
                />
              </Fragment>
            ));
          }
          return [
            <Handle
              key={`tgt-${t}`}
              type="target"
              position={Position.Left}
              id={t}
              className={`handle-${t}`}
              style={{ top: slotTop(t) }}
            />,
          ];
        })}

      <div className="mm-node-type">
        <span>{describeKind(data.kind, supported)}</span>
        {!data.implicit && (
          <button
            type="button"
            className="mm-node-edit"
            title="Open in Inspector"
            aria-label="Open this node's properties in the Inspector"
            onMouseDown={(e) => e.stopPropagation()}
            onClick={(e) => {
              e.stopPropagation();
              window.dispatchEvent(
                new CustomEvent(INSPECTOR_OPEN_EVENT, { detail: { id } }),
              );
            }}
          >
            ✎
          </button>
        )}
      </div>
      <div className="mm-node-title">{heading}</div>
      {data.sublabel && (isInput || isOutput) ? (
        <button
          type="button"
          className="mm-node-sub mm-node-sub-btn"
          title={data.sublabel as string}
          onMouseDown={(e) => e.stopPropagation()}
          onClick={(e) => {
            e.stopPropagation();
            window.dispatchEvent(new CustomEvent(URL_BROWSE_EVENT, { detail: { id } }));
          }}
        >
          {data.sublabel as string}
        </button>
      ) : (
        data.sublabel && <div className="mm-node-sub">{data.sublabel}</div>
      )}
      {(data.hwDevice || data.hwRoundTrip) && (
        <div className="mm-node-hw-row">
          {data.hwDevice && (
            <span className="hw-badge" title={`Hardware device: ${data.hwDevice}`}>
              ⊞ {data.hwDevice}
            </span>
          )}
          {data.hwRoundTrip && (
            <span className="hw-warn-badge" title="SW filter adjacent to HW node — forces hwdownload/hwupload round-trips">
              ⚠ sw/hw
            </span>
          )}
        </div>
      )}
      {run && (run.frames !== undefined || run.errors !== undefined) && (
        <div className="mm-node-run">
          {run.frames !== undefined && <span>{run.frames} pkt</span>}
          {run.fps !== undefined && run.fps > 0 && <span>{run.fps.toFixed(1)} fps</span>}
          {(run.errors ?? 0) > 0 && <span className="badge-err">{run.errors} err</span>}
        </div>
      )}

      {/* Source (right) handles */}
      {!isOutput &&
        supported.flatMap((t) => {
          if (t === 'audio' && audioSrcCount > 1) {
            return Array.from({ length: audioSrcCount }, (_, i) => (
              <Fragment key={`src-audio-${i}`}>
                <span
                  className="handle-track-label handle-track-label--src"
                  style={{ top: slotTop('audio', i) }}
                  aria-hidden="true"
                >
                  a:{i}
                </span>
                <Handle
                  type="source"
                  position={Position.Right}
                  id={`audio:${i}`}
                  className="handle-audio"
                  style={{ top: slotTop('audio', i) }}
                />
              </Fragment>
            ));
          }
          return [
            <Handle
              key={`src-${t}`}
              type="source"
              position={Position.Right}
              id={t}
              className={`handle-${t}`}
              style={{ top: slotTop(t) }}
            />,
          ];
        })}
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
    case 'device_input':
      return 'Capture device / Demux';
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
    case 'filter_source':
      return single ? `${cap(single)} virtual source` : 'Virtual source';
    case 'filter_sink':
      return single ? `${cap(single)} virtual sink` : 'Virtual sink';
    default:
      return kind;
  }
}
