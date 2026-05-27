// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"testing"
)

// ---------- helpers ----------

// minCfg builds a minimal valid Config with the given inputs, nodes, edges,
// and outputs. It fills in the required schema_version field.
func minCfg(
	inputs []Input,
	nodes []NodeDef,
	edges []EdgeDef,
	outputs []Output,
) *Config {
	return &Config{
		SchemaVersion: "1.0",
		Inputs:        inputs,
		Graph: GraphDef{
			Nodes: nodes,
			Edges: edges,
		},
		Outputs: outputs,
	}
}

func defaultInput() Input {
	return Input{ID: "in", URL: "file:///input.mp4", Streams: []StreamSelect{{Type: "video"}}}
}

func defaultOutput() Output {
	return Output{ID: "out", URL: "file:///output.mp4", Format: "mp4", CodecVideo: "libx264"}
}

func hasCode(r *ValidationReport, code string) bool {
	for _, iss := range r.Issues {
		if iss.Code == code {
			return true
		}
	}
	return false
}

func hasError(r *ValidationReport, code string) bool {
	for _, iss := range r.Issues {
		if iss.Code == code && iss.Severity == SeverityError {
			return true
		}
	}
	return false
}

func hasWarning(r *ValidationReport, code string) bool {
	for _, iss := range r.Issues {
		if iss.Code == code && iss.Severity == SeverityWarning {
			return true
		}
	}
	return false
}

// ---------- topology tests ----------

func TestTopology_DanglingSource(t *testing.T) {
	cfg := minCfg(
		[]Input{defaultInput()},
		nil,
		nil, // no edges: source "in" is dangling
		[]Output{defaultOutput()},
	)
	// Output has no inbound edges either, but source check fires first.
	r := ValidateConfigStatic(cfg, nil)
	if !hasError(r, "TOPO_DANGLING_SOURCE") {
		t.Errorf("expected TOPO_DANGLING_SOURCE; got %+v", r.Issues)
	}
}

// TestTopology_DanglingSourceEventsEdge verifies that an input whose only
// outbound connection is an events edge is not flagged as TOPO_DANGLING_SOURCE.
// Events edges are stripped from the AV graph but are valid connections for
// go_processor-only pipelines (e.g. TwelveLabs indexer with no AV output).
func TestTopology_DanglingSourceEventsEdge(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.1",
		Inputs:        []Input{{ID: "in0", URL: "file:///clip.mp4"}},
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "indexer", Type: "go_processor", Processor: "twelvelabs_indexer"},
			},
			Edges: []EdgeDef{
				{From: "in0", To: "indexer", Type: "events"},
			},
		},
		Outputs: []Output{},
	}
	r := ValidateConfigStatic(cfg, nil)
	for _, iss := range r.Issues {
		if iss.Code == "TOPO_DANGLING_SOURCE" {
			t.Errorf("unexpected TOPO_DANGLING_SOURCE: %+v", iss)
		}
	}
}

func TestTopology_DanglingSink(t *testing.T) {
	cfg := minCfg(
		[]Input{defaultInput()},
		nil,
		// Edge from in → out present, but add a second output with no edges.
		[]EdgeDef{{From: "in", To: "out", Type: "video"}},
		[]Output{
			defaultOutput(),
			{ID: "out2", URL: "file:///out2.mp4", Format: "mp4", CodecVideo: "libx264"},
		},
	)
	r := ValidateConfigStatic(cfg, nil)
	if !hasError(r, "TOPO_DANGLING_SINK") {
		t.Errorf("expected TOPO_DANGLING_SINK; got %+v", r.Issues)
	}
}

func TestTopology_Cycle(t *testing.T) {
	cfg := minCfg(
		[]Input{defaultInput()},
		[]NodeDef{
			{ID: "a", Type: "filter", Filter: "scale"},
			{ID: "b", Type: "filter", Filter: "hflip"},
		},
		[]EdgeDef{
			{From: "in", To: "a", Type: "video"},
			{From: "a", To: "b", Type: "video"},
			{From: "b", To: "a", Type: "video"}, // cycle
			{From: "b", To: "out", Type: "video"},
		},
		[]Output{defaultOutput()},
	)
	r := ValidateConfigStatic(cfg, nil)
	if !hasError(r, "TOPO_CYCLE") {
		t.Errorf("expected TOPO_CYCLE; got %+v", r.Issues)
	}
}

func TestTopology_UnreachableNode(t *testing.T) {
	cfg := minCfg(
		[]Input{defaultInput()},
		[]NodeDef{
			{ID: "deadNode", Type: "filter", Filter: "scale"},
		},
		// deadNode is not connected to in or out.
		[]EdgeDef{{From: "in", To: "out", Type: "video"}},
		[]Output{defaultOutput()},
	)
	r := ValidateConfigStatic(cfg, nil)
	if !hasWarning(r, "TOPO_UNREACHABLE_NODE") {
		t.Errorf("expected TOPO_UNREACHABLE_NODE warning; got %+v", r.Issues)
	}
}

func TestTopology_Clean(t *testing.T) {
	cfg := minCfg(
		[]Input{defaultInput()},
		[]NodeDef{
			{ID: "scl", Type: "filter", Filter: "scale", Params: map[string]any{"w": "1280", "h": "720"}},
		},
		[]EdgeDef{
			{From: "in", To: "scl", Type: "video"},
			{From: "scl", To: "out", Type: "video"},
		},
		[]Output{defaultOutput()},
	)
	r := ValidateConfigStatic(cfg, nil)
	for _, iss := range r.Issues {
		if iss.Severity == SeverityError {
			t.Errorf("unexpected error: %+v", iss)
		}
	}
}

// ---------- codec/container tests ----------

func TestCodecContainer_VP8InMP4(t *testing.T) {
	cfg := minCfg(
		[]Input{defaultInput()},
		nil,
		[]EdgeDef{{From: "in", To: "out", Type: "video"}},
		[]Output{{
			ID: "out", URL: "file:///out.mp4", Format: "mp4",
			CodecVideo: "libvpx",
		}},
	)
	r := ValidateConfigStatic(cfg, nil)
	if !hasError(r, "CONTAINER_CODEC_UNSUPPORTED") {
		t.Errorf("expected CONTAINER_CODEC_UNSUPPORTED; got %+v", r.Issues)
	}
}

func TestCodecContainer_H264InMP4_OK(t *testing.T) {
	cfg := minCfg(
		[]Input{defaultInput()},
		nil,
		[]EdgeDef{{From: "in", To: "out", Type: "video"}},
		[]Output{{ID: "out", URL: "file:///out.mp4", Format: "mp4", CodecVideo: "libx264"}},
	)
	r := ValidateConfigStatic(cfg, nil)
	if hasCode(r, "CONTAINER_CODEC_UNSUPPORTED") {
		t.Errorf("unexpected CONTAINER_CODEC_UNSUPPORTED for h264 in mp4")
	}
}

func TestCodecContainer_OpusInMP4_Warning(t *testing.T) {
	cfg := minCfg(
		[]Input{{ID: "in", URL: "file:///in.mp4", Streams: []StreamSelect{{Type: "audio"}}}},
		nil,
		[]EdgeDef{{From: "in", To: "out", Type: "audio"}},
		[]Output{{ID: "out", URL: "file:///out.mp4", Format: "mp4", CodecAudio: "libopus"}},
	)
	r := ValidateConfigStatic(cfg, nil)
	if !hasWarning(r, "CONTAINER_OPUS_IN_MP4") {
		t.Errorf("expected CONTAINER_OPUS_IN_MP4 warning; got %+v", r.Issues)
	}
}

func TestCodecContainer_PCMInMP4_Error(t *testing.T) {
	cfg := minCfg(
		[]Input{{ID: "in", URL: "file:///in.mp4", Streams: []StreamSelect{{Type: "audio"}}}},
		nil,
		[]EdgeDef{{From: "in", To: "out", Type: "audio"}},
		[]Output{{ID: "out", URL: "file:///out.mp4", Format: "mp4", CodecAudio: "pcm_s16le"}},
	)
	r := ValidateConfigStatic(cfg, nil)
	if !hasError(r, "CONTAINER_PCM_IN_MP4") {
		t.Errorf("expected CONTAINER_PCM_IN_MP4 error; got %+v", r.Issues)
	}
}

func TestCodecContainer_HEVCTagMissing(t *testing.T) {
	cfg := minCfg(
		[]Input{defaultInput()},
		nil,
		[]EdgeDef{{From: "in", To: "out", Type: "video"}},
		[]Output{{ID: "out", URL: "file:///out.mp4", Format: "mp4", CodecVideo: "libx265"}},
	)
	r := ValidateConfigStatic(cfg, nil)
	if !hasWarning(r, "CONTAINER_HEVC_TAG_MISSING") {
		t.Errorf("expected CONTAINER_HEVC_TAG_MISSING warning; got %+v", r.Issues)
	}
}

func TestCodecContainer_HEVCTagPresent_OK(t *testing.T) {
	cfg := minCfg(
		[]Input{defaultInput()},
		nil,
		[]EdgeDef{{From: "in", To: "out", Type: "video"}},
		[]Output{{
			ID: "out", URL: "file:///out.mp4", Format: "mp4",
			CodecVideo: "libx265",
			Options:    map[string]any{"tag:v": "hvc1"},
		}},
	)
	r := ValidateConfigStatic(cfg, nil)
	if hasCode(r, "CONTAINER_HEVC_TAG_MISSING") {
		t.Errorf("unexpected CONTAINER_HEVC_TAG_MISSING when tag:v=hvc1 is set")
	}
}

func TestCodecContainer_HLSBadVideoCodec(t *testing.T) {
	cfg := minCfg(
		[]Input{defaultInput()},
		nil,
		[]EdgeDef{{From: "in", To: "out", Type: "video"}},
		[]Output{{
			ID: "out", URL: "file:///stream.m3u8", Format: "hls",
			CodecVideo: "libvpx-vp9",
		}},
	)
	r := ValidateConfigStatic(cfg, nil)
	if !hasError(r, "CONTAINER_HLS_CODEC") {
		t.Errorf("expected CONTAINER_HLS_CODEC error; got %+v", r.Issues)
	}
}

func TestCodecContainer_DASHMissingMovflags(t *testing.T) {
	cfg := minCfg(
		[]Input{defaultInput()},
		nil,
		[]EdgeDef{{From: "in", To: "out", Type: "video"}},
		[]Output{{
			ID: "out", URL: "file:///stream.mpd", Format: "dash",
			CodecVideo: "libx264",
		}},
	)
	r := ValidateConfigStatic(cfg, nil)
	if !hasError(r, "CONTAINER_DASH_NO_FRAGMENTED") {
		t.Errorf("expected CONTAINER_DASH_NO_FRAGMENTED error; got %+v", r.Issues)
	}
}

func TestCodecContainer_WebMVP9InWebM_OK(t *testing.T) {
	cfg := minCfg(
		[]Input{defaultInput()},
		nil,
		[]EdgeDef{{From: "in", To: "out", Type: "video"}},
		[]Output{{
			ID: "out", URL: "file:///out.webm", Format: "webm",
			CodecVideo: "libvpx-vp9",
		}},
	)
	r := ValidateConfigStatic(cfg, nil)
	if hasCode(r, "CONTAINER_CODEC_UNSUPPORTED") {
		t.Errorf("unexpected CONTAINER_CODEC_UNSUPPORTED for VP9 in WebM")
	}
}

// ---------- two-pass tests ----------

func TestTwoPass_MissingPass1(t *testing.T) {
	cfg := minCfg(
		[]Input{defaultInput()},
		[]NodeDef{
			{
				ID: "enc", Type: "encoder",
				Params: map[string]any{"codec": "libx264", "pass": 2, "passlogfile": "stats"},
			},
		},
		[]EdgeDef{
			{From: "in", To: "enc", Type: "video"},
			{From: "enc", To: "out", Type: "video"},
		},
		[]Output{defaultOutput()},
	)
	r := ValidateConfigStatic(cfg, nil)
	if !hasError(r, "TWOPASS_MISSING_PASS1") {
		t.Errorf("expected TWOPASS_MISSING_PASS1; got %+v", r.Issues)
	}
}

func TestTwoPass_CodecMismatch(t *testing.T) {
	cfg := minCfg(
		[]Input{defaultInput()},
		[]NodeDef{
			{
				ID: "enc1", Type: "encoder",
				Params: map[string]any{"codec": "libx264", "pass": 1, "passlogfile": "stats"},
			},
			{
				ID: "enc2", Type: "encoder",
				Params: map[string]any{"codec": "libx265", "pass": 2, "passlogfile": "stats"},
			},
		},
		[]EdgeDef{
			{From: "in", To: "enc1", Type: "video"},
			{From: "in", To: "enc2", Type: "video"},
			{From: "enc1", To: "out", Type: "video"},
		},
		[]Output{defaultOutput()},
	)
	r := ValidateConfigStatic(cfg, nil)
	if !hasError(r, "TWOPASS_CODEC_MISMATCH") {
		t.Errorf("expected TWOPASS_CODEC_MISMATCH; got %+v", r.Issues)
	}
}

// ---------- audio tests ----------

func TestAudio_SampleFmtMismatch(t *testing.T) {
	cfg := minCfg(
		[]Input{{ID: "in", URL: "file:///in.mp4", Streams: []StreamSelect{{Type: "audio"}}}},
		[]NodeDef{
			{
				ID: "enc", Type: "encoder",
				Params: map[string]any{"codec": "aac", "sample_fmt": "s16"},
			},
		},
		[]EdgeDef{
			{From: "in", To: "enc", Type: "audio"},
			{From: "enc", To: "out", Type: "audio"},
		},
		[]Output{{ID: "out", URL: "file:///out.m4a", Format: "mp4", CodecAudio: "aac"}},
	)
	r := ValidateConfigStatic(cfg, nil)
	if !hasError(r, "AUDIO_SAMPLE_FMT_MISMATCH") {
		t.Errorf("expected AUDIO_SAMPLE_FMT_MISMATCH for aac+s16; got %+v", r.Issues)
	}
}

func TestAudio_SampleRateMismatch(t *testing.T) {
	cfg := minCfg(
		[]Input{{ID: "in", URL: "file:///in.mp4", Streams: []StreamSelect{{Type: "audio"}}}},
		[]NodeDef{
			{
				ID: "enc", Type: "encoder",
				Params: map[string]any{"codec": "aac", "sample_rate": 7000}, // not in AAC allowed rates
			},
		},
		[]EdgeDef{
			{From: "in", To: "enc", Type: "audio"},
			{From: "enc", To: "out", Type: "audio"},
		},
		[]Output{{ID: "out", URL: "file:///out.m4a", Format: "mp4", CodecAudio: "aac"}},
	)
	r := ValidateConfigStatic(cfg, nil)
	if !hasError(r, "AUDIO_SAMPLE_RATE_MISMATCH") {
		t.Errorf("expected AUDIO_SAMPLE_RATE_MISMATCH for aac@7000Hz; got %+v", r.Issues)
	}
}

func TestAudio_SampleRate48k_OK(t *testing.T) {
	cfg := minCfg(
		[]Input{{ID: "in", URL: "file:///in.mp4", Streams: []StreamSelect{{Type: "audio"}}}},
		[]NodeDef{
			{
				ID: "enc", Type: "encoder",
				Params: map[string]any{"codec": "aac", "sample_rate": 48000},
			},
		},
		[]EdgeDef{
			{From: "in", To: "enc", Type: "audio"},
			{From: "enc", To: "out", Type: "audio"},
		},
		[]Output{{ID: "out", URL: "file:///out.m4a", Format: "mp4", CodecAudio: "aac"}},
	)
	r := ValidateConfigStatic(cfg, nil)
	if hasCode(r, "AUDIO_SAMPLE_RATE_MISMATCH") {
		t.Errorf("unexpected AUDIO_SAMPLE_RATE_MISMATCH for aac@48000Hz")
	}
}

// ---------- video tests ----------

func TestVideo_ZeroDimension_Encoder(t *testing.T) {
	cfg := minCfg(
		[]Input{defaultInput()},
		[]NodeDef{
			{ID: "enc", Type: "encoder", Params: map[string]any{"codec": "libx264", "width": "0", "height": "720"}},
		},
		[]EdgeDef{
			{From: "in", To: "enc", Type: "video"},
			{From: "enc", To: "out", Type: "video"},
		},
		[]Output{defaultOutput()},
	)
	r := ValidateConfigStatic(cfg, nil)
	if !hasError(r, "VIDEO_ZERO_DIMENSION") {
		t.Errorf("expected VIDEO_ZERO_DIMENSION; got %+v", r.Issues)
	}
}

func TestVideo_ZeroFramerate(t *testing.T) {
	cfg := minCfg(
		[]Input{defaultInput()},
		[]NodeDef{
			{ID: "fps", Type: "filter", Filter: "fps", Params: map[string]any{"fps": "0"}},
		},
		[]EdgeDef{
			{From: "in", To: "fps", Type: "video"},
			{From: "fps", To: "out", Type: "video"},
		},
		[]Output{defaultOutput()},
	)
	r := ValidateConfigStatic(cfg, nil)
	if !hasError(r, "VIDEO_ZERO_FRAMERATE") {
		t.Errorf("expected VIDEO_ZERO_FRAMERATE; got %+v", r.Issues)
	}
}

func TestVideo_ExpressionDimension_OK(t *testing.T) {
	cfg := minCfg(
		[]Input{defaultInput()},
		[]NodeDef{
			{ID: "scl", Type: "filter", Filter: "scale", Params: map[string]any{"w": "iw/2", "h": "ih/2"}},
		},
		[]EdgeDef{
			{From: "in", To: "scl", Type: "video"},
			{From: "scl", To: "out", Type: "video"},
		},
		[]Output{defaultOutput()},
	)
	r := ValidateConfigStatic(cfg, nil)
	if hasCode(r, "VIDEO_ZERO_DIMENSION") {
		t.Errorf("unexpected VIDEO_ZERO_DIMENSION for expression dimensions")
	}
}

// ---------- security tests ----------

func TestSecurity_DisallowedScheme(t *testing.T) {
	sec := DefaultSecurityConfig()
	sec.AllowedSchemes = []string{"file"}
	cfg := minCfg(
		[]Input{{ID: "in", URL: "rtmp://stream.example.com/live/key", Streams: []StreamSelect{{Type: "video"}}}},
		nil,
		[]EdgeDef{{From: "in", To: "out", Type: "video"}},
		[]Output{defaultOutput()},
	)
	r := ValidateConfigStatic(cfg, &sec)
	if !hasError(r, "SEC_DISALLOWED_SCHEME") {
		t.Errorf("expected SEC_DISALLOWED_SCHEME; got %+v", r.Issues)
	}
}

func TestSecurity_AllowedScheme_OK(t *testing.T) {
	sec := DefaultSecurityConfig()
	cfg := minCfg(
		[]Input{{ID: "in", URL: "https://cdn.example.com/video.mp4", Streams: []StreamSelect{{Type: "video"}}}},
		nil,
		[]EdgeDef{{From: "in", To: "out", Type: "video"}},
		[]Output{defaultOutput()},
	)
	r := ValidateConfigStatic(cfg, &sec)
	if hasCode(r, "SEC_DISALLOWED_SCHEME") {
		t.Errorf("unexpected SEC_DISALLOWED_SCHEME for https URL")
	}
}

func TestSecurity_MaxStreamsExceeded(t *testing.T) {
	sec := DefaultSecurityConfig()
	sec.MaxStreams = 1
	cfg := minCfg(
		[]Input{defaultInput()},
		nil,
		[]EdgeDef{
			{From: "in", To: "out", Type: "video"},
			{From: "in", To: "out2", Type: "video"},
		},
		[]Output{
			defaultOutput(),
			{ID: "out2", URL: "file:///out2.mp4", Format: "mp4", CodecVideo: "libx264"},
		},
	)
	r := ValidateConfigStatic(cfg, &sec)
	if !hasError(r, "SEC_MAX_STREAMS_EXCEEDED") {
		t.Errorf("expected SEC_MAX_STREAMS_EXCEEDED; got %+v", r.Issues)
	}
}

func TestSecurity_MaxThreadsExceeded_Global(t *testing.T) {
	sec := DefaultSecurityConfig()
	sec.MaxThreads = 4
	cfg := minCfg(
		[]Input{defaultInput()},
		nil,
		[]EdgeDef{{From: "in", To: "out", Type: "video"}},
		[]Output{defaultOutput()},
	)
	cfg.GlobalOptions.Threads = 16
	r := ValidateConfigStatic(cfg, &sec)
	if !hasWarning(r, "SEC_MAX_THREADS_EXCEEDED") {
		t.Errorf("expected SEC_MAX_THREADS_EXCEEDED warning; got %+v", r.Issues)
	}
}

func TestSecurity_MaxDimensionsExceeded(t *testing.T) {
	sec := DefaultSecurityConfig()
	sec.MaxWidth = 1920
	sec.MaxHeight = 1080
	cfg := minCfg(
		[]Input{defaultInput()},
		[]NodeDef{
			{ID: "enc", Type: "encoder", Params: map[string]any{"codec": "libx264", "width": 7680, "height": 4320}},
		},
		[]EdgeDef{
			{From: "in", To: "enc", Type: "video"},
			{From: "enc", To: "out", Type: "video"},
		},
		[]Output{defaultOutput()},
	)
	r := ValidateConfigStatic(cfg, &sec)
	if !hasError(r, "SEC_MAX_DIMENSIONS_EXCEEDED") {
		t.Errorf("expected SEC_MAX_DIMENSIONS_EXCEEDED; got %+v", r.Issues)
	}
}

// ---------- inferContainer ----------

func TestInferContainer(t *testing.T) {
	cases := []struct {
		out      Output
		expected string
	}{
		{Output{Format: "mp4"}, "mp4"},
		{Output{URL: "out.mp4"}, "mp4"},
		{Output{URL: "out.mkv"}, "mkv"},
		{Output{URL: "out.webm"}, "webm"},
		{Output{URL: "stream.m3u8"}, "hls"},
		{Output{URL: "stream.mpd"}, "dash"},
		{Output{URL: "out.ts"}, "mpegts"},
	}
	for _, c := range cases {
		got := inferContainer(c.out)
		if got != c.expected {
			t.Errorf("inferContainer(%q/%q) = %q; want %q", c.out.Format, c.out.URL, got, c.expected)
		}
	}
}

// ---------- paramToInt ----------

func TestParamToInt(t *testing.T) {
	cases := []struct {
		v    any
		want int
	}{
		{42, 42},
		{42.0, 42},
		{"42", 42},
		{"0", 0},
		{0, 0},
		{"abc", -1},
	}
	for _, c := range cases {
		got := paramToInt(c.v)
		if got != c.want {
			t.Errorf("paramToInt(%v) = %d; want %d", c.v, got, c.want)
		}
	}
}

// TestValidate_HighestQualityPreset verifies that highest_quality_preset is
// validated against known codec ladders when realtime mode is on.
func TestValidate_HighestQualityPreset(t *testing.T) {
	base := minCfg(
		[]Input{defaultInput()},
		[]NodeDef{{ID: "enc", Type: "encoder", Params: map[string]any{"codec": "libx264"}}},
		[]EdgeDef{{From: "in0", To: "enc", Type: "video"}},
		[]Output{defaultOutput()},
	)
	base.GlobalOptions.Realtime = true

	t.Run("known_preset_ok", func(t *testing.T) {
		cfg := *base
		cfg.GlobalOptions.HighestQualityPreset = "medium"
		if err := validate(&cfg); err != nil {
			t.Fatalf("unexpected error for known preset: %v", err)
		}
	})

	t.Run("unknown_preset_rejected", func(t *testing.T) {
		cfg := *base
		cfg.GlobalOptions.HighestQualityPreset = "bogus_preset"
		if err := validate(&cfg); err == nil {
			t.Fatal("expected error for unknown preset, got nil")
		}
	})

	t.Run("empty_is_ok", func(t *testing.T) {
		cfg := *base
		cfg.GlobalOptions.HighestQualityPreset = ""
		if err := validate(&cfg); err != nil {
			t.Fatalf("unexpected error for empty preset: %v", err)
		}
	})

	t.Run("not_validated_when_realtime_off", func(t *testing.T) {
		cfg := *base
		cfg.GlobalOptions.Realtime = false
		cfg.GlobalOptions.HighestQualityPreset = "bogus_preset"
		if err := validate(&cfg); err != nil {
			t.Fatalf("unexpected error when realtime is off: %v", err)
		}
	})
}
