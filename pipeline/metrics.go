// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
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

	mu sync.Mutex
}

// RecordLatency records a single frame's processing duration.
func (m *NodeMetrics) RecordLatency(d time.Duration) {
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
		NodeID:     m.NodeID,
		Frames:     frames,
		Errors:     m.Errors.Load(),
		Bytes:      m.Bytes.Load(),
		FPS:        fps,
		Elapsed:    time.Since(m.StartTime),
		AvgLatency: avgLatency,
		MaxLatency: maxLatency,
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
}

// MetricsSnapshot is a complete metrics snapshot for the pipeline.
type MetricsSnapshot struct {
	State   string
	Elapsed time.Duration
	Nodes   []NodeMetricsSnapshot
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
	return snap
}
