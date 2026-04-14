// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"context"
	"fmt"
	"math"
	"time"
)

// PolicyKind identifies the error handling strategy.
type PolicyKind string

const (
	PolicyAbort    PolicyKind = "abort"
	PolicySkip     PolicyKind = "skip"
	PolicyRetry    PolicyKind = "retry"
	PolicyFallback PolicyKind = "fallback"
)

// DefaultErrorPolicy is used when a node does not specify one.
var DefaultErrorPolicy = ErrorPolicy{Policy: string(PolicyAbort)}

// PipelineError is a rich error type carrying node context and transience info.
type PipelineError struct {
	NodeID    string
	Stage     string // "demux", "decode", "filter", "encode", "mux"
	Err       error
	Transient bool // true if error is likely temporary
}

func (e *PipelineError) Error() string {
	tag := ""
	if e.Transient {
		tag = " [transient]"
	}
	return fmt.Sprintf("node %q (%s)%s: %v", e.NodeID, e.Stage, tag, e.Err)
}

func (e *PipelineError) Unwrap() error { return e.Err }

// ErrorPolicyEngine evaluates error policies for pipeline nodes.
type ErrorPolicyEngine struct {
	policies map[string]ErrorPolicy // nodeID → policy
	events   *EventBus
	retries  map[string]int // nodeID → current retry count
}

// NewErrorPolicyEngine creates an engine from the config's per-node policies.
func NewErrorPolicyEngine(cfg *Config, events *EventBus) *ErrorPolicyEngine {
	policies := make(map[string]ErrorPolicy)
	for _, n := range cfg.Graph.Nodes {
		if n.ErrorPolicy != nil {
			policies[n.ID] = *n.ErrorPolicy
		}
	}
	return &ErrorPolicyEngine{
		policies: policies,
		events:   events,
		retries:  make(map[string]int),
	}
}

// PolicyFor returns the error policy for a node (default: abort).
func (e *ErrorPolicyEngine) PolicyFor(nodeID string) ErrorPolicy {
	if p, ok := e.policies[nodeID]; ok {
		return p
	}
	return DefaultErrorPolicy
}

// HandleError evaluates the node's error policy and returns an action.
// Returns nil if the error should be skipped (processing continues).
// Returns the original or wrapped error if the pipeline should abort.
// For retry policy, blocks with exponential backoff before returning nil.
func (e *ErrorPolicyEngine) HandleError(ctx context.Context, perr *PipelineError) error {
	policy := e.PolicyFor(perr.NodeID)

	e.events.Post(ErrorEvent{
		NodeID: perr.NodeID,
		Stage:  perr.Stage,
		Err:    perr.Err,
		Time:   time.Now(),
	})

	switch PolicyKind(policy.Policy) {
	case PolicyAbort:
		return perr

	case PolicySkip:
		e.events.Post(ErrorPolicyApplied{
			NodeID: perr.NodeID,
			Policy: string(PolicySkip),
			Err:    perr.Err,
		})
		return nil

	case PolicyRetry:
		maxRetries := policy.MaxRetries
		if maxRetries <= 0 {
			maxRetries = 3
		}
		count := e.retries[perr.NodeID]
		if count >= maxRetries {
			// Exhausted retries — try fallback or abort.
			e.retries[perr.NodeID] = 0
			if policy.FallbackNode != "" {
				return e.handleFallback(perr, policy)
			}
			return fmt.Errorf("node %q: retries exhausted (%d/%d): %w",
				perr.NodeID, count, maxRetries, perr.Err)
		}
		e.retries[perr.NodeID] = count + 1

		// Exponential backoff: 100ms * 2^attempt, capped at 5s.
		backoff := time.Duration(100*math.Pow(2, float64(count))) * time.Millisecond
		if backoff > 5*time.Second {
			backoff = 5 * time.Second
		}

		e.events.Post(ErrorPolicyApplied{
			NodeID:  perr.NodeID,
			Policy:  string(PolicyRetry),
			Err:     perr.Err,
			Attempt: count + 1,
			Backoff: backoff,
		})

		select {
		case <-time.After(backoff):
			return nil // Caller should retry the operation.
		case <-ctx.Done():
			return ctx.Err()
		}

	case PolicyFallback:
		return e.handleFallback(perr, policy)

	default:
		return perr
	}
}

func (e *ErrorPolicyEngine) handleFallback(perr *PipelineError, policy ErrorPolicy) error {
	if policy.FallbackNode == "" {
		return fmt.Errorf("node %q: fallback policy but no fallback_node configured: %w",
			perr.NodeID, perr.Err)
	}
	e.events.Post(ErrorPolicyApplied{
		NodeID:       perr.NodeID,
		Policy:       string(PolicyFallback),
		Err:          perr.Err,
		FallbackNode: policy.FallbackNode,
	})
	// Fallback rerouting must be handled by the caller (dynamic graph change).
	// For now, return the error to signal that fallback was requested.
	return &FallbackRequested{
		NodeID:       perr.NodeID,
		FallbackNode: policy.FallbackNode,
		Err:          perr.Err,
	}
}

// ResetRetries clears the retry counter for a node (call after successful processing).
func (e *ErrorPolicyEngine) ResetRetries(nodeID string) {
	delete(e.retries, nodeID)
}

// FallbackRequested is returned by HandleError when a fallback reroute is needed.
type FallbackRequested struct {
	NodeID       string
	FallbackNode string
	Err          error
}

func (f *FallbackRequested) Error() string {
	return fmt.Sprintf("node %q requested fallback to %q: %v", f.NodeID, f.FallbackNode, f.Err)
}

func (f *FallbackRequested) Unwrap() error { return f.Err }

// ErrorPolicyApplied is emitted when an error policy is invoked.
type ErrorPolicyApplied struct {
	NodeID       string
	Policy       string
	Err          error
	Attempt      int           // for retry policy
	Backoff      time.Duration // for retry policy
	FallbackNode string        // for fallback policy
}

func (ErrorPolicyApplied) eventTag() {}
