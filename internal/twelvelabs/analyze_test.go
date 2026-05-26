// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package twelvelabs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnalyze_Sync(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/analyze" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Stream bool `json:"stream"`
		}
		json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
		if body.Stream {
			t.Error("sync Analyze should not set stream=true")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(AnalyzeResult{
			ID:   "result1",
			Text: "This is a video about cats.",
		})
	}))
	defer srv.Close()

	c := newTestClient(srv)
	result, err := c.Analyze(context.Background(), AnalyzeRequest{
		VideoID: "vid1",
		Prompt:  "What is this video about?",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != "result1" {
		t.Errorf("ID: got %q, want result1", result.ID)
	}
	if result.Text != "This is a video about cats." {
		t.Errorf("Text: got %q", result.Text)
	}
}

func TestAnalyze_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]string{
			"code":    "video_not_found",
			"message": "Video not found",
		})
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.Analyze(context.Background(), AnalyzeRequest{VideoID: "bad", Prompt: "?"})
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.Code != "video_not_found" {
		t.Errorf("Code: got %q", apiErr.Code)
	}
}

func TestAnalyzeStream(t *testing.T) {
	events := []string{
		`data: {"type":"text_delta","data":"The "}`,
		`data: {"type":"text_delta","data":"video"}`,
		`data: {"type":"completed","data":"The video"}`,
		`data: [DONE]`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Stream bool `json:"stream"`
		}
		json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
		if !body.Stream {
			t.Error("AnalyzeStream should set stream=true")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, e := range events {
			w.Write([]byte(e + "\n")) //nolint:errcheck
		}
	}))
	defer srv.Close()

	c := newTestClient(srv)
	var received []AnalyzeChunk
	err := c.AnalyzeStream(context.Background(), AnalyzeRequest{
		VideoID: "vid1",
		Prompt:  "describe",
	}, func(chunk AnalyzeChunk) error {
		received = append(received, chunk)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(received) != 3 { // [DONE] is skipped
		t.Errorf("chunks: got %d, want 3", len(received))
	}
	combined := ""
	for _, c := range received {
		combined += c.Data
	}
	if !strings.Contains(combined, "The video") {
		t.Errorf("combined text: got %q", combined)
	}
}

func TestAnalyzeStream_CallbackError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"type":"text_delta","data":"x"}` + "\n")) //nolint:errcheck
	}))
	defer srv.Close()

	sentinel := fmt.Errorf("stop processing")
	c := newTestClient(srv)
	err := c.AnalyzeStream(context.Background(), AnalyzeRequest{
		VideoID: "v1",
		Prompt:  "p",
	}, func(_ AnalyzeChunk) error {
		return sentinel
	})
	if err != sentinel {
		t.Errorf("got %v, want sentinel error", err)
	}
}
