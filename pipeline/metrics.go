// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// NodeMetrics holds per-node performance counters. Fields are updated
// atomically from processing goroutines.
type NodeMetrics struct {
	NodeID    string
	Frames    atomic.Int64
	Errors    atomic.Int64
	Bytes     atomic.Int64
	StartTime time.Time

	// Per-frame latency tracking.
	latencySum   atomic.Int64 // cumulative nanoseconds
	latencyCount atomic.Int64
	latencyMax   atomic.Int64 // peak nanoseconds

	// Media-time progress tracking. Source nodes update mediaPTSNs as
	// they read packets so the GUI can show how far through the input
	// stream we are. mediaDurationNs is set once at source-open from
	// the demuxer's reported stream duration; it stays 0 for live or
	// unknown-duration inputs. Both are nanoseconds.
	mediaPTSNs      atomic.Int64
	mediaDurationNs atomic.Int64

	// outputPTSNs is set by sink nodes after each successfully
	// written packet (max across streams). It tracks how far the
	// pipeline output has actually advanced — i.e. how much media
	// has been *encoded and muxed*, not just demuxed. This is the
	// signal the GUI wants for progress/ETA/speed: the source
	// demuxer typically races ahead of encoders, so source mediaPTS
	// hits 100% long before the run is finished.
	outputPTSNs atomic.Int64

	mu sync.Mutex
}

// RecordLatency records a single frame's processing duration and
// increments the frame counter. Handlers call this once per frame they
// successfully processed, so it doubles as the FPS / throughput source.
func (m *NodeMetrics) RecordLatency(d time.Duration) {
	m.Frames.Add(1)
	ns := d.Nanoseconds()
	m.latencySum.Add(ns)
	m.latencyCount.Add(1)
	for {
		cur := m.latencyMax.Load()
		if ns <= cur || m.latencyMax.CompareAndSwap(cur, ns) {
			break
		}
	}
}

// SetMediaDuration records the total media duration of an input
// (nanoseconds). Source handlers call this once after opening the
// demuxer. A value of 0 signals "unknown" (e.g. live streams).
func (m *NodeMetrics) SetMediaDuration(d time.Duration) {
	m.mediaDurationNs.Store(d.Nanoseconds())
}

// AdvanceMediaPTS bumps the latest media-time position observed on
// this source node. Updates are monotonic — out-of-order packets
// (e.g. B-frames) leave the value unchanged.
func (m *NodeMetrics) AdvanceMediaPTS(d time.Duration) {
	ns := d.Nanoseconds()
	for {
		cur := m.mediaPTSNs.Load()
		if ns <= cur || m.mediaPTSNs.CompareAndSwap(cur, ns) {
			return
		}
	}
}

// AdvanceOutputPTS bumps the latest output-side media position for
// this node (sink-side). Monotonic; out-of-order packet timestamps are
// ignored.
func (m *NodeMetrics) AdvanceOutputPTS(d time.Duration) {
	ns := d.Nanoseconds()
	for {
		cur := m.outputPTSNs.Load()
		if ns <= cur || m.outputPTSNs.CompareAndSwap(cur, ns) {
			return
		}
	}
}

// Snapshot returns a point-in-time copy of the metrics.
func (m *NodeMetrics) Snapshot() NodeMetricsSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	frames := m.Frames.Load()
	elapsed := time.Since(m.StartTime).Seconds()
	var fps float64
	if elapsed > 0 {
		fps = float64(frames) / elapsed
	}

	var avgLatency, maxLatency time.Duration
	if cnt := m.latencyCount.Load(); cnt > 0 {
		avgLatency = time.Duration(m.latencySum.Load() / cnt)
		maxLatency = time.Duration(m.latencyMax.Load())
	}

	return NodeMetricsSnapshot{
		NodeID:        m.NodeID,
		Frames:        frames,
		Errors:        m.Errors.Load(),
		Bytes:         m.Bytes.Load(),
		FPS:           fps,
		Elapsed:       time.Since(m.StartTime),
		AvgLatency:    avgLatency,
		MaxLatency:    maxLatency,
		MediaPTS:      time.Duration(m.mediaPTSNs.Load()),
		MediaDuration: time.Duration(m.mediaDurationNs.Load()),
		OutputPTS:     time.Duration(m.outputPTSNs.Load()),
	}
}

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
}

// MetricsRegistry tracks metrics for all nodes in a pipeline.
type MetricsRegistry struct {
	mu    sync.RWMutex
	nodes map[string]*NodeMetrics
	start time.Time
}

// NewMetricsRegistry creates a registry and records the start time.
func NewMetricsRegistry() *MetricsRegistry {
	return &MetricsRegistry{
		nodes: make(map[string]*NodeMetrics),
		start: time.Now(),
	}
}

// Node returns (or creates) the NodeMetrics for the given node ID.
func (r *MetricsRegistry) Node(id string) *NodeMetrics {
	r.mu.RLock()
	m, ok := r.nodes[id]
	r.mu.RUnlock()
	if ok {
		return m
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	// Double-check after acquiring write lock.
	if m, ok = r.nodes[id]; ok {
		return m
	}
	m = &NodeMetrics{NodeID: id, StartTime: r.start}
	r.nodes[id] = m
	return m
}

// Snapshot returns a complete metrics snapshot.
func (r *MetricsRegistry) Snapshot() MetricsSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()

	snap := MetricsSnapshot{
		Elapsed: time.Since(r.start),
		Nodes:   make([]NodeMetricsSnapshot, 0, len(r.nodes)),
	}
	for _, m := range r.nodes {
		snap.Nodes = append(snap.Nodes, m.Snapshot())
	}
	// Aggregate media-time progress across all source nodes. Take the
	// max so multi-input jobs report progress against the longest
	// input, which is usually what a user expects.
	//
	// OutputPTS is aggregated as the *min* across sinks that have
	// started writing (>0). The pipeline's effective progress is
	// constrained by its slowest sink — taking the max would let a
	// fast audio sink that races ahead of the video encoder report
	// 100% completion while video still has minutes left, which makes
	// the GUI's ETA/speed estimates meaningless. Sinks that haven't
	// produced any packet yet are ignored so a not-yet-started sink
	// doesn't pin progress at 0 forever.
	var minOut time.Duration
	var sawSink bool
	for i := range snap.Nodes {
		if snap.Nodes[i].MediaPTS > snap.MediaPTS {
			snap.MediaPTS = snap.Nodes[i].MediaPTS
		}
		if snap.Nodes[i].MediaDuration > snap.MediaDuration {
			snap.MediaDuration = snap.Nodes[i].MediaDuration
		}
		if op := snap.Nodes[i].OutputPTS; op > 0 {
			if !sawSink || op < minOut {
				minOut = op
				sawSink = true
			}
		}
	}
	if sawSink {
		snap.OutputPTS = minOut
	}
	// Stable, deterministic order so the GUI metrics table doesn't
	// reshuffle rows on every poll. Map iteration order is randomised
	// in Go and would otherwise cause the rows to jump around.
	sort.Slice(snap.Nodes, func(i, j int) bool {
		return snap.Nodes[i].NodeID < snap.Nodes[j].NodeID
	})
	return snap
}
