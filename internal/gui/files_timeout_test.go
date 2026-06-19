// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package gui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestCallWithTimeout covers the bound used to keep a slow/unresponsive volume
// from hanging the file dialog: a fast function returns its value; a slow one
// reports a timeout instead of blocking the caller.
func TestCallWithTimeout(t *testing.T) {
	t.Parallel()
	if v, ok := callWithTimeout(time.Second, func() int { return 42 }); !ok || v != 42 {
		t.Fatalf("fast fn = (%d, %v), want (42, true)", v, ok)
	}
	if _, ok := callWithTimeout(20*time.Millisecond, func() int {
		time.Sleep(300 * time.Millisecond)
		return 1
	}); ok {
		t.Fatal("slow fn must report timeout (ok=false)")
	}
}

// TestHandleListDir_ListsTempDir is a functional regression for the refactor:
// a normal directory still lists correctly through the timeout wrapper.
func TestHandleListDir_ListsTempDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/files?path="+url.QueryEscape(dir), nil)
	rec := httptest.NewRecorder()
	handleListDir(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp fileListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	byName := make(map[string]fileEntry, len(resp.Entries))
	for _, e := range resp.Entries {
		byName[e.Name] = e
	}
	if e, ok := byName["a.json"]; !ok || e.IsDir {
		t.Errorf("a.json missing or marked dir: %+v", resp.Entries)
	}
	if e, ok := byName["sub"]; !ok || !e.IsDir {
		t.Errorf("sub missing or not marked dir: %+v", resp.Entries)
	}
}

// TestCachedVolumeRoots_Caches verifies the volume enumeration is reused within
// the TTL (so the picker doesn't re-walk /Volumes on every request) and that it
// refreshes once the cache is invalidated.
func TestCachedVolumeRoots_Caches(t *testing.T) {
	// Not parallel: mutates the package-level volume cache.
	volumesMu.Lock()
	volRootsCache, volDrivesCache, volumesExpiry = nil, nil, time.Time{}
	volumesMu.Unlock()

	r1, _ := cachedVolumeRoots()
	if len(r1) == 0 {
		t.Fatal("expected at least one shortcut root (e.g. HOME / \"/\")")
	}
	volumesMu.Lock()
	exp1 := volumesExpiry
	volumesMu.Unlock()
	if exp1.IsZero() || !time.Now().Before(exp1) {
		t.Fatalf("cache expiry not set into the future: %v", exp1)
	}

	// A second call within the TTL must reuse the cached value (expiry unchanged).
	cachedVolumeRoots()
	volumesMu.Lock()
	exp2 := volumesExpiry
	volumesMu.Unlock()
	if !exp2.Equal(exp1) {
		t.Errorf("expiry changed on a cached call: %v -> %v (recomputed instead of cached)", exp1, exp2)
	}
}
