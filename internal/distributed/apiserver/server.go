// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

// Package apiserver implements the Tier 2 distributed API server. It accepts
// Job documents (schema_version "1.4") and bare Config documents (back-compat),
// delegates to an Orchestrator, and serves state and event-log queries.
//
// SSE is delivered by long-polling the SQLite event log; Phase C will switch
// to in-process channels for the common single-binary case.
package apiserver

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/MediaMolder/MediaMolder/internal/auth"
	"github.com/MediaMolder/MediaMolder/internal/distributed/orchestrator"
	j "github.com/MediaMolder/MediaMolder/job"
)

// Options configures the Tier 2 API server.
type Options struct {
	// Addr is the TCP listen address, e.g. ":8080".
	Addr string

	// TLSCert / TLSKey are paths to the PEM certificate and private key.
	// When both are set the server uses TLS; otherwise HTTP.
	TLSCert string
	TLSKey  string

	// AuthToken is the expected Bearer token. An empty string disables auth.
	AuthToken string

	// OIDCIssuer enables OIDC JWT validation when non-empty.
	// Takes precedence over AuthToken.
	OIDCIssuer string
	// OIDCClientID is the expected "aud" claim; empty accepts any audience.
	OIDCClientID string

	// MTLSCACert is the path to a PEM CA bundle used to require and verify
	// client certificates. Requires TLSCert + TLSKey.
	MTLSCACert string

	// Orch is the Orchestrator instance shared with the embedded worker(s).
	Orch *orchestrator.Orchestrator
}

// Server is the Tier 2 API server.
type Server struct {
	opts    Options
	httpSrv *http.Server
}

// NewServer creates and configures a Tier 2 API server.
func NewServer(opts Options) (*Server, error) {
	if opts.Addr == "" {
		opts.Addr = ":8080"
	}
	if opts.Orch == nil {
		return nil, errors.New("apiserver: Orch is required")
	}

	s := &Server{opts: opts}
	mux := http.NewServeMux()

	// Job lifecycle endpoints.
	mux.HandleFunc("POST /v1/jobs", s.handleSubmitJob)
	mux.HandleFunc("GET /v1/jobs/{id}", s.handleGetJob)
	mux.HandleFunc("GET /v1/jobs/{id}/events", s.handleJobEvents)
	mux.HandleFunc("GET /v1/jobs/{id}/tasks", s.handleListTasks)
	mux.HandleFunc("GET /v1/jobs/{id}/artifacts", s.handleJobArtifacts)
	mux.HandleFunc("GET /v1/jobs/{id}/dlq", s.handleListDLQ)
	mux.HandleFunc("DELETE /v1/jobs/{id}", s.handleCancelJob)

	// Health probes.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	var handler http.Handler = mux
	switch {
	case opts.OIDCIssuer != "":
		// OIDC JWT validation takes precedence over static bearer token.
		verifier, err := auth.NewOIDCVerifier(opts.OIDCIssuer, opts.OIDCClientID)
		if err != nil {
			return nil, fmt.Errorf("apiserver: setup OIDC verifier: %w", err)
		}
		handler = verifier.Middleware(mux)
	case opts.AuthToken != "":
		handler = s.bearerAuthMiddleware(mux)
	}
	handler = logMiddleware(handler)

	s.httpSrv = &http.Server{
		Addr:              opts.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 15 * time.Second,
	}
	return s, nil
}

// ListenAndServe starts the server. Uses TLS when TLSCert and TLSKey are set.
// When MTLSCACert is also set, client certificate verification is enforced.
func (s *Server) ListenAndServe() error {
	if s.opts.TLSCert != "" && s.opts.TLSKey != "" {
		cert, err := tls.LoadX509KeyPair(s.opts.TLSCert, s.opts.TLSKey)
		if err != nil {
			return fmt.Errorf("apiserver: load TLS cert: %w", err)
		}
		tlsCfg := &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS13,
		}
		if s.opts.MTLSCACert != "" {
			mtlsCfg, err := auth.NewMTLSTLSConfig(s.opts.MTLSCACert)
			if err != nil {
				return fmt.Errorf("apiserver: configure mTLS: %w", err)
			}
			tlsCfg.ClientAuth = mtlsCfg.ClientAuth
			tlsCfg.ClientCAs = mtlsCfg.ClientCAs
		}
		s.httpSrv.TLSConfig = tlsCfg
		return s.httpSrv.ListenAndServeTLS("", "")
	}
	return s.httpSrv.ListenAndServe()
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpSrv.Shutdown(ctx)
}

// ---- Handlers ---------------------------------------------------------------

func (s *Server) handleSubmitJob(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Content-Type") != "application/json" {
		writeJSONError(w, http.StatusUnsupportedMediaType,
			errors.New("Content-Type must be application/json"))
		return
	}

	// 8 MiB body limit.
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)

	// Peek at schema_version to distinguish Job v1.4 from bare Config.
	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("decode body: %w", err))
		return
	}

	var job j.Job
	if isJobDocument(raw) {
		if err := json.Unmarshal(raw, &job); err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Errorf("decode job: %w", err))
			return
		}
	} else {
		// Back-compat: treat as bare Config.
		var cfg j.Config
		if err := json.Unmarshal(raw, &cfg); err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Errorf("decode config: %w", err))
			return
		}
		job = j.Job{
			SchemaVersion: j.JobSchemaVersion,
			Config:        cfg,
		}
	}

	id, err := s.opts.Orch.AcceptJob(r.Context(), &job)
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			status = http.StatusServiceUnavailable
		}
		writeJSONError(w, status, err)
		return
	}

	base := serverBase(r)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"id":         id,
		"status_url": base + "/v1/jobs/" + id,
		"events_url": base + "/v1/jobs/" + id + "/events",
		"tasks_url":  base + "/v1/jobs/" + id + "/tasks",
	})
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	job, statusRec, err := s.opts.Orch.GetJobStatus(r.Context(), id)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":         id,
		"name":       job.Name,
		"status":     string(statusRec.Status),
		"error":      statusRec.Error,
		"updated_at": statusRec.UpdatedAt,
	})
}

func (s *Server) handleJobEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// Validate job exists.
	if _, _, err := s.opts.Orch.GetJobStatus(r.Context(), id); err != nil {
		writeJSONError(w, http.StatusNotFound, err)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, errors.New("streaming unsupported"))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	var cursor int64
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			evts, err := s.opts.Orch.ListEvents(r.Context(), id, cursor)
			if err != nil {
				return
			}
			for _, e := range evts {
				cursor = e.ID
				b, _ := json.Marshal(e)
				fmt.Fprintf(w, "data: %s\n\n", b)
			}
			if len(evts) > 0 {
				flusher.Flush()
			}
		}
	}
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	recs, err := s.opts.Orch.ListTasks(r.Context(), id)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(recs)
}

func (s *Server) handleJobArtifacts(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	recs, err := s.opts.Orch.ListTasks(r.Context(), id)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, err)
		return
	}
	var artifacts []j.ArtifactRef
	for _, rec := range recs {
		if rec.Result != nil {
			artifacts = append(artifacts, rec.Result.Outputs...)
		}
	}
	if artifacts == nil {
		artifacts = []j.ArtifactRef{} // return [] not null
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(artifacts)
}

func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.opts.Orch.CancelJob(r.Context(), id); err != nil {
		writeJSONError(w, http.StatusNotFound, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"id": id, "status": "cancelled"})
}

func (s *Server) handleListDLQ(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	recs, err := s.opts.Orch.ListDeadLetterTasks(r.Context(), id)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if recs == nil {
		_, _ = w.Write([]byte("[]"))
		return
	}
	_ = json.NewEncoder(w).Encode(recs)
}

// ---- Middleware & helpers ----------------------------------------------------

func (s *Server) bearerAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip health probes.
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		want := "Bearer " + s.opts.AuthToken
		if auth != want {
			writeJSONError(w, http.StatusUnauthorized, errors.New("invalid or missing bearer token"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("apiserver: %s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}

func writeJSONError(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

// isJobDocument returns true when raw contains "schema_version":"1.4".
func isJobDocument(raw json.RawMessage) bool {
	var probe struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	return probe.SchemaVersion == j.JobSchemaVersion
}

// serverBase returns the scheme+host of the request (for building URLs in
// response bodies). Uses X-Forwarded-Proto when behind a reverse proxy.
func serverBase(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.ToLower(r.Header.Get("X-Forwarded-Proto")) == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
