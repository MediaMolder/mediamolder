// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

// roundtrip_test.go — first batch of `compat/ffcli` round-trip tests.
//
// Each entry takes an FFmpeg command line, runs it through the
// ffmpeg(1) binary, parses the same command through ffcli.Parse +
// runs the resulting JSON through pipeline.Pipeline, then probes both
// outputs with ffprobe(1) and asserts the technical metadata matches.
// This is the minimum-viable proof that "JSON round-trips through
// MediaMolder produce the same media as the equivalent ffmpeg
// invocation" claimed by docs/ffmpeg-coverage-roadmap.md §3.
//
// The whole suite is skipped (not failed) when ffmpeg/ffprobe are not
// in PATH or when testdata/BBB_10sec.mp4 is missing — keeping the
// default `go test ./...` run usable on machines without the FFmpeg
// CLI installed alongside the libraries.

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/pipeline"
)

type roundTripCase struct {
	name string
	// cmd is an FFmpeg command-line template. {{input}} is replaced
	// with the absolute path to testdata/BBB_10sec.mp4 and {{output}}
	// with a tmpdir path (different per arm). The command MUST end
	// with the {{output}} placeholder so both arms write to disk in
	// the same position.
	cmd string
	// outExt is the expected output extension (must match the
	// extension implied by `cmd`).
	outExt string
	// durationTol is the per-stream duration tolerance in seconds
	// when comparing the ffmpeg arm against the MediaMolder arm.
	// 0.5s covers the usual GOP-alignment slop on short clips.
	durationTol float64
}

var roundTripCases = []roundTripCase{
	{
		name:        "stream_copy_mp4_to_mkv",
		cmd:         "ffmpeg -y -i {{input}} -c copy {{output}}",
		outExt:      ".mkv",
		durationTol: 0.5,
	},
	{
		name:        "video_copy_audio_aac",
		cmd:         "ffmpeg -y -i {{input}} -c:v copy -c:a aac {{output}}",
		outExt:      ".mp4",
		durationTol: 0.5,
	},
	{
		name:        "input_side_ss_t_copy",
		cmd:         "ffmpeg -y -ss 1 -t 2 -i {{input}} -c copy {{output}}",
		outExt:      ".mp4",
		durationTol: 0.5,
	},
	{
		// Output-format change via -f and stream copy: exercises the
		// muxer-selection branch independently of any encoder.
		name:        "force_output_format_matroska",
		cmd:         "ffmpeg -y -i {{input}} -c copy -f matroska {{output}}",
		outExt:      ".mkv",
		durationTol: 0.5,
	},
}

// TestFFCLIRoundTrip is the first batch of compat/ffcli round-trip
// tests. See file header for semantics.
func TestFFCLIRoundTrip(t *testing.T) {
	ffmpegBin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not in PATH; skipping compat/ffcli round-trip suite")
	}
	ffprobeBin, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not in PATH; skipping compat/ffcli round-trip suite")
	}
	inputAbs, err := filepath.Abs(filepath.Join("..", "..", "testdata", "BBB_10sec.mp4"))
	if err != nil {
		t.Fatalf("abs input path: %v", err)
	}
	if _, err := os.Stat(inputAbs); err != nil {
		t.Skipf("testdata/BBB_10sec.mp4 not found at %s; skipping", inputAbs)
	}

	for _, tc := range roundTripCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			ffmpegOut := filepath.Join(tmp, "ffmpeg"+tc.outExt)
			mmOut := filepath.Join(tmp, "mediamolder"+tc.outExt)

			ffmpegCmd := substitute(tc.cmd, inputAbs, ffmpegOut)
			mmCmd := substitute(tc.cmd, inputAbs, mmOut)

			// --- ffmpeg arm ---
			args := tokenize(ffmpegCmd)
			if len(args) > 0 && (args[0] == "ffmpeg" || strings.HasSuffix(args[0], "/ffmpeg")) {
				args = args[1:]
			}
			runFFmpeg(t, ffmpegBin, args)

			// --- mediamolder arm ---
			cfg, err := Parse(mmCmd)
			if err != nil {
				t.Fatalf("ffcli.Parse(%q): %v", mmCmd, err)
			}
			runMediaMolder(t, cfg)

			// --- compare via ffprobe ---
			ffmpegProbe := probe(t, ffprobeBin, ffmpegOut)
			mmProbe := probe(t, ffprobeBin, mmOut)
			compareProbes(t, ffmpegProbe, mmProbe, tc.durationTol)
		})
	}
}

func substitute(cmd, input, output string) string {
	cmd = strings.ReplaceAll(cmd, "{{input}}", input)
	cmd = strings.ReplaceAll(cmd, "{{output}}", output)
	return cmd
}

func runFFmpeg(t *testing.T, bin string, args []string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg arm failed: %v\nargs: %v\noutput:\n%s", err, args, out)
	}
}

func runMediaMolder(t *testing.T, cfg *pipeline.Config) {
	t.Helper()
	eng, err := pipeline.NewPipeline(cfg)
	if err != nil {
		t.Fatalf("pipeline.NewPipeline: %v", err)
	}
	if err := eng.Run(context.Background()); err != nil {
		t.Fatalf("pipeline.Run: %v", err)
	}
}

// probeResult is a minimal subset of `ffprobe -show_format
// -show_streams -of json` output, just the fields we actually compare.
type probeResult struct {
	Format struct {
		FormatName string `json:"format_name"`
		Duration   string `json:"duration"`
		Size       string `json:"size"`
		NbStreams  int    `json:"nb_streams"`
	} `json:"format"`
	Streams []struct {
		Index     int    `json:"index"`
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
		Duration  string `json:"duration"`
		Width     int    `json:"width,omitempty"`
		Height    int    `json:"height,omitempty"`
		SampleRate string `json:"sample_rate,omitempty"`
	} `json:"streams"`
}

func probe(t *testing.T, bin, path string) probeResult {
	t.Helper()
	if st, err := os.Stat(path); err != nil {
		t.Fatalf("probe: stat %s: %v", path, err)
	} else if st.Size() == 0 {
		t.Fatalf("probe: %s is empty", path)
	}
	cmd := exec.Command(bin,
		"-v", "error",
		"-show_format", "-show_streams",
		"-of", "json",
		path,
	)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", path, err)
	}
	var pr probeResult
	if err := json.Unmarshal(out, &pr); err != nil {
		t.Fatalf("ffprobe %s: parse json: %v", path, err)
	}
	return pr
}

func compareProbes(t *testing.T, want, got probeResult, durTol float64) {
	t.Helper()

	if want.Format.NbStreams != got.Format.NbStreams {
		t.Errorf("nb_streams: ffmpeg=%d mediamolder=%d", want.Format.NbStreams, got.Format.NbStreams)
	}

	// Compare each stream by codec_type+codec_name, allowing the
	// streams to come back in either order.
	wantByType := indexByType(want)
	gotByType := indexByType(got)
	for typ, wstream := range wantByType {
		gstream, ok := gotByType[typ]
		if !ok {
			t.Errorf("missing %s stream in mediamolder output", typ)
			continue
		}
		if wstream.CodecName != gstream.CodecName {
			t.Errorf("%s codec_name: ffmpeg=%q mediamolder=%q", typ, wstream.CodecName, gstream.CodecName)
		}
		if typ == "video" {
			if wstream.Width != gstream.Width || wstream.Height != gstream.Height {
				t.Errorf("video resolution: ffmpeg=%dx%d mediamolder=%dx%d",
					wstream.Width, wstream.Height, gstream.Width, gstream.Height)
			}
		}
	}
	for typ := range gotByType {
		if _, ok := wantByType[typ]; !ok {
			t.Errorf("unexpected %s stream in mediamolder output (ffmpeg arm produced none)", typ)
		}
	}

	// Format duration comparison — only when both sides report a
	// finite value (some PCM/raw outputs return "N/A").
	wantDur, wantOK := parseDuration(want.Format.Duration)
	gotDur, gotOK := parseDuration(got.Format.Duration)
	if wantOK && gotOK {
		if math.Abs(wantDur-gotDur) > durTol {
			t.Errorf("format duration: ffmpeg=%.3fs mediamolder=%.3fs (tolerance %.3fs)", wantDur, gotDur, durTol)
		}
	}
}

func indexByType(pr probeResult) map[string]struct {
	CodecType string
	CodecName string
	Width     int
	Height    int
} {
	out := map[string]struct {
		CodecType string
		CodecName string
		Width     int
		Height    int
	}{}
	for _, s := range pr.Streams {
		// First occurrence of each type wins; the first batch of
		// round-trip cases never exercises >1 stream of any type.
		if _, exists := out[s.CodecType]; exists {
			continue
		}
		out[s.CodecType] = struct {
			CodecType string
			CodecName string
			Width     int
			Height    int
		}{s.CodecType, s.CodecName, s.Width, s.Height}
	}
	return out
}

func parseDuration(s string) (float64, bool) {
	if s == "" || s == "N/A" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
