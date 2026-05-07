// TeeForm — wizard for `output.kind === 'tee'`.
// The FFmpeg tee muxer writes to multiple independent outputs from a
// single encoding pass.  Each target is a TeeTarget with its own URL,
// optional format, stream-select specifier, BSF chain, on-fail policy,
// optional FIFO wrapper, and arbitrary AVOption overrides.
//
// Renders the target list + per-target sub-forms.
// The Kind selector (file / tee) lives in OutputForm and controls
// whether this component is shown at all.

import { useState } from 'react';
import type { TeeTarget } from '../lib/jobTypes';
import { FileBrowser } from './FileBrowser';

const ONFAIL_OPTIONS = ['', 'abort', 'ignore'] as const;

interface Props {
  targets: TeeTarget[];
  onChange: (next: TeeTarget[]) => void;
}

export function TeeForm({ targets, onChange }: Props) {
  const add = () =>
    onChange([...targets, { url: '' }]);

  const update = (i: number, t: TeeTarget) => {
    const next = [...targets];
    next[i] = t;
    onChange(next);
  };

  const remove = (i: number) =>
    onChange(targets.filter((_, idx) => idx !== i));

  return (
    <>
      <div style={{ marginTop: 10, marginBottom: 4, fontSize: 11, color: 'var(--text-dim)' }}>
        The <strong>tee</strong> muxer writes a single encoding pass to multiple
        outputs. Each target can use a different container, stream selection, and
        bitstream filters.
      </div>
      {targets.length === 0 && (
        <div className="empty" style={{ marginTop: 4, marginBottom: 6 }}>
          No targets. Add at least one.
        </div>
      )}
      {targets.map((t, i) => (
        <TeeTargetEditor
          key={i}
          index={i}
          target={t}
          onChange={(next) => update(i, next)}
          onRemove={() => remove(i)}
        />
      ))}
      <button type="button" style={{ marginTop: 6 }} onClick={add}>
        + add target
      </button>
    </>
  );
}

/* ---------- Per-target collapsible editor ---------- */
function TeeTargetEditor({
  index,
  target,
  onChange,
  onRemove,
}: {
  index: number;
  target: TeeTarget;
  onChange: (next: TeeTarget) => void;
  onRemove: () => void;
}) {
  const [open, setOpen] = useState(index === 0);

  const set = <K extends keyof TeeTarget>(k: K, v: TeeTarget[K]) => {
    const next = { ...target };
    if (v === undefined || v === '') {
      delete next[k];
    } else {
      next[k] = v;
    }
    // url is required — preserve empty string until user fills it in
    if (k === 'url') next.url = v as string ?? '';
    onChange(next);
  };

  const label = target.url ? shortenPath(target.url) : `Target ${index + 1}`;

  return (
    <div
      style={{
        marginTop: 8,
        border: '1px solid var(--border)',
        borderRadius: 4,
        overflow: 'hidden',
      }}
    >
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 6,
          padding: '4px 8px',
          background: 'var(--panel-2)',
          borderBottom: open ? '1px solid var(--border)' : 'none',
        }}
      >
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          style={{
            flex: 1,
            textAlign: 'left',
            background: 'transparent',
            border: 'none',
            color: 'var(--text)',
            fontSize: 12,
            cursor: 'pointer',
            padding: 0,
          }}
        >
          {open ? '▾' : '▸'} {label}
        </button>
        <button
          type="button"
          onClick={onRemove}
          title="Remove target"
          style={{ color: 'var(--text-dim)', background: 'transparent', border: 'none', cursor: 'pointer', fontSize: 13 }}
        >
          ×
        </button>
      </div>

      {open && (
        <div style={{ padding: '4px 8px 8px' }}>
          <TargetUrlField
            value={target.url}
            onChange={(v) => onChange({ ...target, url: v })}
          />

          <label>Format</label>
          <input
            type="text"
            value={target.format ?? ''}
            placeholder="(auto-detect from URL)"
            onChange={(e) => set('format', e.target.value || undefined)}
          />

          <label>Stream select</label>
          <div style={{ fontSize: 11, color: 'var(--text-dim)', marginBottom: 3 }}>
            Comma-separated stream specifiers, e.g. <code>v:0,a:0</code>. Empty = all streams.
          </div>
          <input
            type="text"
            value={target.select ?? ''}
            placeholder="v:0,a:0"
            spellCheck={false}
            onChange={(e) => set('select', e.target.value || undefined)}
          />

          <label>Bitstream filters</label>
          <input
            type="text"
            value={target.bsfs ?? ''}
            placeholder="e.g. h264_mp4toannexb"
            spellCheck={false}
            onChange={(e) => set('bsfs', e.target.value || undefined)}
          />

          <label>On failure</label>
          <select
            value={target.onfail ?? ''}
            onChange={(e) => set('onfail', (e.target.value || undefined) as TeeTarget['onfail'])}
          >
            {ONFAIL_OPTIONS.map((o) => (
              <option key={o} value={o}>
                {o === '' ? '(default — abort)' : o}
              </option>
            ))}
          </select>

          <label>Use FIFO</label>
          <select
            value={target.use_fifo === undefined ? '' : target.use_fifo ? 'true' : 'false'}
            onChange={(e) => {
              const v = e.target.value;
              set('use_fifo', v === '' ? undefined : v === 'true');
            }}
          >
            <option value="">(default — false)</option>
            <option value="true">true</option>
            <option value="false">false</option>
          </select>

          {target.use_fifo && (
            <>
              <label>FIFO options</label>
              <div style={{ fontSize: 11, color: 'var(--text-dim)', marginBottom: 3 }}>
                Semicolon-separated <code>key=value</code> options for the fifo muxer.
              </div>
              <input
                type="text"
                value={target.fifo_options ?? ''}
                placeholder="queue_size=1024;recover_any_error=1"
                spellCheck={false}
                onChange={(e) => set('fifo_options', e.target.value || undefined)}
              />
            </>
          )}

          <label style={{ marginTop: 12 }}>Extra options</label>
          <TargetOptionsEditor
            options={target.options ?? {}}
            onChange={(o) => set('options', Object.keys(o).length ? o : undefined)}
          />
        </div>
      )}
    </div>
  );
}

/* ---------- URL field with file browser ---------- */
function TargetUrlField({
  value,
  onChange,
}: {
  value: string;
  onChange: (v: string) => void;
}) {
  const [open, setOpen] = useState(false);

  return (
    <>
      <label>
        URL <span style={{ color: 'var(--mm-error, #c33)', fontWeight: 600 }}>*</span>
      </label>
      <div className="file-field">
        <input
          type="text"
          value={value}
          placeholder="/path/to/output.mp4  or  rtmp://server/live/key"
          style={!value.trim() ? { outline: '1px solid var(--mm-error, #c33)' } : undefined}
          onChange={(e) => onChange(e.target.value)}
        />
        <button type="button" onClick={() => setOpen(true)} title="Browse local filesystem">
          Browse…
        </button>
      </div>
      <FileBrowser
        open={open}
        mode="save"
        defaultFilename="output.mp4"
        initialPath={inferDir(value)}
        onClose={() => setOpen(false)}
        onPick={(p) => onChange(p)}
      />
    </>
  );
}

/* ---------- Inline key/value options editor ---------- */
function TargetOptionsEditor({
  options,
  onChange,
}: {
  options: Record<string, unknown>;
  onChange: (next: Record<string, unknown>) => void;
}) {
  const entries = Object.entries(options);

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
    entries.forEach(([k, v], idx) => { if (idx !== i) next[k] = v; });
    onChange(next);
  };

  return (
    <>
      {entries.length === 0 && (
        <div className="empty" style={{ marginTop: 0, marginBottom: 4, fontSize: 11 }}>
          No extra options.
        </div>
      )}
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
          <button type="button" onClick={() => remove(i)} title="Remove">×</button>
        </div>
      ))}
      <button
        type="button"
        style={{ fontSize: 11, marginTop: 4 }}
        onClick={() => onChange({ ...options, '': '' })}
      >
        + add option
      </button>
    </>
  );
}

function shortenPath(p: string): string {
  if (!p) return '';
  const parts = p.split('/');
  const name = parts[parts.length - 1];
  return name || p;
}

function inferDir(p: string): string | undefined {
  if (!p) return undefined;
  const i = p.lastIndexOf('/');
  if (i <= 0) return undefined;
  return p.slice(0, i);
}
