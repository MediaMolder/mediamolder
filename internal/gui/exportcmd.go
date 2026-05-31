// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package gui

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/MediaMolder/MediaMolder/compat/ffcli"
	"github.com/MediaMolder/MediaMolder/job"
)

// exportCmdRequest is the JSON body accepted by POST /api/export-cmd.
// Config is a mediamolder JobConfig (job.Config) as produced by
// the GUI editor's flowToConfig helper. The server converts it into an
// ffmpeg command-line string via compat/ffcli.Export.
type exportCmdRequest struct {
	Config *job.Config `json:"config"`
}

// exportCmdResponse is the success body returned by POST /api/export-cmd.
// Command is the full single-line ffmpeg command.
// Lines is the same command split into display lines for the GUI's
// multi-line panel (each flag+value pair on its own line).
// Unsupported lists mediamolder-only features that have no CLI equivalent.
type exportCmdResponse struct {
	Command     string   `json:"command"`
	Lines       []string `json:"lines"`
	Unsupported []string `json:"unsupported"`
}

// handleExportCmd converts a mediamolder JobConfig into an ffmpeg command
// line, surfacing any mediamolder-only features as warnings rather than
// errors (the export is a best-effort round-trip oracle, not a perfect
// lossless transform). Any malformed request body is surfaced with a 422.
func handleExportCmd(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var req exportCmdRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("decode body: %w", err))
		return
	}
	if req.Config == nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("config is required"))
		return
	}
	result := ffcli.Export(req.Config)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(exportCmdResponse{
		Command:     result.Command,
		Lines:       result.Lines,
		Unsupported: result.Unsupported,
	})
}
