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

// TestSourceHandler_MultiTrackAudioRouting is a regression test for the bug
// where handleSource grouped all outbound audio edges into a single type-keyed
// bucket. When two edges from the same source requested different audio tracks
// (e.g. in0:a:0 and in0:a:1), the handler sent every decoded audio frame —
// always the same *av.Frame pointer — to both output channels. The first
// consumer freed the frame; the second read freed memory, silently dropping
// all audio from the output.
//
// The fix builds a per-stream-index routing map so each edge only receives
// frames from the specific source stream it requested.
//
// testdata/two_audio_tracks.mp4: H.264 video + two mono AAC audio streams.
// The job merges the two tracks with amerge and encodes to AAC.
func TestSourceHandler_MultiTrackAudioRouting(t *testing.T) {
	fixture := filepath.Join("..", "testdata", "two_audio_tracks.mp4")
	if _, err := os.Stat(fixture); err != nil {
		t.Skipf("testdata/two_audio_tracks.mp4 missing: %v", err)
	}

	output := filepath.Join(t.TempDir(), "out.mp4")

	rawCfg := fmt.Sprintf(`{
		"schema_version": "1.1",
		"inputs": [{
			"id": "in0",
			"url": %q,
			"streams": [
				{"input_index": 0, "type": "video", "track": 0},
				{"input_index": 0, "type": "audio", "track": 0},
				{"input_index": 0, "type": "audio", "track": 1}
			]
		}],
		"graph": {
			"nodes": [
				{"id": "amerge0", "type": "filter", "filter": "amerge", "params": {"inputs": "2"}}
			],
			"edges": [
				{"from": "in0:a:0", "to": "amerge0", "type": "audio"},
				{"from": "in0:a:1", "to": "amerge0", "type": "audio"},
				{"from": "amerge0",  "to": "out0:a", "type": "audio"}
			]
		},
		"outputs": [{
			"id": "out0",
			"url": %q,
			"format": "mp4",
			"codec_video": "copy",
			"codec_audio": "aac"
		}]
	}`, fixture, output)

	cfg, err := ParseConfig([]byte(rawCfg))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	pipe, err := NewPipeline(cfg)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	if err := pipe.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	info, err := os.Stat(output)
	if err != nil {
		t.Fatalf("output file missing: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("output file is empty")
	}
}
