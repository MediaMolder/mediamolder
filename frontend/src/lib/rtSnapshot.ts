import { useEffect, useState } from 'react'

// Mirrors pipeline/snap.ControllerNodeSnapshot (Go PascalCase, no explicit json tags).
export interface ControllerNodeSnapshot {
  NodeID: string
  FPS: number
  FPSTarget: number
  FPSDeficit: number
  ActiveFrac: number
  StalledFrac: number
  IdleFrac: number
  ThreadsConfigured: number
  ThreadsBusy: number
  InputBufferFillFrac: number
  OutputBufferFillFrac: number
  FrameLatencyMean: number    // nanoseconds
  CurrentPreset: string
  PresetIndex: number
  PresetLadder: string[]
  PresetLocked: boolean
  PresetSwitches: number
  WindowsSincePreset: number
  CooldownRemaining: number
  OvershootWindows: number
  ThreadRestarts: number
}

// Mirrors pipeline/snap.SinkNodeSnapshot.
export interface SinkNodeSnapshot {
  NodeID: string
  OutputBufferFillFrac: number
}

// Mirrors pipeline/snap.DecisionRecord (has explicit json tags in Go).
export interface RTDecisionRecord {
  time: string       // RFC3339
  node: string
  action: string     // "step_faster"|"step_slower"|"restart_threads"|"drop_frames"|"lock"
  from?: string
  to?: string
  deficit?: number
  reason?: string
}

// Mirrors pipeline/snap.RTControllerSnapshot.
export interface RTControllerSnapshot {
  Enabled: boolean
  Status: string    // "disabled"|"observing"|"cooldown"|"dropping"|"satisfied"
  Tick: number
  Elapsed: number   // nanoseconds
  FPSTarget: number
  FPSActual: number
  Satisfied: boolean
  HighestQualityPreset: string
  GroupStep: boolean
  CooldownWindows: number
  TickIntervalMs: number
  Nodes: ControllerNodeSnapshot[]
  Sinks: SinkNodeSnapshot[]
  RecentDecisions: RTDecisionRecord[]
}

/**
 * useRTSnapshot subscribes to /realtime/snapshot/stream SSE while enabled=true.
 * Returns the most recently received RTControllerSnapshot, or null when disabled
 * or when realtime mode is not active.
 */
export function useRTSnapshot(enabled: boolean): RTControllerSnapshot | null {
  const [snapshot, setSnapshot] = useState<RTControllerSnapshot | null>(null)

  useEffect(() => {
    if (!enabled) {
      setSnapshot(null)
      return
    }

    const source = new EventSource('/realtime/snapshot/stream')

    source.onmessage = (event: MessageEvent<string>) => {
      try {
        const snap = JSON.parse(event.data) as RTControllerSnapshot
        setSnapshot(snap.Enabled ? snap : null)
      } catch {
        // ignore malformed events
      }
    }

    // Server sends "event: error" and closes when realtime mode ends.
    // Distinguish from an HTTP-level error (plain Event) by checking for MessageEvent.
    source.addEventListener('error', (e: Event) => {
      if (e instanceof MessageEvent) {
        // Server-sent named "error" event — realtime mode ended; stop reconnecting.
        source.close()
        setSnapshot(null)
      }
      // HTTP-level error: EventSource auto-reconnects; do nothing.
    })

    return () => {
      source.close()
      setSnapshot(null)
    }
  }, [enabled])

  return snapshot
}
