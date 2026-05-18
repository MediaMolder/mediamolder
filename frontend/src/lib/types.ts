// NodePerfSnapshot mirrors the Go pipeline.NodePerfSnapshot struct.
// Fields are PascalCase to match the JSON encoding from the Go backend.
export interface NodePerfSnapshot {
  NodeID: string
  FPS: number
  FPSTarget: number
  FPSDeficit: number
  ActiveFrac: number
  IdleFrac: number
  StalledFrac: number
  StallCount: number
  MaxStallDuration: number   // nanoseconds
  QueueFillFrac: number
  ThreadsConfigured: number
  ThreadMode: string
  ThreadsBusy: number        // -1 if unavailable
  EstimatedCPUCores: number
  FrameLatencyMean: number   // nanoseconds
}

// MetricsSnapshot mirrors the Go pipeline.MetricsSnapshot struct.
export interface MetricsSnapshot {
  State: string
  Elapsed: number            // nanoseconds
  Nodes: NodeMetricsSnapshot[]
  Perf: NodePerfSnapshot[] | null
}

export interface NodeMetricsSnapshot {
  NodeID: string
  FramesIn: number
  FramesOut: number
  Errors: number
  BytesIn: number
  BytesOut: number
}
