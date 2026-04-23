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

// handleValidate parses + structurally validates a JobConfig posted as JSON.
// Returns 200 with {ok:true,...} on success, 400 with {ok:false,error:"..."}.
func handleValidate(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("read body: %w", err))
		return
	}
	cfg, err := pipeline.ParseConfig(body)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":      true,
		"inputs":  len(cfg.Inputs),
		"outputs": len(cfg.Outputs),
		"nodes":   len(cfg.Graph.Nodes),
		"edges":   len(cfg.Graph.Edges),
	})
}

// makeRunHandler returns a handler that parses the posted JobConfig, starts a
// pipeline run via the job manager, and returns {job_id: "..."}.
func makeRunHandler(jm *jobManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
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
