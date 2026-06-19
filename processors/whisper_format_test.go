// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

func TestFormatTimestamp(t *testing.T) {
	cases := []struct {
		d     time.Duration
		comma bool
		want  string
	}{
		{0, true, "00:00:00,000"},
		{ms(1500), true, "00:00:01,500"},
		{ms(1500), false, "00:00:01.500"},
		{61*time.Second + ms(250), false, "00:01:01.250"},
		{2*time.Hour + 3*time.Minute + 4*time.Second + ms(5), true, "02:03:04,005"},
		{-ms(10), true, "00:00:00,000"},
	}
	for _, c := range cases {
		if got := formatTimestamp(c.d, c.comma); got != c.want {
			t.Errorf("formatTimestamp(%v, comma=%v) = %q, want %q", c.d, c.comma, got, c.want)
		}
	}
}

func sampleSegs() []whisperSeg {
	return []whisperSeg{
		{Start: ms(0), End: ms(1500), Text: "Hello world", Confidence: 0.9},
		{Start: ms(1500), End: ms(3200), Text: "  second line  ", Confidence: 0.8},
	}
}

func TestFormatSRT(t *testing.T) {
	got := string(formatSRT(sampleSegs()))
	want := "1\n00:00:00,000 --> 00:00:01,500\nHello world\n\n" +
		"2\n00:00:01,500 --> 00:00:03,200\nsecond line\n\n"
	if got != want {
		t.Errorf("formatSRT mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestFormatVTT(t *testing.T) {
	got := string(formatVTT(sampleSegs()))
	want := "WEBVTT\n\n" +
		"00:00:00.000 --> 00:00:01.500\nHello world\n\n" +
		"00:00:01.500 --> 00:00:03.200\nsecond line\n\n"
	if got != want {
		t.Errorf("formatVTT mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestFormatTXT(t *testing.T) {
	segs := append(sampleSegs(), whisperSeg{Start: ms(3200), End: ms(3300), Text: "   "})
	got := string(formatTXT(segs))
	want := "Hello world\nsecond line\n"
	if got != want {
		t.Errorf("formatTXT = %q, want %q", got, want)
	}
}

func TestFormatJSON(t *testing.T) {
	var out []transcriptSegmentJSON
	if err := json.Unmarshal(formatJSON(sampleSegs()), &out); err != nil {
		t.Fatalf("formatJSON produced invalid JSON: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d segments, want 2", len(out))
	}
	if out[0].Text != "Hello world" || out[1].Text != "second line" {
		t.Errorf("text not trimmed/decoded: %+v", out)
	}
	if out[0].Start != 0 || out[0].End != 1.5 || out[1].Start != 1.5 {
		t.Errorf("timing wrong: %+v", out)
	}
	if out[0].Confidence != 0.9 {
		t.Errorf("confidence = %v, want 0.9", out[0].Confidence)
	}
}

func TestFormattersSkipEmptySegments(t *testing.T) {
	// whisper emits empty/whitespace segments on silence/music; SRT/VTT/JSON
	// must skip them (matching TXT and the event emit) and SRT must renumber.
	segs := []whisperSeg{
		{Start: ms(0), End: ms(1000), Text: "first", Confidence: 0.9},
		{Start: ms(1000), End: ms(2000), Text: "   ", Confidence: 0.1},
		{Start: ms(2000), End: ms(3000), Text: "second", Confidence: 0.8},
	}
	wantSRT := "1\n00:00:00,000 --> 00:00:01,000\nfirst\n\n" +
		"2\n00:00:02,000 --> 00:00:03,000\nsecond\n\n"
	if got := string(formatSRT(segs)); got != wantSRT {
		t.Errorf("formatSRT did not skip empty / renumber:\n got: %q\nwant: %q", got, wantSRT)
	}
	wantVTT := "WEBVTT\n\n" +
		"00:00:00.000 --> 00:00:01.000\nfirst\n\n" +
		"00:00:02.000 --> 00:00:03.000\nsecond\n\n"
	if got := string(formatVTT(segs)); got != wantVTT {
		t.Errorf("formatVTT did not skip empty:\n got: %q\nwant: %q", got, wantVTT)
	}
	var out []transcriptSegmentJSON
	if err := json.Unmarshal(formatJSON(segs), &out); err != nil {
		t.Fatalf("formatJSON: %v", err)
	}
	if len(out) != 2 || out[0].Text != "first" || out[1].Text != "second" {
		t.Errorf("formatJSON did not skip empty segment: %+v", out)
	}
}

func TestSanitizeOutputPathRejectsRelative(t *testing.T) {
	if _, err := sanitizeOutputPath("out.srt"); err == nil {
		t.Fatal("expected error for a relative output path")
	}
	abs := filepath.Join(t.TempDir(), "t.srt")
	if got, err := sanitizeOutputPath(abs); err != nil || got != filepath.Clean(abs) {
		t.Fatalf("sanitizeOutputPath(%q) = (%q, %v), want (%q, nil)", abs, got, err, filepath.Clean(abs))
	}
}

func TestFormatTranscriptInvalid(t *testing.T) {
	if _, err := formatTranscript("docx", sampleSegs()); err == nil {
		t.Fatal("expected error for unknown format")
	}
	if _, err := formatTranscript("", sampleSegs()); err != nil {
		t.Fatalf("empty format should default to srt: %v", err)
	}
}

func TestWriteTranscript(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.srt")
	if err := writeTranscript(path, "srt", sampleSegs()); err != nil {
		t.Fatalf("writeTranscript: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != string(formatSRT(sampleSegs())) {
		t.Errorf("written file does not match formatSRT output")
	}
}

func TestWriteTranscriptRejectsRelative(t *testing.T) {
	if err := writeTranscript("out.srt", "srt", sampleSegs()); err == nil {
		t.Fatal("expected error for relative output path")
	}
}
