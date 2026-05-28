import { Fragment } from 'react';
import { Handle, Position, useEdges, useNodes, type NodeProps } from '@xyflow/react';
import type { FlowNodeData } from '../lib/jsonAdapter';
import { MULTI_AUDIO_INPUT_FILTERS } from '../lib/jsonAdapter';
import type { NodeDef, ProbedStream } from '../lib/jobTypes';
import { displayName, lookupFriendlyName, useNamingMode } from '../lib/friendlyNames';

const STREAM_HANDLES = ['video', 'audio', 'subtitle', 'data', 'events', 'file'] as const;
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

// Named audio layouts with per-channel names in FFmpeg av_channel_layout_default order.
const AUDIO_LAYOUTS: { name: string; channels: number; chNames: string[] }[] = [
  { name: 'mono',   channels: 1, chNames: ['mono'] },
  { name: 'stereo', channels: 2, chNames: ['FL', 'FR'] },
  { name: '2.1',    channels: 3, chNames: ['FL', 'FR', 'LFE'] },
  { name: '3.0',    channels: 3, chNames: ['FL', 'FR', 'FC'] },
  { name: '4.0',    channels: 4, chNames: ['FL', 'FR', 'FC', 'BC'] },
  { name: '5.0',    channels: 5, chNames: ['FL', 'FR', 'FC', 'BL', 'BR'] },
  { name: '5.1',    channels: 6, chNames: ['FL', 'FR', 'FC', 'LFE', 'BL', 'BR'] },
  { name: '6.1',    channels: 7, chNames: ['FL', 'FR', 'FC', 'LFE', 'BL', 'BR', 'BC'] },
  { name: '7.1',    channels: 8, chNames: ['FL', 'FR', 'FC', 'LFE', 'BL', 'BR', 'SL', 'SR'] },
];

function layoutByName(name: string) {
  return AUDIO_LAYOUTS.find((l) => l.name === name);
}

// Fallback: channel names by count (for auto/unrecognised layout names).
function defaultChNames(n: number): string[] {
  return AUDIO_LAYOUTS.find((l) => l.channels === n)?.chNames
    ?? Array.from({ length: n }, (_, i) => `ch.${i + 1}`);
}

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
  // For audio encoders with channel_layout set, show one handle per channel.
  const audioTgtCount: number = (() => {
    if (isInput || isOutput) return 1;
    const ref = data.ref;
    if (ref?.kind !== 'node') return 1;
    const def = ref.def as NodeDef;
    if (def.type === 'encoder') {
      const layout = def.params?.channel_layout as string | undefined;
      if (layout) {
        const n = layoutByName(layout)?.channels ?? 0;
        if (n >= 2) return n;
      }
      return 1;
    }
    if (!MULTI_AUDIO_INPUT_FILTERS.has(def.filter ?? '')) return 1;
    const n = Number(def.params?.inputs ?? def.params?.nb_inputs ?? 2);
    return Number.isFinite(n) && n >= 2 ? n : 2;
  })();

  // Channel names for per-handle encoder targets.
  const encoderChNames: string[] | undefined = (() => {
    if (audioTgtCount <= 1) return undefined;
    const def = (data.ref?.kind === 'node' ? data.ref.def : undefined) as NodeDef | undefined;
    if (def?.type !== 'encoder') return undefined;
    const layout = def.params?.channel_layout as string | undefined;
    return layout ? layoutByName(layout)?.chNames : undefined;
  })();

  // For encoder/filter nodes with a single audio target handle, compute the
  // per-input channel assignment (amerge concatenates inputs in order, so each
  // inbound edge maps to consecutive output channels).
  const allEdges = useEdges();
  const allNodes = useNodes();
  const inboundAudioMappings: Array<{ trackLabel: string; outChans: string[] }> = (() => {
    if (audioTgtCount > 1) return [];
    const audioEdges = allEdges
      .filter((e) => e.target === id && (e.targetHandle === 'audio' || e.targetHandle?.startsWith('audio:')))
      .sort((a, b) => {
        const trackNum = (e: typeof a) =>
          parseInt((e.data as { rawFrom?: string } | undefined)?.rawFrom?.match(/:a:([0-9]+)$/)?.[1] ?? '0', 10);
        return trackNum(a) - trackNum(b);
      });
    if (audioEdges.length < 2) return [];

    // Look up channel count for each edge from the source node's probed data.
    const edgeChannels = audioEdges.map((e) => {
      const srcNode = allNodes.find((n) => n.id === e.source);
      const probed = (srcNode?.data as { probed?: ProbedStream[] } | undefined)?.probed;
      const trackIdx = parseInt((e.sourceHandle ?? '').replace(/^audio:/, ''), 10);
      const audioStreams = probed?.filter((s) => s.type === 'audio') ?? [];
      return audioStreams[trackIdx]?.channels ?? 1;
    });

    const totalChannels = edgeChannels.reduce((s, c) => s + c, 0);
    const layoutNames = defaultChNames(totalChannels);
    let offset = 0;
    return audioEdges.map((e, i) => {
      const raw: string = (e.data as { rawFrom?: string } | undefined)?.rawFrom ?? '';
      const m = raw.match(/:a:([0-9]+)$/);
      const trackLabel = m ? `a:${parseInt(m[1], 10) + 1}` : raw.split(':')[0];
      const nCh = edgeChannels[i];
      const outChans = layoutNames
        ? layoutNames.slice(offset, offset + nCh)
        : Array.from({ length: nCh }, (_, j) => `ch.${offset + j + 1}`);
      offset += nCh;
      return { trackLabel, outChans };
    });
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
      case 'events': return AUDIO_BASE + maxAudioSlots * TRACK_PITCH + SLOT_PITCH * 2;
      case 'file':   return AUDIO_BASE + maxAudioSlots * TRACK_PITCH + SLOT_PITCH * 3;
      default: return 16;
    }
  };

  // Ensure the node container is tall enough to contain all handle dots.
  const nodeMinHeight = maxAudioSlots > 1
    ? slotTop('file') + 12
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
          // file target handles only make sense on go_processor nodes.
          if (t === 'file' && data.kind !== 'go_processor') return [];
          if (t === 'audio' && audioTgtCount > 1) {
            return Array.from({ length: audioTgtCount }, (_, i) => (
              <Fragment key={`tgt-audio-${i}`}>
                <span
                  className="handle-track-label handle-track-label--tgt"
                  style={{ top: slotTop('audio', i) }}
                  aria-hidden="true"
                >
                  {encoderChNames?.[i] ?? i}
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
            <Fragment key={`tgt-${t}`}>
              {t === 'audio' && inboundAudioMappings.length > 0 && (
                <span
                  className="handle-track-label handle-track-label--tgt"
                  style={{ top: slotTop(t) }}
                  title={inboundAudioMappings.map((m) => `${m.trackLabel} → ${m.outChans.join(', ')}`).join('\n')}
                  aria-hidden="true"
                >
                  {inboundAudioMappings.map((m) => `${m.trackLabel}→${m.outChans.join(',')}`).join(' · ')}
                </span>
              )}
              <Handle
                key={`tgt-${t}`}
                type="target"
                position={Position.Left}
                id={t}
                className={`handle-${t}`}
                style={{ top: slotTop(t) }}
              />
            </Fragment>,
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

      {/* Events and file source handles on output nodes: events edges flow
          from an output (completion trigger) and file edges flow from an
          output (written file path) to downstream go_processor nodes. */}
      {isOutput && supported.includes('events') && (
        <Handle
          type="source"
          position={Position.Right}
          id="events"
          className="handle-events"
          style={{ top: slotTop('events') }}
        />
      )}
      {isOutput && (
        <Handle
          type="source"
          position={Position.Right}
          id="file"
          className="handle-file"
          style={{ top: slotTop('file') }}
        />
      )}

      {/* Source (right) handles */}
      {!isOutput &&
        supported.flatMap((t) => {
          if (t === 'audio' && audioSrcCount > 1) {
            const audioStreams = Array.isArray(data.probed)
              ? (data.probed as ProbedStream[]).filter((s) => s.type === 'audio')
              : [];
            return Array.from({ length: audioSrcCount }, (_, i) => {
              const s = audioStreams[i];
              const parts = [s?.channel_layout, s?.language].filter(Boolean);
              const meta = parts.join(' · ');
              const fullTitle = s
                ? [s.codec, s.channel_layout, s.language].filter(Boolean).join(' · ')
                : undefined;
              return (
              <Fragment key={`src-audio-${i}`}>
                <span
                  className="handle-track-label handle-track-label--src"
                  style={{ top: slotTop('audio', i) }}
                  title={fullTitle}
                  aria-hidden="true"
                >
                  a:{i + 1}{meta ? ` · ${meta}` : ''}
                </span>
                <Handle
                  type="source"
                  position={Position.Right}
                  id={`audio:${i}`}
                  className="handle-audio"
                  style={{ top: slotTop('audio', i) }}
                />
              </Fragment>
              );
            });
          }
          // Input nodes: audio source handle must use "audio:0" to match
          // the track-indexed sourceHandle that configToFlow assigns to
          // edges coming from input nodes (even single-track ones).
          // file source handles are only valid on input nodes.
          if (t === 'file' && !isInput) return [];
          const handleId = (isInput && t === 'audio') ? 'audio:0' : t;
          return [
            <Handle
              key={`src-${t}`}
              type="source"
              position={Position.Right}
              id={handleId}
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
