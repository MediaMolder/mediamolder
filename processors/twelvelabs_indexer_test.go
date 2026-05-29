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

	"github.com/MediaMolder/MediaMolder/internal/twelvelabs"
)

// indexerMockServer simulates the TwelveLabs REST surface used by
// TwelveLabsIndexer: POST /tasks (multipart upload), GET /tasks/{id},
// and POST /indexes for auto-create.
type indexerMockServer struct {
	t              *testing.T
	srv            *httptest.Server
	mu             sync.Mutex
	createdTasks   int
	createdIndexes int
	taskFailsOnce  bool
	indexResponse  string // id returned by /indexes
	taskID         string
	videoID        string
	terminalStatus string // ready or failed
	// readyAfter sets how many GET /tasks/{id} calls return "indexing"
	// before returning terminalStatus.
	readyAfter int
	taskPolls  int32
}

func newIndexerMockServer(t *testing.T) *indexerMockServer {
	t.Helper()
	m := &indexerMockServer{
		t:              t,
		taskID:         "task-1",
		videoID:        "vid-1",
		indexResponse:  "idx-auto",
		terminalStatus: "ready",
	}
	m.srv = httptest.NewServer(http.HandlerFunc(m.handle))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *indexerMockServer) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/indexes":
		m.mu.Lock()
		m.createdIndexes++
		id := m.indexResponse
		m.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"_id":  id,
			"name": "test",
		})
	case r.Method == http.MethodPost && r.URL.Path == "/tasks":
		// drain multipart body
		_ = r.ParseMultipartForm(1 << 20)
		m.mu.Lock()
		m.createdTasks++
		fail := m.taskFailsOnce
		m.taskFailsOnce = false
		m.mu.Unlock()
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"boom"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"_id":      m.taskID,
			"index_id": "idx-1",
			"video_id": m.videoID,
			"status":   "pending",
		})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/tasks/"):
		n := atomic.AddInt32(&m.taskPolls, 1)
		status := "indexing"
		if int(n) > m.readyAfter {
			status = m.terminalStatus
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"_id":      m.taskID,
			"index_id": "idx-1",
			"video_id": m.videoID,
			"status":   status,
		})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "seg.mp4")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	return p
}

func newTestIndexer(t *testing.T, m *indexerMockServer, extra map[string]any) *TwelveLabsIndexer {
	t.Helper()
	p := &TwelveLabsIndexer{}
	params := map[string]any{
		"api_key":         "test-key",
		"index_id":        "idx-1",
		"wait_for_ready":  true,
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

func TestTwelveLabsIndexer_Init_MissingAPIKey(t *testing.T) {
	t.Setenv("TWELVELABS_API_KEY", "")
	// Redirect config-file lookup away from any real ~/.config file.
	orig := twelvelabs.DefaultConfigPath
	twelvelabs.DefaultConfigPath = filepath.Join(t.TempDir(), "no_such.json")
	t.Cleanup(func() { twelvelabs.DefaultConfigPath = orig })
	p := &TwelveLabsIndexer{}
	err := p.Init(map[string]any{"index_id": "idx-1"})
	if err == nil || !strings.Contains(err.Error(), "api key") {
		t.Fatalf("expected api key error, got %v", err)
	}
}

func TestTwelveLabsIndexer_APIKeyFromConfigFile(t *testing.T) {
	t.Setenv("TWELVELABS_API_KEY", "")
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "twelvelabs.json")
	if err := os.WriteFile(cfgFile, []byte(`{"api_key":"filecfgkey"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	orig := twelvelabs.DefaultConfigPath
	twelvelabs.DefaultConfigPath = cfgFile
	t.Cleanup(func() { twelvelabs.DefaultConfigPath = orig })
	p := &TwelveLabsIndexer{}
	if err := p.Init(map[string]any{"index_id": "idx-1"}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if p.apiKey != "filecfgkey" {
		t.Fatalf("apiKey = %q, want filecfgkey", p.apiKey)
	}
}

func TestTwelveLabsIndexer_Init_MissingIndex(t *testing.T) {
	p := &TwelveLabsIndexer{}
	err := p.Init(map[string]any{"api_key": "k"})
	if err == nil || !strings.Contains(err.Error(), "index_id") {
		t.Fatalf("expected index_id error, got %v", err)
	}
}

func TestTwelveLabsIndexer_Init_AutoCreateRequiresName(t *testing.T) {
	p := &TwelveLabsIndexer{}
	err := p.Init(map[string]any{
		"api_key":           "k",
		"auto_create_index": true,
	})
	if err == nil || !strings.Contains(err.Error(), "index_name") {
		t.Fatalf("expected index_name error, got %v", err)
	}
}

func TestTwelveLabsIndexer_APIKeyFromEnv(t *testing.T) {
	t.Setenv("MY_TL_KEY", "envkey")
	p := &TwelveLabsIndexer{}
	if err := p.Init(map[string]any{
		"api_key_env": "MY_TL_KEY",
		"index_id":    "idx-1",
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if p.apiKey != "envkey" {
		t.Fatalf("apiKey = %q, want envkey", p.apiKey)
	}
}

// --- OnSegmentCompleted happy path ---

func TestTwelveLabsIndexer_OnSegment_HappyPath(t *testing.T) {
	m := newIndexerMockServer(t)
	m.readyAfter = 1
	p := newTestIndexer(t, m, nil)

	var got []*Metadata
	var mu sync.Mutex
	p.SetMetadataEmitter(func(md *Metadata) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, md)
	})

	file := writeTempFile(t, "fake mp4 bytes")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	p.OnSegmentCompleted(ctx, SegmentEvent{
		OutputID:     "out1",
		FilePath:     file,
		SegmentIndex: 0,
	})
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Filter to final events only (progress events like "uploading", "task_created",
	// "waiting" are emitted in addition to the terminal "indexed"/"error" event).
	var finals []*Metadata
	for _, md := range got {
		if tl, ok := md.Custom["twelvelabs"].(map[string]any); ok {
			ev, _ := tl["event"].(string)
			if ev == "indexed" || ev == "error" {
				finals = append(finals, md)
			}
		}
	}
	if len(finals) != 1 {
		t.Fatalf("emitted %d final metadata, want 1", len(finals))
	}
	payload, ok := finals[0].Custom["twelvelabs"].(map[string]any)
	if !ok {
		t.Fatalf("missing twelvelabs payload: %#v", got[0].Custom)
	}
	if payload["event"] != "indexed" {
		t.Errorf("event = %v, want indexed", payload["event"])
	}
	if payload["status"] != "ready" {
		t.Errorf("status = %v, want ready", payload["status"])
	}
	if payload["task_id"] != m.taskID {
		t.Errorf("task_id = %v, want %s", payload["task_id"], m.taskID)
	}
	if payload["file_path"] != file {
		t.Errorf("file_path = %v, want %s", payload["file_path"], file)
	}
}

// --- Error path: CreateIndexTask fails ---

func TestTwelveLabsIndexer_OnSegment_CreateTaskError(t *testing.T) {
	m := newIndexerMockServer(t)
	m.taskFailsOnce = true
	p := newTestIndexer(t, m, nil)

	var got *Metadata
	var mu sync.Mutex
	p.SetMetadataEmitter(func(md *Metadata) {
		mu.Lock()
		defer mu.Unlock()
		got = md
	})

	file := writeTempFile(t, "x")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	p.OnSegmentCompleted(ctx, SegmentEvent{OutputID: "out1", FilePath: file})
	_ = p.Close()

	if got == nil {
		t.Fatal("no metadata emitted on error")
	}
	payload := got.Custom["twelvelabs"].(map[string]any)
	if payload["event"] != "error" {
		t.Errorf("event = %v, want error", payload["event"])
	}
}

// --- Concurrency: max_concurrent caps in-flight uploads ---

func TestTwelveLabsIndexer_MaxConcurrent(t *testing.T) {
	m := newIndexerMockServer(t)
	m.readyAfter = 2
	p := newTestIndexer(t, m, map[string]any{"max_concurrent": float64(2)})

	var counter int32
	p.SetMetadataEmitter(func(md *Metadata) {
		// Count only terminal events (indexed/error), not progress events.
		if tl, ok := md.Custom["twelvelabs"].(map[string]any); ok {
			ev, _ := tl["event"].(string)
			if ev == "indexed" || ev == "error" {
				atomic.AddInt32(&counter, 1)
			}
		}
	})

	file := writeTempFile(t, "x")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for i := 0; i < 5; i++ {
		p.OnSegmentCompleted(ctx, SegmentEvent{
			OutputID:     "out1",
			FilePath:     file,
			SegmentIndex: i,
		})
	}
	_ = p.Close()
	if atomic.LoadInt32(&counter) != 5 {
		t.Fatalf("processed %d / 5", counter)
	}
	if m.createdTasks != 5 {
		t.Errorf("createdTasks = %d, want 5", m.createdTasks)
	}
}

// --- Auto-create index runs at most once ---

func TestTwelveLabsIndexer_AutoCreateOnce(t *testing.T) {
	m := newIndexerMockServer(t)
	m.readyAfter = 0
	p := newTestIndexer(t, m, map[string]any{
		"auto_create_index": true,
		"index_name":        "auto",
		"index_id":          "", // override default to force auto
	})

	p.SetMetadataEmitter(func(md *Metadata) {})
	file := writeTempFile(t, "x")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for i := 0; i < 3; i++ {
		p.OnSegmentCompleted(ctx, SegmentEvent{FilePath: file, SegmentIndex: i})
	}
	_ = p.Close()
	if m.createdIndexes != 1 {
		t.Errorf("createdIndexes = %d, want 1", m.createdIndexes)
	}
}

// --- Process is a pass-through ---

func TestTwelveLabsIndexer_ProcessPassThrough(t *testing.T) {
	p := &TwelveLabsIndexer{}
	frame, md, err := p.Process(nil, ProcessorContext{})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if md != nil {
		t.Errorf("md = %v, want nil", md)
	}
	if frame != nil {
		t.Errorf("frame = %v, want nil", frame)
	}
}

// --- Registry ---

func TestTwelveLabsIndexer_Registered(t *testing.T) {
	p, err := Get("twelvelabs_indexer")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, ok := p.(*TwelveLabsIndexer); !ok {
		t.Fatalf("registered type = %T, want *TwelveLabsIndexer", p)
	}
}
