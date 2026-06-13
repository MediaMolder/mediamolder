// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
)

// mockVidiServer returns an httptest.Server that responds to POST /infer with
// the supplied response JSON. If body is empty the server returns 500.
func mockVidiServer(t *testing.T, statusCode int, body any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/infer" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		if body != nil {
			if err := json.NewEncoder(w).Encode(body); err != nil {
				t.Errorf("mock server encode: %v", err)
			}
		}
	}))
}

// newVidiAnalyzer is a helper that initialises a VidiAnalyzer with the given
// service URL and buffer_frames, using sensible defaults for everything else.
func newVidiAnalyzer(t *testing.T, serviceURL string, bufferFrames int) *VidiAnalyzer {
	t.Helper()
	p := &VidiAnalyzer{}
	params := map[string]any{
		"service_url":   serviceURL,
		"buffer_frames": float64(bufferFrames),
	}
	if err := p.Init(params); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return p
}

// --- Init ---

func TestVidiAnalyzer_Init_MissingServiceURL(t *testing.T) {
	p := &VidiAnalyzer{}
	err := p.Init(map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing service_url, got nil")
	}
}

func TestVidiAnalyzer_Init_InvalidServiceURL(t *testing.T) {
	p := &VidiAnalyzer{}
	err := p.Init(map[string]any{"service_url": "not a url"})
	if err == nil {
		t.Fatal("expected error for invalid service_url, got nil")
	}
}

func TestVidiAnalyzer_Init_ValidParams(t *testing.T) {
	p := &VidiAnalyzer{}
	err := p.Init(map[string]any{
		"service_url":   "http://localhost:8000",
		"query":         "find objects",
		"task":          "grounding",
		"buffer_frames": float64(4),
		"process_every": float64(2),
		"jpeg_quality":  float64(80),
		"timeout_s":     float64(60),
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if p.bufferFrames != 4 {
		t.Errorf("bufferFrames = %d, want 4", p.bufferFrames)
	}
	if p.processEvery != 2 {
		t.Errorf("processEvery = %d, want 2", p.processEvery)
	}
	if p.jpegQuality != 80 {
		t.Errorf("jpegQuality = %d, want 80", p.jpegQuality)
	}
	if p.task != "grounding" {
		t.Errorf("task = %q, want grounding", p.task)
	}
}

// --- Process: non-video passthrough ---

func TestVidiAnalyzer_Process_AudioPassthrough(t *testing.T) {
	p := newVidiAnalyzer(t, "http://localhost:8000", 2)
	defer p.Close()

	frame := av.NewTestFrame(t, 64, 48, 0)
	defer frame.Close()

	ctx := ProcessorContext{
		MediaType: av.MediaTypeAudio,
		Context:   context.Background(),
	}
	out, md, err := p.Process(frame, ctx)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if out != frame {
		t.Error("expected frame passthrough for audio")
	}
	if md != nil {
		t.Errorf("expected nil metadata for audio frame, got %+v", md)
	}
}

// --- Process: frame buffering ---

func TestVidiAnalyzer_Process_BufferingNoMetadataUntilFull(t *testing.T) {
	srv := mockVidiServer(t, http.StatusOK, vidiResponse{Caption: "a test caption"})
	defer srv.Close()

	const bufN = 3
	p := newVidiAnalyzer(t, srv.URL, bufN)
	defer p.Close()

	ctx := ProcessorContext{
		MediaType: av.MediaTypeVideo,
		Context:   context.Background(),
	}

	// The first bufN-1 frames should produce no metadata.
	for i := range bufN - 1 {
		frame := av.NewTestFrame(t, 64, 48, 0)
		_, md, err := p.Process(frame, ctx)
		frame.Close()
		if err != nil {
			t.Fatalf("frame %d Process: %v", i, err)
		}
		if md != nil {
			t.Errorf("frame %d: expected nil metadata during accumulation, got %+v", i, md)
		}
	}
	if len(p.buf) != bufN-1 {
		t.Errorf("buf len = %d, want %d", len(p.buf), bufN-1)
	}
}

// --- Process: successful inference ---

func TestVidiAnalyzer_Process_InferenceRoundTrip(t *testing.T) {
	want := vidiResponse{
		Caption: "a bird in flight",
		Boxes: []vidiBox{
			{FrameIndex: 0, Label: "bird", Confidence: 0.92, Box2D: [4]float64{10, 20, 30, 40}},
		},
	}
	srv := mockVidiServer(t, http.StatusOK, want)
	defer srv.Close()

	p := newVidiAnalyzer(t, srv.URL, 1) // buffer_frames=1 fires on every frame
	defer p.Close()

	frame := av.NewTestFrame(t, 64, 48, 0)
	defer frame.Close()

	ctx := ProcessorContext{
		MediaType: av.MediaTypeVideo,
		Context:   context.Background(),
	}
	out, md, err := p.Process(frame, ctx)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if out != frame {
		t.Error("expected frame passthrough")
	}
	if md == nil {
		t.Fatal("expected non-nil metadata")
	}
	if len(md.Detections) != 1 {
		t.Errorf("Detections len = %d, want 1", len(md.Detections))
	} else {
		det := md.Detections[0]
		if det.Label != "bird" {
			t.Errorf("detection label = %q, want bird", det.Label)
		}
		if det.Confidence != 0.92 {
			t.Errorf("detection confidence = %v, want 0.92", det.Confidence)
		}
	}
	caption, _ := md.Custom["caption"].(string)
	if caption != "a bird in flight" {
		t.Errorf("caption = %q, want %q", caption, "a bird in flight")
	}
}

// --- Process: service error is non-fatal ---

func TestVidiAnalyzer_Process_ServiceErrorNonFatal(t *testing.T) {
	srv := mockVidiServer(t, http.StatusInternalServerError, nil)
	defer srv.Close()

	p := newVidiAnalyzer(t, srv.URL, 1)
	defer p.Close()

	frame := av.NewTestFrame(t, 64, 48, 0)
	defer frame.Close()

	ctx := ProcessorContext{
		MediaType: av.MediaTypeVideo,
		Context:   context.Background(),
	}
	out, md, err := p.Process(frame, ctx)
	// The pipeline must NOT receive an error — errors are surfaced as metadata.
	if err != nil {
		t.Fatalf("expected nil error for service failure, got: %v", err)
	}
	if out != frame {
		t.Error("expected frame passthrough on service error")
	}
	if md == nil {
		t.Fatal("expected metadata with vidi_error key")
	}
	if _, ok := md.Custom["vidi_error"]; !ok {
		t.Errorf("expected vidi_error in Custom, got %+v", md.Custom)
	}
}

// --- Process: process_every skipping ---

func TestVidiAnalyzer_Process_ProcessEverySkipping(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(vidiResponse{Caption: "ok"})
	}))
	defer srv.Close()

	p := &VidiAnalyzer{}
	if err := p.Init(map[string]any{
		"service_url":   srv.URL,
		"buffer_frames": float64(1),
		"process_every": float64(3), // only process every 3rd frame
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer p.Close()

	ctx := ProcessorContext{
		MediaType: av.MediaTypeVideo,
		Context:   context.Background(),
	}

	// Send 6 frames: only frames 3 and 6 (frameCount % 3 == 0) should trigger.
	for range 6 {
		frame := av.NewTestFrame(t, 32, 32, 0)
		_, _, _ = p.Process(frame, ctx)
		frame.Close()
	}

	if callCount != 2 {
		t.Errorf("inference calls = %d, want 2 (every 3rd of 6 frames)", callCount)
	}
}

// --- Registration ---

func TestVidiAnalyzer_Registered(t *testing.T) {
	proc, err := Get("vidi_analyzer")
	if err != nil {
		t.Fatalf("vidi_analyzer not found in processor registry: %v", err)
	}
	if _, ok := proc.(*VidiAnalyzer); !ok {
		t.Fatalf("Get returned %T, want *VidiAnalyzer", proc)
	}
}
