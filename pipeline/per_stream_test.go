// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidateStreamSpec exercises the up-front validator added in
// pipeline.Config.validate so that a typo in `Output.Streams[*].Type`
// or a negative `Index` is rejected at config-parse time rather than
// surfacing as an obscure runtime error inside handleSink.
func TestValidateStreamSpec(t *testing.T) {
	cases := []struct {
		name string
		spec StreamSpec
		want string
	}{
		{"bad type", StreamSpec{Type: "x", Index: 0}, "invalid type"},
		{"negative index", StreamSpec{Type: "a", Index: -1}, "invalid index"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{
				SchemaVersion: "1.1",
				Inputs:        []Input{{ID: "in0", URL: "x"}},
				Outputs: []Output{{
					ID: "out0", URL: "x", Format: "matroska",
					Streams: []StreamSpec{tc.spec},
				}},
			}
			err := validate(cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validate err = %v, want substring %q", err, tc.want)
			}
		})
	}
}

// TestApplyPerStreamMetadataAndDisposition drives a stream-copy
// pipeline that tags the audio stream with `language=eng` and the
// video stream with `disposition=default+forced`, then ffprobes the
// result to confirm both surfaced. Mirrors the canonical
// dual-language / forced-narrative use cases that motivated this
// feature.
func TestApplyPerStreamMetadataAndDisposition(t *testing.T) {
	inputURL := filepath.Join("..", "testdata", "BBB_10sec.mp4")
	if _, err := os.Stat(inputURL); err != nil {
		t.Skip("testdata/BBB_10sec.mp4 missing")
	}
	ffprobeBin, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not in PATH")
	}

	outDir := t.TempDir()
	output := filepath.Join(outDir, "per_stream.mkv")

	rawCfg := fmt.Sprintf(`{
		"schema_version": "1.1",
		"inputs": [{
			"id": "in0",
			"url": %q,
			"streams": [
				{"input_index": 0, "type": "video", "track": 0},
				{"input_index": 0, "type": "audio", "track": 0}
			]
		}],
		"graph": {
			"nodes": [],
			"edges": [
				{"from": "in0:v:0", "to": "out0:v", "type": "video"},
				{"from": "in0:a:0", "to": "out0:a", "type": "audio"}
			]
		},
		"outputs": [{
			"id": "out0",
			"url": %q,
			"format": "matroska",
			"codec_video": "copy",
			"codec_audio": "copy",
			"streams": [
				{"type": "a", "index": 0, "metadata": {"language": "eng", "title": "Main"}},
				{"type": "v", "index": 0, "disposition": "default+forced"}
			]
		}]
	}`, inputURL, output)

	cfg, err := ParseConfig([]byte(rawCfg))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	eng, err := NewPipeline(cfg)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	if err := eng.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	cmd := exec.Command(ffprobeBin,
		"-v", "error",
		"-show_streams",
		"-of", "json",
		output)
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("ffprobe: %v (stderr: %s)", err, exitStderr(err))
	}

	var probe struct {
		Streams []struct {
			CodecType   string            `json:"codec_type"`
			Tags        map[string]string `json:"tags"`
			Disposition map[string]int    `json:"disposition"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("ffprobe parse: %v", err)
	}

	var video, audio int = -1, -1
	for i, s := range probe.Streams {
		switch s.CodecType {
		case "video":
			if video < 0 {
				video = i
			}
		case "audio":
			if audio < 0 {
				audio = i
			}
		}
	}
	if video < 0 || audio < 0 {
		t.Fatalf("expected both video+audio streams, got %s", raw)
	}

	// Audio stream should carry language=eng + title=Main. Matroska
	// stores tags case-sensitively (lower for language, original
	// case for free-form keys); accept either case.
	at := probe.Streams[audio].Tags
	gotLang := firstNonEmpty(at["language"], at["LANGUAGE"])
	if gotLang != "eng" {
		t.Errorf("audio.tags.language = %q, want %q (tags=%v)", gotLang, "eng", at)
	}
	gotTitle := firstNonEmpty(at["title"], at["TITLE"])
	if gotTitle != "Main" {
		t.Errorf("audio.tags.title = %q, want %q", gotTitle, "Main")
	}

	// Video stream disposition should have default=1 and forced=1.
	vd := probe.Streams[video].Disposition
	if vd["default"] != 1 {
		t.Errorf("video.disposition.default = %d, want 1 (full=%v)", vd["default"], vd)
	}
	if vd["forced"] != 1 {
		t.Errorf("video.disposition.forced = %d, want 1 (full=%v)", vd["forced"], vd)
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
