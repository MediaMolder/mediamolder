// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

import "testing"

// TestParseMetadataContainer covers the bare `-metadata key=value`
// form, which lands on Output.Metadata (mirrors
// fftools/ffmpeg_mux_init.c::of_add_metadata for the global scope).
func TestParseMetadataContainer(t *testing.T) {
	cfg, err := Parse(`ffmpeg -i in.mp4 -c copy -metadata title=Show -metadata artist=MM out.mkv`)
	if err != nil {
		t.Fatal(err)
	}
	out := cfg.Outputs[0]
	if got := out.Metadata["title"]; got != "Show" {
		t.Errorf("metadata.title = %q, want %q", got, "Show")
	}
	if got := out.Metadata["artist"]; got != "MM" {
		t.Errorf("metadata.artist = %q, want %q", got, "MM")
	}
}

// TestParseMetadataPerStream covers `-metadata:s:<type>:<idx>
// key=value`, which accumulates into Output.Streams entries.
func TestParseMetadataPerStream(t *testing.T) {
	cfg, err := Parse(`ffmpeg -i in.mp4 -c copy -metadata:s:a:0 language=eng -metadata:s:a:0 title=Main -metadata:s:v:0 language=und out.mkv`)
	if err != nil {
		t.Fatal(err)
	}
	out := cfg.Outputs[0]
	if len(out.Streams) != 2 {
		t.Fatalf("Output.Streams = %d, want 2 (%+v)", len(out.Streams), out.Streams)
	}
	// Sorted by "<type>:<idx>": "a:0" < "v:0".
	if out.Streams[0].Type != "a" || out.Streams[0].Index != 0 {
		t.Errorf("Streams[0] = %+v, want a:0", out.Streams[0])
	}
	if got := out.Streams[0].Metadata["language"]; got != "eng" {
		t.Errorf("a:0 language = %q, want eng", got)
	}
	if got := out.Streams[0].Metadata["title"]; got != "Main" {
		t.Errorf("a:0 title = %q, want Main", got)
	}
	if out.Streams[1].Type != "v" || out.Streams[1].Index != 0 {
		t.Errorf("Streams[1] = %+v, want v:0", out.Streams[1])
	}
	if got := out.Streams[1].Metadata["language"]; got != "und" {
		t.Errorf("v:0 language = %q, want und", got)
	}
}

// TestParseDispositionPerStream covers `-disposition:s:<type>:<idx>
// flags`, the FFmpeg flag for tagging a stream as
// default/forced/hearing_impaired/etc.
func TestParseDispositionPerStream(t *testing.T) {
	cfg, err := Parse(`ffmpeg -i in.mp4 -c copy -disposition:s:v:0 default+forced -disposition:s:a:1 hearing_impaired out.mkv`)
	if err != nil {
		t.Fatal(err)
	}
	out := cfg.Outputs[0]
	if len(out.Streams) != 2 {
		t.Fatalf("Output.Streams = %d, want 2 (%+v)", len(out.Streams), out.Streams)
	}
	// Sorted: "a:1" < "v:0".
	if out.Streams[0].Type != "a" || out.Streams[0].Index != 1 || out.Streams[0].Disposition != "hearing_impaired" {
		t.Errorf("Streams[0] = %+v, want a:1 hearing_impaired", out.Streams[0])
	}
	if out.Streams[1].Type != "v" || out.Streams[1].Index != 0 || out.Streams[1].Disposition != "default+forced" {
		t.Errorf("Streams[1] = %+v, want v:0 default+forced", out.Streams[1])
	}
}

// TestParseStreamSpecRejectsInvalid covers the validation paths in
// parseStreamSpec — bad type letter, non-numeric index, missing kv.
func TestParseStreamSpecRejectsInvalid(t *testing.T) {
	cases := []string{
		`ffmpeg -i in.mp4 -metadata:s:x:0 language=eng out.mkv`,
		`ffmpeg -i in.mp4 -metadata:s:a:abc language=eng out.mkv`,
		`ffmpeg -i in.mp4 -metadata:s:a:0 nosuchequals out.mkv`,
		`ffmpeg -i in.mp4 -disposition:s:x:0 default out.mkv`,
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			if _, err := Parse(c); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}
