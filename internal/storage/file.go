// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FileFS is a storage.FS backed by the local filesystem.
type FileFS struct{}

// NewFileFS returns a new FileFS.
func NewFileFS() *FileFS { return &FileFS{} }

func (f *FileFS) Open(_ context.Context, uri string) (io.ReadCloser, error) {
	path, err := fileURIPath(uri)
	if err != nil {
		return nil, err
	}
	return os.Open(path)
}

func (f *FileFS) Create(_ context.Context, uri string) (io.WriteCloser, error) {
	path, err := fileURIPath(uri)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return os.Create(path)
}

func (f *FileFS) Stat(_ context.Context, uri string) (FileInfo, error) {
	path, err := fileURIPath(uri)
	if err != nil {
		return FileInfo{}, err
	}
	st, err := os.Stat(path)
	if err != nil {
		return FileInfo{}, err
	}
	return FileInfo{Size: st.Size()}, nil
}

// Sign returns the original URI unchanged — local files need no presigning.
func (f *FileFS) Sign(_ context.Context, uri string, _ Op, _ time.Duration) (string, error) {
	return uri, nil
}

func (f *FileFS) List(_ context.Context, uriPrefix string) ([]string, error) {
	dir, err := fileURIPath(uriPrefix)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	base := strings.TrimSuffix(uriPrefix, "/")
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			out = append(out, base+"/"+e.Name())
		}
	}
	return out, nil
}

// fileURIPath extracts the local path from a file:// URI or a bare path.
func fileURIPath(uri string) (string, error) {
	switch {
	case strings.HasPrefix(uri, "file://"):
		return filepath.FromSlash(strings.TrimPrefix(uri, "file://")), nil
	case !strings.Contains(uri, "://"):
		return uri, nil // bare path
	default:
		return "", fmt.Errorf("FileFS: unsupported URI scheme: %s", uri)
	}
}
