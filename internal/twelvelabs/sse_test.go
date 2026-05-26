// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package twelvelabs

import (
	"fmt"
	"strings"
	"testing"
)

func TestScanSSE_TextDeltas(t *testing.T) {
	input := strings.Join([]string{
		`data: {"type":"text_delta","data":"Hello"}`,
		`data: {"type":"text_delta","data":", world"}`,
		`data: {"type":"completed","data":"Hello, world"}`,
		`data: [DONE]`,
		``,
	}, "\n")

	var chunks []AnalyzeChunk
	if err := scanSSE(strings.NewReader(input), func(c AnalyzeChunk) error {
		chunks = append(chunks, c)
		return nil
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(chunks) != 3 {
		t.Fatalf("got %d chunks, want 3", len(chunks))
	}
	if chunks[0].Data != "Hello" {
		t.Errorf("chunk[0].Data: got %q, want Hello", chunks[0].Data)
	}
	if chunks[2].Type != "completed" {
		t.Errorf("chunk[2].Type: got %q, want completed", chunks[2].Type)
	}
}

func TestScanSSE_SkipsDONE(t *testing.T) {
	input := "data: [DONE]\n"
	var called bool
	scanSSE(strings.NewReader(input), func(_ AnalyzeChunk) error { //nolint:errcheck
		called = true
		return nil
	})
	if called {
		t.Error("[DONE] should not invoke the callback")
	}
}

func TestScanSSE_SkipsNonDataLines(t *testing.T) {
	input := strings.Join([]string{
		"event: message",
		"id: 1",
		`data: {"type":"text_delta","data":"hi"}`,
		"",
	}, "\n")
	var chunks []AnalyzeChunk
	scanSSE(strings.NewReader(input), func(c AnalyzeChunk) error { //nolint:errcheck
		chunks = append(chunks, c)
		return nil
	})
	if len(chunks) != 1 {
		t.Errorf("got %d chunks, want 1", len(chunks))
	}
}

func TestScanSSE_SkipsMalformedJSON(t *testing.T) {
	input := strings.Join([]string{
		"data: not-json",
		`data: {"type":"text_delta","data":"ok"}`,
	}, "\n")
	var chunks []AnalyzeChunk
	if err := scanSSE(strings.NewReader(input), func(c AnalyzeChunk) error {
		chunks = append(chunks, c)
		return nil
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) != 1 {
		t.Errorf("malformed JSON line should be skipped; got %d chunks, want 1", len(chunks))
	}
}

func TestScanSSE_EmptyStream(t *testing.T) {
	if err := scanSSE(strings.NewReader(""), func(_ AnalyzeChunk) error {
		t.Error("callback should not be called for empty stream")
		return nil
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestScanSSE_CallbackError(t *testing.T) {
	input := `data: {"type":"text_delta","data":"x"}` + "\n"
	sentinel := fmt.Errorf("stop")
	err := scanSSE(strings.NewReader(input), func(_ AnalyzeChunk) error {
		return sentinel
	})
	if err != sentinel {
		t.Errorf("got %v, want sentinel error", err)
	}
}
