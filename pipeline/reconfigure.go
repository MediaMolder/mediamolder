// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"fmt"
	"sync"
	"time"
)

// ReconfigureComplete is emitted when a live filter parameter change succeeds.
type ReconfigureComplete struct {
	NodeID string
	Params map[string]any
	Time   time.Time
}

func (ReconfigureComplete) eventTag() {}

// reconfigurable tracks the filter graphs available for live reconfiguration.
// It is populated during runGraph and cleared on teardown.
type reconfigurable struct {
	mu      sync.Mutex
	filters map[string]*reconfigEntry
}

type reconfigEntry struct {
	filterName string
	params     map[string]any
}

// copyParams returns a shallow copy of a parameter map.
func copyParams(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Reconfigure changes parameters on a running filter node.
// The node must be a filter node in a graph-based pipeline.
// params keys are AVFilter option names (e.g. "x", "y", "fontsize", "volume").
// This uses avfilter_graph_send_command under the hood and does not
// drop or reorder frames.
func (p *Pipeline) Reconfigure(nodeID string, params map[string]any) error {
	p.mu.Lock()
	state := p.state
	reconf := p.reconf
	p.mu.Unlock()

	if state != StatePlaying && state != StatePaused {
		return fmt.Errorf("cannot reconfigure in state %s", state)
	}
	if reconf == nil {
		return fmt.Errorf("reconfigure not available (pipeline not using graph mode)")
	}

	reconf.mu.Lock()
	defer reconf.mu.Unlock()

	entry, ok := reconf.filters[nodeID]
	if !ok {
		return fmt.Errorf("node %q is not a reconfigurable filter", nodeID)
	}

	// Find the corresponding FilterGraph in the running graphRunner.
	p.mu.Lock()
	runner := p.graphRunner
	p.mu.Unlock()

	if runner == nil {
		return fmt.Errorf("graphRunner not available")
	}

	fg := runner.filters[nodeID]
	if fg == nil {
		return fmt.Errorf("no filter graph for node %q", nodeID)
	}

	// Send each parameter as a command to the filter.
	for k, v := range params {
		arg := fmt.Sprintf("%v", v)
		if err := fg.SendCommand(entry.filterName, k, arg); err != nil {
			return fmt.Errorf("reconfigure %q param %q: %w", nodeID, k, err)
		}
		entry.params[k] = v
	}

	p.events.Post(ReconfigureComplete{
		NodeID: nodeID,
		Params: params,
		Time:   time.Now(),
	})
	return nil
}
