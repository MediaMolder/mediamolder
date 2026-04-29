// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

import "testing"

// TestParseBSFAll verifies -bsf:v / -bsf:a / -bsf:s drain into the
// matching Output fields, including the chain syntax produced by
// av_bsf_list_parse_str (`f1[=k=v[:k=v]][,f2]`).
func TestParseBSFAll(t *testing.T) {
	cmd := `ffmpeg -i in.mp4 -c copy ` +
		`-bsf:v "h264_mp4toannexb,h264_redundant_pps" ` +
		`-bsf:a aac_adtstoasc ` +
		`-bsf:s "text2movsub" out.ts`
	cfg, err := Parse(cmd)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Outputs[0].BSFVideo != "h264_mp4toannexb,h264_redundant_pps" {
		t.Errorf("BSFVideo = %q", cfg.Outputs[0].BSFVideo)
	}
	if cfg.Outputs[0].BSFAudio != "aac_adtstoasc" {
		t.Errorf("BSFAudio = %q", cfg.Outputs[0].BSFAudio)
	}
	if cfg.Outputs[0].BSFSubtitle != "text2movsub" {
		t.Errorf("BSFSubtitle = %q", cfg.Outputs[0].BSFSubtitle)
	}
}

// TestParseBSFRequiresArg verifies the missing-argument error path
// for each of the three BSF flags.
func TestParseBSFRequiresArg(t *testing.T) {
	for _, flag := range []string{"-bsf:v", "-bsf:a", "-bsf:s"} {
		t.Run(flag, func(t *testing.T) {
			if _, err := Parse("ffmpeg -i in.mp4 " + flag); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}
