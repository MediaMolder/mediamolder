import { useEffect, useRef, useState } from 'react';
import type { JobConfig } from '../lib/jobTypes';

interface Props {
  open: boolean;
  onClose: () => void;
  /** Called with the parsed JobConfig when the server returns one. */
  onImported: (cfg: JobConfig) => void;
}

interface ApiError {
  error: string;
}

/** Modal dialog that takes an FFmpeg-style command line, posts it to
 *  POST /api/convert-cmd, and either hands the resulting JobConfig back to
 *  the parent (which calls loadJob) or surfaces the parse error verbatim
 *  so the user can fix it. */
export function ImportFFmpegDialog({ open, onClose, onImported }: Props) {
  const [command, setCommand] = useState(
    'ffmpeg -i input.mp4 -vf scale=1280:720 -c:v libx264 -c:a aac output.mp4',
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  // Reset error and focus the textarea every time the dialog opens.
  useEffect(() => {
    if (!open) return;
    setError(null);
    setBusy(false);
    // setTimeout so the focus call runs after the dialog is in the DOM.
    setTimeout(() => textareaRef.current?.focus(), 0);
  }, [open]);

  // Esc closes when not busy.
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !busy) onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [open, busy, onClose]);

  if (!open) return null;

  const submit = async () => {
    const cmd = command.trim();
    if (!cmd) {
      setError('Enter an FFmpeg command line.');
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const r = await fetch('/api/convert-cmd', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ command: cmd }),
      });
      const text = await r.text();
      if (!r.ok) {
        // The server returns {"error": "..."} on failure; fall back to the
        // raw body if it isn't valid JSON for any reason.
        let msg = text;
        try {
          const body = JSON.parse(text) as ApiError;
          if (body.error) msg = body.error;
        } catch {
          /* keep raw */
        }
        throw new Error(msg || `HTTP ${r.status}`);
      }
      const body = JSON.parse(text) as { config: JobConfig };
      if (!body.config) throw new Error('Server returned no config.');
      onImported(body.config);
      onClose();
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  };

  // Submit on Ctrl/Cmd+Enter. The textarea swallows plain Enter so the
  // user can paste multi-line commands.
  const onTextareaKey = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
      e.preventDefault();
      void submit();
    }
  };

  return (
    <div
      className="dialog-overlay"
      onClick={(e) => {
        if (e.target === e.currentTarget && !busy) onClose();
      }}
    >
      <div
        className="dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="import-ffmpeg-title"
        style={{ maxWidth: 720, width: '90%' }}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="dialog-header">
          <h3 id="import-ffmpeg-title" style={{ margin: 0 }}>
            Import FFmpeg command
          </h3>
          <button onClick={onClose} disabled={busy}>×</button>
        </div>
        <div style={{ padding: 16 }}>
        <p style={{ fontSize: 12, color: 'var(--text-dim)', marginTop: 0 }}>
          Paste an FFmpeg-style command line. It will be parsed into a
          MediaMolder graph and loaded into the canvas, replacing what's there.
        </p>
        <label htmlFor="import-ffmpeg-cmd" style={{ display: 'block', marginBottom: 4 }}>
          Command
        </label>
        <textarea
          id="import-ffmpeg-cmd"
          ref={textareaRef}
          value={command}
          onChange={(e) => setCommand(e.target.value)}
          onKeyDown={onTextareaKey}
          rows={5}
          spellCheck={false}
          autoComplete="off"
          style={{
            width: '100%',
            fontFamily: 'var(--mm-monofont, ui-monospace, SFMono-Regular, Menlo, monospace)',
            fontSize: 12,
            resize: 'vertical',
            boxSizing: 'border-box',
          }}
          disabled={busy}
        />
        {error && (
          <pre
            className="probe-error"
            style={{
              marginTop: 12,
              padding: 8,
              maxHeight: 200,
              overflow: 'auto',
              whiteSpace: 'pre-wrap',
              fontSize: 12,
            }}
          >
            {error}
          </pre>
        )}
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 16 }}>
          <button onClick={onClose} disabled={busy}>
            Cancel
          </button>
          <button className="primary" onClick={submit} disabled={busy}>
            {busy ? 'Importing…' : 'Import'}
          </button>
        </div>
        <div style={{ fontSize: 11, color: 'var(--text-dim)', marginTop: 8 }}>
          Tip: <kbd>Ctrl</kbd>/<kbd>⌘</kbd>+<kbd>Enter</kbd> to import.
        </div>
        </div>
      </div>
    </div>
  );
}
