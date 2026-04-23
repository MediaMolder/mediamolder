import { useEffect, useState } from 'react';
import type { FlowNode } from '../lib/jsonAdapter';
import type { Input, NodeDef, Output } from '../lib/jobTypes';

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

  return (
    <div className="inspector">
      <div className="inspector-header">
        <h3>{node.data.label}</h3>
        <button className="danger" onClick={() => onDelete(node.id)}>Delete</button>
      </div>
      <div className="mm-node-type" style={{ marginBottom: 12 }}>{node.data.kind}</div>

      {ref.kind === 'input' && (
        <InputForm
          def={ref.def}
          onChange={(def) => onChange(updateRef(node, { kind: 'input', def }, def.id, def.url))}
        />
      )}
      {ref.kind === 'output' && (
        <OutputForm
          def={ref.def}
          onChange={(def) => onChange(updateRef(node, { kind: 'output', def }, def.id, def.url))}
        />
      )}
      {ref.kind === 'node' && (
        <NodeForm
          def={ref.def}
          onChange={(def) =>
            onChange(updateRef(node, { kind: 'node', def }, def.id, def.filter || def.processor || def.type))
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
function InputForm({ def, onChange }: { def: Input; onChange: (next: Input) => void }) {
  return (
    <>
      <Field label="ID" value={def.id} onChange={(v) => onChange({ ...def, id: v })} />
      <Field label="URL" value={def.url} onChange={(v) => onChange({ ...def, url: v })} />
    </>
  );
}

/* ---------- Output form ---------- */
function OutputForm({ def, onChange }: { def: Output; onChange: (next: Output) => void }) {
  return (
    <>
      <Field label="ID" value={def.id} onChange={(v) => onChange({ ...def, id: v })} />
      <Field label="URL" value={def.url} onChange={(v) => onChange({ ...def, url: v })} />
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
