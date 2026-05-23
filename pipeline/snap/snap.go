// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

// Package snap contains the shared snapshot types for pipeline metrics so that
// both the pipeline and observability packages can reference them without an
// import cycle.
package snap

import "time"

// NodeMetricsSnapshot is a read-only copy of node metrics at a point in time.
type NodeMetricsSnapshot struct {
	NodeID     string
	Frames     int64
	Errors     int64
	Bytes      int64
	FPS        float64
	Elapsed    time.Duration
	AvgLatency time.Duration
	MaxLatency time.Duration
	// MediaPTS is the latest input timestamp this node has read
	// (source nodes only; 0 elsewhere). MediaDuration is the total
	// known input duration (0 for live / unknown).
	MediaPTS      time.Duration
	MediaDuration time.Duration
	// OutputPTS is the latest output timestamp written by this node
	// (sink nodes only; 0 elsewhere). It reflects how much media has
	// actually been encoded + muxed, which is what the GUI uses for
	// progress/speed/ETA.
	OutputPTS time.Duration
}

// MetricsSnapshot is a complete metrics snapshot for the pipeline.
type MetricsSnapshot struct {
	State   string
	Elapsed time.Duration
	Nodes   []NodeMetricsSnapshot
	// MediaPTS / MediaDuration are aggregated across all source nodes
	// (max of per-source values), giving the GUI a single
	// "how-far-through-the-input" pair without needing to know which
	// node is the source. MediaDuration is 0 when no input declares
	// one (live streams).
	MediaPTS      time.Duration
	MediaDuration time.Duration
	// OutputPTS is the slowest sink's latest output timestamp (min
	// over sinks that have started writing). It tracks how much
	// media has actually been written by every output and is the
	// basis for progress/speed/ETA in the GUI — using max here would
	// let a fast sink (e.g. AAC audio) report 100% before the slower
	// video encoder is anywhere close to done.
	OutputPTS time.Duration
	// Perf holds per-node performance timing snapshots collected by the
	// NodePerfTracker instances registered via RegisterPerfTracker.
	Perf []NodePerfSnapshot

	// Realtime is the Phase 6 graph-level summary populated when adaptive
	// real-time mode is enabled. Zero value is reported when realtime is
	// off so existing JSON consumers see no new fields.
	Realtime RealtimeSnapshot `json:",omitempty"`
}

// RealtimeSnapshot is the graph-level adaptive real-time summary attached to
// MetricsSnapshot.Realtime when --realtime is enabled. All fields are
// optional; consumers should treat zero values as "not applicable".
type RealtimeSnapshot struct {
	Enabled   bool             `json:",omitempty"`
	FPSTarget float64          `json:",omitempty"` // max of per-node targets
	FPSActual float64          `json:",omitempty"` // min of per-video-node fps
	Satisfied bool             `json:",omitempty"`
	Decisions []DecisionRecord `json:",omitempty"`
	// Outputs holds the Phase 7 per-output preroll readiness. Empty
	// when realtime mode is off or no outputs are configured.
	Outputs []OutputBufferSnapshot `json:",omitempty"`
	// Ready is true once every entry of Outputs is in
	// {READY, READY_PARTIAL, STREAMING}.
	Ready bool `json:",omitempty"`
	// ReadyAt is the wall-clock time at which Ready first became true;
	// zero when still filling.
	ReadyAt time.Time `json:",omitempty"`
}

// OutputBufferSnapshot is the per-output Phase 7 preroll state attached
// to MetricsSnapshot for the GUI toolbar pill, /realtime/ready, and the
// pipeline_output_buffer_* Prometheus metrics.
type OutputBufferSnapshot struct {
	NodeID      string        `json:"node_id"`
	State       string        `json:"state"`
	BufferedDur time.Duration `json:"buffered_ns"`
	TargetDur   time.Duration `json:"target_ns"`
	Evictions   int64         `json:"evictions"`
}

// DecisionRecord captures one autonomous step taken by the real-time
// controller. Exposed via /realtime/decisions, `mediamolder perf
// --decisions`, and pipeline.RealtimeDecisions().
type DecisionRecord struct {
	Time    time.Time `json:"time"`
	NodeID  string    `json:"node"`
	Action  string    `json:"action"` // "step_faster", "step_slower", "restart_threads", "drop_frames", "lock"
	From    string    `json:"from,omitempty"`
	To      string    `json:"to,omitempty"`
	Deficit float64   `json:"deficit,omitempty"`
	Reason  string    `json:"reason,omitempty"`
}

// NodePerfSnapshot is a point-in-time read of all performance data for one node.
type NodePerfSnapshot struct {
	NodeID string

	// Windowed throughput (computed over the last ~perfTsBufSize frames).
	FPS        float64
	FPSTarget  float64 // desired output frame rate; 0 = no target set
	FPSDeficit float64 // FPSTarget − FPS; positive = behind; negative = headroom

	// Time-distribution fractions (0.0–1.0, always sum to 1.0).
	ActiveFrac  float64 // fraction of time in PROCESSING
	IdleFrac    float64 // fraction of time in IDLE (waiting for input)
	StalledFrac float64 // fraction of time in STALLED (output channel full)

	// Absolute cumulative durations.
	TotalActive  time.Duration
	TotalIdle    time.Duration
	TotalStalled time.Duration

	// Stall event detail.
	StallCount       int64
	MaxStallDuration time.Duration

	// EWMA of input channel fill fraction at receive time (0.0–1.0).
	// A sustained value near 0.0 indicates this node is starved by upstream.
	InputQueueFillFrac float64

	// EWMA of output channel fill fraction at send time (0.0–1.0).
	// A sustained value near 1.0 indicates this node produces faster than
	// its downstream can consume.
	QueueFillFrac float64

	// Total elapsed wall-clock time since the node started.
	Elapsed time.Duration

	// Thread information. Populated from av package accessors via SetThreadInfo
	// and SetThreadBusyFn.
	ThreadsConfigured int     // libav configured thread count (0 = unknown/n/a)
	ThreadMode        string  // "none", "frame", "slice", "auto", "n/a"
	ThreadsBusy       int     // live tasks in-flight; -1 = not available
	EstimatedCPUCores float64 // ThreadsConfigured × ActiveFrac; upper-bound estimate

	// EWMA of frame processing latency (wall-clock time from perfReceive to
	// last perfSend for a given frame).  Set by RecordFrameLatency.
	FrameLatencyMean time.Duration

	// ThreadRestarts is the cumulative number of graceful codec restarts
	// triggered by the real-time adaptive control loop (Phase 5).
	// Monotonically non-decreasing. 0 when real-time mode is disabled.
	ThreadRestarts int64

	// Phase 6: adaptive preset stepping. CodecName is the encoder codec
	// (libx264/libx265/libsvtav1); empty for non-encoder nodes. CurrentPreset
	// is the live preset name; PresetLadder is the ordered slowest→fastest
	// list of presets the controller may select; PresetIndex is the
	// position of CurrentPreset within PresetLadder (-1 when not on the
	// ladder). PresetSwitches counts completed transitions; PresetLocked
	// reports whether automatic stepping is disabled for this node.
	CodecName      string   `json:",omitempty"`
	CurrentPreset  string   `json:",omitempty"`
	PresetLadder   []string `json:",omitempty"`
	PresetIndex    int      `json:",omitempty"`
	PresetSwitches int64    `json:",omitempty"`
	PresetLocked   bool     `json:",omitempty"`
}

// ControllerNodeSnapshot is the controller's per-tick view of one video
// encoder node: both the observed performance metrics and the controller's
// applied state.
type ControllerNodeSnapshot struct {
	NodeID string

	// Observed metrics read by the controller each tick.
	FPS                  float64
	FPSTarget            float64
	FPSDeficit           float64
	ActiveFrac           float64
	StalledFrac          float64
	IdleFrac             float64
	ThreadsConfigured    int
	ThreadsBusy          int // -1 = unavailable
	InputBufferFillFrac  float64
	OutputBufferFillFrac float64
	FrameLatencyMean     time.Duration

	// Controller-applied state.
	CurrentPreset      string
	PresetIndex        int
	PresetLadder       []string
	PresetLocked       bool
	PresetSwitches     int64
	WindowsSincePreset int
	CooldownRemaining  int // max(0, rtPresetCooldownWins - WindowsSincePreset)
	OvershootWindows   int
	ThreadRestarts     int64
}

// SinkNodeSnapshot captures the output-buffer state of a muxer/sink node.
type SinkNodeSnapshot struct {
	NodeID               string
	OutputBufferFillFrac float64
	// BufferedNs is the currently buffered PTS span in nanoseconds.
	// TargetNs is the configured target fill duration in nanoseconds.
	BufferedNs int64
	TargetNs   int64
}

// RTControllerSnapshot is the full per-tick state of the realtime controller.
// Exposed via GET /realtime/snapshot and the /realtime/snapshot/stream SSE feed.
type RTControllerSnapshot struct {
	Enabled bool
	// Status is one of: "disabled", "observing", "cooldown", "dropping", "satisfied".
	Status               string
	Tick                 int64 // monotonically increasing observe() call count; JSON-only (not shown in CLI table)
	Elapsed              time.Duration
	FPSTarget            float64
	FPSActual            float64
	Satisfied            bool
	HighestQualityPreset string
	GroupStep            bool
	// CooldownWindows is max(node.CooldownRemaining) across all controlled nodes.
	CooldownWindows int
	TickIntervalMs  int64
	Nodes           []ControllerNodeSnapshot
	Sinks           []SinkNodeSnapshot
	RecentDecisions []DecisionRecord
}
