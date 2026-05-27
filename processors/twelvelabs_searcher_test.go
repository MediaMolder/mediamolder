// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// searcherMockServer simulates POST /search.
type searcherMockServer struct {
	srv     *httptest.Server
	calls   int32
	fail    bool
	mu      sync.Mutex
	lastReq map[string]any
	results []map[string]any
}

func newSearcherMockServer(t *testing.T) *searcherMockServer {
	t.Helper()
	m := &searcherMockServer{
		results: []map[string]any{
			{"video_id": "v1", "start": 0.0, "end": 2.5, "score": 0.9, "confidence": "high"},
			{"video_id": "v2", "start": 5.0, "end": 7.0, "score": 0.3, "confidence": "low"},
		},
	}
	m.srv = httptest.NewServer(http.HandlerFunc(m.handle))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *searcherMockServer) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/search":
		atomic.AddInt32(&m.calls, 1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		m.mu.Lock()
		m.lastReq = body
		m.mu.Unlock()
		if m.fail {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"boom"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": m.results})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func newTestSearcher(t *testing.T, m *searcherMockServer, extra map[string]any) *TwelveLabsSearcher {
	t.Helper()
	p := &TwelveLabsSearcher{}
	params := map[string]any{
		"api_key":  "k",
		"index_id": "idx-1",
		"query":    "cat",
		"base_url": m.srv.URL,
	}
	for k, v := range extra {
		params[k] = v
	}
	if err := p.Init(params); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return p
}

func TestTwelveLabsSearcher_Init_MissingQuery(t *testing.T) {
	p := &TwelveLabsSearcher{}
	err := p.Init(map[string]any{"api_key": "k", "index_id": "i"})
	if err == nil || !strings.Contains(err.Error(), "query") {
		t.Fatalf("expected query error, got %v", err)
	}
}

func TestTwelveLabsSearcher_Init_MissingIndex(t *testing.T) {
	p := &TwelveLabsSearcher{}
	err := p.Init(map[string]any{"api_key": "k", "query": "cat"})
	if err == nil || !strings.Contains(err.Error(), "index_id") {
		t.Fatalf("expected index_id error, got %v", err)
	}
}

func TestTwelveLabsSearcher_OnSegment_HappyPath(t *testing.T) {
	m := newSearcherMockServer(t)
	p := newTestSearcher(t, m, nil)

	var got *Metadata
	var mu sync.Mutex
	p.SetMetadataEmitter(func(md *Metadata) {
		mu.Lock()
		got = md
		mu.Unlock()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	p.OnSegmentCompleted(ctx, SegmentEvent{OutputID: "o", FilePath: "/tmp/seg.mp4", SegmentIndex: 1})
	_ = p.Close()

	if got == nil {
		t.Fatal("no metadata emitted")
	}
	payload := got.Custom["twelvelabs"].(map[string]any)
	if payload["event"] != "search" {
		t.Errorf("event = %v", payload["event"])
	}
	matches := payload["matches"].([]map[string]any)
	if len(matches) != 2 {
		t.Errorf("matches = %d, want 2", len(matches))
	}
	if payload["file_path"] != "/tmp/seg.mp4" {
		t.Errorf("file_path = %v", payload["file_path"])
	}
}

func TestTwelveLabsSearcher_MinScoreFilter(t *testing.T) {
	m := newSearcherMockServer(t)
	p := newTestSearcher(t, m, map[string]any{"min_score": 0.5})

	var got *Metadata
	var mu sync.Mutex
	p.SetMetadataEmitter(func(md *Metadata) {
		mu.Lock()
		got = md
		mu.Unlock()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	p.OnSegmentCompleted(ctx, SegmentEvent{FilePath: "x"})
	_ = p.Close()

	payload := got.Custom["twelvelabs"].(map[string]any)
	matches := payload["matches"].([]map[string]any)
	if len(matches) != 1 {
		t.Errorf("filtered matches = %d, want 1", len(matches))
	}
	if matches[0]["score"].(float64) < 0.5 {
		t.Errorf("low-score match leaked through: %v", matches[0])
	}
}

func TestTwelveLabsSearcher_TimerMode(t *testing.T) {
	m := newSearcherMockServer(t)
	p := newTestSearcher(t, m, map[string]any{"interval_s": 0.05})

	var count int32
	p.SetMetadataEmitter(func(*Metadata) {
		atomic.AddInt32(&count, 1)
	})

	// Wait for at least 2 ticks (initial immediate + one ticker fire).
	time.Sleep(120 * time.Millisecond)
	_ = p.Close()

	if c := atomic.LoadInt32(&count); c < 2 {
		t.Errorf("timer mode emitted %d events, want >=2", c)
	}
	// In timer mode, OnSegmentCompleted is a no-op.
	before := atomic.LoadInt32(&m.calls)
	p.OnSegmentCompleted(context.Background(), SegmentEvent{FilePath: "x"})
	time.Sleep(20 * time.Millisecond)
	if atomic.LoadInt32(&m.calls) != before {
		t.Errorf("segment event should be ignored in timer mode")
	}
}

func TestTwelveLabsSearcher_Error(t *testing.T) {
	m := newSearcherMockServer(t)
	m.fail = true
	p := newTestSearcher(t, m, nil)

	var got *Metadata
	var mu sync.Mutex
	p.SetMetadataEmitter(func(md *Metadata) {
		mu.Lock()
		got = md
		mu.Unlock()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	p.OnSegmentCompleted(ctx, SegmentEvent{FilePath: "x"})
	_ = p.Close()

	payload := got.Custom["twelvelabs"].(map[string]any)
	if payload["event"] != "error" {
		t.Errorf("event = %v, want error", payload["event"])
	}
}

func TestTwelveLabsSearcher_Registry(t *testing.T) {
	if p, err := Get("twelvelabs_searcher"); err != nil || p == nil {
		t.Fatalf("twelvelabs_searcher not registered: p=%v err=%v", p, err)
	}
}
