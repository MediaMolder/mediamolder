// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package gui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/MediaMolder/MediaMolder/compat/ffcli"
	"github.com/MediaMolder/MediaMolder/pipeline"
)

// convertCmdRequest is the JSON body accepted by POST /api/convert-cmd. The
// command is the verbatim FFmpeg command line the user pasted into the GUI
// (with or without a leading "ffmpeg" / "/path/to/ffmpeg" token - ffcli.Parse
// handles either).
type convertCmdRequest struct {
	Command string `json:"command"`
}

// convertCmdResponse is the success body returned by POST /api/convert-cmd.
// Config is the parsed mediamolder JobConfig, ready to be fed into loadJob
// on the client side. Unsupported lists any deprecated, out-of-scope, or
// Wave 5–7 schema-promoted flags encountered during import.
type convertCmdResponse struct {
	Config      *pipeline.Config `json:"config"`
	Unsupported []string         `json:"unsupported,omitempty"`
}

// handleConvertCmd parses an FFmpeg-style command line into a mediamolder
// JobConfig, mirroring the `mediamolder convert-cmd` CLI subcommand. Any
// parse error is surfaced verbatim with a 422 so the GUI can show the
// detailed feedback the user needs to fix their command.
func handleConvertCmd(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var req convertCmdRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("decode body: %w", err))
		return
	}
	cmd := strings.TrimSpace(req.Command)
	if cmd == "" {
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("command is required"))
		return
	}
	res, err := ffcli.ParseFull(cmd)
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(convertCmdResponse{Config: res.Config, Unsupported: res.Unsupported})
}
