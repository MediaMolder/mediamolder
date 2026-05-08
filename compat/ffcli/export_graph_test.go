// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

import (
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/pipeline"
)

// TestExportGraph_RoundTrip is the F1.2 acceptance gate. For every
// representative configuration, ExportGraph(cfg, NormalizeConfig(cfg))
// must produce the same FFmpeg command as Export(cfg). This proves
// that the graph-sourced view (resolveOutputViewFromGraph) and the
// shorthand-sourced view (resolveOutputViewFromConfig) carry the
// same information through the formatter.
//
// When new shorthand rows are added to pipeline.Output, extend either
// the cases here or the lowering passes in pipeline/handlers_graph_build.go
// until the round-trip stays identical.
func TestExportGraph_RoundTrip(t *testing.T) {
	cases := []struct {
		name string
		cfg  *pipeline.Config
	}{
		{
			name: "minimal_no_codec",
			cfg: &pipeline.Config{
				SchemaVersion: "1.2",
				Inputs:        []pipeline.Input{{ID: "in0", URL: "in.mp4"}},
				Outputs:       []pipeline.Output{{ID: "out0", URL: "out.mp4"}},
			},
		},
		{
			name: "video_codec_shorthand",
			cfg: &pipeline.Config{
				SchemaVersion: "1.2",
				Inputs:        []pipeline.Input{{ID: "in0", URL: "in.mp4"}},
				Outputs: []pipeline.Output{{
					ID: "out0", URL: "out.mp4", CodecVideo: "libx264",
				}},
			},
		},
		{
			name: "video_codec_with_x264_params",
			cfg: &pipeline.Config{
				SchemaVersion: "1.2",
				Inputs:        []pipeline.Input{{ID: "in0", URL: "in.mp4"}},
				Outputs: []pipeline.Output{{
					ID: "out0", URL: "out.mp4", CodecVideo: "libx264",
					EncoderParamsVideo: map[string]any{
						"crf":    22,
						"preset": "slow",
					},
				}},
			},
		},
		{
			name: "video_audio_codecs",
			cfg: &pipeline.Config{
				SchemaVersion: "1.2",
				Inputs:        []pipeline.Input{{ID: "in0", URL: "in.mp4"}},
				Outputs: []pipeline.Output{{
					ID: "out0", URL: "out.mp4",
					CodecVideo: "libx264", CodecAudio: "aac",
				}},
			},
		},
		{
			name: "fps_mode_cfr",
			cfg: &pipeline.Config{
				SchemaVersion: "1.2",
				Inputs:        []pipeline.Input{{ID: "in0", URL: "in.mp4"}},
				Outputs: []pipeline.Output{{
					ID: "out0", URL: "out.mp4",
					CodecVideo: "libx264", FPSMode: "cfr",
				}},
			},
		},
		{
			name: "audio_sync_2",
			cfg: &pipeline.Config{
				SchemaVersion: "1.2",
				Inputs:        []pipeline.Input{{ID: "in0", URL: "in.mp4"}},
				Outputs: []pipeline.Output{{
					ID: "out0", URL: "out.mp4",
					CodecAudio: "aac", AudioSync: 2,
				}},
			},
		},
		{
			name: "two_pass_first",
			cfg: &pipeline.Config{
				SchemaVersion: "1.2",
				Inputs:        []pipeline.Input{{ID: "in0", URL: "in.mp4"}},
				Outputs: []pipeline.Output{{
					ID: "out0", URL: "out.mp4",
					CodecVideo: "libx264", Pass: 1, PassLogFile: "stats",
				}},
			},
		},
		{
			name: "force_key_frames",
			cfg: &pipeline.Config{
				SchemaVersion: "1.2",
				Inputs:        []pipeline.Input{{ID: "in0", URL: "in.mp4"}},
				Outputs: []pipeline.Output{{
					ID: "out0", URL: "out.mp4",
					CodecVideo: "libx264", ForceKeyFrames: "expr:gte(t,n_forced*2)",
				}},
			},
		},
		{
			name: "explicit_encoder_node",
			cfg: &pipeline.Config{
				SchemaVersion: "1.2",
				Inputs:        []pipeline.Input{{ID: "in0", URL: "in.mp4"}},
				Graph: pipeline.GraphDef{
					Nodes: []pipeline.NodeDef{
						{ID: "enc0", Type: "encoder", Params: map[string]any{
							"codec": "libx265", "crf": 24, "preset": "medium",
						}},
					},
					Edges: []pipeline.EdgeDef{
						{From: "in0:v:0", To: "enc0:in:0", Type: "video"},
						{From: "enc0:v", To: "out0:v", Type: "video"},
					},
				},
				Outputs: []pipeline.Output{{ID: "out0", URL: "out.mp4"}},
			},
		},
		{
			name: "copy_video",
			cfg: &pipeline.Config{
				SchemaVersion: "1.2",
				Inputs:        []pipeline.Input{{ID: "in0", URL: "in.mp4"}},
				Graph: pipeline.GraphDef{
					Nodes: []pipeline.NodeDef{{ID: "cp0", Type: "copy"}},
					Edges: []pipeline.EdgeDef{
						{From: "in0:v:0", To: "cp0", Type: "video"},
						{From: "cp0:v", To: "out0:v", Type: "video"},
					},
				},
				Outputs: []pipeline.Output{{ID: "out0", URL: "out.mp4"}},
			},
		},
		{
			// Implicit encoder synthesis: filter chain feeds the
			// output, expandImplicitEncoders splices an "__enc__"
			// node that carries the codec + EncoderParamsVideo on
			// its Params and the FPSMode shorthand on its
			// Internal.Encoder. The graph-sourced view must
			// surface all three identically to the shorthand path.
			name: "implicit_encoder_with_filter_and_shorthand",
			cfg: &pipeline.Config{
				SchemaVersion: "1.2",
				Inputs:        []pipeline.Input{{ID: "in0", URL: "in.mp4"}},
				Graph: pipeline.GraphDef{
					Nodes: []pipeline.NodeDef{
						{ID: "scale0", Type: "filter", Filter: "scale", Params: map[string]any{"w": 1280, "h": 720}},
					},
					Edges: []pipeline.EdgeDef{
						{From: "in0:v:0", To: "scale0:in:0", Type: "video"},
						{From: "scale0:out:0", To: "out0:v", Type: "video"},
					},
				},
				Outputs: []pipeline.Output{{
					ID: "out0", URL: "out.mp4",
					CodecVideo: "libx264",
					EncoderParamsVideo: map[string]any{
						"crf":    23,
						"preset": "fast",
					},
					FPSMode: "cfr",
				}},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rShort := Export(tc.cfg)

			def, warnings, err := pipeline.NormalizeConfig(tc.cfg)
			if err != nil {
				t.Fatalf("NormalizeConfig: %v", err)
			}
			rGraph := ExportGraph(tc.cfg, def, warnings)

			if rShort.Command != rGraph.Command {
				t.Errorf("round-trip mismatch:\n  Export:      %s\n  ExportGraph: %s\n  diff: %s",
					rShort.Command, rGraph.Command, diffArgs(rShort.Command, rGraph.Command))
			}
		})
	}
}

// diffArgs is a small helper that reports the first arg index where
// two ffmpeg command strings differ, with a few words of surrounding
// context. Returns "" when the commands are token-equal.
func diffArgs(a, b string) string {
	af := strings.Fields(a)
	bf := strings.Fields(b)
	n := len(af)
	if len(bf) < n {
		n = len(bf)
	}
	for i := 0; i < n; i++ {
		if af[i] != bf[i] {
			return "first diff at arg " + itoa(i) + ": " + quote(af[i]) + " vs " + quote(bf[i])
		}
	}
	if len(af) != len(bf) {
		return "length differs: " + itoa(len(af)) + " vs " + itoa(len(bf))
	}
	return ""
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func quote(s string) string { return "\"" + s + "\"" }

// TestExportGraph_ExplicitEncoderCoverage is the F1.3 acceptance
// gate. It exercises ExportGraph against graphs whose encoder nodes
// are user-authored (no Internal.Generated provenance) — covering
// codec + AVOption combinations the implicit-encoder path cannot
// produce on its own. For each case the round-trip identity
// `Export(cfg).Command == ExportGraph(cfg, NormalizeConfig(cfg)).Command`
// must hold, and the rendered command must contain the asserted
// per-output codec / AVOption flags.
//
// The implicit (synthesised) encoder path already covers shorthand
// like `Output.CodecVideo` + `EncoderParamsVideo`; these cases prove
// that an authored `encoder` node carrying the same payload through
// `NodeDef.Params` reaches the formatter via the graph-sourced view
// the same way.
func TestExportGraph_ExplicitEncoderCoverage(t *testing.T) {
	cases := []struct {
		name     string
		cfg      *pipeline.Config
		contains []string // substrings the rendered command must contain
	}{
		{
			name: "explicit_libx264_crf_preset",
			cfg: &pipeline.Config{
				SchemaVersion: "1.2",
				Inputs:        []pipeline.Input{{ID: "in0", URL: "in.mp4"}},
				Graph: pipeline.GraphDef{
					Nodes: []pipeline.NodeDef{
						{ID: "enc0", Type: "encoder", Params: map[string]any{
							"codec": "libx264", "crf": 22, "preset": "slow",
						}},
					},
					Edges: []pipeline.EdgeDef{
						{From: "in0:v:0", To: "enc0:in:0", Type: "video"},
						{From: "enc0:v", To: "out0:v", Type: "video"},
					},
				},
				Outputs: []pipeline.Output{{ID: "out0", URL: "out.mp4"}},
			},
			contains: []string{"-c:v libx264", "x264-params", "crf=22", "preset=slow"},
		},
		{
			name: "explicit_aac_profile",
			cfg: &pipeline.Config{
				SchemaVersion: "1.2",
				Inputs:        []pipeline.Input{{ID: "in0", URL: "in.mp4"}},
				Graph: pipeline.GraphDef{
					Nodes: []pipeline.NodeDef{
						{ID: "enca", Type: "encoder", Params: map[string]any{
							"codec": "aac", "profile": "aac_low", "b": "192k",
						}},
					},
					Edges: []pipeline.EdgeDef{
						{From: "in0:a:0", To: "enca:in:0", Type: "audio"},
						{From: "enca:a", To: "out0:a", Type: "audio"},
					},
				},
				Outputs: []pipeline.Output{{ID: "out0", URL: "out.mp4"}},
			},
			contains: []string{"-c:a aac", "-profile:a aac_low", "-b:a 192k"},
		},
		{
			name: "explicit_libx265_crf_preset",
			cfg: &pipeline.Config{
				SchemaVersion: "1.2",
				Inputs:        []pipeline.Input{{ID: "in0", URL: "in.mp4"}},
				Graph: pipeline.GraphDef{
					Nodes: []pipeline.NodeDef{
						{ID: "enc0", Type: "encoder", Params: map[string]any{
							"codec": "libx265", "crf": 28, "preset": "fast",
						}},
					},
					Edges: []pipeline.EdgeDef{
						{From: "in0:v:0", To: "enc0:in:0", Type: "video"},
						{From: "enc0:v", To: "out0:v", Type: "video"},
					},
				},
				Outputs: []pipeline.Output{{ID: "out0", URL: "out.mp4"}},
			},
			contains: []string{"-c:v libx265", "x265-params", "crf=28", "preset=fast"},
		},
		{
			// One explicit + one implicit encoder, distinct outputs.
			// The explicit libx265 video node lives in cfg.Graph;
			// the audio side is implicit-shorthand on out0 (no audio
			// encoder node, lowered by expandImplicitEncoders).
			name: "explicit_video_implicit_audio",
			cfg: &pipeline.Config{
				SchemaVersion: "1.2",
				Inputs:        []pipeline.Input{{ID: "in0", URL: "in.mp4"}},
				Graph: pipeline.GraphDef{
					Nodes: []pipeline.NodeDef{
						{ID: "encv", Type: "encoder", Params: map[string]any{
							"codec": "libx265", "crf": 24,
						}},
					},
					Edges: []pipeline.EdgeDef{
						{From: "in0:v:0", To: "encv:in:0", Type: "video"},
						{From: "encv:v", To: "out0:v", Type: "video"},
					},
				},
				Outputs: []pipeline.Output{{
					ID: "out0", URL: "out.mp4",
					CodecAudio: "aac",
					EncoderParamsAudio: map[string]any{
						"b": "128k",
					},
				}},
			},
			contains: []string{"-c:v libx265", "x265-params", "crf=24", "-c:a aac", "-b:a 128k"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rShort := Export(tc.cfg)

			def, warnings, err := pipeline.NormalizeConfig(tc.cfg)
			if err != nil {
				t.Fatalf("NormalizeConfig: %v", err)
			}
			rGraph := ExportGraph(tc.cfg, def, warnings)

			if rShort.Command != rGraph.Command {
				t.Errorf("round-trip mismatch:\n  Export:      %s\n  ExportGraph: %s\n  diff: %s",
					rShort.Command, rGraph.Command, diffArgs(rShort.Command, rGraph.Command))
			}
			for _, want := range tc.contains {
				if !strings.Contains(rGraph.Command, want) {
					t.Errorf("ExportGraph command missing %q\n  cmd: %s", want, rGraph.Command)
				}
			}
		})
	}
}
