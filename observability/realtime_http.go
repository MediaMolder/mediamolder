// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package observability

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/MediaMolder/MediaMolder/pipeline/snap"
)

// RealtimeController is the minimal interface MetricsServer needs from a
// running pipeline to expose adaptive realtime mutation endpoints. The
// pipeline package implements this on *Pipeline.
type RealtimeController interface {
	SetPresetOverride(nodeID, preset string) error
	ClearPresetOverride(nodeID string) error
	RealtimeDecisions() []snap.DecisionRecord
	RealtimeStatus() snap.RealtimeSnapshot
	// Phase 8: full per-tick controller snapshot.
	RealtimeControllerSnapshot() snap.RTControllerSnapshot
}

// ReadyReporter is the optional sub-interface for Phase 7 per-output
// preroll readiness. Implemented on *pipeline.Pipeline via Ready() and
// ReadyState() / via RealtimeStatus().Outputs.
type ReadyReporter interface {
	Ready() <-chan struct{}
}

// RegisterRealtimeHandlers wires the Phase 6 control surface on the
// MetricsServer. Mutating endpoints (POST) are gated to loopback callers
// so a misconfigured listener can't be abused remotely.
//
// Endpoints:
//   - POST /realtime/preset        body {"node": "...", "preset": "..."}
//   - POST /realtime/preset/clear  body {"node": "..."}
//   - GET  /realtime/decisions[?n=N]
//   - GET  /realtime/status
//
// Must be called before Start.
func (s *MetricsServer) RegisterRealtimeHandlers(ctrl RealtimeController) {
	s.mux.HandleFunc("/realtime/preset", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		if !isLoopback(r.RemoteAddr) {
			http.Error(w, "loopback only", http.StatusForbidden)
			return
		}
		var body struct {
			Node, Preset string
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := ctrl.SetPresetOverride(body.Node, body.Preset); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	s.mux.HandleFunc("/realtime/preset/clear", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		if !isLoopback(r.RemoteAddr) {
			http.Error(w, "loopback only", http.StatusForbidden)
			return
		}
		var body struct{ Node string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := ctrl.ClearPresetOverride(body.Node); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	s.mux.HandleFunc("/realtime/decisions", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		decisions := ctrl.RealtimeDecisions()
		if nStr := r.URL.Query().Get("n"); nStr != "" {
			if n, err := strconv.Atoi(nStr); err == nil && n > 0 && n < len(decisions) {
				decisions = decisions[len(decisions)-n:]
			}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(decisions)
	})

	s.mux.HandleFunc("/realtime/status", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(ctrl.RealtimeStatus())
	})

	// Phase 7: graph-level readiness.
	s.mux.HandleFunc("/realtime/ready", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		st := ctrl.RealtimeStatus()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		body := map[string]any{
			"ready":    st.Ready,
			"ready_at": st.ReadyAt,
			"outputs":  st.Outputs,
		}
		if !st.Ready {
			w.WriteHeader(http.StatusTooEarly) // 425
		}
		_ = json.NewEncoder(w).Encode(body)
	})

	rdy, _ := ctrl.(ReadyReporter)
	s.mux.HandleFunc("/realtime/ready/stream", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		writeEvent := func() {
			st := ctrl.RealtimeStatus()
			buf, _ := json.Marshal(map[string]any{
				"ready":    st.Ready,
				"ready_at": st.ReadyAt,
				"outputs":  st.Outputs,
			})
			fmt.Fprintf(w, "data: %s\n\n", buf)
			flusher.Flush()
		}
		writeEvent()

		var readyCh <-chan struct{}
		if rdy != nil {
			readyCh = rdy.Ready()
		}
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		last := ctrl.RealtimeStatus()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-readyCh:
				writeEvent()
				readyCh = nil // closed channel fires forever
			case <-t.C:
				cur := ctrl.RealtimeStatus()
				changed := cur.Ready != last.Ready || len(cur.Outputs) != len(last.Outputs)
				if !changed {
					for i := range cur.Outputs {
						if cur.Outputs[i].State != last.Outputs[i].State {
							changed = true
							break
						}
					}
				}
				if changed {
					writeEvent()
					last = cur
				}
			}
		}
	})

	// Phase 8: per-tick controller inspector.
	//
	//   GET /realtime/snapshot        → one JSON-encoded RTControllerSnapshot; 404 when disabled.
	//   GET /realtime/snapshot/stream → SSE stream; one event per ~500 ms tick.
	s.mux.HandleFunc("/realtime/snapshot", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		cs := ctrl.RealtimeControllerSnapshot()
		if !cs.Enabled {
			http.Error(w, "realtime mode not active", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(cs)
	})

	s.mux.HandleFunc("/realtime/snapshot/stream", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
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
				cs := ctrl.RealtimeControllerSnapshot()
				if !cs.Enabled {
					// Realtime was disabled mid-run; send a terminal event and close.
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
}

// isLoopback reports whether remoteAddr (host:port form, as set by
// net/http on Request.RemoteAddr) originates from the loopback interface.
// Used to gate mutating realtime endpoints so they cannot be invoked
// from a non-local client even if the listener is bound to 0.0.0.0.
func isLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}
