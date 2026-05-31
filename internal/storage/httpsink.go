// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTPSinkFS is a write-only storage.FS that writes objects to pre-signed or
// plain HTTPS PUT endpoints. It is used when task outputs are already-presigned
// S3 PUT URLs (https://…?X-Amz-…) placed into Task.Config.Outputs by the
// orchestrator's PresignResolver. Workers never need S3 credentials — they
// write directly to the presigned URL via a plain HTTP PUT.
//
// Read operations (Open, Stat, List) are not supported; only Create is.
type HTTPSinkFS struct {
	client *http.Client
}

// NewHTTPSinkFS creates an HTTPSinkFS with a 5-minute upload timeout.
func NewHTTPSinkFS() *HTTPSinkFS {
	return &HTTPSinkFS{
		client: &http.Client{Timeout: 5 * time.Minute},
	}
}

// Create returns a writer that buffers the body and PUTs it to uri on Close.
// uri must be an http:// or https:// URL.
func (h *HTTPSinkFS) Create(_ context.Context, uri string) (io.WriteCloser, error) {
	if !strings.HasPrefix(uri, "http://") && !strings.HasPrefix(uri, "https://") {
		return nil, fmt.Errorf("HTTPSinkFS: unsupported URI scheme: %s", uri)
	}
	return &httpPutWriter{uri: uri, client: h.client}, nil
}

// Open is not supported by HTTPSinkFS.
func (h *HTTPSinkFS) Open(_ context.Context, uri string) (io.ReadCloser, error) {
	return nil, fmt.Errorf("HTTPSinkFS: Open not supported (uri %q)", uri)
}

// Stat is not supported by HTTPSinkFS.
func (h *HTTPSinkFS) Stat(_ context.Context, uri string) (FileInfo, error) {
	return FileInfo{}, fmt.Errorf("HTTPSinkFS: Stat not supported (uri %q)", uri)
}

// Sign returns the uri unchanged — presigned URLs are already signed by the
// orchestrator before being placed in the task config.
func (h *HTTPSinkFS) Sign(_ context.Context, uri string, _ Op, _ time.Duration) (string, error) {
	return uri, nil
}

// List is not supported by HTTPSinkFS.
func (h *HTTPSinkFS) List(_ context.Context, uriPrefix string) ([]string, error) {
	return nil, fmt.Errorf("HTTPSinkFS: List not supported (prefix %q)", uriPrefix)
}

// httpPutWriter buffers bytes and PUTs them to a URL on Close.
type httpPutWriter struct {
	buf    bytes.Buffer
	uri    string
	client *http.Client
}

func (w *httpPutWriter) Write(p []byte) (int, error) {
	return w.buf.Write(p)
}

func (w *httpPutWriter) Close() error {
	req, err := http.NewRequest(http.MethodPut, w.uri, bytes.NewReader(w.buf.Bytes()))
	if err != nil {
		return fmt.Errorf("HTTPSinkFS PUT: build request: %w", err)
	}
	req.ContentLength = int64(w.buf.Len())
	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("HTTPSinkFS PUT %s: %w", w.uri, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTPSinkFS PUT %s: HTTP %d", w.uri, resp.StatusCode)
	}
	return nil
}

// RouterFS routes storage operations to the appropriate FS adapter by URI scheme.
// It enables a single storage FS abstraction across workers that may receive
// file://, https:// (presigned PUT), and s3:// URIs in the same task config.
type RouterFS struct {
	file  *FileFS
	sink  *HTTPSinkFS
	s3    *S3FS // nil when no S3 credentials are configured
}

// NewRouterFS creates a RouterFS. s3 may be nil for workers without S3 creds.
func NewRouterFS(s3 *S3FS) *RouterFS {
	return &RouterFS{
		file: NewFileFS(),
		sink: NewHTTPSinkFS(),
		s3:   s3,
	}
}

func (r *RouterFS) Open(ctx context.Context, uri string) (io.ReadCloser, error) {
	switch {
	case strings.HasPrefix(uri, "https://") || strings.HasPrefix(uri, "http://"):
		return r.sink.Open(ctx, uri)
	case strings.HasPrefix(uri, "s3://"):
		if r.s3 == nil {
			return nil, fmt.Errorf("RouterFS: no S3 credentials configured for s3:// URI %q", uri)
		}
		return r.s3.Open(ctx, uri)
	default:
		return r.file.Open(ctx, uri)
	}
}

func (r *RouterFS) Create(ctx context.Context, uri string) (io.WriteCloser, error) {
	switch {
	case strings.HasPrefix(uri, "https://") || strings.HasPrefix(uri, "http://"):
		return r.sink.Create(ctx, uri)
	case strings.HasPrefix(uri, "s3://"):
		if r.s3 == nil {
			return nil, fmt.Errorf("RouterFS: no S3 credentials configured for s3:// URI %q", uri)
		}
		return r.s3.Create(ctx, uri)
	default:
		return r.file.Create(ctx, uri)
	}
}

func (r *RouterFS) Stat(ctx context.Context, uri string) (FileInfo, error) {
	switch {
	case strings.HasPrefix(uri, "https://") || strings.HasPrefix(uri, "http://"):
		return r.sink.Stat(ctx, uri)
	case strings.HasPrefix(uri, "s3://"):
		if r.s3 == nil {
			return FileInfo{}, fmt.Errorf("RouterFS: no S3 credentials configured for s3:// URI %q", uri)
		}
		return r.s3.Stat(ctx, uri)
	default:
		return r.file.Stat(ctx, uri)
	}
}

func (r *RouterFS) Sign(ctx context.Context, uri string, op Op, ttl time.Duration) (string, error) {
	switch {
	case strings.HasPrefix(uri, "https://") || strings.HasPrefix(uri, "http://"):
		return r.sink.Sign(ctx, uri, op, ttl)
	case strings.HasPrefix(uri, "s3://"):
		if r.s3 == nil {
			return "", fmt.Errorf("RouterFS: no S3 credentials configured for s3:// URI %q", uri)
		}
		return r.s3.Sign(ctx, uri, op, ttl)
	default:
		return r.file.Sign(ctx, uri, op, ttl)
	}
}

func (r *RouterFS) List(ctx context.Context, uriPrefix string) ([]string, error) {
	switch {
	case strings.HasPrefix(uriPrefix, "s3://"):
		if r.s3 == nil {
			return nil, fmt.Errorf("RouterFS: no S3 credentials configured for s3:// URI %q", uriPrefix)
		}
		return r.s3.List(ctx, uriPrefix)
	default:
		return r.file.List(ctx, uriPrefix)
	}
}
