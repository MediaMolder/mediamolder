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

// Wave 7 #36d — KindFilterSink runtime handler end-to-end.
//
// A pure source-to-sink pipeline: testsrc2 → nullsink. Confirms that
// createFilterSink builds a SinkFilterGraphConfig from the inbound
// edges, that handleFilterSink pumps frames into the buffer source
// until EOF, and that the engine accepts a config with zero Outputs
// when at least one filter_sink node is present.
func TestFilterSinkRuntime_TestsrcNullsink(t *testing.T) {
	rawCfg := `{
		"schema_version": "1.1",
		"inputs": [],
		"graph": {
			"nodes": [
				{"id":"src","type":"filter_source","filter":"testsrc2",
				 "params":{"size":"320x240","rate":"24","duration":"0.5"}},
				{"id":"drain","type":"filter_sink","filter":"nullsink"}
			],
			"edges": [
				{"from":"src","to":"drain","type":"video"}
			]
		},
		"outputs": []
	}`

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
}

// Audio variant: sine → anullsink. Exercises the audio buffersrc
// configuration path through createFilterSink.
func TestFilterSinkRuntime_SineAnullsink(t *testing.T) {
	rawCfg := `{
		"schema_version": "1.1",
		"inputs": [],
		"graph": {
			"nodes": [
				{"id":"src","type":"filter_source","filter":"sine",
				 "params":{"frequency":"440","sample_rate":"48000","duration":"0.5"}},
				{"id":"drain","type":"filter_sink","filter":"anullsink"}
			],
			"edges": [
				{"from":"src","to":"drain","type":"audio"}
			]
		},
		"outputs": []
	}`

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
}

// Mixed pipeline: testsrc2 → filter_sink (side-effect chain) AND
// testsrc2 → encoder → file. Confirms that filter_sink coexists with
// real outputs and that the engine still requires both branches to
// drain to completion.
func TestFilterSinkRuntime_AlongsideEncoder(t *testing.T) {
	output := filepath.Join(t.TempDir(), "side.mp4")
	rawCfg := fmt.Sprintf(`{
		"schema_version": "1.1",
		"inputs": [],
		"graph": {
			"nodes": [
				{"id":"src","type":"filter_source","filter":"testsrc2",
				 "params":{"size":"320x240","rate":"24","duration":"0.5"}},
				{"id":"sp","type":"filter","filter":"split","params":{"outputs":"2"}},
				{"id":"drain","type":"filter_sink","filter":"nullsink"}
			],
			"edges": [
				{"from":"src","to":"sp","type":"video"},
				{"from":"sp:0","to":"drain","type":"video"},
				{"from":"sp:1","to":"out0:v","type":"video"}
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
