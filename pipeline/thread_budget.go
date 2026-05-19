// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"runtime"
	"sync"
)

// ThreadBudget tracks the CPU thread budget across pipeline nodes for the
// adaptive real-time control loop (Phase 5). Each encoder and filter node is
// allocated a share of the total available CPUs; the control loop may
// reallocate threads between nodes without exceeding the cap.
//
// Hardware-accelerated nodes (e.g. NVENC, VideoToolbox, VAAPI) consume GPU
// resources rather than CPU threads and are exempt from this budget. They are
// registered via SetHWNode and are always reported as allocatable.
type ThreadBudget struct {
	mu        sync.Mutex
	total     int
	reserved  int            // threads kept for Go runtime and OS overhead
	allocated map[string]int // nodeID → current CPU thread count
	hwNodes   map[string]bool
}

// newThreadBudget creates a ThreadBudget. When cap > 0 it overrides the
// runtime.NumCPU() default. reserved is always 2 (Go runtime + OS headroom).
func newThreadBudget(cap int) *ThreadBudget {
	total := runtime.NumCPU()
	if cap > 0 {
		total = cap
	}
	return &ThreadBudget{
		total:     total,
		reserved:  2,
		allocated: make(map[string]int),
		hwNodes:   make(map[string]bool),
	}
}

// SetHWNode marks nodeID as hardware-accelerated. HW nodes are exempt from
// the CPU budget; CanAllocate always returns true for them.
func (b *ThreadBudget) SetHWNode(nodeID string) {
	b.mu.Lock()
	b.hwNodes[nodeID] = true
	b.mu.Unlock()
}

// Seed records the initial CPU thread count for nodeID from the opened AV
// context. Must be called for each CPU node before the control loop starts.
// A no-op for HW nodes.
func (b *ThreadBudget) Seed(nodeID string, threads int) {
	b.mu.Lock()
	if !b.hwNodes[nodeID] {
		b.allocated[nodeID] = threads
	}
	b.mu.Unlock()
}

// CanAllocate reports whether nodeID can be reallocated to newCount CPU
// threads without exceeding (total − reserved). Always true for HW nodes.
func (b *ThreadBudget) CanAllocate(nodeID string, newCount int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.hwNodes[nodeID] {
		return true
	}
	used := 0
	for id, v := range b.allocated {
		if id != nodeID {
			used += v
		}
	}
	return used+newCount <= b.total-b.reserved
}

// Allocate records that nodeID now uses threads CPU threads. A no-op for
// HW nodes.
func (b *ThreadBudget) Allocate(nodeID string, threads int) {
	b.mu.Lock()
	if !b.hwNodes[nodeID] {
		b.allocated[nodeID] = threads
	}
	b.mu.Unlock()
}

// Current returns the currently recorded thread count for nodeID, or 0.
func (b *ThreadBudget) Current(nodeID string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.allocated[nodeID]
}

// Available returns the number of unallocated CPU threads remaining above the
// reserved floor. Always non-negative.
func (b *ThreadBudget) Available() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	used := 0
	for _, v := range b.allocated {
		used += v
	}
	avail := b.total - b.reserved - used
	if avail < 0 {
		return 0
	}
	return avail
}

// Total returns the configured thread cap (runtime.NumCPU() or user override).
func (b *ThreadBudget) Total() int {
	return b.total
}
