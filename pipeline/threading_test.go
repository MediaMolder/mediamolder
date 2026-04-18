// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"testing"

	"github.com/MediaMolder/MediaMolder/graph"
)

func TestResolveThreadCount(t *testing.T) {
	pipe := &Pipeline{}
	runner := &graphRunner{
		cfg:  &Config{},
		pipe: pipe,
	}

	node := &graph.Node{
		Params: map[string]any{},
	}

	// Default: no config → 0 (FFmpeg auto).
	if got := runner.resolveThreadCount(node); got != 0 {
		t.Errorf("default threads = %d, want 0", got)
	}

	// Global threads set.
	runner.cfg.GlobalOptions.Threads = 4
	if got := runner.resolveThreadCount(node); got != 4 {
		t.Errorf("global threads = %d, want 4", got)
	}

	// Per-node overrides global.
	node.Params["threads"] = "8"
	if got := runner.resolveThreadCount(node); got != 8 {
		t.Errorf("per-node threads = %d, want 8", got)
	}

	// MaxThreads clamps.
	pipe.maxThreads = 6
	if got := runner.resolveThreadCount(node); got != 6 {
		t.Errorf("clamped threads = %d, want 6", got)
	}

	// Global also clamped by maxThreads.
	node.Params = map[string]any{}
	runner.cfg.GlobalOptions.Threads = 10
	if got := runner.resolveThreadCount(node); got != 6 {
		t.Errorf("clamped global threads = %d, want 6", got)
	}
}

func TestResolveThreadType(t *testing.T) {
	runner := &graphRunner{
		cfg:  &Config{},
		pipe: &Pipeline{},
	}

	node := &graph.Node{
		Params: map[string]any{},
	}

	// Default: empty string.
	if got := runner.resolveThreadType(node); got != "" {
		t.Errorf("default thread_type = %q, want empty", got)
	}

	// Global thread_type.
	runner.cfg.GlobalOptions.ThreadType = "frame"
	if got := runner.resolveThreadType(node); got != "frame" {
		t.Errorf("global thread_type = %q, want %q", got, "frame")
	}

	// Per-node overrides global.
	node.Params["thread_type"] = "slice"
	if got := runner.resolveThreadType(node); got != "slice" {
		t.Errorf("per-node thread_type = %q, want %q", got, "slice")
	}
}

func TestSetMaxThreads(t *testing.T) {
	p := &Pipeline{}
	if p.maxThreads != 0 {
		t.Fatalf("initial maxThreads = %d, want 0", p.maxThreads)
	}
	p.SetMaxThreads(12)
	if p.maxThreads != 12 {
		t.Errorf("maxThreads after Set = %d, want 12", p.maxThreads)
	}
}
