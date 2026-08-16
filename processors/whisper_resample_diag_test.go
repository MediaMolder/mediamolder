// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build with_whisper

package processors

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
)

// TestWhisperSTTResampleDiag is a DIAGNOSTIC, not an assertion: it runs a real file's
// audio through whisper_stt's own accumulate path and reports what the model would
// actually have been given — how many times the resampler was rebuilt mid-stream, what
// errors caused that, and the resulting sample count against the stream's true duration.
// With MM_DIAG_PCM_OUT set it writes the buffered mono 16 kHz PCM as a WAV, so it can be
// diffed against `ffmpeg -i <in> -vn -ac 1 -ar 16000 -c:a pcm_s16le out.wav`.
//
// Motivation: a long tape-capture AVI (PCM s16 stereo @32 kHz) produced transcripts that
// collapsed into one phrase repeated 1,236 times, while the SAME decoder+model given a
// pre-extracted 16 kHz mono WAV of the same file did not. That pointed at the conversion in
// between, and this measures it instead of guessing — which is the point of keeping it.
//
// What it FOUND was a real √2 downmix gain (fixed). What it did NOT find was the cause of the
// loops: they survived that fix. The decoder turns out to be chaotically sensitive on marginal
// audio — a ≤1 LSB difference flips the outcome — so this harness is for verifying the audio is
// what we think it is, NOT for attributing transcript quality to it.
//
//	MM_DIAG_AUDIO=/path/tape.avi MM_DIAG_PCM_OUT=/tmp/mm.wav \
//	  go test -tags with_whisper ./processors/ -run ResampleDiag -v
func TestWhisperSTTResampleDiag(t *testing.T) {
	src := os.Getenv("MM_DIAG_AUDIO")
	if src == "" {
		t.Skip("set MM_DIAG_AUDIO to a media file to run the resampler diagnostic")
	}
	frames := decodeAudioFrames(t, src)
	if len(frames) == 0 {
		t.Skip("no audio frames decoded")
	}
	defer func() {
		for _, f := range frames {
			f.Close()
		}
	}()

	// Report what the decoder actually produced, so a format change mid-stream is
	// visible as a fact rather than assumed away.
	type shape struct {
		fmt      int
		channels int
		rate     int
	}
	shapes := map[shape]int{}
	for _, f := range frames {
		shapes[shape{f.SampleFmt(), f.Channels(), f.SampleRate()}]++
	}
	t.Logf("decoded %d frames in %d distinct format(s):", len(frames), len(shapes))
	for s, n := range shapes {
		t.Logf("   sample_fmt=%d channels=%d rate=%d  ×%d frames", s.fmt, s.channels, s.rate, n)
	}

	// The accumulate path only — no model, no transcription. Init would demand a model
	// file, so build the processor's audio half directly.
	p := &WhisperSTT{}
	for i, f := range frames {
		if err := p.accumulate(f); err != nil {
			t.Fatalf("accumulate frame %d: %v", i, err)
		}
	}
	p.drainResampler()

	rebuilds, errs := p.ResamplerRebuilds()
	t.Logf("resampler rebuilds: %d", rebuilds)
	for i, e := range errs {
		t.Logf("   rebuild error %d: %v", i, e)
	}

	secs := float64(len(p.samples)) / float64(av.WhisperSampleRate)
	t.Logf("buffered %d samples = %.2fs of 16 kHz mono", len(p.samples), secs)

	// Silence and clipping are cheap tells: a botched channel/format conversion usually
	// shows up as one or the other long before it shows up as a bad transcript.
	var peak, sumSq float64
	silent := 0
	for _, s := range p.samples {
		a := math.Abs(float64(s))
		if a > peak {
			peak = a
		}
		if a < 1e-5 {
			silent++
		}
		sumSq += float64(s) * float64(s)
	}
	rms := math.Sqrt(sumSq / math.Max(1, float64(len(p.samples))))
	t.Logf("peak=%.4f rms=%.4f near-silent samples=%.1f%%", peak, rms,
		100*float64(silent)/math.Max(1, float64(len(p.samples))))

	if out := os.Getenv("MM_DIAG_PCM_OUT"); out != "" {
		if err := writeWav16(out, p.samples, av.WhisperSampleRate); err != nil {
			t.Fatalf("write wav: %v", err)
		}
		t.Logf("wrote %s — diff against: ffmpeg -i %q -vn -ac 1 -ar 16000 -c:a pcm_s16le ref.wav", out, src)
	}
}

// writeWav16 writes mono float32 samples as a 16-bit PCM WAV so the buffer can be played
// and diffed with ordinary audio tools.
func writeWav16(path string, samples []float32, rate int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	dataLen := 2 * len(samples)
	hdr := []any{
		[4]byte{'R', 'I', 'F', 'F'}, uint32(36 + dataLen), [4]byte{'W', 'A', 'V', 'E'},
		[4]byte{'f', 'm', 't', ' '}, uint32(16), uint16(1), uint16(1),
		uint32(rate), uint32(rate * 2), uint16(2), uint16(16),
		[4]byte{'d', 'a', 't', 'a'}, uint32(dataLen),
	}
	for _, v := range hdr {
		if err := binary.Write(f, binary.LittleEndian, v); err != nil {
			return err
		}
	}
	buf := make([]byte, dataLen)
	for i, s := range samples {
		v := int16(math.Max(-32768, math.Min(32767, float64(s)*32767)))
		binary.LittleEndian.PutUint16(buf[2*i:], uint16(v))
	}
	_, err = f.Write(buf)
	return err
}
