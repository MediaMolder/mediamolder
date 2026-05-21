// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package observability_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/MediaMolder/MediaMolder/observability"
	"github.com/MediaMolder/MediaMolder/pipeline"
)

func TestNewMetrics(t *testing.T) {
	m := observability.NewMetrics("test-pipeline")
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
	// Phase 3 metrics.
	if m.NodeActiveFrac == nil {
		t.Error("NodeActiveFrac should be registered")
	}
	if m.NodeFPS == nil {
		t.Error("NodeFPS should be registered")
	}
	if m.NodeFrameLatency == nil {
		t.Error("NodeFrameLatency should be registered")
	}
	if m.NodeThreadsConfigured == nil {
		t.Error("NodeThreadsConfigured should be registered")
	}
	if m.PipelineRealtimeSatisfied == nil {
		t.Error("PipelineRealtimeSatisfied should be registered")
	}
}

func TestMetricsUpdate(t *testing.T) {
	m := observability.NewMetrics("upd-test")

	snap := pipeline.MetricsSnapshot{
		State:   "playing",
		Elapsed: 2 * time.Second,
		Perf: []pipeline.NodePerfSnapshot{
			{
				NodeID:            "enc0",
				FPS:               28.5,
				FPSTarget:         30.0,
				FPSDeficit:        1.5,
				ActiveFrac:        0.92,
				IdleFrac:          0.05,
				StalledFrac:       0.03,
				StallCount:        4,
				MaxStallDuration:  12 * time.Millisecond,
				QueueFillFrac:     0.4,
				ThreadsConfigured: 4,
				ThreadMode:        "slice",
				ThreadsBusy:       3,
				EstimatedCPUCores: 3.68,
				FrameLatencyMean:  8 * time.Millisecond,
			},
		},
	}

	// First call: should not panic or error.
	m.Update(snap)

	// Second call with incremented stall count: delta should be positive.
	snap.Perf[0].StallCount = 7
	m.Update(snap)

	// Third call with same stall count: delta should be zero (no double-count).
	m.Update(snap)
}

func TestMetricsServerStartStop(t *testing.T) {
	m := observability.NewMetrics("test")
	srv := observability.NewMetricsServer(":0", m.Registry())
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
	provider, err := observability.Init(ctx, observability.Config{})
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
	observability.EndSpanOK(span)
	_, nodeSpan := observability.StartNodeSpan(spanCtx, "dec", "decoder", "h264", "video")
	observability.EndSpanOK(nodeSpan)
	logger := observability.Logger(ctx)
	if logger == nil {
		t.Error("logger should not be nil")
	}
}
