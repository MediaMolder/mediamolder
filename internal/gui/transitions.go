// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package gui

import (
	"encoding/json"
	"net/http"

	"github.com/MediaMolder/MediaMolder/processors"
)

// handleListTransitions returns the sorted transition type names the
// sequence_editor accepts, as a JSON array of strings. The GUI's timeline editor
// uses it to populate the per-clip transition picker, so the picker can never
// offer a transition the processor would reject.
func handleListTransitions(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(processors.SupportedTransitions())
}
