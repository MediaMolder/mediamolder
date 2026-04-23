// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

// Package gui implements the HTTP server backing the `mediamolder gui`
// subcommand. It serves the embedded React frontend and a small REST API
// that exposes pipeline configurations (Phase 1 scope).
package gui

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
	"strings"
)

// Options configures the GUI server.
type Options struct {
	// Addr is the listen address, e.g. "127.0.0.1:8080".
	Addr string
	// Dev disables embedded asset serving so the Vite dev server (5173) can be
	// used instead. The API is still served on Addr.
	Dev bool
	// ExamplesDir is an optional filesystem directory served at /examples/.
	// When empty, examples are unavailable.
	ExamplesDir string
}

// NewServer wires the HTTP handlers and returns an *http.Server ready to
// ListenAndServe.
func NewServer(opts Options) (*http.Server, error) {
	mux := http.NewServeMux()
	jobs := newJobManager()

	mux.HandleFunc("GET /api/health", handleHealth)
	mux.HandleFunc("GET /api/examples", makeExamplesHandler(opts.ExamplesDir))
	mux.HandleFunc("GET /api/nodes", handleListNodes)
	mux.HandleFunc("POST /api/validate", handleValidate)
	mux.HandleFunc("POST /api/run", makeRunHandler(jobs))
	mux.HandleFunc("POST /api/cancel/{jobId}", makeCancelHandler(jobs))
	mux.HandleFunc("GET /api/events/{jobId}", makeEventsHandler(jobs))

	if opts.ExamplesDir != "" {
		mux.Handle("GET /examples/",
			http.StripPrefix("/examples/", http.FileServer(http.Dir(opts.ExamplesDir))))
	}

	if !opts.Dev {
		assets, err := frontendAssets()
		if err != nil {
			return nil, fmt.Errorf("load frontend assets: %w", err)
		}
		mux.Handle("/", spaHandler(assets))
	} else {
		mux.HandleFunc("GET /", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprintln(w, "mediamolder gui (dev mode): run `npm run dev` in frontend/ and open http://127.0.0.1:5173")
		})
	}

	return &http.Server{
		Addr:    opts.Addr,
		Handler: logRequests(mux),
	}, nil
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

type exampleEntry struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

func makeExamplesHandler(dir string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if dir == "" {
			_, _ = w.Write([]byte("[]"))
			return
		}
		entries, err := fs.ReadDir(os.DirFS(dir), ".")
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err)
			return
		}
		out := make([]exampleEntry, 0, len(entries))
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			out = append(out, exampleEntry{
				Name: e.Name(),
				URL:  "/examples/" + e.Name(),
			})
		}
		_ = json.NewEncoder(w).Encode(out)
	}
}

// spaHandler serves embedded static files and falls back to index.html for
// unknown paths so the React Router (when added later) can handle them.
func spaHandler(root fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			r.URL.Path = "/index.html"
			fileServer.ServeHTTP(w, r)
			return
		}
		clean := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if _, err := fs.Stat(root, clean); err != nil {
			r.URL.Path = "/index.html"
		}
		fileServer.ServeHTTP(w, r)
	})
}

func writeJSONError(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
