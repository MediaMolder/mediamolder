// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/MediaMolder/MediaMolder/internal/distributed/apiserver"
	"github.com/MediaMolder/MediaMolder/internal/distributed/orchestrator"
	"github.com/MediaMolder/MediaMolder/internal/distributed/queue"
	"github.com/MediaMolder/MediaMolder/internal/distributed/state"
	"github.com/MediaMolder/MediaMolder/internal/distributed/worker"
	"github.com/MediaMolder/MediaMolder/internal/server"
	"github.com/MediaMolder/MediaMolder/internal/storage"
)

// allowPaths is a repeatable --allow-path flag.
type allowPaths []string

func (a *allowPaths) String() string { return strings.Join(*a, ",") }
func (a *allowPaths) Set(v string) error {
	*a = append(*a, v)
	return nil
}

// cmdServe implements the `mediamolder serve` subcommand.
// It supports three modes via --mode:
//
//	server  (default) — Tier 1 remote execution server (per-request pipeline runs)
//	api               — Tier 2 API server + embedded workers (distributed jobs via
//	                    an in-memory queue and SQLite state store)
//	worker            — Tier 2 worker-only; connects to an existing api-mode server
//	                    via a shared queue and state store.
func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)

	// Universal flags.
	mode := fs.String("mode", "server", "Operating mode: server | api | worker")
	addr := fs.String("addr", "", "Listen address (default: :8443 for server mode, :8080 for api mode)")
	tlsCert := fs.String("tls-cert", "", "Path to TLS certificate PEM")
	tlsKey := fs.String("tls-key", "", "Path to TLS private key PEM")
	authTokenFile := fs.String("auth-token-file", "", "Path to file containing the Bearer auth token")

	// Tier 1 (--mode=server) flags.
	maxJobs := fs.Int("max-jobs", 0, "Maximum concurrent jobs (0 = unlimited)")
	_ = maxJobs
	s3CredsFile := fs.String("s3-presign-credentials", "", "Path to S3 credentials JSON (mode 0600)")
	s3TTL := fs.Duration("s3-presign-ttl", 24*time.Hour, "Presigned URL validity window")
	enableUploads := fs.Bool("enable-uploads", false, "Enable PUT /v1/uploads/{token} endpoint")
	workdir := fs.String("workdir", "", "Working directory for uploads and temp files (default: OS temp dir)")
	var paths allowPaths
	fs.Var(&paths, "allow-path", "Permit file:// inputs under this directory (may be repeated)")

	// Tier 2 (--mode=api | --mode=worker) flags.
	queueURI := fs.String("queue", "inmemory://", "Queue URI (inmemory:// for single-binary mode)")
	stateURI := fs.String("state", "", "State store URI — sqlite:///path/to/db.sqlite3 (required for api/worker mode)")
	numWorkers := fs.Int("workers", 1, "Number of embedded worker goroutines when --mode=api")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	// Load auth token (all modes).
	authToken := ""
	if *authTokenFile != "" {
		raw, err := os.ReadFile(*authTokenFile)
		if err != nil {
			return fmt.Errorf("serve: read auth-token-file: %w", err)
		}
		authToken = strings.TrimSpace(string(raw))
	}

	switch *mode {
	case "server":
		return runServerMode(*addr, *tlsCert, *tlsKey, authToken,
			*s3CredsFile, *s3TTL, *enableUploads, *workdir, []string(paths))
	case "api":
		return runAPIMode(*addr, *tlsCert, *tlsKey, authToken,
			*queueURI, *stateURI, *numWorkers)
	case "worker":
		return runWorkerMode(*queueURI, *stateURI, *numWorkers)
	default:
		return fmt.Errorf("serve: unknown --mode %q (valid: server, api, worker)", *mode)
	}
}

// ---- mode=server -----------------------------------------------------------

func runServerMode(addr, tlsCert, tlsKey, authToken,
	s3CredsFile string, s3TTL time.Duration,
	enableUploads bool, workdir string,
	paths []string,
) error {
	if addr == "" {
		addr = ":8443"
	}

	var presigner *storage.PresignResolver
	if s3CredsFile != "" {
		creds, err := storage.LoadS3CredentialsFromFile(s3CredsFile)
		if err != nil {
			return fmt.Errorf("serve: %w", err)
		}
		presigner = storage.NewPresignResolver(storage.NewS3FS(creds), s3TTL)
	} else if creds, ok := storage.LoadS3CredentialsFromEnv(); ok {
		presigner = storage.NewPresignResolver(storage.NewS3FS(creds), s3TTL)
	}

	var uploads *storage.UploadStore
	if enableUploads {
		dir := workdir
		if dir == "" {
			dir = os.TempDir()
		}
		var err error
		uploads, err = storage.NewUploadStore(dir)
		if err != nil {
			return fmt.Errorf("serve: %w", err)
		}
	}

	opts := server.Options{
		Addr:       addr,
		TLSCert:    tlsCert,
		TLSKey:     tlsKey,
		AuthToken:  authToken,
		Presigner:  presigner,
		Uploads:    uploads,
		AllowPaths: paths,
	}
	srv, err := server.NewServer(opts)
	if err != nil {
		return err
	}
	scheme := "http"
	if tlsCert != "" {
		scheme = "https"
	}
	fmt.Printf("mediamolder serve: mode=server listening on %s://%s\n", scheme, addr)
	if err := srv.ListenAndServe(); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

// ---- mode=api --------------------------------------------------------------

func runAPIMode(addr, tlsCert, tlsKey, authToken, queueURI, stateURI string, numWorkers int) error {
	if addr == "" {
		addr = ":8080"
	}
	q, err := openQueue(queueURI)
	if err != nil {
		return err
	}
	st, err := openState(stateURI)
	if err != nil {
		return err
	}
	defer st.Close()

	orch := orchestrator.New(st, q)
	w := worker.New(q, st, orch, numWorkers)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Start embedded workers.
	workerErr := make(chan error, 1)
	go func() { workerErr <- w.Run(ctx) }()

	opts := apiserver.Options{
		Addr:      addr,
		TLSCert:   tlsCert,
		TLSKey:    tlsKey,
		AuthToken: authToken,
		Orch:      orch,
	}
	srv, err := apiserver.NewServer(opts)
	if err != nil {
		return err
	}
	scheme := "http"
	if tlsCert != "" {
		scheme = "https"
	}
	fmt.Printf("mediamolder serve: mode=api listening on %s://%s (workers=%d)\n", scheme, addr, numWorkers)

	serverErr := make(chan error, 1)
	go func() { serverErr <- srv.ListenAndServe() }()

	select {
	case <-ctx.Done():
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer shutCancel()
		_ = srv.Shutdown(shutCtx)
		<-workerErr
		return nil
	case err := <-serverErr:
		cancel()
		<-workerErr
		return fmt.Errorf("serve api: %w", err)
	}
}

// ---- mode=worker -----------------------------------------------------------

func runWorkerMode(queueURI, stateURI string, concurrency int) error {
	q, err := openQueue(queueURI)
	if err != nil {
		return err
	}
	st, err := openState(stateURI)
	if err != nil {
		return err
	}
	defer st.Close()

	orch := orchestrator.New(st, q)
	w := worker.New(q, st, orch, concurrency)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	fmt.Printf("mediamolder serve: mode=worker concurrency=%d\n", concurrency)
	return w.Run(ctx)
}

// ---- URI parsers -----------------------------------------------------------

func openQueue(uri string) (queue.Queue, error) {
	if uri == "" || uri == "inmemory://" {
		return queue.NewInMemory(), nil
	}
	return nil, fmt.Errorf("serve: unsupported queue URI %q (Phase B supports: inmemory://)", uri)
}

func openState(uri string) (state.Store, error) {
	if uri == "" {
		// Default to a temp-dir SQLite file so demo/dev works out of the box.
		uri = "sqlite://" + os.TempDir() + "/mediamolder-state.sqlite3"
	}
	if strings.HasPrefix(uri, "sqlite://") {
		path := strings.TrimPrefix(uri, "sqlite://")
		return state.OpenSQLite(path)
	}
	return nil, fmt.Errorf("serve: unsupported state URI %q (Phase B supports: sqlite:///path)", uri)
}
