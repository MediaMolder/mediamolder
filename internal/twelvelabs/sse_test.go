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
		`{"event_type":"stream_start","metadata":{}}`,
		`{"event_type":"text_generation","text":"Hello"}`,
		`{"event_type":"text_generation","text":", world"}`,
		`{"event_type":"stream_end","metadata":{}}`,
		``,
	}, "\n")

	var chunks []AnalyzeChunk
	if err := scanSSE(strings.NewReader(input), func(c AnalyzeChunk) error {
		chunks = append(chunks, c)
		return nil
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2", len(chunks))
	}
	if chunks[0].Text != "Hello" {
		t.Errorf("chunk[0].Text: got %q, want Hello", chunks[0].Text)
	}
	if chunks[1].Text != ", world" {
		t.Errorf("chunk[1].Text: got %q, want \", world\"", chunks[1].Text)
	}
}

func TestScanSSE_StopsAtStreamEnd(t *testing.T) {
	input := `{"event_type":"stream_end","metadata":{}}` + "\n"
	var called bool
	scanSSE(strings.NewReader(input), func(_ AnalyzeChunk) error { //nolint:errcheck
		called = true
		return nil
	})
	if called {
		t.Error("stream_end should not invoke the callback")
	}
}

func TestScanSSE_SkipsNonTextGenerationEvents(t *testing.T) {
	input := strings.Join([]string{
		`{"event_type":"stream_start","metadata":{}}`,
		`{"event_type":"text_generation","text":"hi"}`,
		``,
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
		"not-json",
		`{"event_type":"text_generation","text":"ok"}`,
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
	input := `{"event_type":"text_generation","text":"x"}` + "\n"
	sentinel := fmt.Errorf("stop")
	err := scanSSE(strings.NewReader(input), func(_ AnalyzeChunk) error {
		return sentinel
	})
	if err != sentinel {
		t.Errorf("got %v, want sentinel error", err)
	}
}
