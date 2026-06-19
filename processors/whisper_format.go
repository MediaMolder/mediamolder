// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

// Pure-Go formatting and safe file output for whisper_stt transcripts. This
// file carries no build tag so the formatters are compiled and unit-tested
// without libwhisper (mirroring the untagged YOLOv8 helpers in yolov8.go). The
// build-tagged processor in whisper_stt.go maps each av.WhisperSegment to the
// whisperSeg type defined here before formatting.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// whisperSeg is one transcribed span of audio: a half-open time interval, the
// recognised text, and a 0..1 confidence derived from the model's per-token
// probabilities.
type whisperSeg struct {
	Start      time.Duration
	End        time.Duration
	Text       string
	Confidence float64
}

// transcriptSegmentJSON is the per-segment record written by formatJSON. It is
// the same shape as the per-segment event Custom payload emitted on the bus.
type transcriptSegmentJSON struct {
	Start      float64 `json:"start"`
	End        float64 `json:"end"`
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence"`
}

// formatTimestamp renders a duration as HH:MM:SS,mmm (comma=true, SRT) or
// HH:MM:SS.mmm (comma=false, WebVTT). Negative durations clamp to zero.
func formatTimestamp(d time.Duration, comma bool) string {
	if d < 0 {
		d = 0
	}
	totalMS := d.Milliseconds()
	ms := totalMS % 1000
	totalSec := totalMS / 1000
	s := totalSec % 60
	totalMin := totalSec / 60
	m := totalMin % 60
	h := totalMin / 60
	sep := "."
	if comma {
		sep = ","
	}
	return fmt.Sprintf("%02d:%02d:%02d%s%03d", h, m, s, sep, ms)
}

// formatSRT renders segments as SubRip (.srt) cues. Empty/whitespace-only
// segments (whisper emits them on silence/music) are skipped so cue numbering
// stays sequential and matches the TXT/JSON/event outputs.
func formatSRT(segs []whisperSeg) []byte {
	var b strings.Builder
	n := 0
	for _, sg := range segs {
		text := strings.TrimSpace(sg.Text)
		if text == "" {
			continue
		}
		n++
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n",
			n,
			formatTimestamp(sg.Start, true),
			formatTimestamp(sg.End, true),
			text)
	}
	return []byte(b.String())
}

// formatVTT renders segments as WebVTT (.vtt) cues, skipping empty segments.
func formatVTT(segs []whisperSeg) []byte {
	var b strings.Builder
	b.WriteString("WEBVTT\n\n")
	for _, sg := range segs {
		text := strings.TrimSpace(sg.Text)
		if text == "" {
			continue
		}
		fmt.Fprintf(&b, "%s --> %s\n%s\n\n",
			formatTimestamp(sg.Start, false),
			formatTimestamp(sg.End, false),
			text)
	}
	return []byte(b.String())
}

// formatTXT renders the plain transcript, one non-empty segment per line.
func formatTXT(segs []whisperSeg) []byte {
	var b strings.Builder
	for _, sg := range segs {
		t := strings.TrimSpace(sg.Text)
		if t == "" {
			continue
		}
		b.WriteString(t)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// formatJSON renders segments as an indented JSON array of start/end (seconds),
// text, and confidence.
func formatJSON(segs []whisperSeg) []byte {
	out := make([]transcriptSegmentJSON, 0, len(segs))
	for _, sg := range segs {
		text := strings.TrimSpace(sg.Text)
		if text == "" {
			continue
		}
		out = append(out, transcriptSegmentJSON{
			Start:      sg.Start.Seconds(),
			End:        sg.End.Seconds(),
			Text:       text,
			Confidence: sg.Confidence,
		})
	}
	data, _ := json.MarshalIndent(out, "", "  ") //nolint:errcheck // marshalling a plain struct slice cannot fail
	return append(data, '\n')
}

// formatTranscript dispatches on output format (srt | vtt | json | txt; empty
// defaults to srt) and returns the rendered bytes.
func formatTranscript(format string, segs []whisperSeg) ([]byte, error) {
	switch format {
	case "", "srt":
		return formatSRT(segs), nil
	case "vtt":
		return formatVTT(segs), nil
	case "json":
		return formatJSON(segs), nil
	case "txt":
		return formatTXT(segs), nil
	default:
		return nil, fmt.Errorf("whisper_stt: output_format %q is not valid (want srt, vtt, json, txt)", format)
	}
}

// sanitizeOutputPath requires an absolute, traversal-free path and re-derives it
// from the filesystem root, mirroring fileWriteHook's CWE-022 confinement
// pattern so os.WriteFile receives a value not treated as directly tainted.
func sanitizeOutputPath(path string) (string, error) {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("whisper_stt: output_file must be an absolute path, got %q", path)
	}
	// Confine to the path's own filesystem root — the volume on Windows
	// ("C:\\"), "/" on Unix — so filepath.Rel can express the path and a
	// tainted value never reaches os.WriteFile directly (CWE-022). Using "/"
	// unconditionally would reject every absolute Windows path.
	root := filepath.VolumeName(path) + string(filepath.Separator)
	rel, relErr := filepath.Rel(root, path)
	if relErr != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("whisper_stt: output_file %q is not within an accessible filesystem root", path)
	}
	return filepath.Join(root, rel), nil
}

// writeTranscript renders segs in the given format and writes them to path.
func writeTranscript(path, format string, segs []whisperSeg) error {
	data, err := formatTranscript(format, segs)
	if err != nil {
		return err
	}
	safePath, err := sanitizeOutputPath(path)
	if err != nil {
		return err
	}
	return os.WriteFile(safePath, data, 0o644)
}
