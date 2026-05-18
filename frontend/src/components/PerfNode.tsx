import { memo } from 'react'
import { Handle, Position } from '@xyflow/react'
import type { NodeProps } from '@xyflow/react'
import type { NodePerfSnapshot } from '../lib/types'

// fmtNs converts a nanosecond duration to a human-readable string.
function fmtNs(ns: number): string {
  if (ns <= 0) return '—'
  if (ns < 1_000_000) return `${(ns / 1000).toFixed(0)}µs`
  return `${(ns / 1_000_000).toFixed(1)}ms`
}

// deficitColor returns the CSS color for the FPS deficit badge.
//   green  → headroom (deficit ≤ 0)
//   yellow → mild deficit (0 < deficit ≤ 1 fps)
//   red    → significant deficit (deficit > 1 fps)
function deficitColor(deficit: number): string {
  if (deficit > 1.0) return 'var(--mm-red)'
  if (deficit > 0.2) return 'var(--mm-yellow)'
  return 'var(--mm-green)'
}

// PerfNode is the custom React Flow node that renders per-node performance
// data as a coloured activity bar (green=processing, yellow=idle, red=stalled)
// with an FPS deficit badge.  It receives a NodePerfSnapshot as its `data`
// prop via the React Flow node data field.
export const PerfNode = memo(function PerfNode({
  data,
}: NodeProps & { data: NodePerfSnapshot }) {
  const active = data.ActiveFrac ?? 0
  const idle = data.IdleFrac ?? 0
  const stalled = data.StalledFrac ?? 0
  const deficit = data.FPSDeficit ?? 0
  const fps = data.FPS ?? 0
  const fpsTarget = data.FPSTarget ?? 0
  const queueFill = data.QueueFillFrac ?? 0
  const threads = data.ThreadsConfigured ?? 0
  const busy = data.ThreadsBusy ?? -1
  const latency = data.FrameLatencyMean ?? 0

  const dColor = deficitColor(deficit)

  return (
    <div className="mm-node">
      <Handle type="target" position={Position.Left} />

      {/* Header: node ID + FPS badge */}
      <div className="mm-node-header">
        <span className="mm-node-id">{data.NodeID}</span>
        <span className="mm-fps-badge" style={{ color: dColor }}>
          {fps.toFixed(1)}
          {fpsTarget > 0 && (
            <span className="mm-fps-target">/{fpsTarget.toFixed(0)}</span>
          )}
          {deficit > 0.05 && (
            <span className="mm-deficit-badge" style={{ color: dColor }}>
              {' '}+{deficit.toFixed(1)}
            </span>
          )}
        </span>
      </div>

      {/* Activity bar: green=active, yellow=idle, red=stalled */}
      <div
        className="mm-activity-bar"
        title={`Active ${(active * 100).toFixed(0)}% · Idle ${(idle * 100).toFixed(0)}% · Stalled ${(stalled * 100).toFixed(0)}%`}
      >
        <div
          className="mm-bar-seg mm-bar-active"
          style={{ width: `${active * 100}%` }}
        />
        <div
          className="mm-bar-seg mm-bar-idle"
          style={{ width: `${idle * 100}%` }}
        />
        <div
          className="mm-bar-seg mm-bar-stalled"
          style={{ width: `${stalled * 100}%` }}
        />
      </div>

      {/* Stats row */}
      <div className="mm-stats-row">
        <span title="Threads configured / busy">
          T:{threads}
          {busy >= 0 ? `/${busy}` : ''}
        </span>
        <span title="Output queue fill">Q:{(queueFill * 100).toFixed(0)}%</span>
        <span title="Frame latency (EWMA)">{fmtNs(latency)}</span>
      </div>

      <Handle type="source" position={Position.Right} />
    </div>
  )
})
