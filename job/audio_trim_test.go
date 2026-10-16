// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/graph"
)

// decodeAudioSamples opens path, decodes its first audio stream, and returns
// (sampleRate, totalDecodedSamples). The decoder applies container priming /
// edit-list trimming, so the returned count is the effective playable length.
func decodeAudioSamples(t *testing.T, path string) (int, int64) {
	t.Helper()
	in, err := av.OpenInput(path, nil)
	if err != nil {
		t.Fatalf("OpenInput(%s): %v", path, err)
	}
	defer in.Close()

	aIdx, sampleRate := -1, 0
	for i := 0; i < in.NumStreams(); i++ {
		si, err := in.StreamInfo(i)
		if err != nil {
			continue
		}
		if si.Type == av.MediaTypeAudio {
			aIdx, sampleRate = si.Index, si.SampleRate
			break
		}
	}
	if aIdx < 0 {
		t.Fatalf("%s: no audio stream", path)
	}
	dec, err := av.OpenDecoder(in, aIdx)
	if err != nil {
		t.Fatalf("OpenDecoder: %v", err)
	}
	defer dec.Close()

	var total int64
	drain := func() {
		for {
			f, err := av.AllocFrame()
			if err != nil {
				t.Fatalf("AllocFrame: %v", err)
			}
			if err := dec.ReceiveFrame(f); err != nil {
				f.Close()
				return
			}
			total += int64(f.NbSamples())
			f.Close()
		}
	}
	for {
		pkt, err := av.AllocPacket()
		if err != nil {
			t.Fatalf("AllocPacket: %v", err)
		}
		if err := in.ReadPacket(pkt); err != nil {
			pkt.Close()
			if av.IsEOF(err) {
				break
			}
			t.Fatalf("ReadPacket: %v", err)
		}
		if pkt.StreamIndex() == aIdx {
			if err := dec.SendPacket(pkt); err == nil {
				drain()
			}
		}
		pkt.Close()
	}
	_ = dec.Flush()
	drain()
	return sampleRate, total
}

// TestAudioTrimSpliceStructural asserts spliceAudioTrimForOutputs inserts an
// __atrim__ filter with the right window in front of a re-encoded audio stream,
// and that the sink detects the channel as internally trimmed.
func TestAudioTrimSpliceStructural(t *testing.T) {
	cfgJSON := `{
      "schema_version": "1.0",
      "inputs": [{"id": "in0", "url": "in.mp4", "streams": [
        {"input_index": 0, "type": "audio", "track": 0}
      ]}],
      "graph": {"nodes": [], "edges": [
        {"from": "in0:a:0", "to": "out0:a", "type": "audio"}
      ]},
      "outputs": [{
        "id": "out0", "url": "out.mp4",
        "codec_audio": "aac",
        "options": {"ss": "2.5", "to": "8.0"}
      }]
    }`
	cfg, err := ParseConfig([]byte(cfgJSON))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	def := configToGraphDef(cfg)

	var atrim *string
	for _, n := range def.Nodes {
		if strings.HasPrefix(n.ID, "__atrim__") {
			f := n.Filter
			atrim = &f
			break
		}
	}
	if atrim == nil {
		t.Fatalf("no __atrim__ node inserted; nodes=%v", def.Nodes)
	}
	for _, want := range []string{"atrim=", "start=2.500000", "end=8.000000"} {
		if !strings.Contains(*atrim, want) {
			t.Errorf("atrim spec %q missing %q", *atrim, want)
		}
	}

	// The graph must build and the atrim must sit on the audio path to the
	// aac encoder (no dangling / cyclic edges).
	if _, err := graph.Build(def); err != nil {
		t.Fatalf("graph build: %v", err)
	}
}

// TestAudioTrimSampleAccurate runs a re-encoded audio trim and asserts the
// decoded output length matches the requested window to within one AAC frame,
// proving the atrim insertion trims at sample (not packet) granularity.
func TestAudioTrimSampleAccurate(t *testing.T) {
	src := filepath.Join("..", "testdata", "BBB_1080p.mp4")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("testdata/BBB_1080p.mp4 missing; run: bash scripts/fetch-bbb.sh")
	}

	// Deliberately fractional (mid-packet) cut points, early in the file so
	// the decode is fast and the window is reached.
	const startSec, endSec = 1.345678, 5.876543
	tmp := t.TempDir()
	outPath := filepath.ToSlash(filepath.Join(tmp, "clip.m4a"))
	cfgJSON := fmt.Sprintf(`{
      "schema_version": "1.0",
      "inputs": [{"id": "in0", "url": %q, "streams": [
        {"input_index": 0, "type": "audio", "track": 0}
      ]}],
      "graph": {"nodes": [], "edges": [
        {"from": "in0:a:0", "to": "out0:a", "type": "audio"}
      ]},
      "outputs": [{
        "id": "out0", "url": %q,
        "codec_audio": "aac",
        "options": {"ss": "%.6f", "to": "%.6f"}
      }]
    }`, filepath.ToSlash(src), outPath, startSec, endSec)

	cfg, err := ParseConfig([]byte(cfgJSON))
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

	if fi, err := os.Stat(outPath); err != nil || fi.Size() == 0 {
		t.Fatalf("output missing/empty: err=%v", err)
	} else {
		t.Logf("output size %d bytes", fi.Size())
	}
	sampleRate, total := decodeAudioSamples(t, outPath)
	if sampleRate <= 0 {
		t.Fatalf("no sample rate")
	}
	want := int64(math.Round((endSec - startSec) * float64(sampleRate)))
	diff := total - want
	if diff < 0 {
		diff = -diff
	}
	// One AAC frame = 1024 samples (~21 ms). Sample-accurate trimming lands
	// within a frame; packet-granular sink trimming would drift up to two
	// frames and would not be centred on the exact window.
	if diff > 1024 {
		t.Fatalf("audio length off by %d samples (got %d, want %d @ %d Hz) — not sample-accurate",
			diff, total, want, sampleRate)
	}
	t.Logf("audio trim: %d samples decoded, want %d (%.1f ms off) @ %d Hz",
		total, want, float64(diff)*1000/float64(sampleRate), sampleRate)
}

// probeAudioPackets returns every audio packet's PTS plus the stream's
// time_base, start time and duration (both in time_base units).
func probeAudioPackets(t *testing.T, path string) (pts []int64, tb [2]int, start, duration int64) {
	t.Helper()
	in, err := av.OpenInput(path, nil)
	if err != nil {
		t.Fatalf("OpenInput(%s): %v", path, err)
	}
	defer in.Close()
	aIdx := -1
	for i := 0; i < in.NumStreams(); i++ {
		if si, err := in.StreamInfo(i); err == nil && si.Type == av.MediaTypeAudio {
			aIdx, tb, start, duration = si.Index, si.TimeBase, si.StartTime, si.Duration
			break
		}
	}
	if aIdx < 0 {
		t.Fatalf("%s: no audio stream", path)
	}
	for {
		pkt, err := av.AllocPacket()
		if err != nil {
			t.Fatal(err)
		}
		if err := in.ReadPacket(pkt); err != nil {
			pkt.Close()
			if av.IsEOF(err) {
				break
			}
			t.Fatalf("ReadPacket: %v", err)
		}
		if pkt.StreamIndex() == aIdx {
			pts = append(pts, pkt.PTS())
		}
		pkt.Close()
	}
	return pts, tb, start, duration
}

func mustRunJob(t *testing.T, cfgJSON string) {
	t.Helper()
	if _, err := runJob(t, cfgJSON); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// TestAudioTrimAACKeepsHeadOfWindow is the regression test for a re-encoded
// AAC trim whose start is not on a sample boundary. The sink's packet-level
// drop used to discard the encoder's priming packet (-1024 samples) and,
// for an unaligned start, the packet holding the window's first samples, so
// the output began one AAC frame late with a 21 ms empty edit. The atrim is
// now followed by asetpts and the sink leaves that channel alone, so the
// output matches ffmpeg's: first packet at -1024, start_time 0, duration
// equal to the window.
func TestAudioTrimAACKeepsHeadOfWindow(t *testing.T) {
	if !av.FindEncoder("aac") {
		t.Skip("aac encoder not available")
	}
	dir := t.TempDir()
	src := filepath.ToSlash(filepath.Join(dir, "src.m4a"))
	mustRunJob(t, fmt.Sprintf(`{
		"schema_version": "1.1",
		"inputs": [],
		"graph": {"nodes": [
			{"id":"tone","type":"filter_source","filter":"sine",
			 "params":{"frequency":"440","sample_rate":"48000","duration":"2.5"}}],
			"edges": [{"from":"tone","to":"out:a","type":"audio"}]},
		"outputs": [{"id":"out","url":%q,"format":"mp4","codec_audio":"aac"}]
	}`, src))

	const ss, to = 0.567233, 1.968633 // frame 17 and frame 59 of a 29.97 fps clip, floored to the microsecond
	out := filepath.ToSlash(filepath.Join(dir, "cut.m4a"))
	mustRunJob(t, fmt.Sprintf(`{
		"schema_version": "1.0",
		"inputs": [{"id":"in0","url":%q,"streams":[{"input_index":0,"type":"audio","track":0}]}],
		"graph": {"nodes": [], "edges": [{"from":"in0:a:0","to":"out0:a","type":"audio"}]},
		"outputs": [{"id":"out0","url":%q,"format":"mp4","codec_audio":"aac","options":{"ss":"%.6f","to":"%.6f"}}]
	}`, src, out, ss, to))

	pts, tb, start, duration := probeAudioPackets(t, out)
	if tb != [2]int{1, 48000} {
		t.Fatalf("audio time_base %v, want 1/48000", tb)
	}
	if len(pts) < 2 || pts[0] != -1024 || pts[1] != 0 {
		t.Errorf("first packets %v, want [-1024 0 ...] (encoder priming packet kept, stream rebased to zero)", pts[:min(len(pts), 3)])
	}
	if start != 0 {
		t.Errorf("audio start_time %d ticks, want 0", start)
	}
	want := int64(math.Round((to - ss) * 48000))
	if d := duration - want; d > 48 || d < -48 { // within 1 ms
		t.Errorf("audio duration %d samples, want %d (window %.6f s)", duration, want, to-ss)
	}
}
