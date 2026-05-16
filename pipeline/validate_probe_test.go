// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/graph"
)

// testdataDir returns the absolute path to the testdata directory.
func testdataDir(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(filename), "..", "testdata")
}

// testdataFile returns the absolute path to a file in testdata/.
func testdataFile(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(testdataDir(t), name)
}

// ---------- integration tests (require real files) ----------

func TestValidateConfig_ProbeFailed_Warning(t *testing.T) {
	cfg := &Config{
		Inputs: []Input{{
			ID:      "in",
			URL:     "file:///nonexistent_file_for_probe_test.mp4",
			Streams: []StreamSelect{{InputIndex: 0, Type: "video"}},
		}},
		Graph: GraphDef{
			Nodes: []NodeDef{{ID: "enc", Type: "encoder", Params: map[string]any{"codec": "libx264"}}},
			Edges: []EdgeDef{
				{From: "in", To: "enc", Type: "video"},
				{From: "enc", To: "out", Type: "video"},
			},
		},
		Outputs: []Output{{ID: "out", URL: "/tmp/out.mp4", Format: "mp4"}},
	}
	report, err := ValidateConfig(cfg, nil)
	if err != nil {
		t.Fatalf("ValidateConfig returned error: %v", err)
	}
	if !hasCode(report, "PROBE_FAILED") {
		t.Error("expected PROBE_FAILED warning for nonexistent input")
	}
	// PROBE_FAILED should be a warning, not an error.
	for _, iss := range report.Issues {
		if iss.Code == "PROBE_FAILED" && iss.Severity != SeverityWarning {
			t.Errorf("PROBE_FAILED should be WARNING, got %s", iss.Severity)
		}
	}
}

func TestValidateConfig_ProbeStreamIndexOutOfRange(t *testing.T) {
	bbb := testdataFile(t, "BBB_10sec.mp4")
	// BBB_10sec.mp4 has 1 video stream (track 0); track 5 is out of range.
	cfg := &Config{
		Inputs: []Input{{
			ID:  "in",
			URL: bbb,
			Streams: []StreamSelect{
				{Type: "video", Track: 5},
			},
		}},
		Graph: GraphDef{
			Nodes: []NodeDef{{ID: "enc", Type: "encoder", Params: map[string]any{"codec": "libx264"}}},
			Edges: []EdgeDef{
				{From: "in", To: "enc", Type: "video"},
				{From: "enc", To: "out", Type: "video"},
			},
		},
		Outputs: []Output{{ID: "out", URL: "/tmp/out.mp4", Format: "mp4"}},
	}
	report, err := ValidateConfig(cfg, nil)
	if err != nil {
		t.Fatalf("ValidateConfig returned error: %v", err)
	}
	if !hasCode(report, "STREAM_INDEX_OUT_OF_RANGE") {
		t.Error("expected STREAM_INDEX_OUT_OF_RANGE error for index 5 (file has 2 streams)")
		for _, iss := range report.Issues {
			t.Logf("  %s %s: %s", iss.Severity, iss.Code, iss.Message)
		}
	}
}

func TestValidateConfig_ProbeStreamTrackOutOfRange(t *testing.T) {
	bbb := testdataFile(t, "BBB_10sec.mp4")
	// BBB_10sec.mp4 has 1 audio stream (track 0); track 50 does not exist.
	cfg := &Config{
		Inputs: []Input{{
			ID:  "in",
			URL: bbb,
			Streams: []StreamSelect{
				{Type: "audio", Track: 50},
			},
		}},
		Graph: GraphDef{
			Nodes: []NodeDef{{ID: "enc", Type: "encoder", Params: map[string]any{"codec": "aac"}}},
			Edges: []EdgeDef{
				{From: "in", To: "enc", Type: "audio"},
				{From: "enc", To: "out", Type: "audio"},
			},
		},
		Outputs: []Output{{ID: "out", URL: "/tmp/out.mp4", Format: "mp4"}},
	}
	report, err := ValidateConfig(cfg, nil)
	if err != nil {
		t.Fatalf("ValidateConfig returned error: %v", err)
	}
	if !hasCode(report, "STREAM_INDEX_OUT_OF_RANGE") {
		t.Error("expected STREAM_INDEX_OUT_OF_RANGE for audio track 50 (file has only 1 audio stream)")
		for _, iss := range report.Issues {
			t.Logf("  %s %s: %s", iss.Severity, iss.Code, iss.Message)
		}
	}
}

func TestValidateConfig_ProbeCleanRun(t *testing.T) {
	bbb := testdataFile(t, "BBB_10sec.mp4")
	// BBB_10sec.mp4 stream 0 is yuv420p H.264, compatible with libx264.
	cfg := &Config{
		Inputs: []Input{{
			ID:  "in",
			URL: bbb,
			Streams: []StreamSelect{
				{Type: "video", Track: 0},
			},
		}},
		Graph: GraphDef{
			Nodes: []NodeDef{{ID: "enc", Type: "encoder", Params: map[string]any{"codec": "libx264"}}},
			Edges: []EdgeDef{
				{From: "in", To: "enc", Type: "video"},
				{From: "enc", To: "out", Type: "video"},
			},
		},
		Outputs: []Output{{ID: "out", URL: "/tmp/out.mp4", Format: "mp4"}},
	}
	report, err := ValidateConfig(cfg, nil)
	if err != nil {
		t.Fatalf("ValidateConfig returned error: %v", err)
	}
	probeIssues := filterProbeIssues(report)
	if len(probeIssues) > 0 {
		t.Errorf("expected no probe issues for valid BBB_10sec.mp4 + libx264 config, got:")
		for _, iss := range probeIssues {
			t.Logf("  %s %s: %s", iss.Severity, iss.Code, iss.Message)
		}
	}
}

// ---------- unit tests for internal probe check functions ----------

func TestCheckStreamSelect_OutOfRange(t *testing.T) {
	streams := []av.StreamInfo{
		{Index: 0, Type: av.MediaTypeVideo},
		{Index: 1, Type: av.MediaTypeAudio},
	}
	r := &ValidationReport{}
	// Request video track 5; only track 0 exists.
	ss := StreamSelect{Type: "video", Track: 5}
	checkStreamSelect("in", ss, streams, r)
	if !hasCode(r, "STREAM_INDEX_OUT_OF_RANGE") {
		t.Error("expected STREAM_INDEX_OUT_OF_RANGE")
	}
}

func TestCheckStreamSelect_AudioTrackOutOfRange(t *testing.T) {
	streams := []av.StreamInfo{
		{Index: 0, Type: av.MediaTypeVideo},
		{Index: 1, Type: av.MediaTypeAudio},
	}
	r := &ValidationReport{}
	// Request audio track 1; only track 0 exists.
	ss := StreamSelect{Type: "audio", Track: 1}
	checkStreamSelect("in", ss, streams, r)
	if !hasCode(r, "STREAM_INDEX_OUT_OF_RANGE") {
		t.Error("expected STREAM_INDEX_OUT_OF_RANGE for non-existent audio track")
	}
}

func TestCheckStreamSelect_Valid(t *testing.T) {
	streams := []av.StreamInfo{
		{Index: 0, Type: av.MediaTypeVideo},
		{Index: 1, Type: av.MediaTypeAudio},
	}
	r := &ValidationReport{}
	checkStreamSelect("in", StreamSelect{Type: "video", Track: 0}, streams, r)
	checkStreamSelect("in", StreamSelect{Type: "audio", Track: 0}, streams, r)
	if len(r.Issues) > 0 {
		t.Errorf("expected no issues for valid stream selects, got: %v", r.Issues)
	}
}

func TestCheckInterlacedNoDeinterlace_Interlaced(t *testing.T) {
	g, node := buildMinimalVideoGraph(t)
	stream := av.StreamInfo{
		Type:       av.MediaTypeVideo,
		FieldOrder: 2, // AV_FIELD_TT — interlaced
	}
	r := &ValidationReport{}
	checkInterlacedNoDeinterlace(node, g, stream, r)
	if !hasCode(r, "VIDEO_INTERLACED_NO_DEINTERLACE") {
		t.Error("expected VIDEO_INTERLACED_NO_DEINTERLACE for interlaced stream")
	}
}

func TestCheckInterlacedNoDeinterlace_Progressive(t *testing.T) {
	g, node := buildMinimalVideoGraph(t)
	stream := av.StreamInfo{
		Type:       av.MediaTypeVideo,
		FieldOrder: 1, // AV_FIELD_PROGRESSIVE
	}
	r := &ValidationReport{}
	checkInterlacedNoDeinterlace(node, g, stream, r)
	if hasCode(r, "VIDEO_INTERLACED_NO_DEINTERLACE") {
		t.Error("expected no warning for progressive stream")
	}
}

func TestCheckInterlacedNoDeinterlace_WithDeinterlaceFilter(t *testing.T) {
	g, node := buildVideoGraphWithFilter(t, "yadif")
	stream := av.StreamInfo{
		Type:       av.MediaTypeVideo,
		FieldOrder: 3, // AV_FIELD_BB — interlaced
	}
	r := &ValidationReport{}
	checkInterlacedNoDeinterlace(node, g, stream, r)
	if hasCode(r, "VIDEO_INTERLACED_NO_DEINTERLACE") {
		t.Error("expected no warning when yadif filter is present")
	}
}

func TestCheckHDRNoTonemap_HDR10(t *testing.T) {
	g, node := buildMinimalVideoGraph(t)
	stream := av.StreamInfo{
		Type:           av.MediaTypeVideo,
		ColorPrimaries: avColPriBT2020,
		ColorTransfer:  avColTrcSMPTE2084, // PQ
	}
	r := &ValidationReport{}
	checkHDRNoTonemap(node, g, "libx264", stream, r)
	if !hasCode(r, "VIDEO_HDR_NO_TONEMAP") {
		t.Error("expected VIDEO_HDR_NO_TONEMAP for HDR10 stream without tonemap filter")
	}
}

func TestCheckHDRNoTonemap_SDR(t *testing.T) {
	g, node := buildMinimalVideoGraph(t)
	stream := av.StreamInfo{
		Type:           av.MediaTypeVideo,
		ColorPrimaries: 1, // BT.709
		ColorTransfer:  1, // BT.709
	}
	r := &ValidationReport{}
	checkHDRNoTonemap(node, g, "libx264", stream, r)
	if hasCode(r, "VIDEO_HDR_NO_TONEMAP") {
		t.Error("expected no warning for SDR stream")
	}
}

func TestCheckHDRNoTonemap_HLG(t *testing.T) {
	g, node := buildMinimalVideoGraph(t)
	stream := av.StreamInfo{
		Type:          av.MediaTypeVideo,
		ColorTransfer: avColTrcARIB_STD_B67, // HLG
	}
	r := &ValidationReport{}
	checkHDRNoTonemap(node, g, "libx264", stream, r)
	if !hasCode(r, "VIDEO_HDR_NO_TONEMAP") {
		t.Error("expected VIDEO_HDR_NO_TONEMAP for HLG stream without tonemap filter")
	}
}

func TestCheckVFRToCFREncoder_VFR(t *testing.T) {
	g, node := buildMinimalVideoGraph(t)
	stream := av.StreamInfo{
		Type:       av.MediaTypeVideo,
		FrameRate:  [2]int{24000, 1001}, // ~23.976 fps (average)
		RFrameRate: [2]int{60, 1},       // 60 fps (container declares higher)
	}
	r := &ValidationReport{}
	checkVFRToCFREncoder(node, g, stream, r)
	if !hasCode(r, "VIDEO_VFR_TO_CFR_ENCODER") {
		t.Error("expected VIDEO_VFR_TO_CFR_ENCODER for VFR stream")
	}
}

func TestCheckVFRToCFREncoder_CFR(t *testing.T) {
	g, node := buildMinimalVideoGraph(t)
	stream := av.StreamInfo{
		Type:       av.MediaTypeVideo,
		FrameRate:  [2]int{24, 1},
		RFrameRate: [2]int{24, 1},
	}
	r := &ValidationReport{}
	checkVFRToCFREncoder(node, g, stream, r)
	if hasCode(r, "VIDEO_VFR_TO_CFR_ENCODER") {
		t.Error("expected no warning for CFR stream")
	}
}

func TestCheckMultichannelNoDownmix_5_1(t *testing.T) {
	g, node := buildMinimalAudioGraph(t, "aac")
	stream := av.StreamInfo{
		Type:     av.MediaTypeAudio,
		Channels: 6, // 5.1
	}
	r := &ValidationReport{}
	checkMultichannelNoDownmix(node, g, "aac", stream, r)
	if !hasCode(r, "AUDIO_MULTICHANNEL_NO_DOWNMIX") {
		t.Error("expected AUDIO_MULTICHANNEL_NO_DOWNMIX for 5.1 into default stereo aac encoder")
	}
}

func TestCheckMultichannelNoDownmix_Stereo(t *testing.T) {
	g, node := buildMinimalAudioGraph(t, "aac")
	stream := av.StreamInfo{
		Type:     av.MediaTypeAudio,
		Channels: 2,
	}
	r := &ValidationReport{}
	checkMultichannelNoDownmix(node, g, "aac", stream, r)
	if hasCode(r, "AUDIO_MULTICHANNEL_NO_DOWNMIX") {
		t.Error("expected no warning for stereo source")
	}
}

// ---------- graph construction helpers ----------

// buildMinimalVideoGraph builds: source → encoder → sink
// and returns the graph and the encoder node.
func buildMinimalVideoGraph(t *testing.T) (*graph.Graph, *graph.Node) {
	t.Helper()
	def := &graph.Def{
		Inputs:  []graph.InputDef{{ID: "in"}},
		Nodes:   []graph.NodeDef{{ID: "enc", Type: "encoder"}},
		Outputs: []graph.OutputDef{{ID: "out"}},
		Edges: []graph.EdgeDef{
			{From: "in", To: "enc", Type: "video"},
			{From: "enc", To: "out", Type: "video"},
		},
	}
	g, err := graph.Build(def)
	if err != nil {
		t.Fatalf("graph.Build: %v", err)
	}
	return g, g.Nodes["enc"]
}

// buildVideoGraphWithFilter builds: source → filter → encoder → sink
// and returns the graph and the encoder node.
func buildVideoGraphWithFilter(t *testing.T, filterName string) (*graph.Graph, *graph.Node) {
	t.Helper()
	def := &graph.Def{
		Inputs:  []graph.InputDef{{ID: "in"}},
		Nodes:   []graph.NodeDef{{ID: "flt", Type: "filter", Filter: filterName}, {ID: "enc", Type: "encoder"}},
		Outputs: []graph.OutputDef{{ID: "out"}},
		Edges: []graph.EdgeDef{
			{From: "in", To: "flt", Type: "video"},
			{From: "flt", To: "enc", Type: "video"},
			{From: "enc", To: "out", Type: "video"},
		},
	}
	g, err := graph.Build(def)
	if err != nil {
		t.Fatalf("graph.Build: %v", err)
	}
	return g, g.Nodes["enc"]
}

// buildMinimalAudioGraph builds: source → encoder → sink for audio
// and returns the graph and the encoder node.
func buildMinimalAudioGraph(t *testing.T, codec string) (*graph.Graph, *graph.Node) {
	t.Helper()
	def := &graph.Def{
		Inputs:  []graph.InputDef{{ID: "in"}},
		Nodes:   []graph.NodeDef{{ID: "enc", Type: "encoder", Params: map[string]any{"codec": codec}}},
		Outputs: []graph.OutputDef{{ID: "out"}},
		Edges: []graph.EdgeDef{
			{From: "in", To: "enc", Type: "audio"},
			{From: "enc", To: "out", Type: "audio"},
		},
	}
	g, err := graph.Build(def)
	if err != nil {
		t.Fatalf("graph.Build: %v", err)
	}
	return g, g.Nodes["enc"]
}

// ---------- report query helpers ----------

// filterProbeIssues returns issues that are produced only by Phase B probe checks.
var phaseAIssueCodes = map[string]bool{
	"TOPO_SOURCE_DISCONNECTED":    true,
	"TOPO_SINK_DISCONNECTED":      true,
	"TOPO_CYCLE":                  true,
	"TOPO_MULTIPLE_WRITERS":       true,
	"VIDEO_ZERO_DIMENSION":        true,
	"VIDEO_ZERO_FRAMERATE":        true,
	"AUDIO_SAMPLE_FMT_MISMATCH":   true, // can also be Phase A
	"AUDIO_SAMPLE_RATE_MISMATCH":  true, // can also be Phase A
	"CONTAINER_CODEC_UNSUPPORTED": true,
	"HW_CODEC_UNAVAILABLE":        true,
	"HW_PLATFORM_MISMATCH":        true,
}

func filterProbeIssues(r *ValidationReport) []ValidationIssue {
	var out []ValidationIssue
	for _, iss := range r.Issues {
		if !phaseAIssueCodes[iss.Code] {
			out = append(out, iss)
		}
	}
	return out
}
