import type { RunState } from '../lib/useJobRun';

interface Props {
  run: RunState;
  onClose: () => void;
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

  return (
    <div className="run-panel">
      <div className="run-panel-header">
        <span className={`run-status run-status-${run.status}`}>{run.status}</span>
        {run.pipelineState && <span className="run-pipeline-state">{run.pipelineState}</span>}
        <span style={{ color: 'var(--text-dim)' }}>
          frames: {totalFrames} · errors: {totalErrors}
        </span>
        <div className="spacer" />
        <button onClick={onClose} title="Hide log panel">×</button>
      </div>

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
