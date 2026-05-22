// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package observability

import (
	"encoding/json"
	"net"
	"net/http"
	"strconv"

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
