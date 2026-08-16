// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build with_whisper

package processors

import (
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
// Deliberately exercises applyMaxContext rather than Init: Init loads the model as its last step,
// which aborts the process outright when ggml has no compute backend beside the test binary. The
// mapping and its rejections are the interesting part and they are reachable without a model.
func TestApplyMaxContext(t *testing.T) {
	t.Run("absent leaves the library default", func(t *testing.T) {
		var opts av.WhisperOptions
		if err := applyMaxContext(map[string]any{}, &opts); err != nil {
			t.Fatalf("applyMaxContext: %v", err)
		}
		if opts.MaxTextCtx != nil {
			t.Fatalf("MaxTextCtx = %d, want nil — omitting the param must not override whisper.cpp",
				*opts.MaxTextCtx)
		}
	})

	t.Run("zero is honoured, not treated as unset", func(t *testing.T) {
		var opts av.WhisperOptions
		if err := applyMaxContext(map[string]any{"max_context": 0}, &opts); err != nil {
			t.Fatalf("applyMaxContext: %v", err)
		}
		if opts.MaxTextCtx == nil {
			t.Fatal("max_context 0 was dropped — that is the value which disables the prompt lock")
		}
		if *opts.MaxTextCtx != 0 {
			t.Fatalf("MaxTextCtx = %d, want 0", *opts.MaxTextCtx)
		}
	})

	t.Run("a positive value passes through", func(t *testing.T) {
		var opts av.WhisperOptions
		if err := applyMaxContext(map[string]any{"max_context": 64}, &opts); err != nil {
			t.Fatalf("applyMaxContext: %v", err)
		}
		if opts.MaxTextCtx == nil || *opts.MaxTextCtx != 64 {
			t.Fatalf("MaxTextCtx = %v, want 64", opts.MaxTextCtx)
		}
	})

	// The rejections below all describe things whisper.cpp ACCEPTS and then mishandles in
	// silence. Each leaves the option unset so a caller that ignores the error is not left
	// holding a value the engine would misuse.
	for _, tc := range []struct {
		name   string
		params map[string]any
		opts   av.WhisperOptions
		want   string
	}{
		{
			name:   "negative is rejected",
			params: map[string]any{"max_context": -1},
			want:   "invalid",
		},
		{
			name:   "one is rejected (whisper.cpp reads out of bounds)",
			params: map[string]any{"max_context": 1},
			want:   "out of",
		},
		{
			name:   "zero with an initial prompt is rejected, not silently dropped",
			params: map[string]any{"max_context": 0},
			opts:   av.WhisperOptions{InitialPrompt: "names: Frank, Elana"},
			want:   "silently ignored",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := tc.opts
			err := applyMaxContext(tc.params, &opts)
			if err == nil {
				t.Fatal("want an error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
			if opts.MaxTextCtx != nil {
				t.Fatalf("a rejected value was left applied: %d", *opts.MaxTextCtx)
			}
		})
	}
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
