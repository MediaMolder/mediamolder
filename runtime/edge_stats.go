// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package runtime

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// EdgeStats tracks per-edge backpressure metrics.
type EdgeStats struct {
	EdgeID   string // "from→to:port_type"
	FromNode string
	ToNode   string
	PortType string
	BufSize  int // channel capacity

	fill     atomic.Int64 // fill ratio * 1e9 (fixed-point)
	peakFill atomic.Int64 // highest fill ratio * 1e9
	stalls   atomic.Int64 // samples where channel was full
}

// Fill returns the most recently sampled fill ratio (0.0–1.0).
func (e *EdgeStats) Fill() float64 { return float64(e.fill.Load()) / 1e9 }

// PeakFill returns the highest fill ratio observed.
func (e *EdgeStats) PeakFill() float64 { return float64(e.peakFill.Load()) / 1e9 }

// Stalls returns the number of samples where the channel was completely full.
func (e *EdgeStats) Stalls() int64 { return e.stalls.Load() }

// EdgeStatsSnapshot is a point-in-time copy of edge stats.
type EdgeStatsSnapshot struct {
	EdgeID   string
	FromNode string
	ToNode   string
	PortType string
	BufSize  int
	Fill     float64
	PeakFill float64
	Stalls   int64
}

// Snapshot returns a point-in-time copy.
func (e *EdgeStats) Snapshot() EdgeStatsSnapshot {
	return EdgeStatsSnapshot{
		EdgeID:   e.EdgeID,
		FromNode: e.FromNode,
		ToNode:   e.ToNode,
		PortType: e.PortType,
		BufSize:  e.BufSize,
		Fill:     e.Fill(),
		PeakFill: e.PeakFill(),
		Stalls:   e.Stalls(),
	}
}

// EdgeStatsRegistry holds stats for all edges in a running graph.
type EdgeStatsRegistry struct {
	mu    sync.RWMutex
	edges []*edgeEntry
}

type edgeEntry struct {
	stats *EdgeStats
	ch    chan any
}

// NewEdgeStatsRegistry creates an empty registry.
func NewEdgeStatsRegistry() *EdgeStatsRegistry {
	return &EdgeStatsRegistry{}
}

// Register adds a channel to be monitored.
func (r *EdgeStatsRegistry) Register(id, from, to, portType string, ch chan any) *EdgeStats {
	es := &EdgeStats{
		EdgeID:   id,
		FromNode: from,
		ToNode:   to,
		PortType: portType,
		BufSize:  cap(ch),
	}
	r.mu.Lock()
	r.edges = append(r.edges, &edgeEntry{stats: es, ch: ch})
	r.mu.Unlock()
	return es
}

// Sample polls all registered channels once and updates stats.
func (r *EdgeStatsRegistry) Sample() {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, e := range r.edges {
		c := cap(e.ch)
		if c == 0 {
			continue
		}
		ratio := float64(len(e.ch)) / float64(c)
		fixed := int64(ratio * 1e9)
		e.stats.fill.Store(fixed)

		// Update peak via CAS.
		for {
			cur := e.stats.peakFill.Load()
			if fixed <= cur || e.stats.peakFill.CompareAndSwap(cur, fixed) {
				break
			}
		}

		if len(e.ch) == c {
			e.stats.stalls.Add(1)
		}
	}
}

// StartSampler launches a goroutine that polls channel fill levels at the
// given interval. It stops when ctx is cancelled.
func (r *EdgeStatsRegistry) StartSampler(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.Sample()
			}
		}
	}()
}

// Snapshot returns a copy of all edge stats.
func (r *EdgeStatsRegistry) Snapshot() []EdgeStatsSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]EdgeStatsSnapshot, len(r.edges))
	for i, e := range r.edges {
		out[i] = e.stats.Snapshot()
	}
	return out
}
