// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package gui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/MediaMolder/MediaMolder/pipeline"
)

// jobConfigBodyLimit caps the request body for POST /api/validate and
// POST /api/run. 1 MiB comfortably covers typical pipelines with dozens of
// nodes, embedded concat lists, and per-node option maps. Pipelines that
// embed very large inline data (e.g. thousands of concat entries) may hit
// this limit; raise the constant here and recompile.
const jobConfigBodyLimit = 1 << 20 // 1 MiB

// handleValidate parses and validates a JobConfig posted as JSON.
// Query param ?no_probe=1 skips Phase B (file I/O) and runs only Phase A static checks.
// Always returns 200 with a ValidationReport JSON object.
func handleValidate(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, jobConfigBodyLimit))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("read body: %w", err))
		return
	}
	cfg, err := pipeline.ParseConfig(body)
	if err != nil {
		// Return a synthetic single-issue report so the frontend always gets a ValidationReport.
		writeValidationParseError(w, err)
		return
	}

	var report *pipeline.ValidationReport
	if r.URL.Query().Get("no_probe") == "1" {
		report = pipeline.ValidateConfigStatic(cfg, nil)
	} else {
		report, err = pipeline.ValidateConfig(cfg, nil)
		if err != nil {
			writeValidationParseError(w, err)
			return
		}
	}

	// Ensure Issues is never null in the JSON response.
	if report.Issues == nil {
		report.Issues = []pipeline.ValidationIssue{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(report)
}

func writeValidationParseError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(&pipeline.ValidationReport{
		Issues: []pipeline.ValidationIssue{{
			Severity: pipeline.SeverityError,
			Code:     "PARSE_ERROR",
			Message:  err.Error(),
		}},
		HasErrors: true,
	})
}

// makeRunHandler returns a handler that parses the posted JobConfig, starts a
// pipeline run via the job manager, and returns {job_id: "..."}.
func makeRunHandler(jm *jobManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, jobConfigBodyLimit))
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Errorf("read body: %w", err))
			return
		}
		cfg, err := pipeline.ParseConfig(body)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err)
			return
		}
		id, err := jm.start(cfg)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"job_id": id})
	}
}

// makeCancelHandler returns a handler that cancels the specified job.
func makeCancelHandler(jm *jobManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("jobId")
		if err := jm.cancel(id); err != nil {
			writeJSONError(w, http.StatusNotFound, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}
}

// makeEventsHandler streams jobEvents over Server-Sent Events. The browser
// EventSource API consumes this with no extra dependencies on either side.
func makeEventsHandler(jm *jobManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("jobId")
		job := jm.get(id)
		if job == nil {
			writeJSONError(w, http.StatusNotFound, fmt.Errorf("job %q not found", id))
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			writeJSONError(w, http.StatusInternalServerError, fmt.Errorf("streaming unsupported"))
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no") // disable proxy buffering
		w.WriteHeader(http.StatusOK)

		ch, cancel := job.subscribe()
		defer cancel()

		// Initial comment to flush headers.
		_, _ = w.Write([]byte(": connected\n\n"))
		flusher.Flush()

		ctx := r.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, open := <-ch:
				if !open {
					return
				}
				payload, err := json.Marshal(ev)
				if err != nil {
					continue
				}
				if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, payload); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	}
}
