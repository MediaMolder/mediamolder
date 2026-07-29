// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package twelvelabs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newTestClient returns a Client wired to srv.
func newTestClient(srv *httptest.Server) *Client {
	c := New("test-api-key")
	c.BaseURL = srv.URL
	c.HTTP = srv.Client()
	return c
}

func TestCreateIndex(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/indexes" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("x-api-key") != "test-api-key" {
			t.Errorf("missing x-api-key header")
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Index{ID: "idx1", Name: "test-index"})
	}))
	defer srv.Close()

	c := newTestClient(srv)
	idx, err := c.CreateIndex(context.Background(), "test-index", []ModelSpec{{Name: "marengo3.0"}})
	if err != nil {
		t.Fatal(err)
	}
	if idx.ID != "idx1" {
		t.Errorf("ID: got %q, want idx1", idx.ID)
	}
	if idx.Name != "test-index" {
		t.Errorf("Name: got %q, want test-index", idx.Name)
	}
	// Regression: request body must use index_name, not name (TwelveLabs v1.3 API).
	if v, ok := gotBody["index_name"]; !ok || v != "test-index" {
		t.Errorf("request body index_name: got %v (key present=%v), want test-index", v, ok)
	}
	if _, hasName := gotBody["name"]; hasName {
		t.Errorf("request body must not contain 'name' key; use 'index_name'")
	}
	// Regression: model name must serialise as model_name, not name.
	if models, ok := gotBody["models"].([]any); ok && len(models) > 0 {
		m, _ := models[0].(map[string]any)
		if v, ok := m["model_name"]; !ok || v != "marengo3.0" {
			t.Errorf("request body models[0].model_name: got %v (key present=%v), want marengo3.0", v, ok)
		}
		if _, hasName := m["name"]; hasName {
			t.Errorf("request body models[0] must not contain 'name' key; use 'model_name'")
		}
	} else {
		t.Error("request body models field missing or empty")
	}
}

func TestCreateIndex_EmptyName(t *testing.T) {
	c := New("key")
	_, err := c.CreateIndex(context.Background(), "", nil)
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestListIndexes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/indexes" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []Index{
				{ID: "a", Name: "first"},
				{ID: "b", Name: "second"},
			},
		})
	}))
	defer srv.Close()

	c := newTestClient(srv)
	indexes, err := c.ListIndexes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(indexes) != 2 {
		t.Errorf("len: got %d, want 2", len(indexes))
	}
}

// TestListIndexes_RawAPIFields regression: the TwelveLabs v1.3 API returns
// index_name / model_name / model_options — verify they decode correctly.
func TestListIndexes_RawAPIFields(t *testing.T) {
	raw := `{"data":[{"_id":"abc123","index_name":"my-index","created_at":"2026-01-01T00:00:00Z","models":[{"model_name":"marengo3.0","model_options":["visual","audio"]}]}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(raw))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	indexes, err := c.ListIndexes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(indexes) != 1 {
		t.Fatalf("len: got %d, want 1", len(indexes))
	}
	idx := indexes[0]
	if idx.ID != "abc123" {
		t.Errorf("ID: got %q, want abc123", idx.ID)
	}
	if idx.Name != "my-index" {
		t.Errorf("Name: got %q, want my-index (check json:\"index_name\" tag)", idx.Name)
	}
	if len(idx.Models) != 1 {
		t.Fatalf("Models len: got %d, want 1", len(idx.Models))
	}
	if idx.Models[0].Name != "marengo3.0" {
		t.Errorf("Models[0].Name: got %q, want marengo3.0 (check json:\"model_name\" tag)", idx.Models[0].Name)
	}
	if len(idx.Models[0].Options) != 2 || idx.Models[0].Options[0] != "visual" {
		t.Errorf("Models[0].Options: got %v, want [visual audio] (check json:\"model_options\" tag)", idx.Models[0].Options)
	}
}

func TestDeleteIndex(t *testing.T) {
	var deleted string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.NotFound(w, r)
			return
		}
		deleted = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	if err := c.DeleteIndex(context.Background(), "idx1"); err != nil {
		t.Fatal(err)
	}
	if deleted != "/indexes/idx1" {
		t.Errorf("path: got %q, want /indexes/idx1", deleted)
	}
}

func TestDeleteIndex_EmptyID(t *testing.T) {
	c := New("key")
	if err := c.DeleteIndex(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty id")
	}
}

func TestGetTask(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tasks/task1" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Task{ID: "task1", Status: TaskStatusReady})
	}))
	defer srv.Close()

	c := newTestClient(srv)
	task, err := c.GetTask(context.Background(), "task1")
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != "task1" || task.Status != TaskStatusReady {
		t.Errorf("unexpected task: %+v", task)
	}
}

func TestGetTask_EmptyID(t *testing.T) {
	c := New("key")
	_, err := c.GetTask(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty id")
	}
}

func TestWaitForTask_ImmediatelyReady(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Task{ID: "t1", Status: TaskStatusReady, VideoID: "v1"})
	}))
	defer srv.Close()

	c := newTestClient(srv)
	task, err := c.WaitForTask(context.Background(), "t1", WaitOpts{
		InitialInterval: time.Millisecond,
		MaxInterval:     5 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.VideoID != "v1" {
		t.Errorf("VideoID: got %q, want v1", task.VideoID)
	}
}

func TestWaitForTask_EventuallyReady(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		status := TaskStatusIndexing
		if calls >= 3 {
			status = TaskStatusReady
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Task{ID: "t1", Status: status})
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.WaitForTask(context.Background(), "t1", WaitOpts{
		InitialInterval: time.Millisecond,
		MaxInterval:     5 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls < 3 {
		t.Errorf("calls: got %d, want ≥3", calls)
	}
}

func TestWaitForTask_Failed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Task{ID: "t1", Status: TaskStatusFailed})
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.WaitForTask(context.Background(), "t1", WaitOpts{
		InitialInterval: time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected error for failed task")
	}
}

func TestWaitForTask_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Task{ID: "t1", Status: TaskStatusIndexing})
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	c := newTestClient(srv)
	_, err := c.WaitForTask(ctx, "t1", WaitOpts{
		InitialInterval: time.Millisecond,
		MaxInterval:     5 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected error when context times out")
	}
}

func TestAPIKey_SentInHeader(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []Index{}})
	}))
	defer srv.Close()

	c := New("my-secret-key")
	c.BaseURL = srv.URL
	c.HTTP = srv.Client()
	c.ListIndexes(context.Background()) //nolint:errcheck

	if gotKey != "my-secret-key" {
		t.Errorf("x-api-key: got %q, want my-secret-key", gotKey)
	}
}
