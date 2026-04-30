// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: GPL-3.0-or-later

package pipeline

import (
	"strings"
	"testing"
)

func baseAttachConfig(out Output) *Config {
	return &Config{
		SchemaVersion: "1.0",
		Inputs:        []Input{{ID: "in0", URL: "in.mkv"}},
		Outputs:       []Output{out},
		Graph:         GraphDef{Edges: []EdgeDef{{From: "in0:v:0", To: "out0:v", Type: "video"}}},
	}
}

func TestValidateAttachments_Valid(t *testing.T) {
	out := Output{
		ID: "out0", URL: "out.mkv", Format: "matroska", CodecVideo: "copy",
		Attachments: []Attachment{
			{Path: "/tmp/font.ttf", MimeType: "application/x-truetype-font"},
		},
	}
	if err := validate(baseAttachConfig(out)); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
}

func TestValidateAttachments_RejectsBadContainer(t *testing.T) {
	out := Output{
		ID: "out0", URL: "out.mp4", Format: "mp4", CodecVideo: "copy",
		Attachments: []Attachment{{Path: "/tmp/font.ttf"}},
	}
	err := validate(baseAttachConfig(out))
	if err == nil || !strings.Contains(err.Error(), "matroska") {
		t.Fatalf("expected container rejection, got %v", err)
	}
}

func TestValidateAttachments_RequiresPath(t *testing.T) {
	out := Output{
		ID: "out0", URL: "out.mkv", Format: "matroska", CodecVideo: "copy",
		Attachments: []Attachment{{Path: ""}},
	}
	err := validate(baseAttachConfig(out))
	if err == nil || !strings.Contains(err.Error(), "path is required") {
		t.Fatalf("expected path-required error, got %v", err)
	}
}
