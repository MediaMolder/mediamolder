// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
)

// TestSourceResources_StreamForTrack is a fast, file-free regression test for
// the pruned-track routing bug: dropUnconsumedSelections may demux only the
// streams an edge consumes, so an edge referencing a non-zero track must still
// resolve via the stream's stable file-rank (trackOf), not its position within
// the pruned selection.
func TestSourceResources_StreamForTrack(t *testing.T) {
	// Input pruned down to a single kept stream: the file's third stream
	// (abs index 2), which is audio track 1. trackOf preserves the file-rank.
	src := &sourceResources{
		streams: map[int]av.StreamInfo{
			2: {Index: 2, Type: av.MediaTypeAudio},
		},
		trackOf: map[int]int{0: 0, 1: 0, 2: 1}, // video:0, audio:0, audio:1
	}
	if idx, _, ok := src.streamForTrack(av.MediaTypeAudio, 1); !ok || idx != 2 {
		t.Fatalf("streamForTrack(audio,1)=(%d,%v), want (2,true) — pruned non-zero track must resolve", idx, ok)
	}
	if _, _, ok := src.streamForTrack(av.MediaTypeAudio, 0); ok {
		t.Error("streamForTrack(audio,0) resolved a pruned-away track; want not-found")
	}

	// Full selection (nothing pruned): both audio tracks keep their file-rank.
	src.streams[1] = av.StreamInfo{Index: 1, Type: av.MediaTypeAudio}
	if idx, _, ok := src.streamForTrack(av.MediaTypeAudio, 0); !ok || idx != 1 {
		t.Fatalf("streamForTrack(audio,0)=(%d,%v), want (1,true)", idx, ok)
	}
	if idx, _, ok := src.streamForTrack(av.MediaTypeAudio, 1); !ok || idx != 2 {
		t.Fatalf("streamForTrack(audio,1)=(%d,%v), want (2,true)", idx, ok)
	}
}

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

// TestSourceHandler_PrunedNonZeroTrackRouting reproduces the reported bug end to
// end: mapping ONLY a non-zero audio track (in0:a:1) — leaving a:0 declared but
// unconsumed. dropUnconsumedSelections then prunes the demux selection down to
// the single consumed stream; before the fix it was renumbered to track 0, so
// the in0:a:1 edge matched nothing and the output carried no audio. The fix
// resolves edge tracks via the stable file-rank, so the audio still routes.
func TestSourceHandler_PrunedNonZeroTrackRouting(t *testing.T) {
	fixture := filepath.Join("..", "testdata", "two_audio_tracks.mp4")
	if _, err := os.Stat(fixture); err != nil {
		t.Skipf("testdata/two_audio_tracks.mp4 missing: %v", err)
	}
	output := filepath.Join(t.TempDir(), "out_a1.mp4")

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
			"nodes": [],
			"edges": [
				{"from": "in0:a:1", "to": "out0:a", "type": "audio"}
			]
		},
		"outputs": [{
			"id": "out0",
			"url": %q,
			"format": "mp4",
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

	if n := countAudioPackets(t, output); n == 0 {
		t.Fatal("output has no audio packets: edge in0:a:1 routed zero frames (pruned-track regression)")
	}
}

// countAudioPackets opens path and counts demuxed audio packets.
func countAudioPackets(t *testing.T, path string) int {
	t.Helper()
	in, err := av.OpenInput(path, nil)
	if err != nil {
		t.Fatalf("OpenInput(%s): %v", path, err)
	}
	defer in.Close()
	streams, err := in.AllStreams()
	if err != nil {
		t.Fatalf("AllStreams: %v", err)
	}
	isAudio := make(map[int]bool)
	for _, si := range streams {
		if si.Type == av.MediaTypeAudio {
			isAudio[si.Index] = true
		}
	}
	pkt, err := av.AllocPacket()
	if err != nil {
		t.Fatalf("AllocPacket: %v", err)
	}
	defer pkt.Close()
	n := 0
	for in.ReadPacket(pkt) == nil {
		if isAudio[pkt.StreamIndex()] {
			n++
		}
		pkt.Unref()
	}
	return n
}
