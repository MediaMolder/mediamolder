import { useCallback, useEffect, useState } from 'react';

export type BrowseMode = 'open' | 'save';

interface FileEntry {
  name: string;
  path: string;
  is_dir: boolean;
  size?: number;
}

interface ListResponse {
  path: string;
  parent?: string;
  entries: FileEntry[];
  roots?: string[];
}

interface Props {
  open: boolean;
  mode: BrowseMode;
  title?: string;
  /** Optional comma-separated extension filter, e.g. "mp4,mkv,mov". */
  filter?: string;
  /** Initial path to seed the browser with. Falls back to $HOME. */
  initialPath?: string;
  /** Default file name when saving. */
  defaultFilename?: string;
  onClose: () => void;
  onPick: (path: string) => void;
}

export function FileBrowser({
  open,
  mode,
  title,
  filter,
  initialPath,
  defaultFilename,
  onClose,
  onPick,
}: Props) {
  const [data, setData] = useState<ListResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [pathInput, setPathInput] = useState('');
  const [filename, setFilename] = useState(defaultFilename ?? '');
  const [selected, setSelected] = useState<FileEntry | null>(null);

  const load = useCallback(
    async (path: string) => {
      setError(null);
      const params = new URLSearchParams();
      if (path) params.set('path', path);
      if (filter && mode === 'open') params.set('filter', filter);
      try {
        const res = await fetch(`/api/files?${params.toString()}`);
        if (!res.ok) {
          const body = await res.json().catch(() => ({}));
          setError((body as { error?: string }).error ?? `${res.status} ${res.statusText}`);
          return;
        }
        const body = (await res.json()) as ListResponse;
        setData(body);
        setPathInput(body.path);
        setSelected(null);
      } catch (err) {
        setError((err as Error).message);
      }
    },
    [filter, mode],
  );

  useEffect(() => {
    if (!open) return;
    void load(initialPath ?? '');
  }, [open, initialPath, load]);

  const createFolder = useCallback(async () => {
    if (!data) return;
    const name = window.prompt('New folder name:');
    if (!name) return;
    const trimmed = name.trim();
    if (!trimmed || /[\\/]/.test(trimmed) || trimmed === '.' || trimmed === '..') {
      setError('Invalid folder name.');
      return;
    }
    try {
      const res = await fetch('/api/files/mkdir', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: data.path, name: trimmed }),
      });
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        setError((body as { error?: string }).error ?? `${res.status} ${res.statusText}`);
        return;
      }
      const body = (await res.json()) as { path: string };
      // Refresh the current listing and navigate into the new folder.
      void load(body.path);
    } catch (err) {
      setError((err as Error).message);
    }
  }, [data, load]);

  if (!open) return null;

  const onSelect = (e: FileEntry) => {
    if (e.is_dir) {
      void load(e.path);
      return;
    }
    setSelected(e);
    if (mode === 'save') setFilename(e.name);
  };

  const onConfirm = () => {
    if (mode === 'open') {
      if (selected && !selected.is_dir) {
        onPick(selected.path);
        onClose();
      }
      return;
    }
    // save
    const name = filename.trim();
    if (!name || !data) return;
    onPick(joinPath(data.path, name));
    onClose();
  };

  const canConfirm =
    mode === 'open' ? !!selected && !selected.is_dir : filename.trim() !== '' && !!data;

  return (
    <div className="dialog-overlay" onClick={onClose}>
      <div className="dialog" onClick={(e) => e.stopPropagation()}>
        <div className="dialog-header">
          <h3>{title ?? (mode === 'open' ? 'Open file' : 'Save file as…')}</h3>
          <button onClick={onClose}>×</button>
        </div>

        <div className="file-browser">
          <div className="file-browser-shortcuts">
            <div className="shortcut-label">Shortcuts</div>
            {(data?.roots ?? []).map((r) => (
              <button key={r} className="shortcut" onClick={() => void load(r)} title={r}>
                {shortcutLabel(r)}
              </button>
            ))}
          </div>

          <div className="file-browser-main">
            <div className="file-browser-pathbar">
              <button
                onClick={() => data?.parent && void load(data.parent)}
                disabled={!data?.parent}
                title="Up one directory"
              >
                ↑
              </button>
              <input
                type="text"
                value={pathInput}
                onChange={(e) => setPathInput(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') void load(pathInput);
                }}
                placeholder="/path/to/folder"
              />
              <button onClick={() => void load(pathInput)}>Go</button>
              {mode === 'save' && (
                <button
                  onClick={() => void createFolder()}
                  disabled={!data}
                  title="Create a new folder inside the current directory"
                >
                  New folder
                </button>
              )}
            </div>

            {error && <div className="file-browser-error">{error}</div>}

            <div className="file-browser-list">
              {data?.entries.length === 0 && <div className="empty">Empty folder.</div>}
              {data?.entries.map((e) => (
                <div
                  key={e.path}
                  className={`file-row ${selected?.path === e.path ? 'selected' : ''}`}
                  onClick={() => onSelect(e)}
                  onDoubleClick={() => {
                    if (e.is_dir) void load(e.path);
                    else {
                      onPick(e.path);
                      onClose();
                    }
                  }}
                >
                  <span className="file-icon">{e.is_dir ? '📁' : '🎬'}</span>
                  <span className="file-name">{e.name}</span>
                  {!e.is_dir && <span className="file-size">{formatSize(e.size ?? 0)}</span>}
                </div>
              ))}
            </div>

            {mode === 'save' && (
              <div className="file-browser-savebar">
                <label>File name</label>
                <input
                  type="text"
                  value={filename}
                  onChange={(e) => setFilename(e.target.value)}
                  placeholder="output.mp4"
                />
              </div>
            )}
          </div>
        </div>

        <div className="dialog-footer">
          <span className="hint">
            {mode === 'open'
              ? 'Double-click a file to open, or select it and click Open.'
              : 'Pick a folder, type a filename, and click Save.'}
          </span>
          <div className="spacer" />
          <button onClick={onClose}>Cancel</button>
          <button className="primary" disabled={!canConfirm} onClick={onConfirm}>
            {mode === 'open' ? 'Open' : 'Save'}
          </button>
        </div>
      </div>
    </div>
  );
}

function joinPath(dir: string, name: string): string {
  if (dir.endsWith('/')) return dir + name;
  return dir + '/' + name;
}

function shortcutLabel(p: string): string {
  if (p === '/') return '/ (root)';
  // Show the trailing path component but keep enough context.
  const parts = p.split('/').filter(Boolean);
  if (parts.length === 0) return p;
  return parts[parts.length - 1] || p;
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
  return `${(bytes / 1024 / 1024 / 1024).toFixed(2)} GB`;
}
