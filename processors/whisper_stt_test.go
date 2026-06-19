// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build with_whisper

package processors

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
)

func TestWhisperSTTRegistered(t *testing.T) {
	p, err := Get("whisper_stt")
	if err != nil {
		t.Fatalf("whisper_stt not registered: %v", err)
	}
	if _, ok := p.(*WhisperSTT); !ok {
		t.Fatalf("whisper_stt registered as %T, want *WhisperSTT", p)
	}
}

func TestWhisperSTTInitRequiresModel(t *testing.T) {
	if err := (&WhisperSTT{}).Init(map[string]any{}); err == nil {
		t.Fatal("expected error when model param is missing")
	}
	if err := (&WhisperSTT{}).Init(map[string]any{"model": "/no/such/model.bin"}); err == nil {
		t.Fatal("expected error for missing model file")
	}
}

func whisperModelPath(t *testing.T) string {
	t.Helper()
	p := os.Getenv("WHISPER_TEST_MODEL")
	if p == "" {
		t.Skip("set WHISPER_TEST_MODEL to a ggml model to run whisper_stt integration test")
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("WHISPER_TEST_MODEL not readable: %v", err)
	}
	return p
}

// silenceFrame builds one mono 16 kHz fltp audio frame of nbSamples zeros.
func silenceFrame(t *testing.T, nbSamples int) *av.Frame {
	t.Helper()
	f, err := av.NewAudioFrame(av.SampleFmtFLTP, 1, nbSamples, av.WhisperSampleRate)
	if err != nil {
		t.Fatalf("NewAudioFrame: %v", err)
	}
	return f
}

// TestWhisperSTTEndToEnd feeds two seconds of silence through the processor and
// confirms a transcript file is produced.
func TestWhisperSTTEndToEnd(t *testing.T) {
	model := whisperModelPath(t)
	out := filepath.Join(t.TempDir(), "out.srt")

	p := &WhisperSTT{}
	if err := p.Init(map[string]any{
		"model":         model,
		"language":      "en",
		"output_file":   out,
		"output_format": "srt",
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	ctx := ProcessorContext{MediaType: av.MediaTypeAudio}
	for i := 0; i < 20; i++ { // 20 × 0.1 s = 2 s
		f := silenceFrame(t, av.WhisperSampleRate/10)
		if _, _, err := p.Process(f, ctx); err != nil {
			f.Close()
			t.Fatalf("Process: %v", err)
		}
		f.Close()
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("expected transcript at %s: %v", out, err)
	}
}
