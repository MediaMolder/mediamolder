// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Wave 7 #36c — KindFilterSource runtime handler end-to-end.
//
// A pure source-only pipeline: testsrc2 → libx264 → mp4 file. Confirms
// that createFilterSource builds a SourceFilterGraphConfig from the
// node, that handleFilterSource pumps frames into the encoder, and
// that the resulting file is non-empty.
func TestFilterSourceRuntime_Testsrc2(t *testing.T) {
	output := filepath.Join(t.TempDir(), "src.mp4")
	rawCfg := fmt.Sprintf(`{
		"schema_version": "1.1",
		"inputs": [],
		"graph": {
			"nodes": [
				{"id":"src","type":"filter_source","filter":"testsrc2",
				 "params":{"size":"320x240","rate":"24","duration":"0.5"}}
			],
			"edges": [
				{"from":"src","to":"out0:v","type":"video"}
			]
		},
		"outputs": [{
			"id":"out0","url":%q,"format":"mp4",
			"codec_video":"libx264",
			"encoder_params_video":{"preset":"ultrafast","crf":"30"}
		}]
	}`, output)

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

	st, err := os.Stat(output)
	if err != nil {
		t.Fatalf("output stat: %v", err)
	}
	if st.Size() == 0 {
		t.Fatalf("output file is empty")
	}
}

// A solid-colour source emits a finite stream when `d=` is set; verifies
// the same path with a different source filter.
func TestFilterSourceRuntime_Color(t *testing.T) {
	output := filepath.Join(t.TempDir(), "color.mp4")
	rawCfg := fmt.Sprintf(`{
		"schema_version": "1.1",
		"inputs": [],
		"graph": {
			"nodes": [
				{"id":"src","type":"filter_source","filter":"color",
				 "params":{"c":"red","s":"320x240","r":"24","d":"0.5"}}
			],
			"edges": [
				{"from":"src","to":"out0:v","type":"video"}
			]
		},
		"outputs": [{
			"id":"out0","url":%q,"format":"mp4",
			"codec_video":"libx264",
			"encoder_params_video":{"preset":"ultrafast","crf":"30"}
		}]
	}`, output)

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

	st, err := os.Stat(output)
	if err != nil {
		t.Fatalf("output stat: %v", err)
	}
	if st.Size() == 0 {
		t.Fatalf("output file is empty")
	}
}

// Audio source via sine: sine → aac → mp4. Confirms the audio code
// path through createFilterSource resolves StreamInfo from the
// buffersink (sample_rate/channels/sample_fmt) for the encoder.
func TestFilterSourceRuntime_SineAudio(t *testing.T) {
	output := filepath.Join(t.TempDir(), "sine.mp4")
	rawCfg := fmt.Sprintf(`{
		"schema_version": "1.1",
		"inputs": [],
		"graph": {
			"nodes": [
				{"id":"src","type":"filter_source","filter":"sine",
				 "params":{"frequency":"440","sample_rate":"48000","duration":"0.5"}}
			],
			"edges": [
				{"from":"src","to":"out0:a","type":"audio"}
			]
		},
		"outputs": [{
			"id":"out0","url":%q,"format":"mp4",
			"codec_audio":"aac"
		}]
	}`, output)

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

	st, err := os.Stat(output)
	if err != nil {
		t.Fatalf("output stat: %v", err)
	}
	if st.Size() == 0 {
		t.Fatalf("output file is empty")
	}
}
