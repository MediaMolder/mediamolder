// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package twelvelabs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAnalyze_AccumulatesText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/analyze" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		for _, line := range []string{
			`{"event_type":"stream_start","metadata":{}}`,
			`{"event_type":"text_generation","text":"This is "}`,
			`{"event_type":"text_generation","text":"a video about cats."}`,
			`{"event_type":"stream_end","metadata":{}}`,
		} {
			w.Write([]byte(line + "\n")) //nolint:errcheck
		}
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
		`{"event_type":"stream_start","metadata":{}}`,
		`{"event_type":"text_generation","text":"The "}`,
		`{"event_type":"text_generation","text":"video"}`,
		`{"event_type":"stream_end","metadata":{}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Stream bool `json:"stream"`
		}
		json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
		if !body.Stream {
			t.Error("AnalyzeStream should set stream=true")
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
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
	if len(received) != 2 { // stream_start and stream_end are skipped
		t.Errorf("chunks: got %d, want 2", len(received))
	}
	combined := ""
	for _, c := range received {
		combined += c.Text
	}
	if combined != "The video" {
		t.Errorf("combined text: got %q, want %q", combined, "The video")
	}
}

func TestAnalyzeStream_CallbackError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Write([]byte(`{"event_type":"text_generation","text":"x"}` + "\n")) //nolint:errcheck
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
