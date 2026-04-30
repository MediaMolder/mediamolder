// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"os"
	"strings"
	"testing"
)

func newCfg(in Input) *Config {
	return &Config{
		SchemaVersion: "1.1",
		Inputs:        []Input{in},
		Outputs: []Output{{
			ID:  "o0",
			URL: "out.mp4",
		}},
	}
}

func TestValidateInput_KindEnum(t *testing.T) {
	for _, k := range []string{"", "file", "lavfi", "raw", "concat"} {
		t.Run("ok/"+k, func(t *testing.T) {
			in := Input{ID: "in0", URL: "x", Kind: k, Streams: nil}
			switch k {
			case "raw":
				in.Format = "rawvideo"
				in.PixelFormat = "yuv420p"
				in.VideoSize = "320x240"
				in.FrameRate = 30
			case "concat":
				in.ConcatList = []ConcatEntry{{File: "a.mp4"}}
			}
			if err := validate(newCfg(in)); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
	t.Run("bad", func(t *testing.T) {
		in := Input{ID: "in0", URL: "x", Kind: "bogus"}
		if err := validate(newCfg(in)); err == nil || !strings.Contains(err.Error(), "invalid kind") {
			t.Fatalf("want invalid kind error, got %v", err)
		}
	})
}

func TestValidateInput_RawRequiresGeometry(t *testing.T) {
	in := Input{ID: "in0", URL: "x", Kind: "raw", Format: "rawvideo"}
	err := validate(newCfg(in))
	if err == nil || !strings.Contains(err.Error(), "pixel_format") {
		t.Fatalf("want missing pixel_format, got %v", err)
	}

	in.PixelFormat = "yuv420p"
	err = validate(newCfg(in))
	if err == nil || !strings.Contains(err.Error(), "video_size") {
		t.Fatalf("want missing video_size, got %v", err)
	}

	in.VideoSize = "320x240"
	err = validate(newCfg(in))
	if err == nil || !strings.Contains(err.Error(), "framerate") {
		t.Fatalf("want missing framerate, got %v", err)
	}

	in.FrameRate = 30
	if err := validate(newCfg(in)); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestValidateInput_RawAudioRequiresRateChannels(t *testing.T) {
	in := Input{ID: "in0", URL: "x", Kind: "raw", Format: "s16le"}
	err := validate(newCfg(in))
	if err == nil || !strings.Contains(err.Error(), "sample_rate") {
		t.Fatalf("want missing sample_rate, got %v", err)
	}
	in.SampleRate = 48000
	err = validate(newCfg(in))
	if err == nil || !strings.Contains(err.Error(), "channels") {
		t.Fatalf("want missing channels, got %v", err)
	}
	in.Channels = 2
	if err := validate(newCfg(in)); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestValidateInput_RawUnknownFormatRejected(t *testing.T) {
	in := Input{ID: "in0", URL: "x", Kind: "raw", Format: "matroska"}
	err := validate(newCfg(in))
	if err == nil || !strings.Contains(err.Error(), "not a recognised raw demuxer") {
		t.Fatalf("want raw-format rejection, got %v", err)
	}
}

func TestValidateInput_ConcatRequiresList(t *testing.T) {
	in := Input{ID: "in0", URL: "x", Kind: "concat"}
	err := validate(newCfg(in))
	if err == nil || !strings.Contains(err.Error(), "concat_list") {
		t.Fatalf("want concat_list error, got %v", err)
	}
	in.ConcatList = []ConcatEntry{{File: "a.mp4"}, {File: "b.mp4", Duration: 5, InPoint: 1, OutPoint: 4}}
	if err := validate(newCfg(in)); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestValidateInput_ConcatRejectsApostrophe(t *testing.T) {
	in := Input{ID: "in0", URL: "x", Kind: "concat", ConcatList: []ConcatEntry{{File: "a'b.mp4"}}}
	err := validate(newCfg(in))
	if err == nil || !strings.Contains(err.Error(), "single quote") {
		t.Fatalf("want apostrophe rejection, got %v", err)
	}
}

func TestValidateInput_ConcatOutpointAfterInpoint(t *testing.T) {
	in := Input{ID: "in0", URL: "x", Kind: "concat", ConcatList: []ConcatEntry{{File: "a.mp4", InPoint: 5, OutPoint: 4}}}
	err := validate(newCfg(in))
	if err == nil || !strings.Contains(err.Error(), "outpoint") {
		t.Fatalf("want outpoint error, got %v", err)
	}
}

func TestValidateInput_PatternTypeEnum(t *testing.T) {
	in := Input{ID: "in0", URL: "x", PatternType: "wat"}
	err := validate(newCfg(in))
	if err == nil || !strings.Contains(err.Error(), "pattern_type") {
		t.Fatalf("want pattern_type error, got %v", err)
	}
	in.PatternType = "glob"
	if err := validate(newCfg(in)); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestValidateInput_VideoSizeWxH(t *testing.T) {
	in := Input{ID: "in0", URL: "x", VideoSize: "1920x1080"}
	if err := validate(newCfg(in)); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	in.VideoSize = "hd720"
	if err := validate(newCfg(in)); err != nil {
		t.Fatalf("unexpected (named preset): %v", err)
	}
	in.VideoSize = "1920x"
	if err := validate(newCfg(in)); err == nil {
		t.Fatalf("want invalid size error")
	}
	in.VideoSize = "0x1080"
	if err := validate(newCfg(in)); err == nil || !strings.Contains(err.Error(), "positive") {
		t.Fatalf("want positive-dim error, got %v", err)
	}
}

func TestValidateInput_ProtocolWhitelist(t *testing.T) {
	in := Input{ID: "in0", URL: "x", ProtocolWhitelist: []string{"file", " "}}
	err := validate(newCfg(in))
	if err == nil || !strings.Contains(err.Error(), "empty entry") {
		t.Fatalf("want empty-entry error, got %v", err)
	}
	in.ProtocolWhitelist = []string{"file,tcp"}
	err = validate(newCfg(in))
	if err == nil || !strings.Contains(err.Error(), "comma") {
		t.Fatalf("want comma error, got %v", err)
	}
}

func TestValidateInput_NumericRanges(t *testing.T) {
	cases := []struct {
		field string
		mut   func(*Input)
		want  string
	}{
		{"framerate", func(i *Input) { i.FrameRate = -1 }, "framerate"},
		{"sample_rate", func(i *Input) { i.SampleRate = -1 }, "sample_rate"},
		{"channels", func(i *Input) { i.Channels = -1 }, "channels"},
		{"thread_queue_size", func(i *Input) { i.ThreadQueueSize = -1 }, "thread_queue_size"},
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			in := Input{ID: "in0", URL: "x"}
			tc.mut(&in)
			err := validate(newCfg(in))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

func TestValidateInput_ConcatListRequiresConcatKind(t *testing.T) {
	in := Input{ID: "in0", URL: "x", Kind: "file", ConcatList: []ConcatEntry{{File: "a.mp4"}}}
	err := validate(newCfg(in))
	if err == nil || !strings.Contains(err.Error(), "kind=\"concat\"") {
		t.Fatalf("want kind-check, got %v", err)
	}
}

func TestMaterialiseConcatList_Roundtrip(t *testing.T) {
	list := []ConcatEntry{
		{File: "a.mp4"},
		{File: "b.mp4", Duration: 5.5, InPoint: 1.25, OutPoint: 4.75, Metadata: map[string]string{"k": "v"}},
	}
	path, cleanup, err := materialiseConcatList(list)
	if err != nil {
		t.Fatalf("materialise: %v", err)
	}
	defer cleanup()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(b)
	want := []string{
		"ffconcat version 1.0",
		"file 'a.mp4'",
		"file 'b.mp4'",
		"duration 5.5",
		"inpoint 1.25",
		"outpoint 4.75",
		"file_packet_metadata k=v",
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("listfile missing %q\n--- got ---\n%s", w, got)
		}
	}
}

func readFile(p string) (string, error) {
	b, err := os.ReadFile(p)
	return string(b), err
}
