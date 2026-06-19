// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build with_whisper

package av

import (
	"context"
	"os"
	"testing"
)

// modelPath returns the ggml model path from WHISPER_TEST_MODEL or skips.
func modelPath(t *testing.T) string {
	t.Helper()
	p := os.Getenv("WHISPER_TEST_MODEL")
	if p == "" {
		t.Skip("set WHISPER_TEST_MODEL to a ggml model to run whisper tests")
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("WHISPER_TEST_MODEL not readable: %v", err)
	}
	return p
}

func TestWhisperLoadAndClose(t *testing.T) {
	m, err := NewWhisperModel(modelPath(t))
	if err != nil {
		t.Fatalf("NewWhisperModel: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestWhisperFullSilence runs inference over one second of silence; it should
// complete without error and not crash the callback bridge.
func TestWhisperFullSilence(t *testing.T) {
	m, err := NewWhisperModel(modelPath(t))
	if err != nil {
		t.Fatalf("NewWhisperModel: %v", err)
	}
	defer m.Close()

	samples := make([]float32, WhisperSampleRate) // 1 s of silence
	calls := 0
	_, err = m.Full(context.Background(), samples, WhisperOptions{Language: "en"}, func(int) { calls++ })
	if err != nil {
		t.Fatalf("Full: %v", err)
	}
	if calls == 0 {
		t.Log("progress callback was not invoked (acceptable for very short input)")
	}
}

func TestWhisperFullCancelled(t *testing.T) {
	m, err := NewWhisperModel(modelPath(t))
	if err != nil {
		t.Fatalf("NewWhisperModel: %v", err)
	}
	defer m.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	samples := make([]float32, WhisperSampleRate*5)
	if _, err := m.Full(ctx, samples, WhisperOptions{Language: "en"}, nil); err == nil {
		t.Fatal("expected cancellation error from Full")
	}
}
