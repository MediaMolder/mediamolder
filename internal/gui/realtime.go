// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package gui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/MediaMolder/MediaMolder/job/snap"
)

// activeRT returns the RealtimeControllerSnapshot for the currently running
// pipeline job, or a zero-value snapshot with Enabled=false when no realtime
// job is in flight.
func (m *jobManager) activeRT() snap.RTControllerSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		j.mu.Lock()
		running := j.status == statusRunning
		j.mu.Unlock()
		if running {
			return j.pipe.RealtimeControllerSnapshot()
		}
	}
	return snap.RTControllerSnapshot{}
}

// activeSetPreset delegates a preset override to the active running job.
func (m *jobManager) activeSetPreset(nodeID, preset string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		j.mu.Lock()
		running := j.status == statusRunning
		j.mu.Unlock()
		if running {
			return j.pipe.SetPresetOverride(nodeID, preset)
		}
	}
	return fmt.Errorf("no running job")
}

// activeClearPreset clears a preset override on the active running job.
func (m *jobManager) activeClearPreset(nodeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		j.mu.Lock()
		running := j.status == statusRunning
		j.mu.Unlock()
		if running {
			return j.pipe.ClearPresetOverride(nodeID)
		}
	}
	return fmt.Errorf("no running job")
}

// registerRealtimeRoutes adds the realtime snapshot and override endpoints to
// mux so the embedded GUI frontend can reach them without a separate port.
//
//	GET  /realtime/snapshot        → one-shot RTControllerSnapshot JSON; 404 when disabled
//	GET  /realtime/snapshot/stream → SSE stream; ~500ms tick
//	POST /realtime/preset          → body {"node_id":"…","preset":"…"}
//	POST /realtime/preset/clear    → body {"node_id":"…"}
func registerRealtimeRoutes(mux *http.ServeMux, jobs *jobManager) {
	mux.HandleFunc("GET /realtime/snapshot", func(w http.ResponseWriter, r *http.Request) {
		cs := jobs.activeRT()
		if !cs.Enabled {
			http.Error(w, "realtime mode not active", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(cs)
	})

	mux.HandleFunc("GET /realtime/snapshot/stream", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				cs := jobs.activeRT()
				if !cs.Enabled {
					fmt.Fprintf(w, "event: error\ndata: realtime mode not active\n\n")
					flusher.Flush()
					return
				}
				buf, _ := json.Marshal(cs)
				fmt.Fprintf(w, "data: %s\n\n", buf)
				flusher.Flush()
			}
		}
	})

	mux.HandleFunc("POST /realtime/preset", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			NodeID string `json:"node_id"`
			Preset string `json:"preset"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := jobs.activeSetPreset(body.NodeID, body.Preset); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("POST /realtime/preset/clear", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			NodeID string `json:"node_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := jobs.activeClearPreset(body.NodeID); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
