// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package observability

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestNewMetrics(t *testing.T) {
	m := NewMetrics("test-pipeline")
	if m.Fps == nil {
		t.Error("Fps gauge should be registered")
	}
	if m.ErrorsTotal == nil {
		t.Error("ErrorsTotal counter should be registered")
	}
	if m.PipelineState == nil {
		t.Error("PipelineState gauge should be registered")
	}
	if m.Registry() == nil {
		t.Error("Registry should not be nil")
	}
}

func TestMetricsServerStartStop(t *testing.T) {
	m := NewMetrics("test")
	srv := NewMetricsServer(":0", m.Registry())
	addr, err := srv.Start()
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if addr == "" {
		t.Fatal("expected non-empty address")
	}
	resp, err := http.Get("http://" + addr + "/health")
	if err != nil {
		t.Fatalf("health check: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("health status = %d, want 200", resp.StatusCode)
	}
	resp, err = http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatalf("metrics endpoint: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("metrics status = %d, want 200", resp.StatusCode)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestTracingInitNoop(t *testing.T) {
	ctx := context.Background()
	provider, err := Init(ctx, Config{})
	if err != nil {
		t.Fatalf("Init noop: %v", err)
	}
	defer func() { _ = provider.Shutdown(ctx) }()
	if provider.Tracer() == nil {
		t.Error("tracer should not be nil")
	}
	spanCtx, span := provider.StartPipelineSpan(ctx, "test")
	if span == nil {
		t.Error("span should not be nil")
	}
	EndSpanOK(span)
	_, nodeSpan := StartNodeSpan(spanCtx, "dec", "decoder", "h264", "video")
	EndSpanOK(nodeSpan)
	logger := Logger(ctx)
	if logger == nil {
		t.Error("logger should not be nil")
	}
}
