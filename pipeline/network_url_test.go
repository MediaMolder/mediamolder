// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"strings"
	"testing"
)

func TestURLScheme(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"rtsp://192.168.1.1/stream", "rtsp"},
		{"RTSP://camera.local/live", "rtsp"},
		{"rtsps://secure.cam/live", "rtsps"},
		{"rtmp://live.example.com/live/key", "rtmp"},
		{"rtmps://live.example.com/live/key", "rtmps"},
		{"srt://receiver:9000", "srt"},
		{"rist://239.0.0.1:5000", "rist"},
		{"rtp://224.0.0.1:1234", "rtp"},
		{"/path/to/file.mp4", ""},
		{"pipe:0", ""},
		{"", ""},
		{"noscheme", ""},
	}
	for _, tc := range cases {
		if got := urlScheme(tc.url); got != tc.want {
			t.Errorf("urlScheme(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}
}

func TestIsNetworkInput(t *testing.T) {
	network := []string{
		"rtsp://cam/live",
		"rtsps://cam/live",
		"rtmp://live.twitch.tv/live/key",
		"rtmps://live.twitch.tv/live/key",
		"rtmpe://host/app/key",
		"rtmpt://host/app/key",
		"rtmpte://host/app/key",
		"srt://receiver:9000",
		"rist://239.0.0.1:5000",
		"rtp://224.0.0.1:1234",
	}
	for _, url := range network {
		if !isNetworkInput(url) {
			t.Errorf("isNetworkInput(%q) = false, want true", url)
		}
	}

	notNetwork := []string{
		"/path/to/file.mp4",
		"pipe:0",
		"http://example.com/file.mp4",
		"https://cdn.example.com/clip.mp4",
		"",
	}
	for _, url := range notNetwork {
		if isNetworkInput(url) {
			t.Errorf("isNetworkInput(%q) = true, want false", url)
		}
	}
}

func TestNetworkInputWarnings_NoOptions(t *testing.T) {
	// RTSP input with no options → rtsp.no_transport warning.
	inputs := []Input{
		{ID: "cam", URL: "rtsp://192.168.1.100/stream", Streams: []StreamSelect{}},
	}
	ws := networkInputWarnings(inputs)
	if len(ws) != 1 {
		t.Fatalf("want 1 warning, got %d: %v", len(ws), ws)
	}
	if ws[0].Code != "input.rtsp.no_transport" {
		t.Errorf("want code input.rtsp.no_transport, got %q", ws[0].Code)
	}
	if !strings.Contains(ws[0].Path, "options.rtsp_transport") {
		t.Errorf("want path to include options.rtsp_transport, got %q", ws[0].Path)
	}
}

func TestNetworkInputWarnings_RTSPWithTransport(t *testing.T) {
	// RTSP input with rtsp_transport set → no warning.
	inputs := []Input{
		{ID: "cam", URL: "rtsp://host/stream", Streams: []StreamSelect{},
			Options: map[string]any{"rtsp_transport": "tcp"}},
	}
	ws := networkInputWarnings(inputs)
	if len(ws) != 0 {
		t.Errorf("want 0 warnings, got %d: %v", len(ws), ws)
	}
}

func TestNetworkInputWarnings_SRTListenerNoTimeout(t *testing.T) {
	// SRT listener mode without listen_timeout → listener_no_timeout warning.
	inputs := []Input{
		{ID: "src", URL: "srt://:9000", Streams: []StreamSelect{},
			Options: map[string]any{"mode": "listener"}},
	}
	ws := networkInputWarnings(inputs)
	if len(ws) != 1 {
		t.Fatalf("want 1 warning, got %d: %v", len(ws), ws)
	}
	if ws[0].Code != "input.srt.listener_no_timeout" {
		t.Errorf("want code input.srt.listener_no_timeout, got %q", ws[0].Code)
	}
}

func TestNetworkInputWarnings_SRTListenerWithTimeout(t *testing.T) {
	// SRT listener mode with listen_timeout set → no warning.
	inputs := []Input{
		{ID: "src", URL: "srt://:9000", Streams: []StreamSelect{},
			Options: map[string]any{
				"mode":           "listener",
				"listen_timeout": "30000000",
			}},
	}
	ws := networkInputWarnings(inputs)
	if len(ws) != 0 {
		t.Errorf("want 0 warnings, got %d: %v", len(ws), ws)
	}
}

func TestNetworkInputWarnings_SRTCallerNoTimeout(t *testing.T) {
	// SRT caller mode without listen_timeout → no warning (timeout only applies to listener).
	inputs := []Input{
		{ID: "src", URL: "srt://host:9000", Streams: []StreamSelect{},
			Options: map[string]any{"mode": "caller"}},
	}
	ws := networkInputWarnings(inputs)
	if len(ws) != 0 {
		t.Errorf("want 0 warnings for caller mode, got %d: %v", len(ws), ws)
	}
}

func TestNetworkInputWarnings_SRTNoMode(t *testing.T) {
	// SRT without mode (defaults to caller) → no warning.
	inputs := []Input{
		{ID: "src", URL: "srt://host:9000", Streams: []StreamSelect{}},
	}
	ws := networkInputWarnings(inputs)
	if len(ws) != 0 {
		t.Errorf("want 0 warnings for SRT without mode, got %d: %v", len(ws), ws)
	}
}

func TestNetworkInputWarnings_RTMP(t *testing.T) {
	// RTMP input → no mandatory warnings (no required options).
	inputs := []Input{
		{ID: "src", URL: "rtmp://live.example.com/live/key", Streams: []StreamSelect{}},
	}
	ws := networkInputWarnings(inputs)
	if len(ws) != 0 {
		t.Errorf("want 0 warnings for RTMP, got %d: %v", len(ws), ws)
	}
}

func TestNetworkInputWarnings_NonNetwork(t *testing.T) {
	// File input → no warnings.
	inputs := []Input{
		{ID: "f", URL: "/video.mp4", Streams: []StreamSelect{}},
	}
	ws := networkInputWarnings(inputs)
	if len(ws) != 0 {
		t.Errorf("want 0 warnings for file input, got %d: %v", len(ws), ws)
	}
}

func TestNetworkInputWarnings_Multiple(t *testing.T) {
	// Two RTSP inputs without transport → two warnings, one per input.
	inputs := []Input{
		{ID: "cam1", URL: "rtsp://host1/stream", Streams: []StreamSelect{}},
		{ID: "cam2", URL: "rtsp://host2/stream", Streams: []StreamSelect{}},
	}
	ws := networkInputWarnings(inputs)
	if len(ws) != 2 {
		t.Fatalf("want 2 warnings, got %d: %v", len(ws), ws)
	}
	for _, w := range ws {
		if w.Code != "input.rtsp.no_transport" {
			t.Errorf("unexpected code %q", w.Code)
		}
	}
}

// TestNormalizeConfig_NetworkWarnings exercises the NormalizeConfig integration.
func TestNormalizeConfig_NetworkWarnings(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.1",
		Inputs: []Input{
			{ID: "cam", URL: "rtsp://192.168.1.100/stream", Streams: []StreamSelect{
				{InputIndex: 0, Type: "video"},
			}},
		},
		Outputs: []Output{
			{ID: "out", URL: "/tmp/out.mp4"},
		},
	}
	_, warnings, err := NormalizeConfig(cfg)
	if err != nil {
		t.Fatalf("NormalizeConfig error: %v", err)
	}
	found := false
	for _, w := range warnings {
		if w.Code == "input.rtsp.no_transport" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected input.rtsp.no_transport warning in NormalizeConfig, got: %v", warnings)
	}
}
