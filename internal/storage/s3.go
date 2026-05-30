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

// S3FS is a storage.FS backed by Amazon S3.
// All operations are performed via presigned HTTPS URLs so the server
// process does not need any AWS SDK or s3:* IAM permissions at runtime.
type S3FS struct {
	creds  S3Credentials
	client *http.Client
}

// NewS3FS creates an S3FS using the provided credentials.
func NewS3FS(creds S3Credentials) *S3FS {
	return &S3FS{
		creds:  creds,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// Sign generates a presigned HTTPS URL for the given S3 URI.
func (s *S3FS) Sign(_ context.Context, uri string, op Op, ttl time.Duration) (string, error) {
	return s3PresignURL(uri, s.creds, op, time.Now(), ttl)
}

// Open fetches the S3 object via a presigned GET request.
func (s *S3FS) Open(ctx context.Context, uri string) (io.ReadCloser, error) {
	signed, err := s.Sign(ctx, uri, OpGet, time.Hour)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, signed, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("S3 GET %s: HTTP %d", uri, resp.StatusCode)
	}
	return resp.Body, nil
}

// Create returns a writer that buffers the body and uploads it to S3 via a
// presigned PUT request on Close. For files larger than 5 GB, a multipart
// upload path should be used instead (not yet implemented).
func (s *S3FS) Create(ctx context.Context, uri string) (io.WriteCloser, error) {
	signed, err := s.Sign(ctx, uri, OpPut, 24*time.Hour)
	if err != nil {
		return nil, err
	}
	return &s3PutWriter{ctx: ctx, signed: signed, client: s.client}, nil
}

// Stat returns the content length and ETag of the S3 object via HEAD.
func (s *S3FS) Stat(ctx context.Context, uri string) (FileInfo, error) {
	signed, err := s.Sign(ctx, uri, OpHead, time.Hour)
	if err != nil {
		return FileInfo{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, signed, nil)
	if err != nil {
		return FileInfo{}, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return FileInfo{}, err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return FileInfo{}, fmt.Errorf("S3 HEAD %s: HTTP %d", uri, resp.StatusCode)
	}
	return FileInfo{
		Size: resp.ContentLength,
		ETag: strings.Trim(resp.Header.Get("ETag"), `"`),
	}, nil
}

// List is not supported for presign-only S3 access (requires s3:ListBucket
// which presigned URLs cannot grant). Returns an error.
func (s *S3FS) List(_ context.Context, uriPrefix string) ([]string, error) {
	return nil, fmt.Errorf("S3FS.List: listing is not supported in presign-only mode (prefix %q)", uriPrefix)
}

// s3PutWriter buffers bytes then PUTs them to S3 on Close.
type s3PutWriter struct {
	ctx    context.Context
	signed string
	client *http.Client
	buf    bytes.Buffer
}

func (w *s3PutWriter) Write(p []byte) (int, error) {
	return w.buf.Write(p)
}

func (w *s3PutWriter) Close() error {
	body := bytes.NewReader(w.buf.Bytes())
	req, err := http.NewRequestWithContext(w.ctx, http.MethodPut, w.signed, body)
	if err != nil {
		return err
	}
	req.ContentLength = int64(w.buf.Len())
	resp, err := w.client.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("S3 PUT: HTTP %d", resp.StatusCode)
	}
	return nil
}
