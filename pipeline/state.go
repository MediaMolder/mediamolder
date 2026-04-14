// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import "fmt"

// State represents the pipeline state machine state.
type State int

const (
	StateNull    State = iota
	StateReady
	StatePaused
	StatePlaying
)

func (s State) String() string {
	switch s {
	case StateNull:
		return "NULL"
	case StateReady:
		return "READY"
	case StatePaused:
		return "PAUSED"
	case StatePlaying:
		return "PLAYING"
	default:
		return fmt.Sprintf("State(%d)", int(s))
	}
}

// ErrInvalidStateTransition is returned when a requested state transition is not allowed.
type ErrInvalidStateTransition struct {
	From State
	To   State
}

func (e *ErrInvalidStateTransition) Error() string {
	return fmt.Sprintf("invalid state transition %s -> %s", e.From, e.To)
}
