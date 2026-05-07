// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

import (
	"strings"
	"testing"
)

// TestParseFullBackwardsCompat ensures the old Parse/ParseArgs signatures
// still work and return the concrete type unchanged.
func TestParseFullBackwardsCompat(t *testing.T) {
	cfg, err := Parse("ffmpeg -i in.mp4 out.mp4")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(cfg.Inputs) != 1 || cfg.Inputs[0].URL != "in.mp4" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

// parseFullAndExpect is a helper that calls ParseFull and asserts that every
// needle string appears in at least one Unsupported entry.
func parseFullAndExpect(t *testing.T, cmdline string, needles ...string) {
	t.Helper()
	res, err := ParseFull(cmdline)
	if err != nil {
		t.Fatalf("ParseFull(%q): %v", cmdline, err)
	}
	for _, needle := range needles {
		found := false
		for _, u := range res.Unsupported {
			if strings.Contains(u, needle) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected Unsupported to contain %q\n  got: %v", needle, res.Unsupported)
		}
	}
}

// ---- Wave 5 promoted flags ------------------------------------------------

func TestImportReport_Vsync(t *testing.T) {
	parseFullAndExpect(t,
		`ffmpeg -i in.mp4 -vsync cfr out.mp4`,
		"-vsync", "fps_mode",
	)
}

func TestImportReport_BSFVideo(t *testing.T) {
	parseFullAndExpect(t,
		`ffmpeg -i in.mp4 -bsf:v h264_mp4toannexb out.mp4`,
		"-bsf:v", "Output.BSFVideo", "Wave 5",
	)
}

func TestImportReport_BSFAudio(t *testing.T) {
	parseFullAndExpect(t,
		`ffmpeg -i in.mp4 -bsf:a aac_adtstoasc out.aac`,
		"-bsf:a", "Output.BSFAudio", "Wave 5",
	)
}

func TestImportReport_MuxDelay(t *testing.T) {
	parseFullAndExpect(t,
		`ffmpeg -i in.mp4 -muxdelay 0.7 out.mp4`,
		"-muxdelay", "Output.MuxDelay", "Wave 5",
	)
}

func TestImportReport_MuxPreload(t *testing.T) {
	parseFullAndExpect(t,
		`ffmpeg -i in.mp4 -muxpreload 0.5 out.mp4`,
		"-muxpreload", "Output.MuxPreload", "Wave 5",
	)
}

func TestImportReport_ITSOffset(t *testing.T) {
	parseFullAndExpect(t,
		`ffmpeg -itsoffset 0.030 -i in.mp4 out.mp4`,
		"-itsoffset", "Input.ITSOffset", "Wave 5",
	)
}

func TestImportReport_Re(t *testing.T) {
	parseFullAndExpect(t,
		`ffmpeg -re -i in.mp4 out.mp4`,
		"-re", "Input.ReadRate", "Wave 5",
	)
}

// ---- Wave 5 color metadata flags ------------------------------------------

func TestImportReport_ColorRange(t *testing.T) {
	parseFullAndExpect(t,
		`ffmpeg -i in.mp4 -color_range tv out.mp4`,
		"Output.Color", "Wave 5",
	)
}

// ---- Wave 6 promoted flags ------------------------------------------------

func TestImportReport_Async(t *testing.T) {
	parseFullAndExpect(t,
		`ffmpeg -i in.mp4 -async 1 out.mp4`,
		"-async", "Output.AudioSync", "Wave 6",
	)
}

func TestImportReport_ForceKeyFrames(t *testing.T) {
	parseFullAndExpect(t,
		`ffmpeg -i in.mp4 -force_key_frames "expr:gte(t,n_forced*2)" out.mp4`,
		"-force_key_frames", "Output.ForceKeyFrames", "Wave 6",
	)
}

func TestImportReport_EncTimeBase(t *testing.T) {
	parseFullAndExpect(t,
		`ffmpeg -i in.mp4 -enc_time_base demux out.mp4`,
		"-enc_time_base", "Output.EncoderTimeBase", "Wave 6",
	)
}

func TestImportReport_FieldOrder(t *testing.T) {
	parseFullAndExpect(t,
		`ffmpeg -i in.mp4 -field_order tt out.mp4`,
		"-field_order", "Output.FieldOrder", "Wave 6",
	)
}

func TestImportReport_FlagsInterlaced(t *testing.T) {
	parseFullAndExpect(t,
		`ffmpeg -i in.mp4 -flags +ildct+ilme out.mp4`,
		"Output.InterlacedEncode", "Wave 6",
	)
}

func TestImportReport_Attach(t *testing.T) {
	parseFullAndExpect(t,
		`ffmpeg -i in.mp4 -attach cover.jpg out.mkv`,
		"-attach", "Output.Attachments", "Wave 6",
	)
}

func TestImportReport_DoVi(t *testing.T) {
	parseFullAndExpect(t,
		`ffmpeg -i in.mp4 -dovi_profile 8 -dovi_bl_present 1 out.mp4`,
		"Output.HDR.DoVi", "Wave 6",
	)
}

// ---- Wave 7 promoted flags ------------------------------------------------

func TestImportReport_FilterComplexThreads(t *testing.T) {
	parseFullAndExpect(t,
		`ffmpeg -filter_complex_threads 4 -i in.mp4 out.mp4`,
		"-filter_complex_threads", "Config.FilterComplexThreads", "Wave 7",
	)
}

// ---- Wave 8 promoted flags ------------------------------------------------

func TestImportReport_SubCharenc(t *testing.T) {
	parseFullAndExpect(t,
		`ffmpeg -sub_charenc UTF-8 -i in.mkv out.mkv`,
		"-sub_charenc", "Input.SubtitleCharenc", "Wave 8",
	)
}

// ---- Deprecated / out-of-scope flags ----------------------------------------

func TestImportReport_Deinterlace(t *testing.T) {
	parseFullAndExpect(t,
		`ffmpeg -i in.mp4 -deinterlace out.mp4`,
		"-deinterlace", "yadif",
	)
}

func TestImportReport_Target(t *testing.T) {
	parseFullAndExpect(t,
		`ffmpeg -i in.mp4 -target DVD out.mpg`,
		"-target", "not supported",
	)
}

func TestImportReport_Fpre(t *testing.T) {
	parseFullAndExpect(t,
		`ffmpeg -i in.mp4 -vpre high out.mp4`,
		"-vpre", "not supported",
	)
}

func TestImportReport_Xerror(t *testing.T) {
	parseFullAndExpect(t,
		`ffmpeg -xerror -i in.mp4 out.mp4`,
		"-xerror", "event bus",
	)
}

func TestImportReport_Stats(t *testing.T) {
	parseFullAndExpect(t,
		`ffmpeg -nostats -i in.mp4 out.mp4`,
		"-nostats", "out-of-scope",
	)
}

func TestImportReport_DebugTS(t *testing.T) {
	parseFullAndExpect(t,
		`ffmpeg -debug_ts -i in.mp4 out.mp4`,
		"-debug_ts", "debugging flag",
	)
}

// TestImportReport_MultipleWarnings verifies that a command with several
// flagged options accumulates multiple Unsupported entries.
func TestImportReport_MultipleWarnings(t *testing.T) {
	res, err := ParseFull(`ffmpeg -re -i in.mp4 -bsf:v h264_mp4toannexb -muxdelay 0.7 out.mp4`)
	if err != nil {
		t.Fatalf("ParseFull: %v", err)
	}
	if len(res.Unsupported) < 3 {
		t.Errorf("expected at least 3 Unsupported entries, got %d: %v", len(res.Unsupported), res.Unsupported)
	}
}

// TestImportReport_CleanCommand verifies that a plain command emits no notes.
func TestImportReport_CleanCommand(t *testing.T) {
	res, err := ParseFull(`ffmpeg -i in.mp4 -c:v libx264 -crf 23 out.mp4`)
	if err != nil {
		t.Fatalf("ParseFull: %v", err)
	}
	if len(res.Unsupported) != 0 {
		t.Errorf("expected no Unsupported entries for clean command, got: %v", res.Unsupported)
	}
}
