// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

// Package server implements the Tier 1 remote execution server.
// It exposes a /v1/ REST+SSE API that accepts pipeline job submissions,
// optionally presigns S3 URIs, and streams progress events to clients.
package server

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

	"github.com/MediaMolder/MediaMolder/internal/storage"
	"github.com/MediaMolder/MediaMolder/pipeline"
)

// Options configures the Tier 1 server.
type Options struct {
	// Addr is the TCP listen address, e.g. ":8443".
	Addr string

	// TLSCert / TLSKey are paths to the PEM certificate and private key.
	// When both are set the server uses TLS; otherwise HTTP.
	TLSCert string
	TLSKey  string

	// AuthToken is the expected Bearer token. An empty string disables auth
	// (not recommended for production).
	AuthToken string

	// MaxConcurrent limits how many jobs may run simultaneously. ≤ 0 means no limit.
	MaxConcurrent int

	// Presigner, when non-nil, converts s3:// URIs before the pipeline runs.
	Presigner *storage.PresignResolver

	// Uploads, when non-nil, enables the PUT /v1/uploads/{token} endpoint.
	Uploads *storage.UploadStore

	// AllowPaths is a set of absolute directory prefixes that file:// inputs
	// are permitted to reference. Empty = no file:// inputs allowed.
	AllowPaths []string
}

// Server is the Tier 1 remote execution server.
type Server struct {
	opts    Options
	jobs    *jobManager
	httpSrv *http.Server
}

// NewServer creates and configures a Tier 1 server but does not start it.
func NewServer(opts Options) (*Server, error) {
	if opts.Addr == "" {
		opts.Addr = ":8443"
	}
	s := &Server{opts: opts, jobs: newJobManager()}
	mux := http.NewServeMux()

	// Job lifecycle endpoints.
	mux.HandleFunc("POST /v1/jobs", s.handleSubmitJob)
	mux.HandleFunc("GET /v1/jobs/{id}", s.handleGetJob)
	mux.HandleFunc("GET /v1/jobs/{id}/events", s.handleJobEvents)
	mux.HandleFunc("GET /v1/jobs/{id}/artifacts", s.handleJobArtifacts)
	mux.HandleFunc("DELETE /v1/jobs/{id}", s.handleCancelJob)

	// Upload endpoints (only wired when Uploads is configured).
	if opts.Uploads != nil {
		mux.HandleFunc("POST /v1/uploads", s.handleAllocateUpload)
		mux.HandleFunc("PUT /v1/uploads/{token}", s.handleReceiveUpload)
	}

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
	if opts.AuthToken != "" {
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
func (s *Server) ListenAndServe() error {
	if s.opts.TLSCert != "" && s.opts.TLSKey != "" {
		cert, err := tls.LoadX509KeyPair(s.opts.TLSCert, s.opts.TLSKey)
		if err != nil {
			return fmt.Errorf("server: load TLS cert: %w", err)
		}
		s.httpSrv.TLSConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS13,
		}
		return s.httpSrv.ListenAndServeTLS("", "")
	}
	return s.httpSrv.ListenAndServe()
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpSrv.Shutdown(ctx)
}

// ---- Handlers -----------------------------------------------------------

func (s *Server) handleSubmitJob(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Content-Type") != "application/json" {
		writeJSONError(w, http.StatusUnsupportedMediaType,
			errors.New("Content-Type must be application/json"))
		return
	}

	// 8 MiB body limit — pipeline configs are never large.
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	var cfg pipeline.Config
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("decode job config: %w", err))
		return
	}

	if err := s.checkAllowedPaths(&cfg); err != nil {
		writeJSONError(w, http.StatusForbidden, err)
		return
	}

	id, err := s.jobs.start(&cfg, startOptions{
		presign: s.opts.Presigner,
		uploads: s.opts.Uploads,
	})
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, err)
		return
	}

	base := serverBase(r)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"id":         id,
		"status_url": base + "/v1/jobs/" + id,
		"events_url": base + "/v1/jobs/" + id + "/events",
	})
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	j := s.jobs.get(id)
	if j == nil {
		writeJSONError(w, http.StatusNotFound, fmt.Errorf("job %q not found", id))
		return
	}
	j.mu.Lock()
	resp := map[string]any{
		"id":        j.id,
		"status":    j.status,
		"error":     j.finalErr,
		"started":   j.start.Unix(),
		"elapsed_ms": j.elapsedMs(),
	}
	j.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleJobEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	j := s.jobs.get(id)
	if j == nil {
		writeJSONError(w, http.StatusNotFound, fmt.Errorf("job %q not found", id))
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, errors.New("streaming unsupported"))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	_, _ = w.Write([]byte(": connected\n\n"))
	flusher.Flush()

	ch, cancel := j.subscribe()
	defer cancel()

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

func (s *Server) handleJobArtifacts(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	j := s.jobs.get(id)
	if j == nil {
		writeJSONError(w, http.StatusNotFound, fmt.Errorf("job %q not found", id))
		return
	}
	j.mu.Lock()
	artifacts := make([]string, len(j.artifacts))
	copy(artifacts, j.artifacts)
	j.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"artifacts": artifacts})
}

func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.jobs.cancel(id); err != nil {
		writeJSONError(w, http.StatusNotFound, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (s *Server) handleAllocateUpload(w http.ResponseWriter, r *http.Request) {
	token, err := s.opts.Uploads.Allocate()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err)
		return
	}
	base := serverBase(r)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"token":      token,
		"upload_url": base + "/v1/uploads/" + token,
		"uri":        "upload://" + token,
	})
}

func (s *Server) handleReceiveUpload(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if err := s.opts.Uploads.Receive(token, r.Body); err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- Middleware ----------------------------------------------------------

// bearerAuthMiddleware rejects requests that do not carry the configured
// Bearer token. The events endpoint also accepts the token as a query
// parameter (?token=…) because EventSource cannot set request headers.
func (s *Server) bearerAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Health probes are exempt.
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}

		token := ""
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			token = strings.TrimPrefix(auth, "Bearer ")
		} else if qt := r.URL.Query().Get("token"); qt != "" {
			// Allow token via query param for SSE (EventSource can't set headers).
			token = qt
		}
		if token != s.opts.AuthToken {
			writeJSONError(w, http.StatusUnauthorized, errors.New("unauthorized"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("server: %s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

// ---- Helpers -------------------------------------------------------------

func writeJSONError(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

// checkAllowedPaths rejects file:// inputs that fall outside the allowed prefixes.
func (s *Server) checkAllowedPaths(cfg *pipeline.Config) error {
	if len(s.opts.AllowPaths) == 0 {
		// No allow-list → reject all file:// inputs.
		for _, inp := range cfg.Inputs {
			if strings.HasPrefix(inp.URL, "file://") || (!strings.Contains(inp.URL, "://") && inp.URL != "") {
				return fmt.Errorf("file:// inputs are disabled; use --allow-path to permit a directory")
			}
		}
		return nil
	}
	for _, inp := range cfg.Inputs {
		path := ""
		if strings.HasPrefix(inp.URL, "file://") {
			path = strings.TrimPrefix(inp.URL, "file://")
		} else if !strings.Contains(inp.URL, "://") {
			path = inp.URL
		}
		if path != "" && !isAllowedPath(path, s.opts.AllowPaths) {
			return fmt.Errorf("input path %q is not under an allowed directory", path)
		}
	}
	return nil
}

func isAllowedPath(path string, allowed []string) bool {
	for _, a := range allowed {
		if strings.HasPrefix(path, a) {
			return true
		}
	}
	return false
}

// serverBase returns "scheme://host" from the request, used to construct
// absolute URLs in responses.
func serverBase(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
