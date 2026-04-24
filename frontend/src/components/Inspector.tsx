import { useEffect, useState } from 'react';
import type { FlowNode } from '../lib/jsonAdapter';
import { displayUrl, nodeDisplayLabel, nodeDisplaySublabel } from '../lib/jsonAdapter';
import type { Input, NodeDef, Output, ProbeResponse, ProbedStream } from '../lib/jobTypes';
import { MEDIA_FILE_EXTENSIONS } from '../lib/mediaExtensions';
import { FileBrowser, type BrowseMode } from './FileBrowser';
import { EncoderForm } from './EncoderForm';
import { describeKind } from './MMNode';

interface Props {
  node: FlowNode | null;
  onChange: (next: FlowNode) => void;
  onDelete: (id: string) => void;
}

export function Inspector({ node, onChange, onDelete }: Props) {
  if (!node) {
    return (
      <div className="inspector">
        <h3>Inspector</h3>
        <div className="empty">Select a node to view its properties.</div>
      </div>
    );
  }

  const ref = node.data.ref;

  if (node.data.implicit) {
    return (
      <div className="inspector">
        <div className="inspector-header">
          <h3>{node.data.label}</h3>
        </div>
        <div className="mm-node-type" style={{ marginBottom: 12 }}>
          {describeKind(node.data.kind, node.data.streams ?? [])} (implicit)
        </div>
        {node.data.sublabel && (
          <div style={{ fontSize: 12, color: 'var(--text-dim)', marginBottom: 12 }}>
            {node.data.sublabel}
          </div>
        )}
        <p style={{ fontSize: 12, color: 'var(--text-dim)', lineHeight: 1.5 }}>
          This stage is auto-generated from the surrounding inputs and outputs.
          The runtime instantiates it on your behalf — to change it, edit the
          input or output it belongs to.
        </p>
      </div>
    );
  }

  return (
    <div className="inspector">
      <div className="inspector-header">
        <h3>{node.data.label}</h3>
        <button className="danger" onClick={() => onDelete(node.id)}>Delete</button>
      </div>
      <div className="mm-node-type" style={{ marginBottom: 12 }}>
        {describeKind(node.data.kind, node.data.streams ?? [])}
      </div>

      {ref.kind === 'input' && (
        <InputForm
          def={ref.def}
          probed={node.data.probed}
          onChange={(def) => onChange(updateRef(node, { kind: 'input', def }, def.id, displayUrl(def.url)))}
          onProbed={(probed) =>
            onChange({ ...node, data: { ...node.data, probed } } as FlowNode)
          }
        />
      )}
      {ref.kind === 'output' && (
        <OutputForm
          def={ref.def}
          onChange={(def) => onChange(updateRef(node, { kind: 'output', def }, def.id, displayUrl(def.url)))}
        />
      )}
      {ref.kind === 'node' && (
        <NodeForm
          def={ref.def}
          onChange={(def) =>
            onChange(updateRef(node, { kind: 'node', def }, nodeDisplayLabel(def), nodeDisplaySublabel(def)))
          }
        />
      )}
    </div>
  );
}

function updateRef(node: FlowNode, ref: FlowNode['data']['ref'], label: string, sublabel: string): FlowNode {
  return {
    ...node,
    data: { ...node.data, ref, label, sublabel },
  };
}

/* ---------- Input form ---------- */
function InputForm({
  def,
  probed,
  onChange,
  onProbed,
}: {
  def: Input;
  probed?: ProbedStream[];
  onChange: (next: Input) => void;
  onProbed: (next: ProbedStream[] | undefined) => void;
}) {
  const [probing, setProbing] = useState(false);
  const [probeError, setProbeError] = useState<string | null>(null);

  const runProbe = async () => {
    if (!def.url) {
      setProbeError('Set a URL first.');
      return;
    }
    setProbing(true);
    setProbeError(null);
    try {
      const r = await fetch('/api/probe', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ url: def.url, options: def.options }),
      });
      if (!r.ok) {
        const body = await r.text();
        throw new Error(body || `HTTP ${r.status}`);
      }
      const resp = (await r.json()) as ProbeResponse;
      onProbed(resp.streams);
    } catch (err) {
      setProbeError((err as Error).message);
      onProbed(undefined);
    } finally {
      setProbing(false);
    }
  };

  return (
    <>
      <Field label="ID" value={def.id} onChange={(v) => onChange({ ...def, id: v })} />
      <FileField
        label="URL"
        value={def.url}
        mode="open"
        filter={MEDIA_FILE_EXTENSIONS}
        onChange={(v) => {
          onChange({ ...def, url: v });
          // Stale once the URL changes.
          if (probed) onProbed(undefined);
        }}
      />
      <div className="probe-actions">
        {def.url && def.url.trim() ? (
          <button onClick={runProbe} disabled={probing}>
            {probing ? 'Probing…' : 'Get properties'}
          </button>
        ) : (
          <div className="empty" style={{ fontSize: 11 }}>
            Set a URL above to probe the file's technical properties.
          </div>
        )}
        {probed && (
          <button className="link-btn" onClick={() => onProbed(undefined)} title="Discard probed metadata">
            Clear
          </button>
        )}
      </div>
      {probeError && <div className="probe-error">{probeError}</div>}
      {probed && <ProbedStreamsView streams={probed} />}
    </>
  );
}

/** Read-only summary of probed streams. Drives the canvas edge attribute
 *  chips via FlowNodeData.probed (see lib/streamAttrs.ts). */
function ProbedStreamsView({ streams }: { streams: ProbedStream[] }) {
  if (!streams.length) return <div className="empty">No streams reported.</div>;
  return (
    <div className="probed-streams">
      <label style={{ marginTop: 12 }}>Probed streams</label>
      {streams.map((s) => (
        <div key={s.index} className="probed-stream">
          <div className="probed-stream-head">
            <span className={`stream-pill stream-${s.type}`}>{s.type}</span>
            <span className="probed-stream-idx">#{s.index}</span>
            {s.codec && <span className="probed-stream-codec">{s.codec}</span>}
            {s.codec_tag && <span className="probed-stream-codec">{s.codec_tag}</span>}
          </div>
          <dl className="probed-stream-attrs">
            {/* Common */}
            {s.bit_rate ? <Pair k="bit_rate" v={formatBitRate(s.bit_rate)} /> : null}
            {s.profile && <Pair k="profile" v={s.profile + (s.level ? `@L${formatLevel(s.profile, s.level)}` : '')} />}
            {s.bit_depth ? <Pair k="bit_depth" v={`${s.bit_depth} bit`} /> : null}
            {/* Video */}
            {s.width && s.height && <Pair k="size" v={`${s.width}×${s.height}`} />}
            {s.sar && s.sar !== '1:1' && <Pair k="sar" v={s.sar} />}
            {s.pix_fmt && <Pair k="pix_fmt" v={s.pix_fmt} />}
            {s.frame_rate && <Pair k="frame_rate" v={`${s.frame_rate} fps`} />}
            {s.r_frame_rate && <Pair k="r_frame_rate" v={`${s.r_frame_rate} fps`} />}
            {s.field_order && s.field_order !== 'progressive' && <Pair k="field_order" v={s.field_order} />}
            {s.color_space && <Pair k="color_space" v={s.color_space} />}
            {s.color_range && <Pair k="color_range" v={s.color_range} />}
            {s.color_primaries && <Pair k="color_primaries" v={s.color_primaries} />}
            {s.color_transfer && <Pair k="color_transfer" v={s.color_transfer} />}
            {/* Audio */}
            {s.sample_rate ? <Pair k="sample_rate" v={`${s.sample_rate} Hz`} /> : null}
            {s.sample_fmt && <Pair k="sample_fmt" v={s.sample_fmt} />}
            {s.channel_layout && <Pair k="channels" v={`${s.channels ?? '?'} (${s.channel_layout})`} />}
            {/* Timing */}
            {s.duration_sec ? <Pair k="duration" v={formatDuration(s.duration_sec)} /> : null}
            {s.start_sec ? <Pair k="start" v={`${s.start_sec.toFixed(3)} s`} /> : null}
          </dl>
        </div>
      ))}
    </div>
  );
}

function formatBitRate(bps: number): string {
  if (bps >= 1_000_000) return `${(bps / 1_000_000).toFixed(2)} Mbps`;
  if (bps >= 1_000) return `${(bps / 1_000).toFixed(0)} kbps`;
  return `${bps} bps`;
}

function formatDuration(sec: number): string {
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const s = sec - h * 3600 - m * 60;
  if (h > 0) return `${h}:${String(m).padStart(2, '0')}:${s.toFixed(2).padStart(5, '0')}`;
  return `${m}:${s.toFixed(2).padStart(5, '0')} (${sec.toFixed(2)} s)`;
}

// H.264 levels are reported as integers like 41 (=> 4.1). Other codecs use
// the raw value. Render H.264/HEVC profiles with a decimal level.
function formatLevel(profile: string, level: number): string {
  const p = profile.toLowerCase();
  if (p.includes('h.264') || p.includes('avc') || level >= 10) {
    return `${Math.floor(level / 10)}.${level % 10}`;
  }
  return String(level);
}

function Pair({ k, v }: { k: string; v: string }) {
  return (
    <>
      <dt>{k}</dt>
      <dd>{v}</dd>
    </>
  );
}

/* ---------- Output form ---------- */
function OutputForm({ def, onChange }: { def: Output; onChange: (next: Output) => void }) {
  return (
    <>
      <Field label="ID" value={def.id} onChange={(v) => onChange({ ...def, id: v })} />
      <FileField
        label="URL"
        value={def.url}
        mode="save"
        defaultFilename="output.mp4"
        onChange={(v) => onChange({ ...def, url: v })}
      />
      <Field label="Format" value={def.format ?? ''} onChange={(v) => onChange({ ...def, format: v || undefined })} />
      <Field
        label="Codec (video)"
        value={def.codec_video ?? ''}
        onChange={(v) => onChange({ ...def, codec_video: v || undefined })}
      />
      <Field
        label="Codec (audio)"
        value={def.codec_audio ?? ''}
        onChange={(v) => onChange({ ...def, codec_audio: v || undefined })}
      />
    </>
  );
}

/* ---------- Graph node form ---------- */
function NodeForm({ def, onChange }: { def: NodeDef; onChange: (next: NodeDef) => void }) {
  return (
    <>
      <Field label="ID" value={def.id} onChange={(v) => onChange({ ...def, id: v })} />
      <Field label="Type" value={def.type} onChange={(v) => onChange({ ...def, type: v })} />
      {def.type === 'filter' && (
        <Field
          label="Filter"
          value={def.filter ?? ''}
          onChange={(v) => onChange({ ...def, filter: v || undefined })}
        />
      )}
      {def.type === 'go_processor' && (
        <Field
          label="Processor"
          value={def.processor ?? ''}
          onChange={(v) => onChange({ ...def, processor: v || undefined })}
        />
      )}
      {def.type === 'encoder' && <EncoderForm def={def} onChange={onChange} />}
      <ParamsEditor params={def.params ?? {}} onChange={(p) => onChange({ ...def, params: p })} />
    </>
  );
}

/* ---------- Params editor (key/value rows) ---------- */
function ParamsEditor({
  params,
  onChange,
}: {
  params: Record<string, unknown>;
  onChange: (next: Record<string, unknown>) => void;
}) {
  const entries = Object.entries(params);

  const update = (i: number, key: string, value: string) => {
    const next: Record<string, unknown> = {};
    entries.forEach(([k, v], idx) => {
      if (idx === i) next[key] = value;
      else next[k] = v;
    });
    onChange(next);
  };
  const remove = (i: number) => {
    const next: Record<string, unknown> = {};
    entries.forEach(([k, v], idx) => {
      if (idx !== i) next[k] = v;
    });
    onChange(next);
  };
  const add = () => {
    onChange({ ...params, '': '' });
  };

  return (
    <>
      <label style={{ marginTop: 14 }}>Params</label>
      {entries.length === 0 && <div className="empty" style={{ marginTop: 4 }}>No params.</div>}
      {entries.map(([k, v], i) => (
        <div key={i} className="param-row">
          <input
            value={k}
            placeholder="key"
            onChange={(e) => update(i, e.target.value, String(v ?? ''))}
          />
          <input
            value={String(v ?? '')}
            placeholder="value"
            onChange={(e) => update(i, k, e.target.value)}
          />
          <button onClick={() => remove(i)} title="Remove">×</button>
        </div>
      ))}
      <button style={{ marginTop: 6 }} onClick={add}>+ add param</button>
    </>
  );
}

/* ---------- Tiny controlled text field ---------- */
function Field({ label, value, onChange }: { label: string; value: string; onChange: (v: string) => void }) {
  // Local state lets the user type freely; commit on blur to avoid touching
  // every parent state on every keystroke. Sync down when the prop changes
  // (e.g. user selects a different node).
  const [local, setLocal] = useState(value);
  useEffect(() => setLocal(value), [value]);
  return (
    <>
      <label>{label}</label>
      <input
        value={local}
        onChange={(e) => setLocal(e.target.value)}
        onBlur={() => {
          if (local !== value) onChange(local);
        }}
      />
    </>
  );
}

/* ---------- File-browser-aware text field ---------- */
function FileField({
  label,
  value,
  mode,
  filter,
  defaultFilename,
  onChange,
}: {
  label: string;
  value: string;
  mode: BrowseMode;
  filter?: string;
  defaultFilename?: string;
  onChange: (v: string) => void;
}) {
  const [local, setLocal] = useState(value);
  const [open, setOpen] = useState(false);
  useEffect(() => setLocal(value), [value]);
  return (
    <>
      <label>{label}</label>
      <div className="file-field">
        <input
          value={local}
          onChange={(e) => setLocal(e.target.value)}
          onBlur={() => {
            if (local !== value) onChange(local);
          }}
          placeholder={mode === 'save' ? '/path/to/output.mp4' : '/path/to/input.mp4'}
        />
        <button onClick={() => setOpen(true)} title="Browse local filesystem">Browse…</button>
      </div>
      <FileBrowser
        open={open}
        mode={mode}
        filter={filter}
        defaultFilename={defaultFilename}
        initialPath={inferDir(value)}
        onClose={() => setOpen(false)}
        onPick={(p) => {
          setLocal(p);
          onChange(p);
        }}
      />
    </>
  );
}

function inferDir(p: string): string | undefined {
  if (!p) return undefined;
  const i = p.lastIndexOf('/');
  if (i <= 0) return undefined;
  return p.slice(0, i);
}
