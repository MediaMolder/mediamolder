// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build with_whisper

package processors

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
)

// decodeAudioFrames decodes the first audio stream of path into frames the
// caller must Close. Used to drive whisper_stt with REAL decoder output, whose
// channel layout and sample format differ from synthetic NewAudioFrame frames.
func decodeAudioFrames(t *testing.T, path string) []*av.Frame {
	t.Helper()
	in, err := av.OpenInput(path, nil)
	if err != nil {
		t.Fatalf("OpenInput(%s): %v", path, err)
	}
	defer in.Close()
	streams, err := in.AllStreams()
	if err != nil {
		t.Fatalf("AllStreams: %v", err)
	}
	audIdx := -1
	for _, si := range streams {
		if si.Type == av.MediaTypeAudio {
			audIdx = si.Index
			break
		}
	}
	if audIdx < 0 {
		t.Skip("fixture has no audio stream")
	}
	dec, err := av.OpenDecoder(in, audIdx)
	if err != nil {
		t.Fatalf("OpenDecoder: %v", err)
	}
	defer dec.Close()
	pkt, err := av.AllocPacket()
	if err != nil {
		t.Fatalf("AllocPacket: %v", err)
	}
	defer pkt.Close()

	var frames []*av.Frame
	drain := func() {
		for {
			f, err := av.AllocFrame()
			if err != nil {
				return
			}
			if err := dec.ReceiveFrame(f); err != nil {
				f.Close()
				return
			}
			frames = append(frames, f)
		}
	}
	for in.ReadPacket(pkt) == nil {
		if pkt.StreamIndex() != audIdx {
			pkt.Unref()
			continue
		}
		if dec.SendPacket(pkt) == nil {
			drain()
		}
		pkt.Unref()
	}
	return frames
}

// TestWhisperSTTRealAudioNoEncodeError drives whisper_stt with real decoder
// frames (testdata/two_audio_tracks.mp4). It is a regression for two bugs the
// synthetic silence test missed: the resampler rejecting an unspecified channel
// layout (AVERROR_INPUT_CHANGED), and Close passing an already-Done context to
// whisper so its abort callback fired on the first encode ("failed to encode").
// Both surfaced only with real decoder output, not synthetic fltp frames.
func TestWhisperSTTRealAudioNoEncodeError(t *testing.T) {
	model := whisperModelPath(t) // skips when WHISPER_TEST_MODEL is unset
	fixture := filepath.Join("..", "testdata", "two_audio_tracks.mp4")
	if _, err := os.Stat(fixture); err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	frames := decodeAudioFrames(t, fixture)
	if len(frames) == 0 {
		t.Skip("no audio frames decoded")
	}
	defer func() {
		for _, f := range frames {
			f.Close()
		}
	}()

	out := filepath.Join(t.TempDir(), "out.json")
	p := &WhisperSTT{}
	if err := p.Init(map[string]any{
		"model": model, "language": "en", "output_file": out, "output_format": "json",
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	ctx := ProcessorContext{MediaType: av.MediaTypeAudio}
	for _, f := range frames {
		if _, _, err := p.Process(f, ctx); err != nil {
			t.Fatalf("Process: %v (resample/channel-layout regression)", err)
		}
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v (whisper encode regression)", err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("transcript not written: %v", err)
	}
}

// TestWhisperSTTCancelledContextStillTranscribes guards the abort-callback bug:
// Close runs at end-of-stream, when the per-frame run context is already Done.
// It must transcribe regardless (a cancelled per-frame context must not abort
// the final transcription of a normally-completed job).
func TestWhisperSTTCancelledContextStillTranscribes(t *testing.T) {
	model := whisperModelPath(t)
	out := filepath.Join(t.TempDir(), "out.srt")
	p := &WhisperSTT{}
	if err := p.Init(map[string]any{"model": model, "language": "en", "output_file": out}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	ctx := ProcessorContext{MediaType: av.MediaTypeAudio, Context: cancelled}
	for i := 0; i < 10; i++ {
		f := silenceFrame(t, av.WhisperSampleRate/10)
		if _, _, err := p.Process(f, ctx); err != nil {
			f.Close()
			t.Fatalf("Process: %v", err)
		}
		f.Close()
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close errored on a normally-completed job: %v", err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("transcript not written: %v", err)
	}
}
