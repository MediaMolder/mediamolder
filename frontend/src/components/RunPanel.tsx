import { useEffect, useRef, useState } from 'react';
import type { MetricsSnapshot, RunState } from '../lib/useJobRun';

export type NodeKind = '' | 'video' | 'audio' | 'subtitle' | 'data';

interface Props {
  run: RunState;
  /** Per-node media kind, derived from edge stream types in app.tsx.
   *  Used to label throughput correctly: video → FPS, anything else
   *  → packets/s. Missing entries fall through to packets/s. */
  nodeKinds: Map<string, NodeKind>;
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

interface Sample {
  /** Wall clock time in ms when the snapshot was received. */
  t: number;
  /** Aggregate output PTS in ns at that moment. */
  outputPTS: number;
  /** Per-node Frames count at that moment. */
  frames: Map<string, number>;
}

/** Sliding-window length used for rate estimates. Smaller windows
 *  react faster but jitter more; the metrics emitter ticks every ~1s
 *  so 5s gives ~5 samples to average over. */
const WINDOW_MS = 5_000;

/**
 * RunPanel — body of the bottom dock. Mirrors the VS Code terminal
 * pattern: a thin status header on top, a two-column body underneath
 * (per-node metrics on the left, scrolling log stream on the right).
 *
 * Throughput numbers (overall realtime speed and per-node FPS /
 * packets-per-second) are computed from a sliding window of recent
 * snapshots, NOT from the cumulative-since-start values returned by
 * the backend. The cumulative values steadily decay toward zero once
 * a fast demuxer races ahead of slow encoders or the run finishes,
 * which is what made the old display "start high and drift down".
 */
export function RunPanel({ run, nodeKinds, onClose }: Props) {
  const totalPackets = run.metrics?.Nodes.reduce((s, n) => s + n.Frames, 0) ?? 0;
  const totalErrors = run.metrics?.Nodes.reduce((s, n) => s + n.Errors, 0) ?? 0;

  /* Sliding window of recent snapshots — reset whenever a new run
     starts (jobId changes) so rates don't leak across runs. */
  const samplesRef = useRef<Sample[]>([]);
  const [, force] = useState(0);
  useEffect(() => {
    samplesRef.current = [];
    force((n) => n + 1);
  }, [run.jobId]);

  useEffect(() => {
    if (!run.metrics) return;
    const frames = new Map<string, number>();
    for (const n of run.metrics.Nodes) frames.set(n.NodeID, n.Frames);
    const sample: Sample = {
      t: Date.now(),
      outputPTS: run.metrics.OutputPTS ?? 0,
      frames,
    };
    const arr = samplesRef.current;
    arr.push(sample);
    const cutoff = sample.t - WINDOW_MS * 2; // keep a little extra slack
    while (arr.length > 1 && arr[0].t < cutoff) arr.shift();
    force((n) => n + 1);
  }, [run.metrics]);

  // Pick the oldest sample within the window for rate estimates.
  const samples = samplesRef.current;
  const newest = samples[samples.length - 1];
  let baseline: Sample | undefined;
  if (newest) {
    const cutoff = newest.t - WINDOW_MS;
    for (const s of samples) {
      if (s.t <= cutoff) baseline = s;
      else break;
    }
    // Fall back to the oldest sample we have if the window isn't full
    // yet — gives a reasonable estimate within the first few seconds
    // instead of "—".
    if (!baseline && samples.length > 1) baseline = samples[0];
  }

  const mediaPTS = run.metrics?.MediaPTS ?? 0;
  const mediaDuration = run.metrics?.MediaDuration ?? 0;
  const outputPTS = run.metrics?.OutputPTS ?? 0;
  const haveDuration = mediaDuration > 0;
  // Progress and ETA are based on the OUTPUT side of the pipeline:
  // how much media has actually been encoded + muxed. Source-side
  // mediaPTS would jump to 100% as soon as the demuxer finished, long
  // before the output is complete.
  const progressBase = outputPTS > 0 ? outputPTS : mediaPTS;
  const progressPct = haveDuration ? Math.min(100, (progressBase / mediaDuration) * 100) : 0;

  // Windowed realtime speed: how many seconds of media-time were
  // produced per wall-second over the recent window. Falls back to
  // cumulative average if we don't have enough samples yet.
  let speedX = 0;
  if (newest && baseline && newest.t > baseline.t) {
    const dWall = (newest.t - baseline.t) / 1000;
    const dMedia = (newest.outputPTS - baseline.outputPTS) / 1e9;
    if (dWall > 0 && dMedia > 0) speedX = dMedia / dWall;
  }
  if (speedX <= 0 && (run.metrics?.Elapsed ?? 0) > 0 && progressBase > 0) {
    speedX = progressBase / (run.metrics?.Elapsed ?? 1);
  }
  const remainingSec = haveDuration && speedX > 0 ? (mediaDuration - progressBase) / 1e9 / speedX : 0;
  const remainingNs = remainingSec * 1e9;

  /** Compute the windowed per-node frames-per-second rate, falling
   *  back to the cumulative FPS when we don't yet have two samples. */
  const rateFor = (nodeID: string, fallback: number): number => {
    if (newest && baseline && newest.t > baseline.t) {
      const a = baseline.frames.get(nodeID);
      const b = newest.frames.get(nodeID);
      if (a !== undefined && b !== undefined) {
        const dWall = (newest.t - baseline.t) / 1000;
        const d = b - a;
        if (dWall > 0 && d >= 0) return d / dWall;
      }
    }
    return fallback;
  };

  return (
    <div className="run-panel">
      <div className="run-panel-header">
        <span className={`run-status run-status-${run.status}`}>{run.status}</span>
        {run.pipelineState && <span className="run-pipeline-state">{run.pipelineState}</span>}
        <span style={{ color: 'var(--text-dim)' }}>
          packets: {totalPackets} · errors: {totalErrors}
        </span>
        {(progressBase > 0 || haveDuration) && (
          <span style={{ color: 'var(--text-dim)' }}>
            media: {formatHMS(progressBase)}
            {haveDuration && ` / ${formatHMS(mediaDuration)}`}
            {haveDuration && ` (${progressPct.toFixed(1)}%)`}
          </span>
        )}
        {speedX > 0 && (
          <span style={{ color: 'var(--text-dim)' }}>
            {speedX.toFixed(2)}× realtime
            {haveDuration && remainingSec > 0 && ` · ETA ${formatHMS(remainingNs)}`}
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
                  <th>Packets</th>
                  <th>Rate</th>
                  <th>Errors</th>
                  <th>Avg latency</th>
                </tr>
              </thead>
              <tbody>
                {run.metrics.Nodes.map((n) => {
                  const kind = nodeKinds.get(n.NodeID) ?? '';
                  const rate = rateFor(n.NodeID, n.FPS);
                  const unit = kind === 'video' ? 'fps' : 'pkt/s';
                  return (
                    <tr key={n.NodeID}>
                      <td>{n.NodeID}</td>
                      <td>{n.Frames}</td>
                      <td>{rate.toFixed(1)} {unit}</td>
                      <td className={n.Errors > 0 ? 'cell-error' : ''}>{n.Errors}</td>
                      <td>{(n.AvgLatency / 1e6).toFixed(2)} ms</td>
                    </tr>
                  );
                })}
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

// Re-export type so existing imports keep resolving.
export type { MetricsSnapshot };
