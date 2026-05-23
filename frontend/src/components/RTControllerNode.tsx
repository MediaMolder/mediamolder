import { memo } from 'react'
import type { NodeProps } from '@xyflow/react'
import type { FlowNodeData } from '../lib/jsonAdapter'
import type { RTControllerSnapshot, ControllerNodeSnapshot } from '../lib/rtSnapshot'

// statusBadgeClass returns the CSS modifier for the status pill.
function statusBadgeClass(status: string): string {
  switch (status) {
    case 'satisfied': return 'rtc-badge--ok'
    case 'cooldown':  return 'rtc-badge--cooldown'
    case 'dropping':  return 'rtc-badge--dropping'
    case 'observing': return 'rtc-badge--observing'
    default:          return 'rtc-badge--disabled'
  }
}

// dotClass returns the CSS modifier for a per-node dot indicator.
function dotClass(n: ControllerNodeSnapshot): string {
  if (n.CooldownRemaining > 0) return 'rtc-dot--cooldown'
  if (n.FPSDeficit > 0.5)     return 'rtc-dot--behind'
  if (n.FPSDeficit > 0.05)    return 'rtc-dot--mild'
  return 'rtc-dot--ok'
}

/**
 * RTControllerNode is a synthetic, read-only React Flow node that represents the
 * real-time controller overlay.  It is injected into decoratedNodes by app.tsx
 * whenever a live RTControllerSnapshot is available (realtime mode + running).
 *
 * The node is not draggable, deletable, or connectable.  Clicking it opens
 * RTControllerInspector via the normal selectedId / Inspector routing in app.tsx.
 */
export const RTControllerNode = memo(function RTControllerNode({
  data,
}: NodeProps & { data: FlowNodeData }) {
  const snap = data.snapshot as RTControllerSnapshot | null | undefined

  if (!snap || !snap.Enabled) {
    return (
      <div className="rtc-node rtc-node--idle">
        <div className="rtc-header">
          <span className="rtc-icon" aria-hidden>⚙</span>
          <span className="rtc-title">Real-Time Controller</span>
          <span className="rtc-badge rtc-badge--disabled">idle</span>
        </div>
      </div>
    )
  }

  const fpsLabel = `${snap.FPSActual.toFixed(1)}\u202f/\u202f${snap.FPSTarget.toFixed(1)} fps`
  const cooldownLabel = snap.CooldownWindows > 0 ? ` (cd\u00a0${snap.CooldownWindows})` : ''

  return (
    <div className="rtc-node">
      <div className="rtc-header">
        <span className="rtc-icon" aria-hidden>⚙</span>
        <span className="rtc-title">Real-Time Controller</span>
        <span className={`rtc-badge ${statusBadgeClass(snap.Status)}`}>
          {snap.Status}{cooldownLabel}
        </span>
        <span className="rtc-fps">{fpsLabel}</span>
      </div>
      {snap.Nodes.length > 0 && (
        <div className="rtc-dots">
          {snap.Nodes.map((n) => (
            <span
              key={n.NodeID}
              className={`rtc-dot ${dotClass(n)}`}
              title={`${n.NodeID}  preset=${n.CurrentPreset || '—'}  Δfps=${n.FPSDeficit > 0 ? '+' : ''}${n.FPSDeficit.toFixed(2)}  cd=${n.CooldownRemaining}`}
            />
          ))}
        </div>
      )}
    </div>
  )
})
