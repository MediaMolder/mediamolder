// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

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
func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", ":8443", "Listen address")
	tlsCert := fs.String("tls-cert", "", "Path to TLS certificate PEM")
	tlsKey := fs.String("tls-key", "", "Path to TLS private key PEM")
	authTokenFile := fs.String("auth-token-file", "", "Path to file containing the Bearer auth token")
	maxJobs := fs.Int("max-jobs", 0, "Maximum concurrent jobs (0 = unlimited)")
	_ = maxJobs // enforced in future; recorded in opts for visibility
	s3CredsFile := fs.String("s3-presign-credentials", "", "Path to S3 credentials JSON (mode 0600)")
	s3TTL := fs.Duration("s3-presign-ttl", 24*time.Hour, "Presigned URL validity window")
	enableUploads := fs.Bool("enable-uploads", false, "Enable PUT /v1/uploads/{token} endpoint")
	workdir := fs.String("workdir", "", "Working directory for uploads and temp files (default: OS temp dir)")

	var paths allowPaths
	fs.Var(&paths, "allow-path", "Permit file:// inputs under this directory (may be repeated)")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	// Load auth token.
	authToken := ""
	if *authTokenFile != "" {
		raw, err := os.ReadFile(*authTokenFile)
		if err != nil {
			return fmt.Errorf("serve: read auth-token-file: %w", err)
		}
		authToken = strings.TrimSpace(string(raw))
	}

	// Load S3 credentials and build presigner.
	var presigner *storage.PresignResolver
	if *s3CredsFile != "" {
		creds, err := storage.LoadS3CredentialsFromFile(*s3CredsFile)
		if err != nil {
			return fmt.Errorf("serve: %w", err)
		}
		presigner = storage.NewPresignResolver(storage.NewS3FS(creds), *s3TTL)
	} else if creds, ok := storage.LoadS3CredentialsFromEnv(); ok {
		presigner = storage.NewPresignResolver(storage.NewS3FS(creds), *s3TTL)
	}

	// Build upload store when enabled.
	var uploads *storage.UploadStore
	if *enableUploads {
		dir := *workdir
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
		Addr:        *addr,
		TLSCert:     *tlsCert,
		TLSKey:      *tlsKey,
		AuthToken:   authToken,
		Presigner:   presigner,
		Uploads:     uploads,
		AllowPaths:  []string(paths),
	}

	srv, err := server.NewServer(opts)
	if err != nil {
		return err
	}

	scheme := "http"
	if *tlsCert != "" {
		scheme = "https"
	}
	fmt.Printf("mediamolder serve: listening on %s://%s\n", scheme, *addr)
	if err := srv.ListenAndServe(); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}
