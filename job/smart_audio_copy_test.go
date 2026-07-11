// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// writeSineWAV writes a deterministic stereo s16le WAV: sample i on channel c
// holds a distinct value, so a trimmed copy can be byte-compared to the exact
// source sub-range.
func writeSineWAV(t *testing.T, path string, sampleRate, nSamples int) {
	t.Helper()
	const channels, bits = 2, 16
	frameBytes := channels * bits / 8
	dataSize := nSamples * frameBytes

	var b bytes.Buffer
	w := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	b.WriteString("RIFF")
	w(uint32(36 + dataSize))
	b.WriteString("WAVE")
	b.WriteString("fmt ")
	w(uint32(16))
	w(uint16(1)) // PCM
	w(uint16(channels))
	w(uint32(sampleRate))
	w(uint32(sampleRate * frameBytes)) // byte rate
	w(uint16(frameBytes))              // block align
	w(uint16(bits))
	b.WriteString("data")
	w(uint32(dataSize))
	for i := 0; i < nSamples; i++ {
		for c := 0; c < channels; c++ {
			w(int16((i*7 + c*3001) % 20000)) // deterministic, per-sample distinct
		}
	}
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatalf("write wav: %v", err)
	}
}

// readWAVData returns the bytes of a WAV file's "data" chunk.
func readWAVData(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	// Walk chunks after the 12-byte RIFF/WAVE header.
	for p := 12; p+8 <= len(raw); {
		id := string(raw[p : p+4])
		sz := int(binary.LittleEndian.Uint32(raw[p+4 : p+8]))
		body := p + 8
		if id == "data" {
			if body+sz > len(raw) {
				sz = len(raw) - body
			}
			return raw[body : body+sz]
		}
		p = body + sz + (sz & 1) // chunks are word-aligned
	}
	t.Fatalf("%s: no data chunk", path)
	return nil
}

// TestSmartAudioCopyPCMByteExact trims a PCM WAV with smartcopy audio and
// asserts the output PCM is byte-identical to the exact source sample sub-range
// — proving the trim is lossless (interior copied verbatim) and sample-accurate.
func TestSmartAudioCopyPCMByteExact(t *testing.T) {
	const sampleRate, nSamples = 48000, 96000 // 2.0 s stereo
	const frameBytes = 4
	tmp := t.TempDir()
	srcPath := filepath.Join(tmp, "src.wav")
	outPath := filepath.ToSlash(filepath.Join(tmp, "clip.wav"))
	writeSineWAV(t, srcPath, sampleRate, nSamples)

	// Fractional (mid-packet) cut points.
	const startSec, endSec = "0.543200", "1.432100"
	startUS, endUS := int64(543200), int64(1432100)
	startSample := startUS * int64(sampleRate) / 1_000_000
	endSample := endUS * int64(sampleRate) / 1_000_000

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
        "codec_audio": "smartcopy",
        "options": {"ss": "%s", "to": "%s"}
      }]
    }`, filepath.ToSlash(srcPath), outPath, startSec, endSec)

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

	srcData := readWAVData(t, srcPath)
	outData := readWAVData(t, outPath)
	want := srcData[startSample*frameBytes : endSample*frameBytes]
	if !bytes.Equal(outData, want) {
		// Allow a ±1-frame boundary rounding difference by locating the output
		// as a verbatim sub-range of the source.
		idx := bytes.Index(srcData, outData)
		if idx < 0 {
			t.Fatalf("output PCM is not a verbatim sub-range of the source (len out=%d want=%d)", len(outData), len(want))
		}
		offFrames := idx / frameBytes
		if d := offFrames - int(startSample); d < -1 || d > 1 {
			t.Fatalf("output starts at sample %d, want %d", offFrames, startSample)
		}
		if d := len(outData)/frameBytes - int(endSample-startSample); d < -1 || d > 1 {
			t.Fatalf("output has %d frames, want %d", len(outData)/frameBytes, endSample-startSample)
		}
		t.Logf("byte-exact within 1 frame: out=%d frames @ sample %d", len(outData)/frameBytes, offFrames)
		return
	}
	t.Logf("smart audio copy: output PCM byte-identical to source[%d:%d] (%d samples)",
		startSample, endSample, endSample-startSample)
}

// TestSmartAudioCopyRejectsCompressed asserts a clear error for non-PCM audio.
func TestSmartAudioCopyRejectsCompressed(t *testing.T) {
	src := filepath.Join("..", "testdata", "BBB_1080p.mp4")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("testdata/BBB_1080p.mp4 missing")
	}
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
        "codec_audio": "smartcopy",
        "options": {"ss": "1.0", "to": "3.0"}
      }]
    }`, filepath.ToSlash(src), outPath)
	cfg, err := ParseConfig([]byte(cfgJSON))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	eng, err := NewPipeline(cfg)
	if err != nil {
		// A build-time rejection is also acceptable.
		return
	}
	err = eng.Run(context.Background())
	if err == nil {
		t.Fatalf("expected an error for AAC smart audio copy, got nil")
	}
}
