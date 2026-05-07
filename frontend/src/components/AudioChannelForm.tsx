/**
 * AudioChannelForm — matrix / routing editor for audio channel filters.
 *
 * Replaces the free-form params dict with a structured visual editor for
 * pan, channelmap, channelsplit, join, amerge, and amix.
 *
 *   pan        → 2-D gain matrix (output channels × input channels)
 *   channelmap → per-output-channel source dropdown
 *   channelsplit → input layout selector
 *   join       → per-output-channel (stream index + channel) source picker
 *   amerge     → input-count spinner
 *   amix       → input-count spinner + per-input weight fields + options
 *
 * All sub-forms write to def.params in the same key format the pipeline
 * backend expects, so the JSON round-trips without schema changes.
 */

import { useState, useMemo } from 'react';
import type { NodeDef } from '../lib/jobTypes';

// ── Channel layout catalogue ─────────────────────────────────────────────

const LAYOUTS: { name: string; chs: string[] }[] = [
  { name: 'mono',      chs: ['FC'] },
  { name: 'stereo',    chs: ['FL', 'FR'] },
  { name: '2.1',       chs: ['FL', 'FR', 'LFE'] },
  { name: '3.0',       chs: ['FL', 'FR', 'FC'] },
  { name: '4.0',       chs: ['FL', 'FR', 'FC', 'BC'] },
  { name: 'quad',      chs: ['FL', 'FR', 'BL', 'BR'] },
  { name: '4.1',       chs: ['FL', 'FR', 'FC', 'LFE', 'BC'] },
  { name: '5.0',       chs: ['FL', 'FR', 'FC', 'BL', 'BR'] },
  { name: '5.1',       chs: ['FL', 'FR', 'FC', 'LFE', 'BL', 'BR'] },
  { name: '6.1',       chs: ['FL', 'FR', 'FC', 'LFE', 'BC', 'SL', 'SR'] },
  { name: '7.0',       chs: ['FL', 'FR', 'FC', 'BL', 'BR', 'SL', 'SR'] },
  { name: '7.1',       chs: ['FL', 'FR', 'FC', 'LFE', 'BL', 'BR', 'SL', 'SR'] },
  { name: '7.1(wide)', chs: ['FL', 'FR', 'FC', 'LFE', 'BL', 'BR', 'FLC', 'FRC'] },
  { name: 'downmix',   chs: ['DL', 'DR'] },
];

// All well-known FFmpeg channel names for input-channel dropdowns
const ALL_CHANNEL_NAMES = [
  'FL', 'FR', 'FC', 'LFE', 'BL', 'BR', 'FLC', 'FRC',
  'BC', 'SL', 'SR', 'TFL', 'TFC', 'TFR', 'TBL', 'TBC', 'TBR',
  'DL', 'DR',
];

function layoutChs(name: string): string[] {
  return LAYOUTS.find((l) => l.name === name)?.chs ?? [];
}

function isKnownLayout(name: string): boolean {
  return LAYOUTS.some((l) => l.name === name);
}

/** Convert positional channel names (c0, c1, …) to named using a reference layout. */
function posToNamed(ch: string, ref: string[]): string {
  const m = /^c(\d+)$/.exec(ch);
  if (m) return ref[parseInt(m[1])] ?? ch;
  return ch;
}

// ── Public API ────────────────────────────────────────────────────────────

/** Filters handled by this component instead of the generic FilterForm options. */
export const AUDIO_ROUTING_FILTERS = new Set([
  'pan', 'channelmap', 'channelsplit', 'join', 'amerge', 'amix',
]);

interface Props {
  def: NodeDef;
  onChange: (next: NodeDef) => void;
}

export function AudioChannelForm({ def, onChange }: Props) {
  const f = def.filter ?? '';
  if (f === 'pan')          return <PanForm          def={def} onChange={onChange} />;
  if (f === 'channelmap')   return <ChannelMapForm   def={def} onChange={onChange} />;
  if (f === 'channelsplit') return <ChannelSplitForm def={def} onChange={onChange} />;
  if (f === 'join')         return <JoinForm         def={def} onChange={onChange} />;
  if (f === 'amerge')       return <AMergeForm       def={def} onChange={onChange} />;
  if (f === 'amix')         return <AMixForm         def={def} onChange={onChange} />;
  return null;
}

// ── Shared sub-components ────────────────────────────────────────────────

/** Text input with a datalist for standard layout autocomplete + dim channel list hint. */
function LayoutInput({
  id,
  label,
  value,
  onChange: onChangeProp,
}: {
  id: string;
  label: string;
  value: string;
  onChange: (v: string) => void;
}) {
  const chs = layoutChs(value);
  return (
    <div className="ch-routing-layout-row">
      <label htmlFor={id}>{label}</label>
      <input
        id={id}
        type="text"
        list={`${id}-dl`}
        value={value}
        onChange={(e) => onChangeProp(e.target.value)}
        placeholder="e.g. stereo"
      />
      <datalist id={`${id}-dl`}>
        {LAYOUTS.map((l) => <option key={l.name} value={l.name} />)}
      </datalist>
      {chs.length > 0 && (
        <span className="ch-routing-chs-hint">{chs.join(' · ')}</span>
      )}
    </div>
  );
}

/** Channel dropdown listing all well-known names. */
function ChSelect({
  value,
  onChange: onChangeProp,
  placeholder,
}: {
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
}) {
  return (
    <select value={value} onChange={(e) => onChangeProp(e.target.value)}>
      {placeholder && <option value="">{placeholder}</option>}
      {ALL_CHANNEL_NAMES.map((ch) => (
        <option key={ch} value={ch}>{ch}</option>
      ))}
    </select>
  );
}

/** Monospace spec preview at the bottom of each sub-form. */
function SpecPreview({ label, value }: { label: string; value: string }) {
  return (
    <div className="ch-routing-preview">
      <span className="ch-routing-preview-label">{label}</span>
      <code>{value || '—'}</code>
    </div>
  );
}

// ── pan filter ────────────────────────────────────────────────────────────
//
// Spec:   pan=outLayout|OUTCH=gain1*INCH1+gain2*INCH2|…
// Stored: params._pos0 = "stereo|FL=0.5*FL+0.5*FR|FR=0.5*FL+0.5*FR"
//
// The gain matrix rows are output channels; columns are input channels.
// Coefficient 1 is shown as "1" (or the channel name alone in the spec).
// Empty / "0" cells are omitted from the serialised spec.

type GainMatrix = Record<string, Record<string, string>>;

function parsePanSpec(spec: string): { outLayout: string; gains: GainMatrix } {
  if (!spec) return { outLayout: 'stereo', gains: {} };
  const parts = spec.split('|');
  const outLayout = (parts[0] ?? '').trim();
  const outChs = layoutChs(outLayout);
  const gains: GainMatrix = {};

  for (let i = 1; i < parts.length; i++) {
    const part = parts[i].trim();
    const sepIdx = part.search(/[=<]/);
    if (sepIdx < 0) continue;
    const rawOutCh = part.slice(0, sepIdx).trim();
    const expr = part.slice(sepIdx + 1).trim();
    const outCh = posToNamed(rawOutCh, outChs);
    gains[outCh] ??= {};

    for (const rawTerm of expr.split('+')) {
      const term = rawTerm.trim();
      if (!term || term === '0') continue;
      const star = term.indexOf('*');
      if (star >= 0) {
        const gain = term.slice(0, star).trim();
        const inCh = posToNamed(term.slice(star + 1).trim(), outChs);
        gains[outCh][inCh] = gain;
      } else {
        gains[outCh][posToNamed(term, outChs)] = '1';
      }
    }
  }
  return { outLayout, gains };
}

function serializePanGains(outLayout: string, gains: GainMatrix): string {
  const outChs = layoutChs(outLayout);
  if (outChs.length === 0) return outLayout;
  const parts: string[] = [outLayout];
  for (const outCh of outChs) {
    const chGains = gains[outCh] ?? {};
    const terms = Object.entries(chGains)
      .filter(([, v]) => v !== '' && v !== '0' && v !== '0.0')
      .map(([inCh, v]) => (v === '1' || v === '1.0' ? inCh : `${v}*${inCh}`));
    parts.push(`${outCh}=${terms.length > 0 ? terms.join('+') : '0'}`);
  }
  return parts.join('|');
}

function PanForm({ def, onChange }: Props) {
  const pos0 = String(def.params?.['_pos0'] ?? '');
  const parsed = useMemo(() => parsePanSpec(pos0), [pos0]);

  // inLayout is local UI state — controls which channels appear as matrix columns.
  // Inferred from existing gain channel names on first render.
  const initialInLayout = useMemo(() => {
    const inChs = new Set<string>();
    for (const chGains of Object.values(parsed.gains)) {
      Object.keys(chGains).forEach((ch) => inChs.add(ch));
    }
    if (inChs.size === 0) return parsed.outLayout || 'stereo';
    for (const l of LAYOUTS) {
      if ([...inChs].every((ch) => l.chs.includes(ch))) return l.name;
    }
    return 'stereo';
  // Only recalculate on mount — inLayout is a UI-only hint, not data.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const [inLayout, setInLayout] = useState(initialInLayout);

  const inChs = layoutChs(inLayout);
  const outChs = layoutChs(parsed.outLayout);
  const unknownOut = parsed.outLayout !== '' && !isKnownLayout(parsed.outLayout);

  const setGain = (outCh: string, inCh: string, value: string) => {
    const newGains: GainMatrix = {
      ...parsed.gains,
      [outCh]: { ...(parsed.gains[outCh] ?? {}), [inCh]: value },
    };
    onChange({
      ...def,
      params: { ...def.params, _pos0: serializePanGains(parsed.outLayout, newGains) },
    });
  };

  const setOutLayout = (layout: string) => {
    const newChs = new Set(layoutChs(layout));
    const newGains: GainMatrix = {};
    for (const [ch, v] of Object.entries(parsed.gains)) {
      if (newChs.has(ch)) newGains[ch] = v;
    }
    onChange({
      ...def,
      params: { ...def.params, _pos0: serializePanGains(layout, newGains) },
    });
  };

  return (
    <div className="ch-routing">
      <LayoutInput id="pan-in"  label="Input channels"  value={inLayout}          onChange={setInLayout} />
      <LayoutInput id="pan-out" label="Output layout"   value={parsed.outLayout} onChange={setOutLayout} />

      {unknownOut ? (
        <div className="ch-routing-notice">
          Layout <code>{parsed.outLayout}</code> is not a standard preset — edit
          the spec in JSON or choose a preset above.
        </div>
      ) : outChs.length === 0 ? (
        <div className="ch-routing-notice">
          Choose an output layout above to show the gain matrix.
        </div>
      ) : inChs.length === 0 ? (
        <div className="ch-routing-notice">
          Choose an input layout above to show the gain matrix columns.
        </div>
      ) : (
        <>
          <div className="ch-routing-section-label">
            Gain matrix — coefficient per cell (0 = mute, 1 = full, 0.5 = half)
          </div>
          {/* Horizontal scroll wrapper for wide matrices (7.1 etc.) */}
          <div className="ch-matrix-wrapper">
            <div
              className="ch-matrix"
              style={{ gridTemplateColumns: `36px repeat(${inChs.length}, 52px)` }}
            >
              {/* Header row */}
              <div className="ch-matrix-corner" />
              {inChs.map((ch) => (
                <div key={ch} className="ch-matrix-header-cell">{ch}</div>
              ))}

              {/* Data rows — flat list; CSS grid auto-places into rows */}
              {outChs.flatMap((outCh) => [
                <div key={`rl-${outCh}`} className="ch-matrix-row-label">{outCh}</div>,
                ...inChs.map((inCh) => {
                  const v = String(parsed.gains[outCh]?.[inCh] ?? '');
                  const active = v !== '' && v !== '0' && v !== '0.0';
                  return (
                    <div
                      key={`c-${outCh}-${inCh}`}
                      className={`ch-matrix-cell${active ? ' ch-matrix-cell-active' : ''}`}
                    >
                      <input
                        type="number"
                        min={0}
                        step={0.1}
                        value={v}
                        placeholder="0"
                        onChange={(e) => setGain(outCh, inCh, e.target.value)}
                      />
                    </div>
                  );
                }),
              ])}
            </div>
          </div>
        </>
      )}

      <SpecPreview label="pan=" value={pos0} />
    </div>
  );
}

// ── channelmap filter ─────────────────────────────────────────────────────
//
// params.channel_layout = 'stereo'     — output layout
// params.map            = 'FR-FL|FL-FR' — IN_CH-OUT_CH pairs; e.g. "FR-FL"
//                                         means: take input FR → put in output FL.
//
// UI: for each output channel, a dropdown selecting the source input channel.

function parseChannelMapStr(mapStr: string): Record<string, string> {
  // Returns: outputCh → inputCh
  const result: Record<string, string> = {};
  for (const pair of String(mapStr || '').split('|')) {
    const dash = pair.trim().indexOf('-');
    if (dash < 0) continue;
    const from = pair.slice(0, dash).trim();
    const to   = pair.slice(dash + 1).trim();
    if (from && to) result[to] = from;
  }
  return result;
}

function serializeChannelMapStr(mapping: Record<string, string>, outChs: string[]): string {
  return outChs
    .filter((ch) => mapping[ch])
    .map((ch) => `${mapping[ch]}-${ch}`)
    .join('|');
}

function ChannelMapForm({ def, onChange }: Props) {
  const outLayout = String(def.params?.['channel_layout'] ?? 'stereo');
  const mapStr    = String(def.params?.['map'] ?? '');
  const mapping   = useMemo(() => parseChannelMapStr(mapStr), [mapStr]);
  const outChs    = layoutChs(outLayout);

  const setOutLayout = (v: string) =>
    onChange({ ...def, params: { ...def.params, channel_layout: v } });

  const setSource = (outCh: string, inCh: string) => {
    const m = { ...mapping, [outCh]: inCh };
    onChange({ ...def, params: { ...def.params, map: serializeChannelMapStr(m, layoutChs(outLayout)) } });
  };

  return (
    <div className="ch-routing">
      <LayoutInput id="cmap-out" label="Output layout" value={outLayout} onChange={setOutLayout} />

      {outChs.length > 0 ? (
        <div className="ch-routing-map-list">
          <div className="ch-routing-map-header">
            <span className="ch-routing-map-ch">Out</span>
            <span />
            <span>Source (input channel)</span>
          </div>
          {outChs.map((outCh) => (
            <div key={outCh} className="ch-routing-map-row">
              <span className="ch-routing-map-ch">{outCh}</span>
              <span className="ch-routing-map-arrow">←</span>
              <ChSelect value={mapping[outCh] ?? outCh} onChange={(v) => setSource(outCh, v)} />
            </div>
          ))}
        </div>
      ) : (
        <div className="ch-routing-notice">Select an output layout above.</div>
      )}

      <SpecPreview label="map=" value={serializeChannelMapStr(mapping, outChs)} />
    </div>
  );
}

// ── channelsplit filter ───────────────────────────────────────────────────
//
// params.channel_layout = 'stereo'
// Produces one output pad per channel in the layout; no mapping needed.

function ChannelSplitForm({ def, onChange }: Props) {
  const layout = String(def.params?.['channel_layout'] ?? 'stereo');
  const chs    = layoutChs(layout);

  const setLayout = (v: string) =>
    onChange({ ...def, params: { ...def.params, channel_layout: v } });

  return (
    <div className="ch-routing">
      <LayoutInput id="csplit-layout" label="Input layout" value={layout} onChange={setLayout} />
      {chs.length > 0 && (
        <div className="ch-routing-notice">
          Splits into {chs.length} output pad{chs.length !== 1 ? 's' : ''}:{' '}
          {chs.map((ch, i) => (
            <span key={ch}><code>{ch}</code>{i < chs.length - 1 ? ', ' : ''}</span>
          ))}
          . Connect each pad to a separate downstream filter or encoder.
        </div>
      )}
    </div>
  );
}

// ── join filter ───────────────────────────────────────────────────────────
//
// params.inputs         = '2'
// params.channel_layout = 'stereo'
// params.map            = '0.0-FL|1.0-FR'   — IN_STREAM.IN_CH-OUT_CH
//
// UI: for each output channel, select the source [stream index, channel].

interface JoinEntry { stream: string; ch: string }

function parseJoinMapStr(mapStr: string): Record<string, JoinEntry> {
  const result: Record<string, JoinEntry> = {};
  for (const part of String(mapStr || '').split('|')) {
    // Expected format: "0.FL-FR" or "0.0-FL" (stream.inCh-outCh)
    const m = /^(\d+)\.(\w+)-(\w+)$/.exec(part.trim());
    if (m) result[m[3]] = { stream: m[1], ch: m[2] };
  }
  return result;
}

function serializeJoinMapStr(mapping: Record<string, JoinEntry>, outChs: string[]): string {
  return outChs
    .filter((ch) => mapping[ch]?.ch)
    .map((ch) => `${mapping[ch].stream}.${mapping[ch].ch}-${ch}`)
    .join('|');
}

function JoinForm({ def, onChange }: Props) {
  const outLayout = String(def.params?.['channel_layout'] ?? 'stereo');
  const inputsN   = Math.max(2, Number(def.params?.['inputs'] ?? 2));
  const mapStr    = String(def.params?.['map'] ?? '');
  const outChs    = layoutChs(outLayout);
  const mapping   = useMemo(() => parseJoinMapStr(mapStr), [mapStr]);

  const streamOpts = Array.from({ length: inputsN }, (_, i) => String(i));

  const setOutLayout = (v: string) =>
    onChange({ ...def, params: { ...def.params, channel_layout: v } });

  const setInputs = (n: number) =>
    onChange({ ...def, params: { ...def.params, inputs: String(n) } });

  const setEntry = (outCh: string, entry: JoinEntry) => {
    const m = { ...mapping, [outCh]: entry };
    onChange({ ...def, params: { ...def.params, map: serializeJoinMapStr(m, layoutChs(outLayout)) } });
  };

  return (
    <div className="ch-routing">
      <div className="ch-routing-layout-row">
        <label htmlFor="join-inputs">Input streams</label>
        <input
          id="join-inputs"
          type="number"
          min={2}
          max={64}
          value={inputsN}
          onChange={(e) => setInputs(Math.max(2, parseInt(e.target.value) || 2))}
          style={{ width: 60 }}
        />
      </div>
      <LayoutInput id="join-out" label="Output layout" value={outLayout} onChange={setOutLayout} />

      {outChs.length > 0 ? (
        <div className="ch-routing-map-list">
          <div className="ch-routing-map-header">
            <span className="ch-routing-map-ch">Out</span>
            <span />
            <span>Stream</span>
            <span>Channel</span>
          </div>
          {outChs.map((outCh) => {
            const entry = mapping[outCh] ?? { stream: '0', ch: outCh };
            return (
              <div key={outCh} className="ch-routing-map-row">
                <span className="ch-routing-map-ch">{outCh}</span>
                <span className="ch-routing-map-arrow">←</span>
                <select
                  value={entry.stream}
                  onChange={(e) => setEntry(outCh, { ...entry, stream: e.target.value })}
                  style={{ width: 48 }}
                >
                  {streamOpts.map((s) => (
                    <option key={s} value={s}>#{s}</option>
                  ))}
                </select>
                <ChSelect value={entry.ch} onChange={(ch) => setEntry(outCh, { ...entry, ch })} />
              </div>
            );
          })}
        </div>
      ) : (
        <div className="ch-routing-notice">Select an output layout above.</div>
      )}

      <SpecPreview label="map=" value={serializeJoinMapStr(mapping, outChs)} />
    </div>
  );
}

// ── amerge filter ─────────────────────────────────────────────────────────
//
// params.inputs = '2'   — number of input audio streams to merge
//
// Each input must have a distinct channel layout. The merged output
// carries the union of all input layouts.

function AMergeForm({ def, onChange }: Props) {
  const inputs = Math.max(2, Number(def.params?.['inputs'] ?? 2));

  const setInputs = (n: number) =>
    onChange({ ...def, params: { ...def.params, inputs: String(n) } });

  return (
    <div className="ch-routing">
      <div className="ch-routing-layout-row">
        <label htmlFor="amerge-inputs">Input streams to merge</label>
        <input
          id="amerge-inputs"
          type="number"
          min={2}
          max={64}
          value={inputs}
          onChange={(e) => setInputs(Math.max(2, parseInt(e.target.value) || 2))}
          style={{ width: 60 }}
        />
      </div>
      <div className="ch-routing-notice">
        Merges {inputs} audio streams into one multi-channel stream. Each input
        must have a distinct channel layout (e.g. two mono streams → stereo;
        stereo + stereo → quad). libavfilter rejects overlapping layouts.
      </div>
    </div>
  );
}

// ── amix filter ───────────────────────────────────────────────────────────
//
// params.inputs              = '2'             — number of input streams
// params.weights             = '0.7 0.3'       — space-separated per-input gains
// params.duration            = 'longest'|…
// params.normalize           = 'true'|'false'
// params.dropout_transition  = '2'             — fade-out duration (s) on EOS

function AMixForm({ def, onChange }: Props) {
  const inputs   = Math.max(2, Number(def.params?.['inputs'] ?? 2));
  const rawW     = String(def.params?.['weights'] ?? '');
  const duration = String(def.params?.['duration'] ?? '');
  const normalize = String(def.params?.['normalize'] ?? '');
  const dropout  = String(def.params?.['dropout_transition'] ?? '');

  const weightArr = useMemo(() => {
    const parts = rawW.trim() ? rawW.trim().split(/\s+/) : [];
    return Array.from({ length: inputs }, (_, i) => parts[i] ?? '1');
  }, [rawW, inputs]);

  const setInputs = (n: number) => {
    const newW = Array.from({ length: n }, (_, i) => weightArr[i] ?? '1');
    onChange({ ...def, params: { ...def.params, inputs: String(n), weights: newW.join(' ') } });
  };

  const setWeight = (idx: number, v: string) => {
    const newW = weightArr.map((w, i) => (i === idx ? v : w));
    onChange({ ...def, params: { ...def.params, weights: newW.join(' ') } });
  };

  const setOpt = (key: string, v: string) => {
    const next = { ...def.params };
    if (v === '') delete next[key];
    else next[key] = v;
    onChange({ ...def, params: next });
  };

  return (
    <div className="ch-routing">
      <div className="ch-routing-layout-row">
        <label htmlFor="amix-inputs">Input streams</label>
        <input
          id="amix-inputs"
          type="number"
          min={2}
          max={64}
          value={inputs}
          onChange={(e) => setInputs(Math.max(2, parseInt(e.target.value) || 2))}
          style={{ width: 60 }}
        />
      </div>

      <div className="ch-routing-section-label">Mix weights (one per input)</div>
      <div className="ch-routing-weights">
        {weightArr.map((w, i) => (
          <div key={i} className="ch-routing-weight-row">
            <span className="ch-routing-map-ch">#{i}</span>
            <input
              type="number"
              min={0}
              step={0.05}
              value={w}
              placeholder="1"
              onChange={(e) => setWeight(i, e.target.value)}
            />
          </div>
        ))}
      </div>

      <div className="ch-routing-section-label">Options</div>
      <div className="ch-routing-layout-row">
        <label htmlFor="amix-duration">Duration policy</label>
        <select
          id="amix-duration"
          value={duration}
          onChange={(e) => setOpt('duration', e.target.value)}
        >
          <option value="">(default — longest)</option>
          <option value="longest">longest</option>
          <option value="shortest">shortest</option>
          <option value="first">first</option>
        </select>
      </div>
      <div className="ch-routing-layout-row">
        <label htmlFor="amix-normalize">Normalize</label>
        <select
          id="amix-normalize"
          value={normalize}
          onChange={(e) => setOpt('normalize', e.target.value)}
        >
          <option value="">(default — true)</option>
          <option value="true">true — rescale so total weights sum to 1</option>
          <option value="false">false — pass weights through unchanged</option>
        </select>
      </div>
      <div className="ch-routing-layout-row">
        <label htmlFor="amix-dropout">Dropout transition (s)</label>
        <input
          id="amix-dropout"
          type="number"
          min={0}
          step={0.1}
          value={dropout}
          placeholder="2.0"
          onChange={(e) => setOpt('dropout_transition', e.target.value)}
          style={{ width: 80 }}
        />
      </div>
    </div>
  );
}
