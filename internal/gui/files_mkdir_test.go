// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package gui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func postMkdir(t *testing.T, parent, name string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(mkdirRequest{Path: parent, Name: name})
	req := httptest.NewRequest(http.MethodPost, "/api/files/mkdir", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handleMkdir(rec, req)
	return rec
}

// handleMkdir creates the directory and reports the sanitized (re-derived) path.
func TestHandleMkdirCreatesWithinRoot(t *testing.T) {
	parent := t.TempDir()
	rec := postMkdir(t, parent, "newdir")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp mkdirResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(parent, "newdir")
	if resp.Path != want {
		t.Errorf("path = %q, want %q", resp.Path, want)
	}
	if info, err := os.Stat(want); err != nil || !info.IsDir() {
		t.Errorf("directory not created: %v", err)
	}
}

// A name that is not a single safe path component is rejected (no traversal).
func TestHandleMkdirRejectsBadName(t *testing.T) {
	parent := t.TempDir()
	for _, name := range []string{"a/b", "..", ".", "a/../b", `a\b`} {
		if rec := postMkdir(t, parent, name); rec.Code != http.StatusBadRequest {
			t.Errorf("name %q: status = %d, want 400", name, rec.Code)
		}
	}
}
