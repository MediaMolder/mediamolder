// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"context"
	"time"
)

// NodeRestart is emitted when a node goroutine is automatically restarted
// after a transient error.
type NodeRestart struct {
	NodeID  string
	Attempt int
	Err     error
	Time    time.Time
}

func (NodeRestart) eventTag() {}

// NodeRestarter watches for transient errors and restarts node processing
// using the error policy engine's retry/backoff logic.
type NodeRestarter struct {
	engine *ErrorPolicyEngine
	events *EventBus
}

// NewNodeRestarter creates a restarter backed by the given error policy engine.
func NewNodeRestarter(engine *ErrorPolicyEngine, events *EventBus) *NodeRestarter {
	return &NodeRestarter{engine: engine, events: events}
}

// Wrap wraps a node operation function with automatic restart logic.
// The supplied fn is the node's main work loop. If fn returns a PipelineError
// with Transient == true, the restarter consults the error policy. If the
// policy says retry (HandleError returns nil), fn is called again.
// Non-transient errors, abort decisions, and context cancellation propagate
// immediately.
func (r *NodeRestarter) Wrap(ctx context.Context, nodeID string, fn func(ctx context.Context) error) error {
	attempt := 0
	for {
		err := fn(ctx)
		if err == nil {
			r.engine.ResetRetries(nodeID)
			return nil
		}

		// Check context first — no restart after cancellation.
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Only auto-restart on PipelineErrors marked transient.
		perr, ok := err.(*PipelineError)
		if !ok || !perr.Transient {
			return err
		}

		attempt++
		r.events.Post(NodeRestart{
			NodeID:  nodeID,
			Attempt: attempt,
			Err:     perr.Err,
			Time:    time.Now(),
		})

		// Consult the error policy (includes backoff sleep).
		policyErr := r.engine.HandleError(ctx, perr)
		if policyErr != nil {
			return policyErr // Policy says stop (abort, fallback, or retries exhausted).
		}
		// Policy returned nil → retry.
	}
}
