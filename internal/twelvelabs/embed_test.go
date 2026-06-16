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

// fakeEmbedTaskResponse encodes the raw nested API shape.
func fakeEmbedTaskResponse(id, status string, segments int) map[string]any {
	segs := make([]map[string]any, segments)
	for i := range segs {
		segs[i] = map[string]any{
			"embedding_scope":  "clip",
			"start_offset_sec": float64(i) * 6,
			"end_offset_sec":   float64(i+1) * 6,
			"float_array":      []float32{0.1, -0.2, 0.3},
		}
	}
	return map[string]any{
		"_id":    id,
		"status": status,
		"video_embedding": map[string]any{
			"segments": segs,
		},
	}
}

func TestEmbedText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/embed" {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
		if body["text"] != "a cat" {
			t.Errorf("text: got %v", body["text"])
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model_name": "marengo3.0",
			"text_embedding": map[string]any{
				"float_array": []float32{0.1, 0.2, 0.3},
			},
		})
	}))
	defer srv.Close()

	c := newTestClient(srv)
	embeddings, err := c.EmbedText(context.Background(), "a cat", EmbedOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(embeddings) != 1 {
		t.Fatalf("len: got %d, want 1", len(embeddings))
	}
	if embeddings[0].Scope != "text" {
		t.Errorf("Scope: got %q, want text", embeddings[0].Scope)
	}
	if len(embeddings[0].Vector) != 3 {
		t.Errorf("Vector len: got %d, want 3", len(embeddings[0].Vector))
	}
}

func TestEmbedText_EmptyText(t *testing.T) {
	c := New("key")
	_, err := c.EmbedText(context.Background(), "", EmbedOpts{})
	if err == nil {
		t.Fatal("expected error for empty text")
	}
}

func TestEmbedVideo_URL(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embed/tasks" {
			http.NotFound(w, r)
			return
		}
		json.NewDecoder(r.Body).Decode(&gotBody) //nolint:errcheck
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fakeEmbedTaskResponse("et1", TaskStatusPending, 0))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	task, err := c.EmbedVideo(context.Background(), EmbedSource{
		URL: "https://example.com/video.mp4",
	}, EmbedOpts{Model: "marengo3.0", Scopes: []string{"clip"}})
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != "et1" {
		t.Errorf("ID: got %q, want et1", task.ID)
	}
	if gotBody["video_url"] != "https://example.com/video.mp4" {
		t.Errorf("video_url: got %v", gotBody["video_url"])
	}
}

func TestEmbedVideo_MissingSource(t *testing.T) {
	c := New("key")
	_, err := c.EmbedVideo(context.Background(), EmbedSource{}, EmbedOpts{})
	if err == nil {
		t.Fatal("expected error when neither File nor URL is set")
	}
}

func TestGetEmbedTask_FlattenSegments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embed/tasks/et1" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fakeEmbedTaskResponse("et1", TaskStatusReady, 3))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	task, err := c.GetEmbedTask(context.Background(), "et1")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != TaskStatusReady {
		t.Errorf("Status: got %q", task.Status)
	}
	if len(task.Embeddings) != 3 {
		t.Fatalf("Embeddings: got %d, want 3", len(task.Embeddings))
	}
	if task.Embeddings[0].Scope != "clip" {
		t.Errorf("Scope: got %q, want clip", task.Embeddings[0].Scope)
	}
	if task.Embeddings[2].StartS != 12 {
		t.Errorf("StartS[2]: got %v, want 12", task.Embeddings[2].StartS)
	}
}

func TestGetEmbedTask_EmptyID(t *testing.T) {
	c := New("key")
	_, err := c.GetEmbedTask(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty id")
	}
}

func TestWaitForEmbedTask_Ready(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fakeEmbedTaskResponse("et1", TaskStatusReady, 2))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	task, err := c.WaitForEmbedTask(context.Background(), "et1", WaitOpts{
		InitialInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(task.Embeddings) != 2 {
		t.Errorf("Embeddings: got %d, want 2", len(task.Embeddings))
	}
}

func TestWaitForEmbedTask_Failed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fakeEmbedTaskResponse("et1", TaskStatusFailed, 0))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.WaitForEmbedTask(context.Background(), "et1", WaitOpts{
		InitialInterval: time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected error for failed embed task")
	}
}

func TestWaitForEmbedTask_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fakeEmbedTaskResponse("et1", TaskStatusIndexing, 0))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	c := newTestClient(srv)
	_, err := c.WaitForEmbedTask(ctx, "et1", WaitOpts{
		InitialInterval: time.Millisecond,
		MaxInterval:     5 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected error when context times out")
	}
}
