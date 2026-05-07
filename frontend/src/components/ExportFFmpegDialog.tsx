import { useEffect, useRef, useState } from 'react';
import type { JobConfig } from '../lib/jobTypes';

interface Props {
  /** The current serialised JobConfig to convert to a CLI command. */
  config: JobConfig;
  onClose: () => void;
}

interface ExportResponse {
  command: string;
  lines: string[];
  unsupported: string[];
}

interface ApiError {
  error: string;
}

/** Modal panel that converts the current JobConfig to an ffmpeg command line
 *  by posting to POST /api/export-cmd. Shows the command in a read-only
 *  monospace block with a Copy button, and lists any mediamolder-only
 *  features that have no CLI equivalent as amber warnings. */
export function ExportFFmpegDialog({ config, onClose }: Props) {
  const [result, setResult] = useState<ExportResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [copied, setCopied] = useState(false);
  const preRef = useRef<HTMLPreElement>(null);

  // Fetch as soon as the dialog opens.
  useEffect(() => {
    let cancelled = false;
    setBusy(true);
    setError(null);
    setResult(null);

    fetch('/api/export-cmd', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ config }),
    })
      .then(async (r) => {
        const text = await r.text();
        if (cancelled) return;
        if (!r.ok) {
          let msg = text;
          try {
            const body = JSON.parse(text) as ApiError;
            if (body.error) msg = body.error;
          } catch {
            /* keep raw */
          }
          setError(msg || `HTTP ${r.status}`);
          return;
        }
        const body = JSON.parse(text) as ExportResponse;
        setResult(body);
      })
      .catch((err: unknown) => {
        if (!cancelled) setError((err as Error).message);
      })
      .finally(() => {
        if (!cancelled) setBusy(false);
      });

    return () => {
      cancelled = true;
    };
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []); // run once on mount

  // Esc closes.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  const copyCommand = () => {
    if (!result?.command) return;
    navigator.clipboard.writeText(result.command).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    }).catch(() => {
      // Fallback: select the text in the pre block.
      if (preRef.current) {
        const sel = window.getSelection();
        const range = document.createRange();
        range.selectNodeContents(preRef.current);
        sel?.removeAllRanges();
        sel?.addRange(range);
      }
    });
  };

  const displayText = result
    ? result.lines.length > 0
      ? result.lines.join(' \\\n  ')
      : result.command
    : '';

  return (
    <div
      className="dialog-overlay"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div className="dialog" style={{ width: 'min(860px, 96vw)' }}>
        <div className="dialog-header">
          <h3>Show as ffmpeg command</h3>
          <button onClick={onClose} title="Close (Esc)">✕</button>
        </div>

        <div style={{ padding: '14px 16px', flex: 1, minHeight: 0, overflow: 'auto' }}>
          {busy && (
            <p style={{ color: 'var(--text-dim)', fontSize: 13 }}>Generating command…</p>
          )}
          {error && (
            <pre
              className="probe-error"
              style={{ whiteSpace: 'pre-wrap', fontSize: 12, padding: 10, borderRadius: 4 }}
            >
              {error}
            </pre>
          )}
          {result && (
            <>
              <div
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'space-between',
                  marginBottom: 8,
                }}
              >
                <span style={{ fontSize: 12, color: 'var(--text-dim)' }}>
                  ffmpeg command line
                </span>
                <button
                  onClick={copyCommand}
                  title="Copy command to clipboard"
                  style={{ fontSize: 12 }}
                >
                  {copied ? '✓ Copied' : 'Copy'}
                </button>
              </div>
              <pre
                ref={preRef}
                style={{
                  background: 'var(--panel-2)',
                  border: '1px solid var(--border)',
                  borderRadius: 4,
                  padding: '10px 12px',
                  fontFamily: 'var(--mm-monofont, ui-monospace, SFMono-Regular, Menlo, monospace)',
                  fontSize: 12,
                  lineHeight: 1.6,
                  overflowX: 'auto',
                  whiteSpace: 'pre',
                  userSelect: 'text',
                  margin: 0,
                }}
              >
                {displayText}
              </pre>
              {result.unsupported && result.unsupported.length > 0 && (
                <div style={{ marginTop: 14 }}>
                  <div
                    style={{
                      fontSize: 12,
                      fontWeight: 600,
                      color: 'var(--warning, #f59e0b)',
                      marginBottom: 6,
                    }}
                  >
                    ⚠ MediaMolder-only features (no CLI equivalent)
                  </div>
                  <ul
                    style={{
                      margin: 0,
                      paddingLeft: 18,
                      fontSize: 12,
                      color: 'var(--text-dim)',
                      lineHeight: 1.7,
                    }}
                  >
                    {result.unsupported.map((msg, i) => (
                      <li key={i}>{msg}</li>
                    ))}
                  </ul>
                </div>
              )}
            </>
          )}
        </div>

        <div className="dialog-footer">
          <span className="hint">
            This command is a best-effort approximation. Some MediaMolder features
            have no direct CLI equivalent — see warnings above.
          </span>
          <div className="spacer" />
          <button onClick={onClose}>Close</button>
        </div>
      </div>
    </div>
  );
}
