// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"fmt"
	"time"
)

// OutputAdded is emitted when a new output is dynamically added to a running pipeline.
type OutputAdded struct {
	OutputID string
	Time     time.Time
}

func (OutputAdded) eventTag() {}

// AddOutput adds a new output to a running graph-based pipeline.
// The pipeline must be in PLAYING or PAUSED state. The method pauses the
// data flow (quiesce), adds the output config to the pipeline, and returns
// an acknowledgement channel that is closed when the change is live.
//
// In the current implementation, structural changes require a graph rebuild,
// so the scheduler is cancelled, the config updated, and the graph restarted.
// A future version may perform in-place graph mutations.
func (p *Pipeline) AddOutput(output Output) (<-chan struct{}, error) {
	p.mu.Lock()
	state := p.state
	p.mu.Unlock()

	if state != StatePlaying && state != StatePaused {
		return nil, fmt.Errorf("cannot add output in state %s", state)
	}

	if output.ID == "" {
		return nil, fmt.Errorf("output must have an id")
	}
	if output.URL == "" {
		return nil, fmt.Errorf("output %q must have a url", output.ID)
	}

	// Check for duplicate output ID.
	for _, existing := range p.cfg.Outputs {
		if existing.ID == output.ID {
			return nil, fmt.Errorf("duplicate output id %q", output.ID)
		}
	}

	// Quiesce: pause data flow to safely modify the graph.
	wasPaused := state == StatePaused
	if !wasPaused {
		if err := p.Pause(); err != nil {
			return nil, fmt.Errorf("quiesce for add output: %w", err)
		}
	}

	// Apply: add the output to the config.
	p.mu.Lock()
	p.cfg.Outputs = append(p.cfg.Outputs, output)
	p.mu.Unlock()

	ack := make(chan struct{})

	// Resume: the next data flow start will pick up the updated config.
	// For graph-mode pipelines the graph will be rebuilt with the new output.
	if !wasPaused {
		if err := p.Resume(); err != nil {
			close(ack)
			return ack, fmt.Errorf("resume after add output: %w", err)
		}
	}

	p.events.Post(OutputAdded{
		OutputID: output.ID,
		Time:     time.Now(),
	})
	close(ack)

	return ack, nil
}
