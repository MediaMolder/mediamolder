// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestStreamLoop_PlaysTwice runs a stream-copy of a 10 s clip with
// `Input.StreamLoop = 1` (FFmpeg's `-stream_loop 1`, which plays
// the source N+1 = 2 times total) and ffprobes the result to
// confirm the output duration is ~20 s. Mirrors the canonical
// "watermark/intro loop" use case.
func TestStreamLoop_PlaysTwice(t *testing.T) {
	inputURL := filepath.Join("..", "testdata", "BBB_10sec.mp4")
	if _, err := os.Stat(inputURL); err != nil {
		t.Skip("testdata/BBB_10sec.mp4 missing")
	}
	ffprobeBin, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not in PATH")
	}

	output := filepath.Join(t.TempDir(), "looped.mkv")
	rawCfg := fmt.Sprintf(`{
		"schema_version": "1.1",
		"inputs": [{
			"id": "in0",
			"url": %q,
			"stream_loop": 1,
			"streams": [
				{"input_index": 0, "type": "video", "track": 0},
				{"input_index": 0, "type": "audio", "track": 0}
			]
		}],
		"graph": {
			"nodes": [],
			"edges": [
				{"from": "in0:v:0", "to": "out0:v", "type": "video"},
				{"from": "in0:a:0", "to": "out0:a", "type": "audio"}
			]
		},
		"outputs": [{
			"id": "out0",
			"url": %q,
			"format": "matroska",
			"codec_video": "copy",
			"codec_audio": "copy"
		}]
	}`, inputURL, output)

	cfg, err := ParseConfig([]byte(rawCfg))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	eng, err := NewPipeline(cfg)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := eng.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	dur := probeDurationSeconds(t, ffprobeBin, output)
	const want = 20.0
	if math.Abs(dur-want) > 1.5 {
		t.Errorf("output duration = %.2fs, want ~%.1fs (stream_loop=1 should double a 10s source)", dur, want)
	}
}

// TestITSOffset_ShiftsStartPTS runs a stream-copy with
// `Input.ITSOffset = 5.0` and ffprobes the first packet's PTS to
// confirm the source has been shifted +5 s on the timeline.
// Mirrors the canonical A/V re-sync use case.
func TestITSOffset_ShiftsStartPTS(t *testing.T) {
	inputURL := filepath.Join("..", "testdata", "BBB_10sec.mp4")
	if _, err := os.Stat(inputURL); err != nil {
		t.Skip("testdata/BBB_10sec.mp4 missing")
	}
	ffprobeBin, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not in PATH")
	}

	output := filepath.Join(t.TempDir(), "shifted.mkv")
	rawCfg := fmt.Sprintf(`{
		"schema_version": "1.1",
		"inputs": [{
			"id": "in0",
			"url": %q,
			"itsoffset": 5.0,
			"streams": [
				{"input_index": 0, "type": "video", "track": 0}
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
			"format": "matroska",
			"codec_video": "copy"
		}]
	}`, inputURL, output)

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

	startPTS := probeFirstVideoPTSSeconds(t, ffprobeBin, output)
	if math.Abs(startPTS-5.0) > 0.2 {
		t.Errorf("first video PTS = %.3fs, want ~5.0s (itsoffset=5)", startPTS)
	}
}

// TestValidateInputDemuxerFlags exercises the up-front validator
// added in pipeline.Config.validate so that out-of-band values for
// the new demuxer-side fields are rejected at config-parse time.
func TestValidateInputDemuxerFlags(t *testing.T) {
	cases := []struct {
		name string
		in   Input
		want string
	}{
		{"loop -2", Input{ID: "in0", URL: "x", StreamLoop: -2}, "stream_loop"},
		{"negative readrate", Input{ID: "in0", URL: "x", ReadRate: -0.5}, "read_rate"},
		{"negative burst", Input{ID: "in0", URL: "x", ReadRateInitialBurst: -1}, "read_rate_initial_burst"},
		{"catchup < rate", Input{ID: "in0", URL: "x", ReadRate: 1.0, ReadRateCatchup: 0.5}, "read_rate_catchup"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{
				SchemaVersion: "1.1",
				Inputs:        []Input{tc.in},
				Outputs:       []Output{{ID: "out0", URL: "x", Format: "matroska"}},
			}
			err := validate(cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validate err = %v, want substring %q", err, tc.want)
			}
		})
	}
}

// probeDurationSeconds returns format.duration parsed by ffprobe.
func probeDurationSeconds(t *testing.T, ffprobeBin, path string) float64 {
	t.Helper()
	cmd := exec.Command(ffprobeBin,
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path)
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("ffprobe: %v (stderr: %s)", err, exitStderr(err))
	}
	s := string(raw)
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	d, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("parse duration %q: %v", s, err)
	}
	return d
}

// probeFirstVideoPTSSeconds returns the first video packet's
// pts_time field as a float (seconds).
func probeFirstVideoPTSSeconds(t *testing.T, ffprobeBin, path string) float64 {
	t.Helper()
	cmd := exec.Command(ffprobeBin,
		"-v", "error",
		"-select_streams", "v:0",
		"-read_intervals", "%+#1",
		"-show_entries", "packet=pts_time",
		"-of", "json",
		path)
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("ffprobe: %v (stderr: %s)", err, exitStderr(err))
	}
	var probe struct {
		Packets []struct {
			PTSTime string `json:"pts_time"`
		} `json:"packets"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("ffprobe parse: %v", err)
	}
	if len(probe.Packets) == 0 {
		t.Fatalf("no packets in %s", path)
	}
	v, err := strconv.ParseFloat(probe.Packets[0].PTSTime, 64)
	if err != nil {
		t.Fatalf("parse pts_time %q: %v", probe.Packets[0].PTSTime, err)
	}
	return v
}
// probeStreamDurationSeconds returns the duration (in seconds) of the
// first stream matching the given codec_type (e.g. "video", "audio")
// as reported by ffprobe.
func probeStreamDurationSeconds(t *testing.T, ffprobeBin, path, codecType string) float64 {
        t.Helper()
        cmd := exec.Command(ffprobeBin,
                "-v", "error",
                "-show_entries", "stream=codec_type,duration",
                "-of", "json",
                path)
        raw, err := cmd.Output()
        if err != nil {
                t.Fatalf("ffprobe: %v (stderr: %s)", err, exitStderr(err))
        }
        var probe struct {
                Streams []struct {
                        CodecType string `json:"codec_type"`
                        Duration  string `json:"duration"`
                } `json:"streams"`
        }
        if err := json.Unmarshal(raw, &probe); err != nil {
                t.Fatalf("ffprobe parse: %v", err)
        }
        for _, s := range probe.Streams {
                if s.CodecType == codecType {
                        d, err := strconv.ParseFloat(s.Duration, 64)
                        if err != nil {
                                t.Fatalf("parse duration %q: %v", s.Duration, err)
                        }
                        return d
                }
        }
        t.Fatalf("no %s stream found in %s", codecType, path)
        return 0
}

// TestPerStreamStop_DrainAllStreams is a regression test for the
// per-stream recording-time stop logic introduced to mirror
// FFmpeg's fftools/ffmpeg_demux.c::input_packet_process behaviour.
//
// In BBB_10sec.mp4 the demuxer returns v@8.000s before a@7.920s and
// a@7.941s in file read order. When -t 8 (stopPTSus = 8 s) is applied:
//
//   - Old code: the outer loop broke immediately on v@8.000 ≥ 8 s,
//     silently discarding the a@7.920 and a@7.941 packets. Output audio
//     was trimmed to ≈ 7.92 s instead of the correct ≈ 8.00 s.
//   - New code: v@8.000 marks video as done (count 1/2) and the loop
//     continues until a@8.005 marks audio done (count 2/2), then breaks.
//     All audio through a@7.984 s is flushed; output audio ≈ 8.00 s.
func TestPerStreamStop_DrainAllStreams(t *testing.T) {
        inputURL := filepath.Join("..", "testdata", "BBB_10sec.mp4")
        if _, err := os.Stat(inputURL); err != nil {
                t.Skip("testdata/BBB_10sec.mp4 missing")
        }
        ffprobeBin, err := exec.LookPath("ffprobe")
        if err != nil {
                t.Skip("ffprobe not in PATH")
        }

        output := filepath.Join(t.TempDir(), "trimmed.mp4")
        rawCfg := fmt.Sprintf(`{
		"schema_version": "1.1",
		"inputs": [{
			"id": "in0",
			"url": %q,
			"options": {"t": "8"},
			"streams": [
				{"input_index": 0, "type": "video", "track": 0},
				{"input_index": 0, "type": "audio", "track": 0}
			]
		}],
		"graph": {
			"nodes": [],
			"edges": [
				{"from": "in0:v:0", "to": "out0:v", "type": "video"},
				{"from": "in0:a:0", "to": "out0:a", "type": "audio"}
			]
		},
		"outputs": [{
			"id": "out0",
			"url": %q,
			"format": "mp4",
			"codec_video": "copy",
			"codec_audio": "copy"
		}]
	}`, inputURL, output)

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

        // Video: last frame just before 8 s → output duration ≈ 8 s.
        vidDur := probeStreamDurationSeconds(t, ffprobeBin, output, "video")
        if math.Abs(vidDur-8.0) > 0.1 {
                t.Errorf("video duration = %.3fs, want ~8.0s", vidDur)
        }

        // Audio: with per-stream stop the packets at 7.920 s and 7.941 s
        // (read AFTER v@8.000s in file order) must not be lost. The audio
        // duration must reach ≥ 7.99 s.  The old break-all bug produced
        // ≈ 7.92 s; this assertion catches a regression.
        audDur := probeStreamDurationSeconds(t, ffprobeBin, output, "audio")
        if audDur < 7.99 {
                t.Errorf("audio duration = %.3fs, want ≥ 7.99s (per-stream stop not draining remaining audio)", audDur)
        }
        if audDur > 8.1 {
                t.Errorf("audio duration = %.3fs, want ≤ 8.1s (stop not enforced)", audDur)
        }
        _ = time.Second // keep time import used
}