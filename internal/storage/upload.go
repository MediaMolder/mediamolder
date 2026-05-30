// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package storage

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const uploadURIPrefix = "upload://"

// UploadStore manages upload:// tokens. Clients allocate a token via
// POST /v1/uploads, upload the file body via PUT /v1/uploads/{token},
// then reference the token in their job config as upload://{token}.
// The server resolves the token to a temp file path before the pipeline runs.
type UploadStore struct {
	dir  string
	mu   sync.Mutex
	live map[string]*uploadEntry
}

type uploadEntry struct {
	path    string
	created time.Time
}

// NewUploadStore creates a store backed by dir (created with mode 0700 if absent).
func NewUploadStore(dir string) (*UploadStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("upload store: %w", err)
	}
	return &UploadStore{
		dir:  dir,
		live: make(map[string]*uploadEntry),
	}, nil
}

// Allocate reserves a new upload token. The backing temp file is created
// lazily when Receive is called.
func (s *UploadStore) Allocate() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b[:])
	s.mu.Lock()
	s.live[token] = &uploadEntry{
		path:    filepath.Join(s.dir, token),
		created: time.Now(),
	}
	s.mu.Unlock()
	return token, nil
}

// Receive writes the reader's content to the file backing token.
// It is safe to call Receive only once per token.
func (s *UploadStore) Receive(token string, r io.Reader) error {
	s.mu.Lock()
	entry, ok := s.live[token]
	s.mu.Unlock()
	if !ok {
		return errors.New("upload: unknown token")
	}
	f, err := os.OpenFile(entry.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("upload: create: %w", err)
	}
	defer f.Close()
	if _, err := io.Copy(f, r); err != nil {
		return fmt.Errorf("upload: write: %w", err)
	}
	return nil
}

// Resolve converts an upload:// URI to the local file path.
// Returns an error if the token is unknown or the file has not been received.
func (s *UploadStore) Resolve(uri string) (string, error) {
	if !strings.HasPrefix(uri, uploadURIPrefix) {
		return "", fmt.Errorf("upload: not an upload URI: %q", uri)
	}
	token := uri[len(uploadURIPrefix):]
	s.mu.Lock()
	entry, ok := s.live[token]
	s.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("upload: unknown token %q", token)
	}
	if _, err := os.Stat(entry.path); err != nil {
		return "", fmt.Errorf("upload: file not received for token %q", token)
	}
	return entry.path, nil
}

// Release removes a token and its backing temp file. Called after the
// pipeline completes so uploads do not accumulate on disk.
func (s *UploadStore) Release(token string) {
	s.mu.Lock()
	entry, ok := s.live[token]
	if ok {
		delete(s.live, token)
	}
	s.mu.Unlock()
	if ok {
		_ = os.Remove(entry.path)
	}
}
