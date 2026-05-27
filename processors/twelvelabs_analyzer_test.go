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

// analyzerMockServer simulates POST /tasks, GET /tasks/{id}, and POST /analyze.
type analyzerMockServer struct {
	srv            *httptest.Server
	taskPolls      int32
	analyzeCalls   int32
	analyzeFails   bool
	taskID         string
	videoID        string
	terminalStatus string
	readyAfter     int
	mu             sync.Mutex
	lastAnalyzeReq map[string]any
}

func newAnalyzerMockServer(t *testing.T) *analyzerMockServer {
	t.Helper()
	m := &analyzerMockServer{
		taskID:         "task-a",
		videoID:        "vid-a",
		terminalStatus: "ready",
	}
	m.srv = httptest.NewServer(http.HandlerFunc(m.handle))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *analyzerMockServer) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/tasks":
		_ = r.ParseMultipartForm(1 << 20)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"_id": m.taskID, "index_id": "idx", "video_id": m.videoID, "status": "pending",
		})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/tasks/"):
		n := atomic.AddInt32(&m.taskPolls, 1)
		status := "indexing"
		if int(n) > m.readyAfter {
			status = m.terminalStatus
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"_id": m.taskID, "video_id": m.videoID, "status": status,
		})
	case r.Method == http.MethodPost && r.URL.Path == "/analyze":
		atomic.AddInt32(&m.analyzeCalls, 1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		m.mu.Lock()
		m.lastAnalyzeReq = body
		m.mu.Unlock()
		if m.analyzeFails {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"boom"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":   "an-1",
			"data": "A cat plays piano.",
			"chapters": []map[string]any{
				{"start": 0.0, "end": 1.5, "chapter_title": "intro"},
				{"start": 1.5, "end": 3.0, "chapter_title": "outro"},
			},
		})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func newTestAnalyzer(t *testing.T, m *analyzerMockServer, extra map[string]any) *TwelveLabsAnalyzer {
	t.Helper()
	p := &TwelveLabsAnalyzer{}
	params := map[string]any{
		"api_key":         "k",
		"index_id":        "idx-1",
		"prompt":          "describe",
		"poll_interval_s": 0.001,
		"max_concurrent":  float64(2),
		"base_url":        m.srv.URL,
	}
	for k, v := range extra {
		params[k] = v
	}
	if err := p.Init(params); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return p
}

// --- Init validation ---

func TestTwelveLabsAnalyzer_Init_MissingKey(t *testing.T) {
	t.Setenv("TWELVELABS_API_KEY", "")
	p := &TwelveLabsAnalyzer{}
	err := p.Init(map[string]any{"index_id": "idx-1"})
	if err == nil || !strings.Contains(err.Error(), "api key") {
		t.Fatalf("expected api key error, got %v", err)
	}
}

func TestTwelveLabsAnalyzer_Init_MissingIndex(t *testing.T) {
	p := &TwelveLabsAnalyzer{}
	err := p.Init(map[string]any{"api_key": "k"})
	if err == nil || !strings.Contains(err.Error(), "index_id") {
		t.Fatalf("expected index_id error, got %v", err)
	}
}

// --- Happy path ---

func TestTwelveLabsAnalyzer_OnSegment_HappyPath(t *testing.T) {
	m := newAnalyzerMockServer(t)
	m.readyAfter = 1
	p := newTestAnalyzer(t, m, map[string]any{"segments": true})

	var got []*Metadata
	var mu sync.Mutex
	p.SetMetadataEmitter(func(md *Metadata) {
		mu.Lock()
		got = append(got, md)
		mu.Unlock()
	})

	file := writeTempFile(t, "x")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	p.OnSegmentCompleted(ctx, SegmentEvent{OutputID: "out1", FilePath: file, SegmentIndex: 3})
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("emitted %d metadata, want 1", len(got))
	}
	payload := got[0].Custom["twelvelabs"].(map[string]any)
	if payload["event"] != "analyzed" {
		t.Errorf("event = %v, want analyzed", payload["event"])
	}
	if payload["video_id"] != m.videoID {
		t.Errorf("video_id = %v", payload["video_id"])
	}
	if payload["text"] != "A cat plays piano." {
		t.Errorf("text = %v", payload["text"])
	}
	if payload["segment_index"] != 3 {
		t.Errorf("segment_index = %v", payload["segment_index"])
	}
	chs, ok := payload["chapters"].([]map[string]any)
	if !ok || len(chs) != 2 {
		t.Fatalf("chapters = %#v", payload["chapters"])
	}
	if chs[0]["title"] != "intro" {
		t.Errorf("chapter[0].title = %v", chs[0]["title"])
	}

	m.mu.Lock()
	req := m.lastAnalyzeReq
	m.mu.Unlock()
	if req["video_id"] != m.videoID {
		t.Errorf("analyze video_id = %v", req["video_id"])
	}
	if req["prompt"] != "describe" {
		t.Errorf("analyze prompt = %v", req["prompt"])
	}
}

// --- Error path: analyze fails ---

func TestTwelveLabsAnalyzer_OnSegment_AnalyzeError(t *testing.T) {
	m := newAnalyzerMockServer(t)
	m.analyzeFails = true
	p := newTestAnalyzer(t, m, nil)

	var got *Metadata
	var mu sync.Mutex
	p.SetMetadataEmitter(func(md *Metadata) {
		mu.Lock()
		got = md
		mu.Unlock()
	})

	file := writeTempFile(t, "x")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	p.OnSegmentCompleted(ctx, SegmentEvent{FilePath: file})
	_ = p.Close()

	if got == nil {
		t.Fatal("no metadata emitted")
	}
	payload := got.Custom["twelvelabs"].(map[string]any)
	if payload["event"] != "error" {
		t.Errorf("event = %v, want error", payload["event"])
	}
	if payload["source"] != "twelvelabs_analyzer" {
		t.Errorf("source = %v", payload["source"])
	}
}

// --- Registry ---

func TestTwelveLabsAnalyzer_Registry(t *testing.T) {
	if p, err := Get("twelvelabs_analyzer"); err != nil || p == nil {
		t.Fatalf("twelvelabs_analyzer not registered: p=%v err=%v", p, err)
	}
}

// --- Process pass-through ---

func TestTwelveLabsAnalyzer_ProcessPassThrough(t *testing.T) {
	p := &TwelveLabsAnalyzer{}
	out, md, err := p.Process(nil, ProcessorContext{})
	if err != nil || md != nil || out != nil {
		t.Fatalf("expected nil pass-through, got out=%v md=%v err=%v", out, md, err)
	}
}
