// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

// Package storage provides a unified FS abstraction for local filesystem and
// object-storage backends used by the Tier 1 and Tier 2 server modes.
package storage

import (
	"context"
	"io"
	"time"
)

// Op identifies the S3 operation a presigned URL permits.
type Op int

const (
	OpGet  Op = iota // presign for GET (download)
	OpPut            // presign for PUT (upload)
	OpHead           // presign for HEAD (stat)
)

// FileInfo holds minimal metadata about a stored object.
type FileInfo struct {
	Size int64
	ETag string // empty for local files
}

// FS is the shared storage interface used across all server modes.
type FS interface {
	// Open returns a reader for the given URI.
	Open(ctx context.Context, uri string) (io.ReadCloser, error)
	// Create returns a writer for the given URI (creates or replaces).
	Create(ctx context.Context, uri string) (io.WriteCloser, error)
	// Stat returns metadata about a URI.
	Stat(ctx context.Context, uri string) (FileInfo, error)
	// Sign returns a presigned HTTPS URL valid for ttl.
	// For file:// adapters this returns the original URI unchanged.
	Sign(ctx context.Context, uri string, op Op, ttl time.Duration) (string, error)
	// List returns URIs with the given prefix.
	List(ctx context.Context, uriPrefix string) ([]string, error)
}
