// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package twelvelabs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSearch_Basic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/search" {
			http.NotFound(w, r)
			return
		}
		var body searchBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
			w.WriteHeader(400)
			return
		}
		if body.IndexID != "idx1" {
			t.Errorf("IndexID: got %q, want idx1", body.IndexID)
		}
		if body.QueryText != "cats playing" {
			t.Errorf("QueryText: got %q", body.QueryText)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []SearchResult{
				{VideoID: "v1", StartS: 0, EndS: 5.2, Score: 0.95, Confidence: "high"},
				{VideoID: "v1", StartS: 10, EndS: 15, Score: 0.80, Confidence: "medium"},
			},
		})
	}))
	defer srv.Close()

	c := newTestClient(srv)
	results, err := c.Search(context.Background(), SearchRequest{
		IndexID:       "idx1",
		Query:         "cats playing",
		SearchOptions: []string{"visual"},
		Threshold:     "medium",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("results: got %d, want 2", len(results))
	}
	if results[0].Score != 0.95 {
		t.Errorf("Score: got %v, want 0.95", results[0].Score)
	}
	if results[0].Confidence != "high" {
		t.Errorf("Confidence: got %q, want high", results[0].Confidence)
	}
}

func TestSearch_EmptyIndexID(t *testing.T) {
	c := New("key")
	_, err := c.Search(context.Background(), SearchRequest{Query: "test"})
	if err == nil {
		t.Fatal("expected error for empty IndexID")
	}
}

func TestSearch_EmptyQuery(t *testing.T) {
	c := New("key")
	_, err := c.Search(context.Background(), SearchRequest{IndexID: "idx1"})
	if err == nil {
		t.Fatal("expected error when both Query and QueryMediaURL are empty")
	}
}

func TestSearch_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(422)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"code":    "invalid_options",
			"message": "unsupported search option",
		})
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.Search(context.Background(), SearchRequest{
		IndexID: "idx1",
		Query:   "test",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.HTTPStatus != 422 {
		t.Errorf("HTTPStatus: got %d, want 422", apiErr.HTTPStatus)
	}
}

func TestSearch_QueryMediaURL(t *testing.T) {
	var gotBody searchBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody) //nolint:errcheck
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []SearchResult{}})
	}))
	defer srv.Close()

	c := newTestClient(srv)
	c.Search(context.Background(), SearchRequest{ //nolint:errcheck
		IndexID:       "idx1",
		QueryMediaURL: "https://example.com/frame.jpg",
	})
	if gotBody.QueryMediaURL != "https://example.com/frame.jpg" {
		t.Errorf("QueryMediaURL: got %q", gotBody.QueryMediaURL)
	}
}
