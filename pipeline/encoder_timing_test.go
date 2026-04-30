// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"strings"
	"testing"
)

func TestParseEncoderTimeBase(t *testing.T) {
	cases := []struct {
		in        string
		wantNum   int
		wantDen   int
		wantSent  bool
		wantError bool
	}{
		{"", 0, 0, false, false},
		{"demux", encTimeBaseDemux, 0, true, false},
		{"filter", encTimeBaseFilter, 0, true, false},
		{"1/30000", 1, 30000, false, false},
		{"1:25", 1, 25, false, false},
		{"abc", 0, 0, false, true},
		{"-1/30", 0, 0, false, true},
		{"1/0", 0, 0, false, true},
		{"60", 0, 0, false, true},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			n, d, sent, err := parseEncoderTimeBase(c.in)
			if (err != nil) != c.wantError {
				t.Fatalf("err=%v, wantErr=%v", err, c.wantError)
			}
			if c.wantError {
				return
			}
			if n != c.wantNum || d != c.wantDen || sent != c.wantSent {
				t.Errorf("got (%d, %d, %v), want (%d, %d, %v)", n, d, sent, c.wantNum, c.wantDen, c.wantSent)
			}
		})
	}
}

func TestValidateEncoderTiming(t *testing.T) {
	cases := []struct {
		name    string
		out     Output
		wantErr string
	}{
		{
			name:    "valid_progressive",
			out:     Output{ID: "o", CodecVideo: "libx264", FieldOrder: "progressive"},
			wantErr: "",
		},
		{
			name:    "valid_interlaced_tt",
			out:     Output{ID: "o", CodecVideo: "libx264", FieldOrder: "tt", InterlacedEncode: true},
			wantErr: "",
		},
		{
			name:    "valid_enc_time_base_demux",
			out:     Output{ID: "o", CodecVideo: "libx264", EncoderTimeBase: "demux"},
			wantErr: "",
		},
		{
			name:    "valid_enc_time_base_rational",
			out:     Output{ID: "o", CodecVideo: "libx264", EncoderTimeBase: "1/30000"},
			wantErr: "",
		},
		{
			name:    "invalid_field_order",
			out:     Output{ID: "o", CodecVideo: "libx264", FieldOrder: "bogus"},
			wantErr: "invalid field_order",
		},
		{
			name:    "field_order_no_video_encoder",
			out:     Output{ID: "o", FieldOrder: "tt"},
			wantErr: "field_order requires a video encoder",
		},
		{
			name:    "interlaced_with_progressive_rejected",
			out:     Output{ID: "o", CodecVideo: "libx264", FieldOrder: "progressive", InterlacedEncode: true},
			wantErr: "interlaced_encode is incompatible",
		},
		{
			name:    "enc_time_base_no_encoder",
			out:     Output{ID: "o", EncoderTimeBase: "demux"},
			wantErr: "requires a video or audio encoder",
		},
		{
			name:    "enc_time_base_subtitle_only",
			out:     Output{ID: "o", CodecSubtitle: "mov_text", EncoderTimeBase: "demux"},
			wantErr: "not supported for subtitle outputs",
		},
		{
			name:    "enc_time_base_bad_rational",
			out:     Output{ID: "o", CodecVideo: "libx264", EncoderTimeBase: "abc"},
			wantErr: "want \"demux\"",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateEncoderTiming(c.out)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("err=%v, want substring %q", err, c.wantErr)
			}
		})
	}
}

func TestFieldOrderEnumValue(t *testing.T) {
	cases := map[string]int{
		"":            avFieldUnknown,
		"progressive": avFieldProgressive,
		"tt":          avFieldTT,
		"bb":          avFieldBB,
		"tb":          avFieldTB,
		"bt":          avFieldBT,
	}
	for s, want := range cases {
		got, ok := fieldOrderEnumValue(s)
		if !ok || got != want {
			t.Errorf("%q: got (%d, %v), want (%d, true)", s, got, ok, want)
		}
	}
	if _, ok := fieldOrderEnumValue("bogus"); ok {
		t.Error("bogus should be rejected")
	}
}
