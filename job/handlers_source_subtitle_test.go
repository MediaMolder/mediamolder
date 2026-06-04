// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// TestSourceHandler_SubtitleStreamNoDecoder is a regression test for the bug
// where a selected subtitle stream with codec_id=0 (AV_CODEC_ID_NONE) caused
// the source handler to fail hard with "no subtitle decoder for codec_id 0".
//
// Broadcast XDCAM MOV files often embed a CEA-708 closed-caption track (codec
// tag "c708") that is classified by libavformat as AVMEDIA_TYPE_SUBTITLE with
// codec_id=AV_CODEC_ID_NONE because the tag has no registered FFmpeg decoder.
// When such a track is explicitly selected in a job (or added by the GUI),
// the pipeline should NOT fatal — it must skip decoder open and treat the
// stream as stream-copy only, mirroring FFmpeg's behaviour.
//
// The test fixture is a large production file that cannot be committed to the
// repository. The test is automatically skipped in CI where the file is absent.
func TestSourceHandler_SubtitleStreamNoDecoder(t *testing.T) {
	const fixture = "/Volumes/SSD/sources/CASINOROYALE_U9M22_TLVOD-0000041759_OAR_ENG_51_ENG_20.mov"
	if _, err := os.Stat(fixture); err != nil {
		t.Skipf("large fixture not present (CI skip): %v", err)
	}

	// Explicitly select stream 0 (video/mpeg2video) AND stream 1
	// (subtitle/c708, codec_id=0).  Before the codec_id guard the latter
	// would immediately cause:
	//   "open subtitle decoder for stream 1: no subtitle decoder for codec_id 0"
	// Process just 1 s so the test completes quickly.
	rawCfg := fmt.Sprintf(`{
		"schema_version": "1.2",
		"inputs": [{
			"id": "in",
			"url": %q,
			"streams": [
				{"input_index": 0, "type": "video",    "track": 0},
				{"input_index": 0, "type": "subtitle", "track": 0}
			],
			"options": {"t": "1"}
		}],
		"graph": {
			"nodes": [
				{
					"id": "sc",
					"type": "go_processor",
					"processor": "scene_change_content",
					"params": {}
				}
			],
			"edges": [
				{"from": "in:v:0", "to": "sc", "type": "video"}
			]
		},
		"outputs": []
	}`, fixture)

	cfg, err := ParseConfig([]byte(rawCfg))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	eng, err := NewPipeline(cfg)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	runErr := eng.Run(ctx)
	if runErr != nil && strings.Contains(runErr.Error(), "no subtitle decoder for codec_id") {
		t.Fatalf("regression: subtitle stream with codec_id=0 must not cause fatal error: %v", runErr)
	}
	// context.DeadlineExceeded / context.Canceled means we processed
	// successfully and were just stopped by the test timeout — that is fine.
}
