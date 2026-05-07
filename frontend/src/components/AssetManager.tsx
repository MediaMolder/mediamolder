import { useState } from 'react';
import type { AssetRef } from '../lib/jobTypes';
import { FileBrowser } from './FileBrowser';

interface Props {
  assets: Record<string, AssetRef>;
  onChange: (assets: Record<string, AssetRef>) => void;
  onClose: () => void;
}

const KIND_LABELS: Record<AssetRef['kind'], string> = {
  font: 'Font (TTF/OTF)',
  model: 'Model (ML)',
  lut: 'LUT (.cube/.3dl)',
  other: 'Other',
};

const BLANK_FORM = { name: '', path: '', kind: 'other' as AssetRef['kind'], desc: '' };

export function AssetManager({ assets, onChange, onClose }: Props) {
  const [editing, setEditing] = useState<string | null>(null); // null = not editing; '' = adding new
  const [form, setForm] = useState(BLANK_FORM);
  const [nameError, setNameError] = useState('');
  const [showBrowser, setShowBrowser] = useState(false);

  const startAdd = () => {
    setEditing('');
    setForm(BLANK_FORM);
    setNameError('');
  };

  const startEdit = (name: string) => {
    const ref = assets[name];
    setEditing(name);
    setForm({ name, path: ref.path, kind: ref.kind, desc: ref.desc ?? '' });
    setNameError('');
  };

  const cancel = () => {
    setEditing(null);
    setNameError('');
  };

  const validateName = (name: string): string => {
    if (!name) return 'Name is required.';
    if (!/^[A-Za-z_][A-Za-z0-9_-]*$/.test(name)) return 'Name must start with a letter/underscore, then letters, digits, underscores, or hyphens.';
    if (editing === '' && name in assets) return `Asset "${name}" already exists.`;
    if (editing !== '' && editing !== null && name !== editing && name in assets) return `Asset "${name}" already exists.`;
    return '';
  };

  const save = () => {
    const err = validateName(form.name);
    if (err) { setNameError(err); return; }
    if (!form.path) { setNameError('Path is required.'); return; }
    const next = { ...assets };
    // Remove old key if renamed.
    if (editing && editing !== form.name) delete next[editing];
    next[form.name] = { path: form.path, kind: form.kind, ...(form.desc ? { desc: form.desc } : {}) };
    onChange(next);
    setEditing(null);
  };

  const remove = (name: string) => {
    const next = { ...assets };
    delete next[name];
    onChange(next);
    if (editing === name) setEditing(null);
  };

  const entries = Object.entries(assets);

  return (
    <div className="dialog-overlay" onClick={onClose}>
      <div className="dialog dialog-assets" onClick={(e) => e.stopPropagation()}>
        <div className="dialog-header">
          <h3>Asset Registry</h3>
          <button onClick={onClose}>×</button>
        </div>

        <div className="assets-body">
          {entries.length === 0 && editing === null ? (
            <p className="assets-empty">No assets registered. Click <strong>Add</strong> to add one.</p>
          ) : (
            <table className="assets-table">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Kind</th>
                  <th>Path</th>
                  <th>Description</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                {entries.map(([name, ref]) =>
                  editing === name ? null : (
                    <tr key={name}>
                      <td><code>{name}</code></td>
                      <td>{KIND_LABELS[ref.kind] ?? ref.kind}</td>
                      <td className="assets-path" title={ref.path}>{ref.path}</td>
                      <td>{ref.desc ?? ''}</td>
                      <td className="assets-actions">
                        <button onClick={() => startEdit(name)}>Edit</button>
                        <button className="btn-danger" onClick={() => remove(name)}>Remove</button>
                      </td>
                    </tr>
                  ),
                )}
              </tbody>
            </table>
          )}

          {editing !== null && (
            <div className="assets-form">
              <h4>{editing === '' ? 'Add Asset' : `Edit "${editing}"`}</h4>
              <div className="assets-form-grid">
                <label>Name</label>
                <div>
                  <input
                    type="text"
                    value={form.name}
                    placeholder="e.g. myFont"
                    onChange={(e) => { setForm((f) => ({ ...f, name: e.target.value })); setNameError(''); }}
                  />
                  {nameError && <span className="assets-error">{nameError}</span>}
                </div>

                <label>Kind</label>
                <select value={form.kind} onChange={(e) => setForm((f) => ({ ...f, kind: e.target.value as AssetRef['kind'] }))}>
                  {Object.entries(KIND_LABELS).map(([k, label]) => (
                    <option key={k} value={k}>{label}</option>
                  ))}
                </select>

                <label>Path</label>
                <div className="assets-path-row">
                  <input
                    type="text"
                    value={form.path}
                    placeholder="/path/to/file or relative/path"
                    onChange={(e) => setForm((f) => ({ ...f, path: e.target.value }))}
                  />
                  <button onClick={() => setShowBrowser(true)}>Browse…</button>
                </div>

                <label>Description</label>
                <input
                  type="text"
                  value={form.desc}
                  placeholder="Optional label (GUI only)"
                  onChange={(e) => setForm((f) => ({ ...f, desc: e.target.value }))}
                />
              </div>
            </div>
          )}
        </div>

        <div className="dialog-footer">
          <button onClick={startAdd} disabled={editing !== null}>Add</button>
          {editing !== null && (
            <>
              <button className="btn-primary" onClick={save}>Save</button>
              <button onClick={cancel}>Cancel</button>
            </>
          )}
          <span className="spacer" />
          <span className="hint">
            Reference assets in filter params as <code>$asset:&lt;name&gt;</code>
          </span>
        </div>
      </div>

      {showBrowser && (
        <FileBrowser
          open={showBrowser}
          mode="open"
          title="Select asset file"
          onClose={() => setShowBrowser(false)}
          onPick={(p) => { setForm((f) => ({ ...f, path: p })); setShowBrowser(false); }}
        />
      )}

      <style>{`
        .dialog-assets { width: min(780px, 96vw); }
        .assets-body { flex: 1; overflow-y: auto; padding: 12px 14px; display: flex; flex-direction: column; gap: 12px; }
        .assets-empty { margin: 0; color: var(--text-dim); font-size: 13px; }
        .assets-table { width: 100%; border-collapse: collapse; font-size: 12px; }
        .assets-table th { text-align: left; padding: 4px 8px; border-bottom: 1px solid var(--border); color: var(--text-dim); font-weight: 600; }
        .assets-table td { padding: 5px 8px; border-bottom: 1px solid var(--border); vertical-align: top; }
        .assets-table td.assets-path { max-width: 220px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-family: monospace; font-size: 11px; }
        .assets-table td.assets-actions { white-space: nowrap; }
        .assets-table td.assets-actions button { margin-left: 4px; }
        .assets-form { background: var(--panel-2); border: 1px solid var(--border); border-radius: 6px; padding: 10px 12px; }
        .assets-form h4 { margin: 0 0 8px; font-size: 13px; }
        .assets-form-grid { display: grid; grid-template-columns: 90px 1fr; gap: 6px 10px; align-items: center; font-size: 12px; }
        .assets-form-grid label { color: var(--text-dim); text-align: right; }
        .assets-form-grid input, .assets-form-grid select { width: 100%; box-sizing: border-box; }
        .assets-path-row { display: flex; gap: 6px; }
        .assets-path-row input { flex: 1; min-width: 0; }
        .assets-error { color: var(--error, #f87171); font-size: 11px; display: block; margin-top: 2px; }
        .btn-danger { background: transparent; border-color: var(--error, #f87171); color: var(--error, #f87171); }
        .btn-danger:hover { background: rgba(248, 113, 113, 0.1); }
        .btn-primary { background: var(--accent); border-color: var(--accent); color: #fff; }
        .btn-primary:hover { opacity: 0.85; }
      `}</style>
    </div>
  );
}
