// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

// Package gui implements the HTTP server backing the `mediamolder gui`
// subcommand. It serves the embedded React frontend and a small REST API
// that exposes pipeline configurations (Phase 1 scope).
package gui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
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
	mux.HandleFunc("GET /api/files", handleListDir)
	mux.HandleFunc("POST /api/probe", handleProbe)
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
// unknown paths so the React Router can handle client-side routing.
//
// The key subtlety: Go's http.FileServer redirects explicit requests for
// "/index.html" back to "/" (to avoid serving the same content at two URLs).
// Rewriting r.URL.Path = "/index.html" before calling FileServer therefore
// creates an infinite redirect loop. The fix:
//   - For "/", let FileServer handle the directory naturally — it serves
//     index.html from a directory without issuing a redirect.
//   - For the SPA fallback (unknown paths), read index.html and serve its
//     bytes directly via http.ServeContent, bypassing FileServer entirely.
func spaHandler(root fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// path.Clean("/") == "/"; TrimPrefix gives "".
		clean := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if clean == "" {
			// Root: FileServer serves the directory's index.html directly.
			fileServer.ServeHTTP(w, r)
			return
		}
		if _, err := fs.Stat(root, clean); err != nil {
			// Unknown path → SPA fallback. Serve index.html bytes directly to
			// avoid FileServer's /index.html → / redirect.
			serveIndexHTML(w, r, root)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

// serveIndexHTML reads index.html from the embedded FS and streams it via
// http.ServeContent. This avoids the redirect that http.FileServer issues for
// any path that ends in "/index.html".
func serveIndexHTML(w http.ResponseWriter, r *http.Request, root fs.FS) {
	content, err := fs.ReadFile(root, "index.html")
	if err != nil {
		http.Error(w, "frontend not built: run make frontend-build", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(content))
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
