// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestApplyDARShorthand transcodes a clip to a 720x576 raster and
// stamps DAR=4:3 on the output. ffprobes the result to confirm the
// muxer wrote SAR=16:15 (the DV-PAL anamorphic shape) onto the
// video stream's codecpar.
func TestApplyDARShorthand(t *testing.T) {
	inputURL := bbbSourcePath(t)
	ffprobeBin, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not in PATH")
	}

	output := filepath.Join(t.TempDir(), "dar.mp4")
	rawCfg := fmt.Sprintf(`{
		"schema_version": "1.1",
		"inputs": [{
			"id": "in0",
			"url": %q,
			"streams": [{"input_index":0,"type":"video","track":0}]
		}],
		"graph": {
			"nodes": [{"id":"f0","type":"filter","filter":"scale","params":{"w":"720","h":"576"}}],
			"edges": [
				{"from":"in0:v:0","to":"f0","type":"video"},
				{"from":"f0","to":"out0:v","type":"video"}
			]
		},
		"outputs": [{
			"id":"out0","url":%q,"format":"mp4",
			"codec_video":"libx264",
			"encoder_params_video":{"preset":"ultrafast","crf":"30"},
			"dar":"4:3"
		}]
	}`, inputURL, output)

	cfg, err := ParseConfig([]byte(rawCfg))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	injectBBBSeek(cfg)
	eng, err := NewPipeline(cfg)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	if err := eng.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	cmd := exec.Command(ffprobeBin, "-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=width,height,sample_aspect_ratio,display_aspect_ratio",
		"-of", "json", output)
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("ffprobe: %v (stderr: %s)", err, exitStderr(err))
	}
	var probe struct {
		Streams []struct {
			Width  int    `json:"width"`
			Height int    `json:"height"`
			SAR    string `json:"sample_aspect_ratio"`
			DAR    string `json:"display_aspect_ratio"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("ffprobe parse: %v: %s", err, raw)
	}
	if len(probe.Streams) == 0 {
		t.Fatalf("no streams: %s", raw)
	}
	s := probe.Streams[0]
	if s.Width != 720 || s.Height != 576 {
		t.Errorf("dims: got %dx%d want 720x576", s.Width, s.Height)
	}
	if s.SAR != "16:15" {
		t.Errorf("SAR: got %q want 16:15", s.SAR)
	}
	if s.DAR != "4:3" {
		t.Errorf("DAR: got %q want 4:3", s.DAR)
	}
}

// TestApplySARShorthand sets SAR directly (NTSC 8:9) and confirms
// the muxer wrote it through unchanged.
func TestApplySARShorthand(t *testing.T) {
	inputURL := bbbSourcePath(t)
	ffprobeBin, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not in PATH")
	}

	output := filepath.Join(t.TempDir(), "sar.mp4")
	rawCfg := fmt.Sprintf(`{
		"schema_version": "1.1",
		"inputs": [{
			"id": "in0",
			"url": %q,
			"streams": [{"input_index":0,"type":"video","track":0}]
		}],
		"graph": {
			"nodes": [{"id":"f0","type":"filter","filter":"scale","params":{"w":"720","h":"480"}}],
			"edges": [
				{"from":"in0:v:0","to":"f0","type":"video"},
				{"from":"f0","to":"out0:v","type":"video"}
			]
		},
		"outputs": [{
			"id":"out0","url":%q,"format":"mp4",
			"codec_video":"libx264",
			"encoder_params_video":{"preset":"ultrafast","crf":"30"},
			"sar":"8:9"
		}]
	}`, inputURL, output)

	cfg, err := ParseConfig([]byte(rawCfg))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	injectBBBSeek(cfg)
	eng, err := NewPipeline(cfg)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	if err := eng.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	cmd := exec.Command(ffprobeBin, "-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=sample_aspect_ratio",
		"-of", "json", output)
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("ffprobe: %v (stderr: %s)", err, exitStderr(err))
	}
	var probe struct {
		Streams []struct {
			SAR string `json:"sample_aspect_ratio"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("ffprobe parse: %v: %s", err, raw)
	}
	if len(probe.Streams) == 0 || probe.Streams[0].SAR != "8:9" {
		t.Errorf("SAR: got %v want 8:9", probe.Streams)
	}
}
