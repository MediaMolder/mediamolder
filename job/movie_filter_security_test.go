// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"strings"
	"testing"
)

// Wave 7 #36e — `movie` / `amovie` security validator.

func TestValidateMovieFilterParams_AcceptsValidFilename(t *testing.T) {
	cfg := &Config{Graph: GraphDef{Nodes: []NodeDef{
		{ID: "logo", Type: "filter_source", Filter: "movie", Params: map[string]any{"filename": "../testdata/sample.jpg"}},
	}}}
	if err := validateMovieFilterParams(cfg); err != nil {
		t.Fatalf("expected accept, got %v", err)
	}
}

func TestValidateMovieFilterParams_AcceptsPositionalFilename(t *testing.T) {
	cfg := &Config{Graph: GraphDef{Nodes: []NodeDef{
		{ID: "logo", Type: "filter_source", Filter: "amovie", Params: map[string]any{"_pos0": "track.flac"}},
	}}}
	if err := validateMovieFilterParams(cfg); err != nil {
		t.Fatalf("expected accept, got %v", err)
	}
}

func TestValidateMovieFilterParams_RejectsMissingFilename(t *testing.T) {
	cfg := &Config{Graph: GraphDef{Nodes: []NodeDef{
		{ID: "logo", Type: "filter_source", Filter: "movie"},
	}}}
	err := validateMovieFilterParams(cfg)
	if err == nil || !strings.Contains(err.Error(), "filename") {
		t.Fatalf("expected filename error, got %v", err)
	}
}

func TestValidateMovieFilterParams_RejectsEmptyFilename(t *testing.T) {
	cfg := &Config{Graph: GraphDef{Nodes: []NodeDef{
		{ID: "logo", Type: "filter_source", Filter: "movie", Params: map[string]any{"filename": "  "}},
	}}}
	if err := validateMovieFilterParams(cfg); err == nil {
		t.Fatalf("expected error for empty filename")
	}
}

func TestValidateMovieFilterParams_RejectsControlChars(t *testing.T) {
	for _, bad := range []string{"foo\x00bar.png", "foo\nbar.png", "foo\rbar.png"} {
		cfg := &Config{Graph: GraphDef{Nodes: []NodeDef{
			{ID: "logo", Type: "filter_source", Filter: "movie", Params: map[string]any{"filename": bad}},
		}}}
		err := validateMovieFilterParams(cfg)
		if err == nil || !strings.Contains(err.Error(), "NUL, CR or LF") {
			t.Fatalf("filename %q: expected control-char error, got %v", bad, err)
		}
	}
}

func TestValidateMovieFilterParams_RejectsEmptyProtocolWhitelist(t *testing.T) {
	cfg := &Config{Graph: GraphDef{Nodes: []NodeDef{
		{ID: "logo", Type: "filter_source", Filter: "movie", Params: map[string]any{
			"filename":           "x.png",
			"protocol_whitelist": "",
		}},
	}}}
	if err := validateMovieFilterParams(cfg); err == nil {
		t.Fatalf("expected error for empty protocol_whitelist")
	}
}

func TestValidateMovieFilterParams_RejectsEmptyProtocolEntry(t *testing.T) {
	cfg := &Config{Graph: GraphDef{Nodes: []NodeDef{
		{ID: "logo", Type: "filter_source", Filter: "movie", Params: map[string]any{
			"filename":           "x.png",
			"protocol_whitelist": "file,,http",
		}},
	}}}
	if err := validateMovieFilterParams(cfg); err == nil {
		t.Fatalf("expected error for empty protocol_whitelist entry")
	}
}

func TestValidateMovieFilterParams_IgnoresNonMovieNodes(t *testing.T) {
	cfg := &Config{Graph: GraphDef{Nodes: []NodeDef{
		{ID: "src", Type: "filter_source", Filter: "color"},
		{ID: "f", Type: "filter", Filter: "scale"},
	}}}
	if err := validateMovieFilterParams(cfg); err != nil {
		t.Fatalf("expected accept for non-movie nodes, got %v", err)
	}
}

func TestMovieFilterParamsForSpec_InjectsFormatOpts(t *testing.T) {
	out := movieFilterParamsForSpec("movie", map[string]any{
		"filename":           "logo.png",
		"protocol_whitelist": "file,crypto",
	})
	if _, ok := out["protocol_whitelist"]; ok {
		t.Fatalf("protocol_whitelist should be removed from spec params, got %v", out)
	}
	fo, _ := out["format_opts"].(string)
	if fo != "protocol_whitelist=file,crypto" {
		t.Fatalf("unexpected format_opts: %q", fo)
	}
	if out["filename"] != "logo.png" {
		t.Fatalf("filename lost: %v", out)
	}
}

func TestMovieFilterParamsForSpec_LeavesExistingFormatOpts(t *testing.T) {
	in := map[string]any{
		"filename":           "logo.png",
		"protocol_whitelist": "file",
		"format_opts":        "user_value=1",
	}
	out := movieFilterParamsForSpec("movie", in)
	// User opted in to raw format_opts — leave their map alone.
	if out["format_opts"] != "user_value=1" {
		t.Fatalf("format_opts overwritten: %v", out["format_opts"])
	}
}

func TestMovieFilterParamsForSpec_PassThroughForOtherFilters(t *testing.T) {
	in := map[string]any{"protocol_whitelist": "file"}
	out := movieFilterParamsForSpec("color", in)
	if out["protocol_whitelist"] != "file" {
		t.Fatalf("non-movie filter params should be untouched, got %v", out)
	}
}
