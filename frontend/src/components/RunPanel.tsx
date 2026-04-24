import type { RunState } from '../lib/useJobRun';

interface Props {
  run: RunState;
  onClose: () => void;
}

/**
 * Format a nanosecond duration as HH:MM:SS (or MM:SS when under an
 * hour). Returns "--:--:--" for zero/negative input.
 */
function formatHMS(ns: number): string {
  if (!isFinite(ns) || ns <= 0) return '--:--:--';
  const totalSec = Math.floor(ns / 1e9);
  const h = Math.floor(totalSec / 3600);
  const m = Math.floor((totalSec % 3600) / 60);
  const s = totalSec % 60;
  const pad = (n: number) => n.toString().padStart(2, '0');
  return h > 0 ? `${h}:${pad(m)}:${pad(s)}` : `${pad(m)}:${pad(s)}`;
}

/**
 * RunPanel — body of the bottom dock. Mirrors the VS Code terminal
 * pattern: a thin status header on top, a two-column body underneath
 * (per-node metrics on the left, scrolling log stream on the right).
 * The dock itself (positioning, resize handle) lives in `RunDock`.
 */
export function RunPanel({ run, onClose }: Props) {
  const totalFrames = run.metrics?.Nodes.reduce((s, n) => s + n.Frames, 0) ?? 0;
  const totalErrors = run.metrics?.Nodes.reduce((s, n) => s + n.Errors, 0) ?? 0;

  // Progress = mediaPTS / mediaDuration (when known). ETA derived from
  // the wall-clock speed at which media-time is being consumed —
  // robust against stalls (slow nodes drag both numerator and
  // denominator) without needing extra plumbing.
  const mediaPTS = run.metrics?.MediaPTS ?? 0;
  const mediaDuration = run.metrics?.MediaDuration ?? 0;
  const elapsed = run.metrics?.Elapsed ?? 0;
  const haveDuration = mediaDuration > 0;
  const progressPct = haveDuration ? Math.min(100, (mediaPTS / mediaDuration) * 100) : 0;
  const speedX = elapsed > 0 ? mediaPTS / elapsed : 0; // realtime multiplier
  const remaining = haveDuration && speedX > 0 ? (mediaDuration - mediaPTS) / speedX : 0;

  return (
    <div className="run-panel">
      <div className="run-panel-header">
        <span className={`run-status run-status-${run.status}`}>{run.status}</span>
        {run.pipelineState && <span className="run-pipeline-state">{run.pipelineState}</span>}
        <span style={{ color: 'var(--text-dim)' }}>
          frames: {totalFrames} · errors: {totalErrors}
        </span>
        {(mediaPTS > 0 || haveDuration) && (
          <span style={{ color: 'var(--text-dim)' }}>
            media: {formatHMS(mediaPTS)}
            {haveDuration && ` / ${formatHMS(mediaDuration)}`}
            {haveDuration && ` (${progressPct.toFixed(1)}%)`}
          </span>
        )}
        {speedX > 0 && (
          <span style={{ color: 'var(--text-dim)' }}>
            {speedX.toFixed(2)}× realtime
            {haveDuration && remaining > 0 && ` · ETA ${formatHMS(remaining)}`}
          </span>
        )}
        <div className="spacer" />
        <button onClick={onClose} title="Hide log panel">×</button>
      </div>

      {haveDuration && run.status === 'running' && (
        <div className="run-progress-bar" role="progressbar" aria-valuenow={progressPct} aria-valuemin={0} aria-valuemax={100}>
          <div className="run-progress-fill" style={{ width: `${progressPct}%` }} />
        </div>
      )}

      {run.finalError && <div className="run-final-error">{run.finalError}</div>}

      <div className="run-panel-body">
        <div className="run-metrics">
          {run.metrics && run.metrics.Nodes.length > 0 ? (
            <table>
              <thead>
                <tr>
                  <th>Node</th>
                  <th>Frames</th>
                  <th>FPS</th>
                  <th>Errors</th>
                  <th>Avg latency</th>
                </tr>
              </thead>
              <tbody>
                {run.metrics.Nodes.map((n) => (
                  <tr key={n.NodeID}>
                    <td>{n.NodeID}</td>
                    <td>{n.Frames}</td>
                    <td>{n.FPS.toFixed(1)}</td>
                    <td className={n.Errors > 0 ? 'cell-error' : ''}>{n.Errors}</td>
                    <td>{(n.AvgLatency / 1e6).toFixed(2)} ms</td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <div className="empty" style={{ fontSize: 11 }}>No metrics yet.</div>
          )}
        </div>

        <div className="run-logs">
          {run.logs.length === 0 && <div className="empty">No logs yet.</div>}
          {run.logs.map((l, i) => (
            <div key={i} className={`run-log run-log-${l.level ?? 'info'}`}>
              <span className="log-time">{(l.time_ms / 1000).toFixed(1)}s</span> {l.message}
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
