// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

import (
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/pipeline"
)

// helpers ─────────────────────────────────────────────────────────────────

func mustExport(t *testing.T, cfg *pipeline.Config) ExportResult {
	t.Helper()
	r := Export(cfg)
	return r
}

func requireArg(t *testing.T, cmd, flag, value string) {
	t.Helper()
	args := strings.Fields(cmd)
	for i, a := range args {
		if a == flag && i+1 < len(args) && args[i+1] == value {
			return
		}
	}
	t.Errorf("expected %s %s in command %q", flag, value, cmd)
}

func requireFlag(t *testing.T, cmd, flag string) {
	t.Helper()
	if !strings.Contains(cmd, flag) {
		t.Errorf("expected %s in command %q", flag, cmd)
	}
}

func requireNoFlag(t *testing.T, cmd, flag string) {
	t.Helper()
	if strings.Contains(cmd, flag) {
		t.Errorf("did not expect %s in command %q", flag, cmd)
	}
}

// ── tests ──────────────────────────────────────────────────────────────────

func TestExport_SimpleStreamCopy(t *testing.T) {
	cfg := &pipeline.Config{
		SchemaVersion: "1.2",
		Inputs: []pipeline.Input{
			{ID: "in0", URL: "input.mp4", Streams: []pipeline.StreamSelect{
				{InputIndex: 0, Type: "video", Track: 0, Optional: true},
				{InputIndex: 0, Type: "audio", Track: 0, Optional: true},
			}},
		},
		Graph:   pipeline.GraphDef{},
		Outputs: []pipeline.Output{{ID: "out0", URL: "output.mkv", CodecVideo: "copy", CodecAudio: "copy"}},
	}
	r := mustExport(t, cfg)

	requireArg(t, r.Command, "-i", "input.mp4")
	requireArg(t, r.Command, "-c:v", "copy")
	requireArg(t, r.Command, "-c:a", "copy")
	requireFlag(t, r.Command, "output.mkv")
	requireNoFlag(t, r.Command, "-map") // default streams → no explicit map
	if len(r.Unsupported) != 0 {
		t.Errorf("expected no unsupported; got %v", r.Unsupported)
	}
}

func TestExport_GlobalOptions(t *testing.T) {
	cfg := &pipeline.Config{
		SchemaVersion: "1.2",
		Inputs:        []pipeline.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:         pipeline.GraphDef{},
		Outputs:       []pipeline.Output{{ID: "out0", URL: "b.mp4"}},
		GlobalOptions: pipeline.Options{Threads: 4},
	}
	r := mustExport(t, cfg)
	requireArg(t, r.Command, "-threads", "4")
}

func TestExport_FilterComplexThreads(t *testing.T) {
	cfg := &pipeline.Config{
		SchemaVersion:        "1.2",
		Inputs:               []pipeline.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:                pipeline.GraphDef{},
		Outputs:              []pipeline.Output{{ID: "out0", URL: "b.mp4"}},
		FilterComplexThreads: 3,
	}
	r := mustExport(t, cfg)
	requireArg(t, r.Command, "-filter_complex_threads", "3")
}

func TestExport_CopyTS(t *testing.T) {
	cfg := &pipeline.Config{
		SchemaVersion: "1.2",
		Inputs:        []pipeline.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:         pipeline.GraphDef{},
		Outputs:       []pipeline.Output{{ID: "out0", URL: "b.mp4"}},
		CopyTS:        true,
		StartAtZero:   true,
	}
	r := mustExport(t, cfg)
	requireFlag(t, r.Command, "-copyts")
	requireFlag(t, r.Command, "-start_at_zero")
}

func TestExport_SubtitleCharenc(t *testing.T) {
	cfg := &pipeline.Config{
		SchemaVersion: "1.2",
		Inputs: []pipeline.Input{
			{ID: "in0", URL: "sub.mkv", SubtitleCharenc: "WINDOWS-1251"},
		},
		Graph:   pipeline.GraphDef{},
		Outputs: []pipeline.Output{{ID: "out0", URL: "out.mkv"}},
	}
	r := mustExport(t, cfg)
	requireArg(t, r.Command, "-sub_charenc", "WINDOWS-1251")
}

func TestExport_StreamLoop(t *testing.T) {
	cfg := &pipeline.Config{
		SchemaVersion: "1.2",
		Inputs: []pipeline.Input{
			{ID: "in0", URL: "logo.png", StreamLoop: -1},
		},
		Graph:   pipeline.GraphDef{},
		Outputs: []pipeline.Output{{ID: "out0", URL: "out.mp4"}},
	}
	r := mustExport(t, cfg)
	requireArg(t, r.Command, "-stream_loop", "-1")
}

func TestExport_ReadRate(t *testing.T) {
	cfg := &pipeline.Config{
		SchemaVersion: "1.2",
		Inputs: []pipeline.Input{
			{ID: "in0", URL: "a.mp4", ReadRate: 1.0},
		},
		Graph:   pipeline.GraphDef{},
		Outputs: []pipeline.Output{{ID: "out0", URL: "out.mp4"}},
	}
	r := mustExport(t, cfg)
	requireFlag(t, r.Command, "-re")
}

func TestExport_ReadRateCustom(t *testing.T) {
	cfg := &pipeline.Config{
		SchemaVersion: "1.2",
		Inputs: []pipeline.Input{
			{ID: "in0", URL: "a.mp4", ReadRate: 2.5},
		},
		Graph:   pipeline.GraphDef{},
		Outputs: []pipeline.Output{{ID: "out0", URL: "out.mp4"}},
	}
	r := mustExport(t, cfg)
	requireArg(t, r.Command, "-readrate", "2.5")
}

func TestExport_ExplicitMap(t *testing.T) {
	cfg := &pipeline.Config{
		SchemaVersion: "1.2",
		Inputs: []pipeline.Input{
			{ID: "in0", URL: "a.mp4", Streams: []pipeline.StreamSelect{
				{InputIndex: 0, Type: "video", All: true, Optional: false},
			}},
		},
		Graph:   pipeline.GraphDef{},
		Outputs: []pipeline.Output{{ID: "out0", URL: "out.mp4"}},
	}
	r := mustExport(t, cfg)
	requireArg(t, r.Command, "-map", "0:v")
}

func TestExport_BSFChains(t *testing.T) {
	cfg := &pipeline.Config{
		SchemaVersion: "1.2",
		Inputs:        []pipeline.Input{{ID: "in0", URL: "a.ts"}},
		Graph:         pipeline.GraphDef{},
		Outputs: []pipeline.Output{{
			ID:       "out0",
			URL:      "out.mp4",
			BSFVideo: "h264_mp4toannexb",
			BSFAudio: "aac_adtstoasc",
		}},
	}
	r := mustExport(t, cfg)
	requireArg(t, r.Command, "-bsf:v", "h264_mp4toannexb")
	requireArg(t, r.Command, "-bsf:a", "aac_adtstoasc")
}

func TestExport_StreamDisposition(t *testing.T) {
	cfg := &pipeline.Config{
		SchemaVersion: "1.2",
		Inputs:        []pipeline.Input{{ID: "in0", URL: "a.mkv"}},
		Graph:         pipeline.GraphDef{},
		Outputs: []pipeline.Output{{
			ID:  "out0",
			URL: "out.mkv",
			Streams: []pipeline.StreamSpec{
				{Type: "s", Index: 0, Disposition: "default+forced"},
			},
		}},
	}
	r := mustExport(t, cfg)
	requireArg(t, r.Command, "-disposition:s:s:0", "default+forced")
}

func TestExport_StreamMetadata(t *testing.T) {
	cfg := &pipeline.Config{
		SchemaVersion: "1.2",
		Inputs:        []pipeline.Input{{ID: "in0", URL: "a.mkv"}},
		Graph:         pipeline.GraphDef{},
		Outputs: []pipeline.Output{{
			ID:  "out0",
			URL: "out.mkv",
			Streams: []pipeline.StreamSpec{
				{Type: "a", Index: 0, Metadata: map[string]string{"language": "eng"}},
			},
		}},
	}
	r := mustExport(t, cfg)
	requireArg(t, r.Command, "-metadata:s:a:0", "language=eng")
}

func TestExport_HLSOptions(t *testing.T) {
	cfg := &pipeline.Config{
		SchemaVersion: "1.2",
		Inputs:        []pipeline.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:         pipeline.GraphDef{},
		Outputs: []pipeline.Output{{
			ID:  "out0",
			URL: "out.m3u8",
			HLS: &pipeline.HLSOptions{
				Time:         4.0,
				PlaylistType: "vod",
				Flags:        []string{"delete_segments", "independent_segments"},
			},
		}},
	}
	r := mustExport(t, cfg)
	requireArg(t, r.Command, "-f", "hls")
	requireArg(t, r.Command, "-hls_time", "4")
	requireArg(t, r.Command, "-hls_playlist_type", "vod")
	requireArg(t, r.Command, "-hls_flags", "delete_segments+independent_segments")
}

func TestExport_DASHOptions(t *testing.T) {
	cfg := &pipeline.Config{
		SchemaVersion: "1.2",
		Inputs:        []pipeline.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:         pipeline.GraphDef{},
		Outputs: []pipeline.Output{{
			ID:  "out0",
			URL: "out.mpd",
			DASH: &pipeline.DASHOptions{
				SegDuration: 6.0,
				WindowSize:  5,
			},
		}},
	}
	r := mustExport(t, cfg)
	requireArg(t, r.Command, "-f", "dash")
	requireArg(t, r.Command, "-seg_duration", "6")
	requireArg(t, r.Command, "-window_size", "5")
}

func TestExport_TeeOutput(t *testing.T) {
	cfg := &pipeline.Config{
		SchemaVersion: "1.2",
		Inputs:        []pipeline.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:         pipeline.GraphDef{},
		Outputs: []pipeline.Output{{
			ID:   "out0",
			Kind: "tee",
			Targets: []pipeline.TeeTarget{
				{URL: "a.mp4", Format: "mp4"},
				{URL: "b.m3u8", Format: "hls"},
			},
		}},
	}
	r := mustExport(t, cfg)
	requireArg(t, r.Command, "-f", "tee")
	requireFlag(t, r.Command, "[f=mp4]a.mp4")
	requireFlag(t, r.Command, "[f=hls]b.m3u8")
	requireFlag(t, r.Command, "|")
}

func TestExport_ContainerMetadata(t *testing.T) {
	cfg := &pipeline.Config{
		SchemaVersion: "1.2",
		Inputs:        []pipeline.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:         pipeline.GraphDef{},
		Outputs: []pipeline.Output{{
			ID:       "out0",
			URL:      "out.mp4",
			Metadata: map[string]string{"title": "demo"},
		}},
	}
	r := mustExport(t, cfg)
	// requireFlag works on the raw Command string (joined without shell quoting).
	requireFlag(t, r.Command, "-metadata")
	requireFlag(t, r.Command, "title=demo")
}

func TestExport_MaxFrames(t *testing.T) {
	cfg := &pipeline.Config{
		SchemaVersion: "1.2",
		Inputs:        []pipeline.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:         pipeline.GraphDef{},
		Outputs: []pipeline.Output{{
			ID:             "out0",
			URL:            "out.mp4",
			MaxFramesVideo: 1,
		}},
	}
	r := mustExport(t, cfg)
	requireArg(t, r.Command, "-frames:v", "1")
}

func TestExport_Shortest(t *testing.T) {
	cfg := &pipeline.Config{
		SchemaVersion: "1.2",
		Inputs:        []pipeline.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:         pipeline.GraphDef{},
		Outputs:       []pipeline.Output{{ID: "out0", URL: "out.mp4", Shortest: true}},
	}
	r := mustExport(t, cfg)
	requireFlag(t, r.Command, "-shortest")
}

func TestExport_DisableStreams(t *testing.T) {
	cfg := &pipeline.Config{
		SchemaVersion: "1.2",
		Inputs:        []pipeline.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:         pipeline.GraphDef{},
		Outputs:       []pipeline.Output{{ID: "out0", URL: "out.mp4", DisableAudio: true, DisableSubtitle: true}},
	}
	r := mustExport(t, cfg)
	requireFlag(t, r.Command, "-an")
	requireFlag(t, r.Command, "-sn")
	requireNoFlag(t, r.Command, "-vn")
}

func TestExport_TwoPass(t *testing.T) {
	cfg := &pipeline.Config{
		SchemaVersion: "1.2",
		Inputs:        []pipeline.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:         pipeline.GraphDef{},
		Outputs:       []pipeline.Output{{ID: "out0", URL: "out.mp4", Pass: 1, PassLogFile: "pass1stats"}},
	}
	r := mustExport(t, cfg)
	requireArg(t, r.Command, "-pass", "1")
	requireArg(t, r.Command, "-passlogfile", "pass1stats")
}

func TestExport_EncoderParams(t *testing.T) {
	cfg := &pipeline.Config{
		SchemaVersion: "1.2",
		Inputs:        []pipeline.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:         pipeline.GraphDef{},
		Outputs: []pipeline.Output{{
			ID:         "out0",
			URL:        "out.mp4",
			CodecVideo: "libx264",
			EncoderParamsVideo: map[string]any{
				"crf":    22,
				"preset": "slow",
			},
		}},
	}
	r := mustExport(t, cfg)
	requireArg(t, r.Command, "-c:v", "libx264")
	requireArg(t, r.Command, "-crf:v", "22")
	requireArg(t, r.Command, "-preset:v", "slow")
}

func TestExport_FilterGraph(t *testing.T) {
	cfg := &pipeline.Config{
		SchemaVersion: "1.2",
		Inputs:        []pipeline.Input{{ID: "in0", URL: "a.mp4"}},
		Graph: pipeline.GraphDef{
			Nodes: []pipeline.NodeDef{
				{ID: "scale0", Type: "filter", Filter: "scale", Params: map[string]any{"w": 1280, "h": 720}},
			},
			Edges: []pipeline.EdgeDef{
				{From: "in0:v:0", To: "scale0:in:0", Type: "video"},
				{From: "scale0:out:0", To: "out0:v", Type: "video"},
			},
		},
		Outputs: []pipeline.Output{{ID: "out0", URL: "out.mp4", CodecVideo: "libx264"}},
	}
	r := mustExport(t, cfg)
	requireFlag(t, r.Command, "-filter_complex")
	requireFlag(t, r.Command, "scale")
}

func TestExport_Assets_Unsupported(t *testing.T) {
	cfg := &pipeline.Config{
		SchemaVersion: "1.2",
		Inputs:        []pipeline.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:         pipeline.GraphDef{},
		Outputs:       []pipeline.Output{{ID: "out0", URL: "out.mp4"}},
		Assets: map[string]pipeline.AssetRef{
			"font": {Path: "/fonts/Arial.ttf", Kind: "font"},
		},
	}
	r := mustExport(t, cfg)
	if len(r.Unsupported) == 0 {
		t.Error("expected Assets to produce an unsupported entry")
	}
	found := false
	for _, u := range r.Unsupported {
		if strings.Contains(u, "Assets") || strings.Contains(u, "asset") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("unsupported messages don't mention assets: %v", r.Unsupported)
	}
}

func TestExport_GoProcessor_Unsupported(t *testing.T) {
	cfg := &pipeline.Config{
		SchemaVersion: "1.2",
		Inputs:        []pipeline.Input{{ID: "in0", URL: "a.mp4"}},
		Graph: pipeline.GraphDef{
			Nodes: []pipeline.NodeDef{
				{ID: "proc0", Type: "go_processor", Processor: "scene_detect"},
			},
			Edges: []pipeline.EdgeDef{
				{From: "in0:v:0", To: "proc0:in:0", Type: "video"},
			},
		},
		Outputs: []pipeline.Output{{ID: "out0", URL: "out.mp4"}},
	}
	r := mustExport(t, cfg)
	if len(r.Unsupported) == 0 {
		t.Error("expected go_processor to produce an unsupported entry")
	}
}

func TestExport_LoudnormPass_Unsupported(t *testing.T) {
	cfg := &pipeline.Config{
		SchemaVersion: "1.2",
		Inputs:        []pipeline.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:         pipeline.GraphDef{},
		Outputs:       []pipeline.Output{{ID: "out0", URL: "out.mp4", LoudnormPass: 1, LoudnormStatsFile: "stats"}},
	}
	r := mustExport(t, cfg)
	if len(r.Unsupported) == 0 {
		t.Error("expected LoudnormPass to produce an unsupported entry")
	}
}

func TestExport_MultipleInputs(t *testing.T) {
	cfg := &pipeline.Config{
		SchemaVersion: "1.2",
		Inputs: []pipeline.Input{
			{ID: "in0", URL: "video.mp4"},
			{ID: "in1", URL: "audio.wav"},
		},
		Graph:   pipeline.GraphDef{},
		Outputs: []pipeline.Output{{ID: "out0", URL: "out.mp4"}},
	}
	r := mustExport(t, cfg)
	requireArg(t, r.Command, "-i", "video.mp4")
	requireFlag(t, r.Command, "audio.wav")
}

func TestExport_FPSMode(t *testing.T) {
	cfg := &pipeline.Config{
		SchemaVersion: "1.2",
		Inputs:        []pipeline.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:         pipeline.GraphDef{},
		Outputs:       []pipeline.Output{{ID: "out0", URL: "out.mp4", FPSMode: "cfr"}},
	}
	r := mustExport(t, cfg)
	requireArg(t, r.Command, "-fps_mode", "cfr")
}

func TestExport_CommandStartsWithFFmpeg(t *testing.T) {
	cfg := &pipeline.Config{
		SchemaVersion: "1.2",
		Inputs:        []pipeline.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:         pipeline.GraphDef{},
		Outputs:       []pipeline.Output{{ID: "out0", URL: "out.mp4"}},
	}
	r := mustExport(t, cfg)
	if !strings.HasPrefix(r.Command, "ffmpeg ") {
		t.Errorf("command should start with 'ffmpeg '; got %q", r.Command[:min(20, len(r.Command))])
	}
	if len(r.Lines) == 0 {
		t.Error("Lines should be non-empty")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestExport_MapChapters(t *testing.T) {
	cfg := &pipeline.Config{
		SchemaVersion: "1.2",
		Inputs:        []pipeline.Input{{ID: "in0", URL: "a.mp4", MapChapters: true}},
		Graph:         pipeline.GraphDef{},
		Outputs:       []pipeline.Output{{ID: "out0", URL: "out.mp4"}},
	}
	r := mustExport(t, cfg)
	requireArg(t, r.Command, "-map_chapters", "0")
}

func TestExport_FormatOutput(t *testing.T) {
	cfg := &pipeline.Config{
		SchemaVersion: "1.2",
		Inputs:        []pipeline.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:         pipeline.GraphDef{},
		Outputs:       []pipeline.Output{{ID: "out0", URL: "out.ts", Format: "mpegts"}},
	}
	r := mustExport(t, cfg)
	requireArg(t, r.Command, "-f", "mpegts")
}

// TestExport_ExplicitEncoderNodeParams verifies that AVOption flags set on an
// explicit encoder graph node (e.g. crf, preset) are included in the exported
// command even when Output.EncoderParamsVideo is not populated.
func TestExport_ExplicitEncoderNodeParams(t *testing.T) {
	cfg := &pipeline.Config{
		SchemaVersion: "1.2",
		Inputs:        []pipeline.Input{{ID: "in0", URL: "input.mp4"}},
		Graph: pipeline.GraphDef{
			Nodes: []pipeline.NodeDef{
				{
					ID:   "enc0",
					Type: "encoder",
					Params: map[string]any{
						"codec":       "libx264",
						"crf":         "22",
						"preset":      "slow",
						"__fps_mode":  "vfr",  // internal sentinel — must NOT appear
						"bitrate":     "0",    // reserved — must NOT appear
						"threads":     "4",    // reserved — must NOT appear
						"thread_type": "auto", // reserved — must NOT appear
					},
				},
			},
			Edges: []pipeline.EdgeDef{
				{From: "in0:v:0", To: "enc0:in:0", Type: "video"},
				{From: "enc0:v", To: "out0:v", Type: "video"},
			},
		},
		Outputs: []pipeline.Output{{ID: "out0", URL: "out.mp4"}},
	}
	r := mustExport(t, cfg)

	requireArg(t, r.Command, "-c:v", "libx264")
	requireArg(t, r.Command, "-crf:v", "22")
	requireArg(t, r.Command, "-preset:v", "slow")

	// Reserved / internal keys must be absent.
	requireNoFlag(t, r.Command, "-__fps_mode:v")
	requireNoFlag(t, r.Command, "-bitrate:v")
	requireNoFlag(t, r.Command, "-threads:v")
	requireNoFlag(t, r.Command, "-thread_type:v")
	requireNoFlag(t, r.Command, "-codec:v")
}

// TestExport_ExplicitEncoderNode_CodecAlreadyOnOutput verifies that when
// Output.CodecVideo is already set (e.g. an imported CLI job), the codec from
// the encoder node does not produce a duplicate -c:v flag.
func TestExport_ExplicitEncoderNode_CodecAlreadyOnOutput(t *testing.T) {
	cfg := &pipeline.Config{
		SchemaVersion: "1.2",
		Inputs:        []pipeline.Input{{ID: "in0", URL: "input.mp4"}},
		Graph: pipeline.GraphDef{
			Nodes: []pipeline.NodeDef{
				{
					ID:   "enc0",
					Type: "encoder",
					Params: map[string]any{
						"codec":  "libx264",
						"preset": "medium",
					},
				},
			},
			Edges: []pipeline.EdgeDef{
				{From: "enc0:v", To: "out0:v", Type: "video"},
			},
		},
		Outputs: []pipeline.Output{{ID: "out0", URL: "out.mp4", CodecVideo: "libx264"}},
	}
	r := mustExport(t, cfg)

	// -c:v should appear exactly once.
	count := strings.Count(r.Command, "-c:v")
	if count != 1 {
		t.Errorf("expected exactly one -c:v flag; got %d in %q", count, r.Command)
	}
	requireArg(t, r.Command, "-preset:v", "medium")
}

// TestExport_CopyNode_Video verifies that an explicit copy node wired to a
// video output produces -c:v copy, even when out.CodecVideo is stale.
func TestExport_CopyNode_Video(t *testing.T) {
	cfg := &pipeline.Config{
		SchemaVersion: "1.2",
		Inputs:        []pipeline.Input{{ID: "in0", URL: "input.mp4"}},
		Graph: pipeline.GraphDef{
			Nodes: []pipeline.NodeDef{
				{ID: "copy_video", Type: "copy"},
			},
			Edges: []pipeline.EdgeDef{
				{From: "in0:v:0", To: "copy_video", Type: "video"},
				{From: "copy_video:v", To: "out0:v", Type: "video"},
			},
		},
		// Stale codec from a previous encoder node — must be overridden by copy.
		Outputs: []pipeline.Output{{ID: "out0", URL: "out.mp4", CodecVideo: "libx264"}},
	}
	r := mustExport(t, cfg)

	requireArg(t, r.Command, "-c:v", "copy")
	// The stale -c:v libx264 must NOT appear.
	if strings.Contains(r.Command, "libx264") {
		t.Errorf("stale codec libx264 should not appear in %q", r.Command)
	}
}

// TestExport_CopyNode_VideoAudio verifies mixed: copy video + explicit AAC encoder.
func TestExport_CopyNode_VideoAudio(t *testing.T) {
	cfg := &pipeline.Config{
		SchemaVersion: "1.2",
		Inputs:        []pipeline.Input{{ID: "in0", URL: "input.mp4"}},
		Graph: pipeline.GraphDef{
			Nodes: []pipeline.NodeDef{
				{ID: "copy_video", Type: "copy"},
				{ID: "enc_audio", Type: "encoder", Params: map[string]any{"codec": "aac"}},
			},
			Edges: []pipeline.EdgeDef{
				{From: "in0:v:0", To: "copy_video", Type: "video"},
				{From: "copy_video:v", To: "out0:v", Type: "video"},
				{From: "in0:a:0", To: "enc_audio:in:0", Type: "audio"},
				{From: "enc_audio:a", To: "out0:a", Type: "audio"},
			},
		},
		Outputs: []pipeline.Output{{ID: "out0", URL: "out.mp4"}},
	}
	r := mustExport(t, cfg)

	requireArg(t, r.Command, "-c:v", "copy")
	requireArg(t, r.Command, "-c:a", "aac")
}

