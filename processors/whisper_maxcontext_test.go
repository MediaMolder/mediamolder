// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build with_whisper

package processors

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
)

// TestApplyMaxContext pins the tri-state of the "max_context" param. 0 is the value that does
// the work — it disables the cross-window prompt feedback behind Whisper's long-form repetition
// loops — so it must never be confused with "the caller said nothing". A plain int field would
// have made Go's zero value mean "disable", silently changing behaviour for every existing caller
// the day the field was added; hence the pointer, and hence this test.
//
// This covers the MAPPING only; the rejections live in Validate and are driven through Init by
// TestWhisperSTTInitRejectsBadMaxContext, because their ordering relative to other params is the
// part that can regress.
func TestApplyMaxContext(t *testing.T) {
	t.Run("absent leaves the library default", func(t *testing.T) {
		var opts av.WhisperOptions
		applyMaxContext(map[string]any{}, &opts)
		if opts.MaxTextCtx != nil {
			t.Fatalf("MaxTextCtx = %d, want nil — omitting the param must not override whisper.cpp",
				*opts.MaxTextCtx)
		}
	})

	t.Run("zero is honoured, not treated as unset", func(t *testing.T) {
		var opts av.WhisperOptions
		applyMaxContext(map[string]any{"max_context": 0}, &opts)
		if opts.MaxTextCtx == nil {
			t.Fatal("max_context 0 was dropped — that is the value which disables the prompt lock")
		}
		if *opts.MaxTextCtx != 0 {
			t.Fatalf("MaxTextCtx = %d, want 0", *opts.MaxTextCtx)
		}
	})

	t.Run("a positive value passes through", func(t *testing.T) {
		var opts av.WhisperOptions
		applyMaxContext(map[string]any{"max_context": 64}, &opts)
		if opts.MaxTextCtx == nil || *opts.MaxTextCtx != 64 {
			t.Fatalf("MaxTextCtx = %v, want 64", opts.MaxTextCtx)
		}
	})

}

// TestWhisperSTTInitRejectsBadMaxContext drives Init, not applyMaxContext, because the ORDER is
// the thing under test. "max_context" is read several statements before "initial_prompt", so
// validating at the point of assignment would inspect a prompt that is still empty and wave the
// invalid pair through — Init would load the model and Process would decode the whole file to
// PCM before Full() finally rejected it. Init must fail fast, like the sidecar-path check does.
//
// These cases return before Init reaches av.NewWhisperModel, so the test needs no model and no
// ggml compute backend beside the test binary (loading one aborts the process outright).
func TestWhisperSTTInitRejectsBadMaxContext(t *testing.T) {
	// Any stat-able file: Init checks existence long before it tries to load anything.
	model := filepath.Join(t.TempDir(), "model.bin")
	if err := os.WriteFile(model, []byte("not a real model"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		params map[string]any
		want   string
	}{
		{
			name:   "negative",
			params: map[string]any{"max_context": -1},
			want:   "invalid",
		},
		{
			name:   "one",
			params: map[string]any{"max_context": 1},
			want:   "not a usable value",
		},
		{
			// The regression this test exists for.
			name:   "zero alongside an initial prompt, parsed AFTER max_context",
			params: map[string]any{"max_context": 0, "initial_prompt": "names: Frank, Elana"},
			want:   "silently ignored",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &WhisperSTT{}
			tc.params["model"] = model
			err := p.Init(tc.params)
			if err == nil {
				p.Close()
				t.Fatal("Init accepted an invalid max_context — it must fail before the model " +
					"is loaded and the file decoded, not at Full()")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}

	// The valid pair must still get through this far (it fails later, on the fake model).
	t.Run("zero without a prompt reaches the model load", func(t *testing.T) {
		p := &WhisperSTT{}
		err := p.Init(map[string]any{"model": model, "max_context": 0})
		if err == nil {
			p.Close()
			t.Fatal("expected the fake model to fail loading")
		}
		if strings.Contains(err.Error(), "silently ignored") || strings.Contains(err.Error(), "invalid") {
			t.Fatalf("max_context 0 on its own must be valid, got: %v", err)
		}
	})
}

// TestWhisperOptionsValidateAllowsSaneValues guards the boundary of the {0} ∪ [2,∞) domain, so a
// future tightening cannot quietly outlaw the two values callers actually use.
func TestWhisperOptionsValidateAllowsSaneValues(t *testing.T) {
	for _, n := range []int{0, 2, 32, 64, 224, 16384} {
		if err := (av.WhisperOptions{MaxTextCtx: &n}).Validate(); err != nil {
			t.Fatalf("MaxTextCtx %d rejected: %v", n, err)
		}
	}
	// A prompt is fine at any non-zero context; only 0 destroys it.
	n := 64
	if err := (av.WhisperOptions{MaxTextCtx: &n, InitialPrompt: "hello"}).Validate(); err != nil {
		t.Fatalf("prompt with MaxTextCtx 64 rejected: %v", err)
	}
	if err := (av.WhisperOptions{InitialPrompt: "hello"}).Validate(); err != nil {
		t.Fatalf("prompt with default MaxTextCtx rejected: %v", err)
	}
}
