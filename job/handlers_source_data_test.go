// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestSourceHandler_DataStreamNoDecoder is a regression test for the bug
// where a selected AVMEDIA_TYPE_DATA stream (e.g. a timecode track in a MOV
// file) with codec_id=0 (AV_CODEC_ID_NONE) caused the source handler to fail
// with "no decoder found for codec_id 0".
//
// Data and unknown streams are not decodable; they must be skipped in the
// decoder-open switch, mirroring how attachment streams are handled.
//
// testdata/with_data.mov: H.264 video (stream 0) + tmcd data track (stream 1).
func TestSourceHandler_DataStreamNoDecoder(t *testing.T) {
	fixture := filepath.Join("..", "testdata", "with_data.mov")
	if _, err := os.Stat(fixture); err != nil {
		t.Skipf("testdata/with_data.mov missing: %v", err)
	}

	output := filepath.Join(t.TempDir(), "out.mp4")

	// Stream 1 is a data/timecode track. Including it in cfg.Streams but
	// wiring only the video edge triggers the bug path: the data stream is
	// selected but has no consuming graph edge (copyOnly["data"] == false)
	// so it falls through to the decoder-open switch default case.
	rawCfg := fmt.Sprintf(`{
		"schema_version": "1.1",
		"inputs": [{
			"id": "in0",
			"url": %q,
			"streams": [
				{"input_index": 0, "type": "video", "track": 0},
				{"input_index": 0, "type": "data",  "track": 0}
			]
		}],
		"graph": {
			"nodes": [],
			"edges": [
				{"from": "in0:v:0", "to": "out0:v", "type": "video"}
			]
		},
		"outputs": [{
			"id": "out0",
			"url": %q,
			"format": "mp4",
			"codec_video": "libx264",
			"encoder_params_video": {"preset": "ultrafast", "crf": "35"}
		}]
	}`, fixture, output)

	cfg, err := ParseConfig([]byte(rawCfg))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	eng, err := NewPipeline(cfg)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	if err := eng.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v (regression: data stream opened decoder)", err)
	}

	if st, err := os.Stat(output); err != nil || st.Size() == 0 {
		t.Fatalf("output missing or empty")
	}
}
