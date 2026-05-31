// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

import (
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/job"
)

// TestParseRTSPTransport verifies that -rtsp_transport before -i is stored
// in Input.Options, not in GlobalOptions (Wave 11 #67).
func TestParseRTSPTransport(t *testing.T) {
	cfg, err := Parse("ffmpeg -rtsp_transport tcp -i rtsp://192.168.1.100/stream output.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Inputs) == 0 {
		t.Fatal("expected at least one input")
	}
	got, ok := cfg.Inputs[0].Options["rtsp_transport"]
	if !ok {
		t.Fatalf("Options[rtsp_transport] not set; Options=%v", cfg.Inputs[0].Options)
	}
	if got != "tcp" {
		t.Errorf("Options[rtsp_transport] = %v, want %q", got, "tcp")
	}
}

// TestParseStimeout verifies that -stimeout is per-input (Wave 11 #67).
func TestParseStimeout(t *testing.T) {
	cfg, err := Parse("ffmpeg -stimeout 5000000 -i rtsp://host/live output.mp4")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := cfg.Inputs[0].Options["stimeout"]
	if !ok {
		t.Fatalf("Options[stimeout] not set; Options=%v", cfg.Inputs[0].Options)
	}
	if got != "5000000" {
		t.Errorf("Options[stimeout] = %v, want %q", got, "5000000")
	}
}

// TestParseSRTMode verifies that -mode and -listen_timeout are per-input (Wave 11 #67).
func TestParseSRTMode(t *testing.T) {
	cfg, err := Parse("ffmpeg -mode listener -listen_timeout 30000000 -i srt://:9000 output.mp4")
	if err != nil {
		t.Fatal(err)
	}
	opts := cfg.Inputs[0].Options
	if opts["mode"] != "listener" {
		t.Errorf("Options[mode] = %v, want %q", opts["mode"], "listener")
	}
	if opts["listen_timeout"] != "30000000" {
		t.Errorf("Options[listen_timeout] = %v, want %q", opts["listen_timeout"], "30000000")
	}
}

// TestParseTimeout verifies that -timeout is per-input (Wave 11 #67).
func TestParseTimeout(t *testing.T) {
	cfg, err := Parse("ffmpeg -timeout 10000000 -i rtmp://live.example.com/live/key output.mp4")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := cfg.Inputs[0].Options["timeout"]
	if !ok {
		t.Fatalf("Options[timeout] not set; Options=%v", cfg.Inputs[0].Options)
	}
	if got != "10000000" {
		t.Errorf("Options[timeout] = %v, want %q", got, "10000000")
	}
}

// TestParseRWTimeout verifies that -rw_timeout is per-input (Wave 11 #67).
func TestParseRWTimeout(t *testing.T) {
	cfg, err := Parse("ffmpeg -rw_timeout 15000000 -i rtmp://host/app/key output.mp4")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := cfg.Inputs[0].Options["rw_timeout"]
	if !ok {
		t.Fatalf("Options[rw_timeout] not set; Options=%v", cfg.Inputs[0].Options)
	}
	if got != "15000000" {
		t.Errorf("Options[rw_timeout] = %v, want %q", got, "15000000")
	}
}

// TestParseNetworkFlagsNotInGlobal verifies that per-input network flags do
// NOT bleed into GlobalOptions (prior to this fix they fell through to the
// default case and were added to globalOpts).
func TestParseNetworkFlagsNotInGlobal(t *testing.T) {
	cfg, err := Parse("ffmpeg -rtsp_transport tcp -i rtsp://host/live -c:v copy output.mp4")
	if err != nil {
		t.Fatal(err)
	}
	// rtsp_transport must appear in the input's Options, not leak into GlobalOptions.
	// GlobalOptions (job.Options) has no extra/arbitrary keys; verifying
	// HardwareAccel is empty is a sufficient proxy that no bleed-through occurred.
	if cfg.GlobalOptions.HardwareAccel != "" {
		t.Errorf("unexpected GlobalOptions.HardwareAccel = %q", cfg.GlobalOptions.HardwareAccel)
	}
	// The flag must be on the input.
	if cfg.Inputs[0].Options["rtsp_transport"] != "tcp" {
		t.Errorf("expected rtsp_transport=tcp on input, got %v", cfg.Inputs[0].Options["rtsp_transport"])
	}
}

// TestExportRTSPTransport verifies that Input.Options["rtsp_transport"] is
// serialised as -rtsp_transport <value> before -i in the CLI output.
func TestExportRTSPTransport(t *testing.T) {
	cfg := &job.Config{
		SchemaVersion: "1.1",
		Inputs: []job.Input{
			{
				ID:      "cam",
				URL:     "rtsp://192.168.1.100/stream",
				Options: map[string]any{"rtsp_transport": "tcp"},
				Streams: []job.StreamSelect{{InputIndex: 0, Type: "video"}},
			},
		},
		Outputs: []job.Output{
			{ID: "out", URL: "/tmp/out.mp4"},
		},
	}
	res := Export(cfg)
	joined := res.Command
	if !strings.Contains(joined, "-rtsp_transport tcp") {
		t.Errorf("expected -rtsp_transport tcp in CLI, got: %s", joined)
	}
	// It must appear BEFORE -i.
	rtspIdx := strings.Index(joined, "-rtsp_transport tcp")
	iIdx := strings.Index(joined, "-i ")
	if rtspIdx == -1 || iIdx == -1 || rtspIdx > iIdx {
		t.Errorf("-rtsp_transport tcp must precede -i; CLI: %s", joined)
	}
}

// TestExportSRTOptions verifies that SRT mode and listen_timeout are emitted
// before -i in the CLI output (Wave 11 #67).
func TestExportSRTOptions(t *testing.T) {
	cfg := &job.Config{
		SchemaVersion: "1.1",
		Inputs: []job.Input{
			{
				ID:  "src",
				URL: "srt://:9000",
				Options: map[string]any{
					"mode":           "listener",
					"listen_timeout": "30000000",
				},
				Streams: []job.StreamSelect{{InputIndex: 0, Type: "video"}},
			},
		},
		Outputs: []job.Output{
			{ID: "out", URL: "/tmp/out.mp4"},
		},
	}
	res := Export(cfg)
	joined := res.Command
	for _, want := range []string{"-mode listener", "-listen_timeout 30000000"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected %q in CLI, got: %s", want, joined)
		}
	}
}

// TestRoundTripRTSP verifies that Parse → Export → re-Parse preserves
// rtsp_transport (Wave 11 #67).
func TestRoundTripRTSP(t *testing.T) {
	const src = "ffmpeg -rtsp_transport tcp -stimeout 5000000 -i rtsp://host/stream output.mp4"
	cfg1, err := Parse(src)
	if err != nil {
		t.Fatalf("first Parse: %v", err)
	}
	res := Export(cfg1)
	cfg2, err := Parse(res.Command)
	if err != nil {
		t.Fatalf("second Parse: %v", err)
	}
	opts := cfg2.Inputs[0].Options
	if opts["rtsp_transport"] != "tcp" {
		t.Errorf("round-trip rtsp_transport: got %v, want %q", opts["rtsp_transport"], "tcp")
	}
	if opts["stimeout"] != "5000000" {
		t.Errorf("round-trip stimeout: got %v, want %q", opts["stimeout"], "5000000")
	}
}
