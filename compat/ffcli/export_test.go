// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

import (
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/job"
)

// helpers ─────────────────────────────────────────────────────────────────

func mustExport(t *testing.T, cfg *job.Config) ExportResult {
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
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs: []job.Input{
			{ID: "in0", URL: "input.mp4", Streams: []job.StreamSelect{
				{InputIndex: 0, Type: "video", Track: 0, Optional: true},
				{InputIndex: 0, Type: "audio", Track: 0, Optional: true},
			}},
		},
		Graph:   job.GraphDef{},
		Outputs: []job.Output{{ID: "out0", URL: "output.mkv", CodecVideo: "copy", CodecAudio: "copy"}},
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
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs:        []job.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:         job.GraphDef{},
		Outputs:       []job.Output{{ID: "out0", URL: "b.mp4"}},
		GlobalOptions: job.Options{Threads: 4},
	}
	r := mustExport(t, cfg)
	requireArg(t, r.Command, "-threads", "4")
}

func TestExport_FilterComplexThreads(t *testing.T) {
	cfg := &job.Config{
		SchemaVersion:        "1.2",
		Inputs:               []job.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:                job.GraphDef{},
		Outputs:              []job.Output{{ID: "out0", URL: "b.mp4"}},
		FilterComplexThreads: 3,
	}
	r := mustExport(t, cfg)
	requireArg(t, r.Command, "-filter_complex_threads", "3")
}

func TestExport_CopyTS(t *testing.T) {
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs:        []job.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:         job.GraphDef{},
		Outputs:       []job.Output{{ID: "out0", URL: "b.mp4"}},
		CopyTS:        true,
		StartAtZero:   true,
	}
	r := mustExport(t, cfg)
	requireFlag(t, r.Command, "-copyts")
	requireFlag(t, r.Command, "-start_at_zero")
}

func TestExport_SubtitleCharenc(t *testing.T) {
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs: []job.Input{
			{ID: "in0", URL: "sub.mkv", SubtitleCharenc: "WINDOWS-1251"},
		},
		Graph:   job.GraphDef{},
		Outputs: []job.Output{{ID: "out0", URL: "out.mkv"}},
	}
	r := mustExport(t, cfg)
	requireArg(t, r.Command, "-sub_charenc", "WINDOWS-1251")
}

func TestExport_StreamLoop(t *testing.T) {
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs: []job.Input{
			{ID: "in0", URL: "logo.png", StreamLoop: -1},
		},
		Graph:   job.GraphDef{},
		Outputs: []job.Output{{ID: "out0", URL: "out.mp4"}},
	}
	r := mustExport(t, cfg)
	requireArg(t, r.Command, "-stream_loop", "-1")
}

func TestExport_ReadRate(t *testing.T) {
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs: []job.Input{
			{ID: "in0", URL: "a.mp4", ReadRate: 1.0},
		},
		Graph:   job.GraphDef{},
		Outputs: []job.Output{{ID: "out0", URL: "out.mp4"}},
	}
	r := mustExport(t, cfg)
	requireFlag(t, r.Command, "-re")
}

func TestExport_ReadRateCustom(t *testing.T) {
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs: []job.Input{
			{ID: "in0", URL: "a.mp4", ReadRate: 2.5},
		},
		Graph:   job.GraphDef{},
		Outputs: []job.Output{{ID: "out0", URL: "out.mp4"}},
	}
	r := mustExport(t, cfg)
	requireArg(t, r.Command, "-readrate", "2.5")
}

func TestExport_ExplicitMap(t *testing.T) {
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs: []job.Input{
			{ID: "in0", URL: "a.mp4", Streams: []job.StreamSelect{
				{InputIndex: 0, Type: "video", All: true, Optional: false},
			}},
		},
		Graph:   job.GraphDef{},
		Outputs: []job.Output{{ID: "out0", URL: "out.mp4"}},
	}
	r := mustExport(t, cfg)
	requireArg(t, r.Command, "-map", "0:v")
}

func TestExport_BSFChains(t *testing.T) {
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs:        []job.Input{{ID: "in0", URL: "a.ts"}},
		Graph:         job.GraphDef{},
		Outputs: []job.Output{{
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
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs:        []job.Input{{ID: "in0", URL: "a.mkv"}},
		Graph:         job.GraphDef{},
		Outputs: []job.Output{{
			ID:  "out0",
			URL: "out.mkv",
			Streams: []job.StreamSpec{
				{Type: "s", Index: 0, Disposition: "default+forced"},
			},
		}},
	}
	r := mustExport(t, cfg)
	requireArg(t, r.Command, "-disposition:s:s:0", "default+forced")
}

func TestExport_StreamMetadata(t *testing.T) {
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs:        []job.Input{{ID: "in0", URL: "a.mkv"}},
		Graph:         job.GraphDef{},
		Outputs: []job.Output{{
			ID:  "out0",
			URL: "out.mkv",
			Streams: []job.StreamSpec{
				{Type: "a", Index: 0, Metadata: map[string]string{"language": "eng"}},
			},
		}},
	}
	r := mustExport(t, cfg)
	requireArg(t, r.Command, "-metadata:s:a:0", "language=eng")
}

func TestExport_HLSOptions(t *testing.T) {
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs:        []job.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:         job.GraphDef{},
		Outputs: []job.Output{{
			ID:  "out0",
			URL: "out.m3u8",
			HLS: &job.HLSOptions{
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
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs:        []job.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:         job.GraphDef{},
		Outputs: []job.Output{{
			ID:  "out0",
			URL: "out.mpd",
			DASH: &job.DASHOptions{
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
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs:        []job.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:         job.GraphDef{},
		Outputs: []job.Output{{
			ID:   "out0",
			Kind: "tee",
			Targets: []job.TeeTarget{
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
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs:        []job.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:         job.GraphDef{},
		Outputs: []job.Output{{
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
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs:        []job.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:         job.GraphDef{},
		Outputs: []job.Output{{
			ID:             "out0",
			URL:            "out.mp4",
			MaxFramesVideo: 1,
		}},
	}
	r := mustExport(t, cfg)
	requireArg(t, r.Command, "-frames:v", "1")
}

func TestExport_Shortest(t *testing.T) {
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs:        []job.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:         job.GraphDef{},
		Outputs:       []job.Output{{ID: "out0", URL: "out.mp4", Shortest: true}},
	}
	r := mustExport(t, cfg)
	requireFlag(t, r.Command, "-shortest")
}

func TestExport_DisableStreams(t *testing.T) {
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs:        []job.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:         job.GraphDef{},
		Outputs:       []job.Output{{ID: "out0", URL: "out.mp4", DisableAudio: true, DisableSubtitle: true}},
	}
	r := mustExport(t, cfg)
	requireFlag(t, r.Command, "-an")
	requireFlag(t, r.Command, "-sn")
	requireNoFlag(t, r.Command, "-vn")
}

func TestExport_TwoPass(t *testing.T) {
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs:        []job.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:         job.GraphDef{},
		Outputs:       []job.Output{{ID: "out0", URL: "out.mp4", Pass: 1, PassLogFile: "pass1stats"}},
	}
	r := mustExport(t, cfg)
	requireArg(t, r.Command, "-pass", "1")
	requireArg(t, r.Command, "-passlogfile", "pass1stats")
}

func TestExport_EncoderParams(t *testing.T) {
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs:        []job.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:         job.GraphDef{},
		Outputs: []job.Output{{
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
	// libx264 is in codecToParamsFlag, so non-reserved AVOptions are
	// packed into a single -x264-params flag (sorted by key).
	requireArg(t, r.Command, "-x264-params:v", "crf=22:preset=slow")
	requireNoFlag(t, r.Command, "-crf:v")
	requireNoFlag(t, r.Command, "-preset:v")
}

// TestExport_EncoderParams_X264_PrivateOptions verifies that
// encoder-private options without a first-class FFmpeg flag (e.g. me,
// subme, aq-mode, psy-rd) round-trip via -x264-params. Keys are
// sorted for deterministic output.
func TestExport_EncoderParams_X264_PrivateOptions(t *testing.T) {
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs:        []job.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:         job.GraphDef{},
		Outputs: []job.Output{{
			ID:         "out0",
			URL:        "out.mp4",
			CodecVideo: "libx264",
			EncoderParamsVideo: map[string]any{
				"me":      "hex",
				"subme":   7,
				"aq-mode": 2,
				"psy-rd":  "1.0:0.15",
			},
		}},
	}
	r := mustExport(t, cfg)
	requireArg(t, r.Command, "-x264-params:v", "aq-mode=2:me=hex:psy-rd=1.0:0.15:subme=7")
}

// TestExport_EncoderParams_X264_RawParamsMerged verifies that a
// pre-built "x264-params" string in EncoderParamsVideo is merged
// verbatim with the other keys rather than re-quoted.
func TestExport_EncoderParams_X264_RawParamsMerged(t *testing.T) {
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs:        []job.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:         job.GraphDef{},
		Outputs: []job.Output{{
			ID:         "out0",
			URL:        "out.mp4",
			CodecVideo: "libx264",
			EncoderParamsVideo: map[string]any{
				"crf":         22,
				"x264-params": "nal-hrd=cbr:force-cfr=1",
			},
		}},
	}
	r := mustExport(t, cfg)
	// Sorted: "crf" then "x264-params" (the raw payload is appended
	// verbatim, not "x264-params=...").
	requireArg(t, r.Command, "-x264-params:v", "crf=22:nal-hrd=cbr:force-cfr=1")
}

// TestExport_EncoderParams_X265 verifies the same packing rule applies
// to libx265 via "-x265-params".
func TestExport_EncoderParams_X265(t *testing.T) {
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs:        []job.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:         job.GraphDef{},
		Outputs: []job.Output{{
			ID:         "out0",
			URL:        "out.mp4",
			CodecVideo: "libx265",
			EncoderParamsVideo: map[string]any{
				"crf":    24,
				"preset": "medium",
			},
		}},
	}
	r := mustExport(t, cfg)
	requireArg(t, r.Command, "-x265-params:v", "crf=24:preset=medium")
}

// TestExport_EncoderParams_NonAllowlistCodec verifies that codecs
// outside codecToParamsFlag (e.g. libvpx-vp9) still emit per-key
// flags as before — there is no "-vp9-params" channel.
func TestExport_EncoderParams_NonAllowlistCodec(t *testing.T) {
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs:        []job.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:         job.GraphDef{},
		Outputs: []job.Output{{
			ID:         "out0",
			URL:        "out.mp4",
			CodecVideo: "libvpx-vp9",
			EncoderParamsVideo: map[string]any{
				"crf":      30,
				"b:v":      "0",
				"deadline": "good",
			},
		}},
	}
	r := mustExport(t, cfg)
	requireArg(t, r.Command, "-crf:v", "30")
	requireArg(t, r.Command, "-deadline:v", "good")
}

func TestExport_FilterGraph(t *testing.T) {
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs:        []job.Input{{ID: "in0", URL: "a.mp4"}},
		Graph: job.GraphDef{
			Nodes: []job.NodeDef{
				{ID: "scale0", Type: "filter", Filter: "scale", Params: map[string]any{"w": 1280, "h": 720}},
			},
			Edges: []job.EdgeDef{
				{From: "in0:v:0", To: "scale0:in:0", Type: "video"},
				{From: "scale0:out:0", To: "out0:v", Type: "video"},
			},
		},
		Outputs: []job.Output{{ID: "out0", URL: "out.mp4", CodecVideo: "libx264"}},
	}
	r := mustExport(t, cfg)
	requireFlag(t, r.Command, "-filter_complex")
	requireFlag(t, r.Command, "scale")
}

func TestExport_Assets_Unsupported(t *testing.T) {
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs:        []job.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:         job.GraphDef{},
		Outputs:       []job.Output{{ID: "out0", URL: "out.mp4"}},
		Assets: map[string]job.AssetRef{
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
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs:        []job.Input{{ID: "in0", URL: "a.mp4"}},
		Graph: job.GraphDef{
			Nodes: []job.NodeDef{
				{ID: "proc0", Type: "go_processor", Processor: "scene_detect"},
			},
			Edges: []job.EdgeDef{
				{From: "in0:v:0", To: "proc0:in:0", Type: "video"},
			},
		},
		Outputs: []job.Output{{ID: "out0", URL: "out.mp4"}},
	}
	r := mustExport(t, cfg)
	if len(r.Unsupported) == 0 {
		t.Error("expected go_processor to produce an unsupported entry")
	}
}

func TestExport_GoProcessor_NoEquivalentNotice(t *testing.T) {
	// Analysis-only graph (no outputs): the command IS a "no equivalent" notice,
	// not a misleading `ffmpeg -i …` that silently omits the unsupported node.
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs:        []job.Input{{ID: "in0", URL: "a.mp4"}},
		Graph: job.GraphDef{
			Nodes: []job.NodeDef{{ID: "faces", Type: "go_processor", Processor: "face_detect"}},
			Edges: []job.EdgeDef{{From: "in0:v:0", To: "faces:default", Type: "video"}},
		},
	}
	r := mustExport(t, cfg)
	if !strings.Contains(r.Command, "No equivalent FFmpeg command") || !strings.Contains(r.Command, "face_detect") {
		t.Errorf("expected a no-equivalent notice naming face_detect; got %q", r.Command)
	}
	if strings.Contains(r.Command, "ffmpeg ") {
		t.Errorf("analysis-only graph must not emit a misleading ffmpeg line; got %q", r.Command)
	}
	if len(r.Unsupported) == 0 {
		t.Error("expected an Unsupported entry as well")
	}

	// With a real output FFmpeg *can* do (a transcode), the notice precedes the
	// best-effort command instead of replacing it.
	cfg.Outputs = []job.Output{{ID: "out0", URL: "out.mp4"}}
	r = mustExport(t, cfg)
	if !strings.Contains(r.Command, "No equivalent FFmpeg command") {
		t.Errorf("expected the notice to prefix the command; got %q", r.Command)
	}
	if !strings.Contains(r.Command, "ffmpeg ") {
		t.Errorf("with an output, the best-effort ffmpeg line should remain; got %q", r.Command)
	}
}

func TestExport_LoudnormPass_Unsupported(t *testing.T) {
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs:        []job.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:         job.GraphDef{},
		Outputs:       []job.Output{{ID: "out0", URL: "out.mp4", LoudnormPass: 1, LoudnormStatsFile: "stats"}},
	}
	r := mustExport(t, cfg)
	if len(r.Unsupported) == 0 {
		t.Error("expected LoudnormPass to produce an unsupported entry")
	}
}

func TestExport_MultipleInputs(t *testing.T) {
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs: []job.Input{
			{ID: "in0", URL: "video.mp4"},
			{ID: "in1", URL: "audio.wav"},
		},
		Graph:   job.GraphDef{},
		Outputs: []job.Output{{ID: "out0", URL: "out.mp4"}},
	}
	r := mustExport(t, cfg)
	requireArg(t, r.Command, "-i", "video.mp4")
	requireFlag(t, r.Command, "audio.wav")
}

func TestExport_FPSMode(t *testing.T) {
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs:        []job.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:         job.GraphDef{},
		Outputs:       []job.Output{{ID: "out0", URL: "out.mp4", FPSMode: "cfr"}},
	}
	r := mustExport(t, cfg)
	requireArg(t, r.Command, "-fps_mode", "cfr")
}

func TestExport_CommandStartsWithFFmpeg(t *testing.T) {
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs:        []job.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:         job.GraphDef{},
		Outputs:       []job.Output{{ID: "out0", URL: "out.mp4"}},
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
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs:        []job.Input{{ID: "in0", URL: "a.mp4", MapChapters: true}},
		Graph:         job.GraphDef{},
		Outputs:       []job.Output{{ID: "out0", URL: "out.mp4"}},
	}
	r := mustExport(t, cfg)
	requireArg(t, r.Command, "-map_chapters", "0")
}

func TestExport_FormatOutput(t *testing.T) {
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs:        []job.Input{{ID: "in0", URL: "a.mp4"}},
		Graph:         job.GraphDef{},
		Outputs:       []job.Output{{ID: "out0", URL: "out.ts", Format: "mpegts"}},
	}
	r := mustExport(t, cfg)
	requireArg(t, r.Command, "-f", "mpegts")
}

// TestExport_ExplicitEncoderNodeParams verifies that AVOption flags set on an
// explicit encoder graph node (e.g. crf, preset) are included in the exported
// command even when Output.EncoderParamsVideo is not populated.
func TestExport_ExplicitEncoderNodeParams(t *testing.T) {
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs:        []job.Input{{ID: "in0", URL: "input.mp4"}},
		Graph: job.GraphDef{
			Nodes: []job.NodeDef{
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
			Edges: []job.EdgeDef{
				{From: "in0:v:0", To: "enc0:in:0", Type: "video"},
				{From: "enc0:v", To: "out0:v", Type: "video"},
			},
		},
		Outputs: []job.Output{{ID: "out0", URL: "out.mp4"}},
	}
	r := mustExport(t, cfg)

	requireArg(t, r.Command, "-c:v", "libx264")
	requireArg(t, r.Command, "-x264-params:v", "crf=22:preset=slow")
	requireNoFlag(t, r.Command, "-crf:v")
	requireNoFlag(t, r.Command, "-preset:v")

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
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs:        []job.Input{{ID: "in0", URL: "input.mp4"}},
		Graph: job.GraphDef{
			Nodes: []job.NodeDef{
				{
					ID:   "enc0",
					Type: "encoder",
					Params: map[string]any{
						"codec":  "libx264",
						"preset": "medium",
					},
				},
			},
			Edges: []job.EdgeDef{
				{From: "enc0:v", To: "out0:v", Type: "video"},
			},
		},
		Outputs: []job.Output{{ID: "out0", URL: "out.mp4", CodecVideo: "libx264"}},
	}
	r := mustExport(t, cfg)

	// -c:v should appear exactly once.
	count := strings.Count(r.Command, "-c:v")
	if count != 1 {
		t.Errorf("expected exactly one -c:v flag; got %d in %q", count, r.Command)
	}
	requireArg(t, r.Command, "-x264-params:v", "preset=medium")
	requireNoFlag(t, r.Command, "-preset:v")
}

// TestExport_CopyNode_Video verifies that an explicit copy node wired to a
// video output produces -c:v copy, even when out.CodecVideo is stale.
func TestExport_CopyNode_Video(t *testing.T) {
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs:        []job.Input{{ID: "in0", URL: "input.mp4"}},
		Graph: job.GraphDef{
			Nodes: []job.NodeDef{
				{ID: "copy_video", Type: "copy"},
			},
			Edges: []job.EdgeDef{
				{From: "in0:v:0", To: "copy_video", Type: "video"},
				{From: "copy_video:v", To: "out0:v", Type: "video"},
			},
		},
		// Stale codec from a previous encoder node — must be overridden by copy.
		Outputs: []job.Output{{ID: "out0", URL: "out.mp4", CodecVideo: "libx264"}},
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
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs:        []job.Input{{ID: "in0", URL: "input.mp4"}},
		Graph: job.GraphDef{
			Nodes: []job.NodeDef{
				{ID: "copy_video", Type: "copy"},
				{ID: "enc_audio", Type: "encoder", Params: map[string]any{"codec": "aac"}},
			},
			Edges: []job.EdgeDef{
				{From: "in0:v:0", To: "copy_video", Type: "video"},
				{From: "copy_video:v", To: "out0:v", Type: "video"},
				{From: "in0:a:0", To: "enc_audio:in:0", Type: "audio"},
				{From: "enc_audio:a", To: "out0:a", Type: "audio"},
			},
		},
		Outputs: []job.Output{{ID: "out0", URL: "out.mp4"}},
	}
	r := mustExport(t, cfg)

	requireArg(t, r.Command, "-c:v", "copy")
	requireArg(t, r.Command, "-c:a", "aac")
}

// TestExport_GraphMaps_MultiInput verifies that -map flags are derived from
// the graph edges when inputs differ per stream type.  In the scenario below,
// video comes from a video-only Y4M source (in0) and audio comes from a
// separate AVI (in1).  The correct command must emit -map 0:v:0 and
// -map 1:a:0 — not a cross-product of both inputs × both stream types.
func TestExport_GraphMaps_MultiInput(t *testing.T) {
	cfg := &job.Config{
		SchemaVersion: "1.2",
		Inputs: []job.Input{
			{ID: "in0", URL: "video.y4m"},
			{ID: "in1", URL: "audio.avi"},
		},
		Graph: job.GraphDef{
			Nodes: []job.NodeDef{
				{ID: "enc_video", Type: "encoder", Params: map[string]any{"codec": "libx264"}},
				{ID: "enc_audio", Type: "encoder", Params: map[string]any{"codec": "aac"}},
			},
			Edges: []job.EdgeDef{
				{From: "in0:v:0", To: "enc_video:in:0", Type: "video"},
				{From: "enc_video:v", To: "out0:v", Type: "video"},
				{From: "in1:a:0", To: "enc_audio:in:0", Type: "audio"},
				{From: "enc_audio:a", To: "out0:a", Type: "audio"},
			},
		},
		Outputs: []job.Output{{ID: "out0", URL: "out.mp4"}},
	}
	r := mustExport(t, cfg)

	requireArg(t, r.Command, "-map", "0:v:0")
	requireArg(t, r.Command, "-map", "1:a:0")
	// Must NOT map the unconnected streams.
	if strings.Contains(r.Command, "0:a:") {
		t.Errorf("unexpected -map for in0 audio in %q", r.Command)
	}
	if strings.Contains(r.Command, "1:v:") {
		t.Errorf("unexpected -map for in1 video in %q", r.Command)
	}
	requireArg(t, r.Command, "-c:v", "libx264")
	requireArg(t, r.Command, "-c:a", "aac")
}
