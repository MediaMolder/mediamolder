// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// embedderMockServer simulates POST /embed/tasks (multipart) and
// GET /embed/tasks/{id}.
type embedderMockServer struct {
	srv          *httptest.Server
	taskID       string
	polls        int32
	readyAfter   int
	uploadCalls  int32
	createFails  bool
	waitFails    bool
	vectorLen    int
	segmentCount int
}

func newEmbedderMockServer(t *testing.T) *embedderMockServer {
	t.Helper()
	m := &embedderMockServer{
		taskID:       "emb-1",
		vectorLen:    4,
		segmentCount: 2,
	}
	m.srv = httptest.NewServer(http.HandlerFunc(m.handle))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *embedderMockServer) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/embed/tasks":
		atomic.AddInt32(&m.uploadCalls, 1)
		_ = r.ParseMultipartForm(1 << 20)
		if m.createFails {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"boom"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"_id":    m.taskID,
			"status": "processing",
		})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/embed/tasks/"):
		n := atomic.AddInt32(&m.polls, 1)
		if m.waitFails {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"_id": m.taskID, "status": "failed",
			})
			return
		}
		status := "processing"
		if int(n) > m.readyAfter {
			status = "ready"
		}
		body := map[string]any{
			"_id":    m.taskID,
			"status": status,
		}
		if status == "ready" {
			segs := make([]map[string]any, m.segmentCount)
			for i := range segs {
				v := make([]float32, m.vectorLen)
				for j := range v {
					v[j] = float32(i*m.vectorLen + j)
				}
				segs[i] = map[string]any{
					"embedding_scope":  "clip",
					"start_offset_sec": float64(i) * 2,
					"end_offset_sec":   float64(i)*2 + 2,
					"float_array":      v,
				}
			}
			body["video_embedding"] = map[string]any{"segments": segs}
		}
		_ = json.NewEncoder(w).Encode(body)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func newTestEmbedder(t *testing.T, m *embedderMockServer, extra map[string]any) *TwelveLabsEmbedder {
	t.Helper()
	p := &TwelveLabsEmbedder{}
	params := map[string]any{
		"api_key":         "k",
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

func TestTwelveLabsEmbedder_Init_MissingKey(t *testing.T) {
	t.Setenv("TWELVELABS_API_KEY", "")
	p := &TwelveLabsEmbedder{}
	if err := p.Init(map[string]any{}); err == nil {
		t.Fatal("expected error for missing api key")
	}
}

func TestTwelveLabsEmbedder_Init_BadFormat(t *testing.T) {
	p := &TwelveLabsEmbedder{}
	err := p.Init(map[string]any{"api_key": "k", "out_format": "npy"})
	if err == nil || !strings.Contains(err.Error(), "out_format") {
		t.Fatalf("expected out_format error, got %v", err)
	}
}

func TestTwelveLabsEmbedder_OnSegment_HappyPath_Inline(t *testing.T) {
	m := newEmbedderMockServer(t)
	m.readyAfter = 1
	p := newTestEmbedder(t, m, nil)

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
	p.OnSegmentCompleted(ctx, SegmentEvent{OutputID: "o", FilePath: file, SegmentIndex: 7})
	_ = p.Close()

	if got == nil {
		t.Fatal("no metadata emitted")
	}
	payload := got.Custom["twelvelabs"].(map[string]any)
	if payload["event"] != "embedded" {
		t.Errorf("event = %v", payload["event"])
	}
	if payload["dim"] != m.vectorLen {
		t.Errorf("dim = %v, want %d", payload["dim"], m.vectorLen)
	}
	if payload["count"] != m.segmentCount {
		t.Errorf("count = %v, want %d", payload["count"], m.segmentCount)
	}
	if _, hasOut := payload["out_file"]; hasOut {
		t.Errorf("unexpected out_file in inline mode")
	}
	embs, ok := payload["embeddings"].([]map[string]any)
	if !ok || len(embs) != m.segmentCount {
		t.Fatalf("embeddings = %#v", payload["embeddings"])
	}
}

func TestTwelveLabsEmbedder_OnSegment_DiskWrite_JSON(t *testing.T) {
	m := newEmbedderMockServer(t)
	m.readyAfter = 1
	outDir := t.TempDir()
	p := newTestEmbedder(t, m, map[string]any{
		"out_dir":    outDir,
		"out_format": "json",
	})

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

	payload := got.Custom["twelvelabs"].(map[string]any)
	outFile, ok := payload["out_file"].(string)
	if !ok || outFile == "" {
		t.Fatalf("missing out_file: %#v", payload)
	}
	wantSuffix := filepath.Join(outDir, "seg.embeddings.json")
	if outFile != wantSuffix {
		t.Errorf("out_file = %q, want %q", outFile, wantSuffix)
	}
	if _, err := os.Stat(outFile); err != nil {
		t.Fatalf("out_file not on disk: %v", err)
	}
	if _, has := payload["embeddings"]; has {
		t.Errorf("embeddings should be omitted when out_file is set")
	}
}

func TestTwelveLabsEmbedder_OnSegment_DiskWrite_JSONL(t *testing.T) {
	m := newEmbedderMockServer(t)
	m.readyAfter = 0
	outDir := t.TempDir()
	p := newTestEmbedder(t, m, map[string]any{
		"out_dir":    outDir,
		"out_format": "jsonl",
	})
	p.SetMetadataEmitter(func(*Metadata) {})

	file := writeTempFile(t, "x")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	p.OnSegmentCompleted(ctx, SegmentEvent{FilePath: file})
	_ = p.Close()

	out := filepath.Join(outDir, "seg.embeddings.jsonl")
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Count(strings.TrimRight(string(data), "\n"), "\n") + 1
	if lines != m.segmentCount {
		t.Errorf("jsonl lines = %d, want %d", lines, m.segmentCount)
	}
}

func TestTwelveLabsEmbedder_OnSegment_CreateError(t *testing.T) {
	m := newEmbedderMockServer(t)
	m.createFails = true
	p := newTestEmbedder(t, m, nil)

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

	payload := got.Custom["twelvelabs"].(map[string]any)
	if payload["event"] != "error" {
		t.Errorf("event = %v", payload["event"])
	}
}

func TestTwelveLabsEmbedder_Registry(t *testing.T) {
	if p, err := Get("twelvelabs_embedder"); err != nil || p == nil {
		t.Fatalf("twelvelabs_embedder not registered: p=%v err=%v", p, err)
	}
}
