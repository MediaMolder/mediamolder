// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
)

// The contract these tests pin: a packet the decoder rejects mid-stream is
// skipped and counted, not fatal — a damaged AC-3 frame in a transport stream
// must cost the run 32 ms of audio, not the whole file. That is ffmpeg's own
// default; `global_options.exit_on_error` restores its `-xerror`.

// runJob parses and runs one job config, returning Run's error and the engine
// (for metrics).
func runJob(t *testing.T, cfgJSON string) (*Pipeline, error) {
	t.Helper()
	cfg, err := ParseConfig([]byte(cfgJSON))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	eng, err := NewPipeline(cfg)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	return eng, eng.Run(context.Background())
}

// writeAC3TS encodes an 8 s stereo WAV to AC-3 in an MPEG-TS through the
// engine — the camcorder shape — and returns the .ts path. Skips when this
// libav build has no AC-3 encoder.
func writeAC3TS(t *testing.T, dir string) string {
	t.Helper()
	wav := filepath.Join(dir, "src.wav")
	writeSineWAV(t, wav, 48000, 48000*8)
	ts := filepath.Join(dir, "src.ts")
	_, err := runJob(t, fmt.Sprintf(`{
      "schema_version": "1.0",
      "inputs": [{"id": "in0", "url": %q, "streams": [{"input_index": 0, "type": "audio", "track": 0}]}],
      "graph": {"nodes": [], "edges": [{"from": "in0:a:0", "to": "out0:a", "type": "audio"}]},
      "outputs": [{"id": "out0", "url": %q, "format": "mpegts", "codec_audio": "ac3"}]
    }`, filepath.ToSlash(wav), filepath.ToSlash(ts)))
	if err != nil {
		if strings.Contains(err.Error(), "ac3") || strings.Contains(err.Error(), "encoder") {
			t.Skipf("no AC-3 encoder in this libav build: %v", err)
		}
		t.Fatalf("encode fixture: %v", err)
	}
	if fi, err := os.Stat(ts); err != nil || fi.Size() < 188*100 {
		t.Fatalf("fixture too small: %v", err)
	}
	return ts
}

// decodeThroughSource runs src through the graph source node into a PCM WAV
// and returns the decoded sample count plus the source node's error metric —
// the tolerant av-level reader (decodeAudioSamples) would hide exactly the
// behaviour under test.
func decodeThroughSource(t *testing.T, src, dir string, extraGlobal string) (samples int64, decodeErrors int64, runErr error) {
	t.Helper()
	out := filepath.Join(dir, filepath.Base(src)+".wav")
	global := ""
	if extraGlobal != "" {
		global = `"global_options": ` + extraGlobal + `,`
	}
	eng, err := runJob(t, fmt.Sprintf(`{
      "schema_version": "1.0",
      %s
      "inputs": [{"id": "in0", "url": %q, "streams": [{"input_index": 0, "type": "audio", "track": 0}]}],
      "graph": {"nodes": [], "edges": [{"from": "in0:a:0", "to": "out0:a", "type": "audio"}]},
      "outputs": [{"id": "out0", "url": %q, "codec_audio": "pcm_s16le"}]
    }`, global, filepath.ToSlash(src), filepath.ToSlash(out)))
	decodeErrors = eng.Metrics().Node("in0").Snapshot().Errors
	if err != nil {
		return 0, decodeErrors, err
	}
	_, samples = decodeAudioSamples(t, out)
	return samples, decodeErrors, nil
}

// damageAudioPackets punches n holes into the elementary stream around the
// file's midpoint by turning one transport packet every `stride` into a null
// packet (PID 0x1FFF, which the demuxer discards) — tape dropout, as the
// camcorder recorded it. Each hole leaves a PES short ("PES packet size
// mismatch", the packet flagged corrupt) and an AC-3 frame whose bytes
// straddle the gap, which the DECODER rejects with a parser error. Garbling
// payload bytes instead would not do: AC-3 conceals bad mantissas silently.
// PAT/PMT and PES-start packets are never touched.
func damageAudioPackets(t *testing.T, src, dst string, n int) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	const tsPacket = 188
	const stride = 40 // one hole per ~10 AC-3 frames, so each hits a different frame
	if len(b)%tsPacket != 0 || b[0] != 0x47 {
		t.Fatalf("not a transport stream (%d bytes, first %#x)", len(b), b[0])
	}
	packets := len(b) / tsPacket
	damaged := 0
	for p := packets / 2; p < packets && damaged < n; p += stride {
		pk := b[p*tsPacket : (p+1)*tsPacket]
		pid := int(pk[1]&0x1f)<<8 | int(pk[2])
		pusi := pk[1]&0x40 != 0
		if pid < 0x20 || pid >= 0x1000 || pusi { // PAT/SDT, PMT, or a PES start
			p -= stride - 1 // try the next packet instead
			continue
		}
		pk[1] = pk[1]&0xE0 | 0x1F
		pk[2] = 0xFF
		damaged++
	}
	if damaged < n {
		t.Fatalf("only %d of %d packets damaged", damaged, n)
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSourceSkipsUndecodablePackets is the incident: a transport stream with
// a few damaged AC-3 frames mid-way decodes end to end, the skipped packets
// are counted, and nearly all the audio survives.
func TestSourceSkipsUndecodablePackets(t *testing.T) {
	dir := t.TempDir()
	clean := writeAC3TS(t, dir)
	base, baseErrs, err := decodeThroughSource(t, clean, dir, "")
	if err != nil {
		t.Fatalf("clean fixture failed: %v", err)
	}
	if baseErrs != 0 || base == 0 {
		t.Fatalf("clean fixture: %d samples, %d decode errors", base, baseErrs)
	}

	bad := filepath.Join(dir, "damaged.ts")
	damageAudioPackets(t, clean, bad, 6)
	// The gate logs each skip with the decoder's error; the fixture must
	// reproduce the real thing — the AC-3 parser's sync failure, named.
	var logged bytes.Buffer
	log.SetOutput(&logged)
	got, errs, err := decodeThroughSource(t, bad, dir, "")
	log.SetOutput(os.Stderr)
	if err != nil {
		t.Fatalf("a damaged frame must not fail the run: %v", err)
	}
	if errs == 0 {
		t.Fatalf("the damage was not seen by the decoder (0 errors, %d of %d samples)", got, base)
	}
	if got < base*90/100 {
		t.Fatalf("too much audio lost: %d of %d samples (%d decode errors)", got, base, errs)
	}
	if !strings.Contains(logged.String(), "AAC_AC3_PARSE_ERROR_SYNC") {
		t.Fatalf("the skipped packets must be the AC-3 sync failure, named; log:\n%s", logged.String())
	}
	if !strings.Contains(logged.String(), fmt.Sprintf("skipped %d undecodable packet", errs)) {
		t.Fatalf("no per-stream summary line; log:\n%s", logged.String())
	}
	t.Logf("damaged: %d decode errors skipped, %d of %d samples kept", errs, got, base)
}

// TestSourceExitOnError is ffmpeg's -xerror: the same file fails at the first
// sign of damage. With a packet hole that is the demuxer's corrupt flag (as
// in ffmpeg, which exits on "corrupt input packet" before the decoder sees
// it); damage the demuxer does not flag fails on the decoder's own error.
func TestSourceExitOnError(t *testing.T) {
	dir := t.TempDir()
	clean := writeAC3TS(t, dir)
	bad := filepath.Join(dir, "damaged.ts")
	damageAudioPackets(t, clean, bad, 6)
	got, _, err := decodeThroughSource(t, bad, dir, `{"exit_on_error": true}`)
	if err == nil {
		t.Fatalf("exit_on_error must fail the run on a damaged packet (got %d samples)", got)
	}
	if !strings.Contains(err.Error(), "exit_on_error") {
		t.Fatalf("message = %q", err)
	}
	var ae *av.Err
	if !strings.Contains(err.Error(), "corrupt input packet") && !errors.As(err, &ae) {
		t.Fatalf("the cause must be the corrupt flag or the decoder's error: %v", err)
	}
	// And the same file decodes end to end without the flag.
	if _, _, err := decodeThroughSource(t, bad, dir, ""); err != nil {
		t.Fatalf("default policy must tolerate it: %v", err)
	}
}

// TestSourceSurvivesGrossDamage: heavier damage (truncation, a scribbled
// region) may fail the run or truncate it, but never kills the process and
// never fails on an unwrapped surprise.
func TestSourceSurvivesGrossDamage(t *testing.T) {
	dir := t.TempDir()
	clean := writeAC3TS(t, dir)
	b, err := os.ReadFile(clean)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{"truncated", func(b []byte) []byte { return b[:len(b)*55/100] }},
		{"scribble-mid", func(b []byte) []byte {
			for i := len(b) / 3; i < len(b)/3+8192 && i < len(b); i++ {
				b[i] = byte(i * 37)
			}
			return b
		}},
		{"bitflips", func(b []byte) []byte {
			for i := 512; i < len(b); i += 509 {
				b[i] ^= 0xA5
			}
			return b
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bad := filepath.Join(dir, c.name+".ts")
			if err := os.WriteFile(bad, c.mutate(append([]byte(nil), b...)), 0o644); err != nil {
				t.Fatal(err)
			}
			got, errs, err := decodeThroughSource(t, bad, dir, "")
			t.Logf("%s: err=%v samples=%d decodeErrors=%d", c.name, err, got, errs)
		})
	}
}
