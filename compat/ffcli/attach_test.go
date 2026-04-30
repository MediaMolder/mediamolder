// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

import "testing"

func TestParseAttachFlag(t *testing.T) {
	cfg, err := ParseArgs([]string{
		"-i", "in.mkv",
		"-c:v", "copy",
		"-attach", "fonts/Arial.ttf",
		"-attach", "fonts/Verdana.ttf",
		"-f", "matroska", "out.mkv",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out := cfg.Outputs[0]
	if len(out.Attachments) != 2 {
		t.Fatalf("want 2 attachments, got %d: %+v", len(out.Attachments), out.Attachments)
	}
	if out.Attachments[0].Path != "fonts/Arial.ttf" || out.Attachments[1].Path != "fonts/Verdana.ttf" {
		t.Fatalf("attachment order wrong: %+v", out.Attachments)
	}
}

func TestParseAttachRequiresArg(t *testing.T) {
	if _, err := ParseArgs([]string{"-i", "in.mkv", "-attach"}); err == nil {
		t.Fatalf("expected -attach to require an argument")
	}
}
