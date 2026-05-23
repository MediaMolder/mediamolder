import { useState, useRef, useEffect } from 'react'
import type { RTControllerSnapshot, ControllerNodeSnapshot } from '../lib/rtSnapshot'

interface Props {
  snapshot: RTControllerSnapshot | null
}

// fmtNs converts a nanosecond duration to a human-readable string.
function fmtNs(ns: number): string {
  if (!ns || ns <= 0) return '—'
  if (ns < 1_000_000) return `${(ns / 1000).toFixed(0)}µs`
  return `${(ns / 1_000_000).toFixed(1)}ms`
}

// fmtPct formats a 0–1 fraction as a percentage string.
function fmtPct(v: number): string {
  return `${(v * 100).toFixed(0)}%`
}

// deficitColor returns a CSS color based on an FPS deficit value.
function deficitColor(d: number): string {
  if (d > 1.0) return '#ef4444'
  if (d > 0.2) return '#eab308'
  return '#22c55e'
}

// FillBar renders a horizontal fill bar for buffer occupancy.
function FillBar({ value }: { value: number }) {
  const pct = Math.min(1, Math.max(0, value)) * 100
  let color = '#22c55e'
  if (value > 0.85) color = '#ef4444'
  else if (value > 0.6) color = '#eab308'
  return (
    <div className="rtc-fill-bar" title={`${pct.toFixed(0)}%`}>
      <div className="rtc-fill-bar-inner" style={{ width: `${pct}%`, background: color }} />
    </div>
  )
}

// LadderBar renders the preset position within the preset ladder.
function LadderBar({ ladder, index }: { ladder: string[]; index: number }) {
  if (!ladder.length) return <span style={{ color: 'var(--text-dim)' }}>—</span>
  return (
    <div className="rtc-ladder-bar" title={`${index + 1} / ${ladder.length}: ${ladder[index] ?? '?'}`}>
      {ladder.map((_, i) => (
        <div
          key={i}
          className={`rtc-ladder-seg ${i === index ? 'rtc-ladder-seg--active' : ''}`}
        />
      ))}
    </div>
  )
}

// ObservedTab renders live per-encoder performance metrics.
function ObservedTab({ nodes }: { nodes: ControllerNodeSnapshot[] }) {
  if (!nodes.length) {
    return <p style={{ color: 'var(--text-dim)', fontSize: 12, marginTop: 8 }}>No encoder nodes observed.</p>
  }
  return (
    <div className="rtc-table-wrap">
      <table className="rtc-table">
        <thead>
          <tr>
            <th>Node</th>
            <th>FPS / Target</th>
            <th>Deficit</th>
            <th>Active</th>
            <th>Stalled</th>
            <th>In buf</th>
            <th>Out buf</th>
            <th>Latency</th>
            <th>Threads</th>
          </tr>
        </thead>
        <tbody>
          {nodes.map((n) => (
            <tr
              key={n.NodeID}
              style={{
                background: n.FPSDeficit > 1.0
                  ? 'rgba(239,68,68,0.08)'
                  : n.FPSDeficit > 0.2
                    ? 'rgba(234,179,8,0.06)'
                    : undefined,
              }}
            >
              <td className="rtc-cell-id" title={n.NodeID}>{n.NodeID}</td>
              <td style={{ tabularNums: true } as React.CSSProperties}>
                {n.FPS.toFixed(1)}&thinsp;/&thinsp;{n.FPSTarget.toFixed(1)}
              </td>
              <td style={{ color: deficitColor(n.FPSDeficit), fontVariantNumeric: 'tabular-nums' }}>
                {n.FPSDeficit > 0 ? '+' : ''}{n.FPSDeficit.toFixed(2)}
              </td>
              <td style={{ color: '#22c55e' }}>{fmtPct(n.ActiveFrac)}</td>
              <td style={{ color: n.StalledFrac > 0.1 ? '#ef4444' : undefined }}>{fmtPct(n.StalledFrac)}</td>
              <td><FillBar value={n.InputBufferFillFrac} /></td>
              <td><FillBar value={n.OutputBufferFillFrac} /></td>
              <td style={{ fontVariantNumeric: 'tabular-nums' }}>{fmtNs(n.FrameLatencyMean)}</td>
              <td>
                {n.ThreadsBusy >= 0
                  ? `${n.ThreadsBusy}/${n.ThreadsConfigured}`
                  : String(n.ThreadsConfigured)}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

// OverrideRow renders one preset override control for a single encoder node.
function OverrideRow({ node }: { node: ControllerNodeSnapshot }) {
  const [chosen, setChosen] = useState(node.CurrentPreset)
  const [status, setStatus] = useState<'idle' | 'sending' | 'ok' | 'err'>('idle')

  // Update dropdown when snapshot changes, but only when not mid-interaction.
  useEffect(() => {
    if (status === 'idle') setChosen(node.CurrentPreset)
  }, [node.CurrentPreset, status])

  async function handleOverride() {
    setStatus('sending')
    try {
      const res = await fetch('/realtime/preset', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ node_id: node.NodeID, preset: chosen }),
      })
      setStatus(res.ok ? 'ok' : 'err')
    } catch {
      setStatus('err')
    }
    setTimeout(() => setStatus('idle'), 1500)
  }

  async function handleClear() {
    setStatus('sending')
    try {
      const res = await fetch('/realtime/preset/clear', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ node_id: node.NodeID }),
      })
      setStatus(res.ok ? 'ok' : 'err')
    } catch {
      setStatus('err')
    }
    setTimeout(() => setStatus('idle'), 1500)
  }

  const statusLabel = status === 'sending' ? '…' : status === 'ok' ? '✓' : status === 'err' ? '✗' : ''

  return (
    <tr>
      <td className="rtc-cell-id" title={node.NodeID}>{node.NodeID}</td>
      <td>
        <select
          value={chosen}
          onChange={(e) => setChosen(e.target.value)}
          className="rtc-preset-select"
          disabled={!node.PresetLadder.length}
        >
          {node.PresetLadder.length
            ? node.PresetLadder.map((p) => <option key={p} value={p}>{p}</option>)
            : <option value={node.CurrentPreset}>{node.CurrentPreset || '—'}</option>
          }
        </select>
      </td>
      <td>
        <button
          className="rtc-btn-override"
          onClick={handleOverride}
          disabled={status === 'sending' || !node.PresetLadder.length}
          title="Lock this encoder to the selected preset"
        >
          Override {statusLabel}
        </button>
      </td>
      <td>
        <button
          className="rtc-btn-clear"
          onClick={handleClear}
          disabled={status === 'sending' || !node.PresetLocked}
          title="Clear manual override and resume automatic control"
        >
          Clear
        </button>
      </td>
    </tr>
  )
}

// AppliedTab renders controller state, decision log, and override controls.
function AppliedTab({ snap }: { snap: RTControllerSnapshot }) {
  const [tail, setTail] = useState(true)
  const [localDecisions, setLocalDecisions] = useState(snap.RecentDecisions ?? [])
  const logRef = useRef<HTMLDivElement>(null)

  // Update decisions log when snapshot changes.
  useEffect(() => {
    setLocalDecisions(snap.RecentDecisions ?? [])
  }, [snap.RecentDecisions])

  // Auto-scroll when tail is active.
  useEffect(() => {
    if (tail && logRef.current) {
      logRef.current.scrollTop = logRef.current.scrollHeight
    }
  }, [localDecisions, tail])

  const displayDecisions = [...localDecisions].reverse().slice(0, 40)

  return (
    <div className="rtc-applied-tab">
      {/* Per-encoder preset state */}
      <div className="rtc-section-label">Preset state</div>
      <div className="rtc-table-wrap">
        <table className="rtc-table">
          <thead>
            <tr>
              <th>Node</th>
              <th>Preset</th>
              <th>Ladder</th>
              <th>Cooldown</th>
              <th>Switches</th>
              <th>Locked</th>
            </tr>
          </thead>
          <tbody>
            {snap.Nodes.map((n) => (
              <tr key={n.NodeID}>
                <td className="rtc-cell-id" title={n.NodeID}>{n.NodeID}</td>
                <td style={{ fontVariantNumeric: 'tabular-nums' }}>{n.CurrentPreset || '—'}</td>
                <td><LadderBar ladder={n.PresetLadder} index={n.PresetIndex} /></td>
                <td>{n.CooldownRemaining > 0 ? `${n.CooldownRemaining} win` : '—'}</td>
                <td>{n.PresetSwitches}</td>
                <td style={{ color: n.PresetLocked ? '#ef4444' : '#22c55e' }}>
                  {n.PresetLocked ? 'locked' : 'auto'}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {/* Global configuration */}
      <div className="rtc-section-label" style={{ marginTop: 12 }}>Configuration</div>
      <div className="rtc-config-grid">
        <span>Highest quality preset</span><span>{snap.HighestQualityPreset || '—'}</span>
        <span>Group step</span><span>{snap.GroupStep ? 'yes' : 'no'}</span>
        <span>Cooldown windows</span><span>{snap.CooldownWindows}</span>
        <span>Tick interval</span><span>{snap.TickIntervalMs} ms</span>
      </div>

      {/* Manual override controls */}
      {snap.Nodes.some((n) => n.PresetLadder.length > 0) && (
        <>
          <div className="rtc-section-label" style={{ marginTop: 12 }}>Manual overrides</div>
          <div className="rtc-table-wrap">
            <table className="rtc-table">
              <thead>
                <tr>
                  <th>Node</th>
                  <th>Preset</th>
                  <th></th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                {snap.Nodes.filter((n) => n.PresetLadder.length > 0).map((n) => (
                  <OverrideRow key={n.NodeID} node={n} />
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}

      {/* Decision log */}
      <div className="rtc-section-label" style={{ marginTop: 12, display: 'flex', alignItems: 'center', gap: 8 }}>
        <span>Decision log</span>
        <label className="rtc-tail-label">
          <input type="checkbox" checked={tail} onChange={(e) => setTail(e.target.checked)} />
          tail
        </label>
        <button
          className="rtc-btn-clear"
          onClick={() => setLocalDecisions([])}
          style={{ marginLeft: 'auto', padding: '1px 6px' }}
        >
          Clear
        </button>
      </div>
      <div className="rtc-decision-log" ref={logRef}>
        {displayDecisions.length === 0 && (
          <span style={{ color: 'var(--text-dim)', fontStyle: 'italic' }}>No decisions yet.</span>
        )}
        {displayDecisions.map((d, i) => (
          <div key={i} className="rtc-decision-row">
            <span className="rtc-decision-time">
              {new Date(d.time).toLocaleTimeString()}
            </span>
            <span className="rtc-decision-node">{d.node}</span>
            <span className={`rtc-decision-action rtc-action--${d.action}`}>{d.action}</span>
            {d.from && d.to && (
              <span className="rtc-decision-change">{d.from} → {d.to}</span>
            )}
            {d.deficit !== undefined && d.deficit !== 0 && (
              <span className="rtc-decision-deficit" style={{ color: deficitColor(d.deficit) }}>
                Δ{d.deficit > 0 ? '+' : ''}{d.deficit.toFixed(2)}
              </span>
            )}
            {d.reason && <span className="rtc-decision-reason">{d.reason}</span>}
          </div>
        ))}
      </div>
    </div>
  )
}

/**
 * RTControllerInspector renders the Inspector panel content for the synthetic
 * __rtc__ Real-Time Controller node.  It shows two tabs:
 *   - Observed: live per-encoder performance metrics
 *   - Applied:  preset state, decisions, and manual override controls
 *
 * The snapshot prop is provided by app.tsx from the useRTSnapshot hook, so
 * it already updates live — no additional SSE subscription is needed here.
 */
export function RTControllerInspector({ snapshot }: Props) {
  const [tab, setTab] = useState<'observed' | 'applied'>('observed')

  return (
    <div className="inspector">
      <div className="inspector-header">
        <h3 style={{ margin: 0 }}>⚙ Real-Time Controller</h3>
        {snapshot && (
          <span className={`rtc-status-pill rtc-status-pill--${snapshot.Status}`}>
            {snapshot.Status}
          </span>
        )}
      </div>
      {snapshot && (
        <div className="rtc-fps-summary">
          {snapshot.FPSActual.toFixed(1)}&thinsp;/&thinsp;{snapshot.FPSTarget.toFixed(1)} fps
          &ensp;·&ensp;tick {snapshot.Tick}
          &ensp;·&ensp;{(snapshot.Elapsed / 1e9).toFixed(1)}s
        </div>
      )}
      <div className="inspector-tabs" style={{ marginTop: 8 }}>
        <button
          className={`inspector-tab${tab === 'observed' ? ' active' : ''}`}
          onClick={() => setTab('observed')}
        >
          Observed
        </button>
        <button
          className={`inspector-tab${tab === 'applied' ? ' active' : ''}`}
          onClick={() => setTab('applied')}
        >
          Applied
        </button>
      </div>

      {!snapshot && (
        <p style={{ color: 'var(--text-dim)', fontStyle: 'italic', fontSize: 12 }}>
          Waiting for real-time controller snapshot…
        </p>
      )}

      {snapshot && tab === 'observed' && (
        <ObservedTab nodes={snapshot.Nodes} />
      )}
      {snapshot && tab === 'applied' && (
        <AppliedTab snap={snapshot} />
      )}
    </div>
  )
}
