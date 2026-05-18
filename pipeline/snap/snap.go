package snap
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
}
