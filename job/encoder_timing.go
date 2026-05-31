// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"fmt"
	"strconv"
	"strings"
)

// AVFieldOrder enum mirror (libavcodec/defs.h::AVFieldOrder).
const (
	avFieldUnknown     = 0
	avFieldProgressive = 1
	avFieldTT          = 2
	avFieldBB          = 3
	avFieldTB          = 4
	avFieldBT          = 5
)

// FieldOrder string -> AVFieldOrder enum mapping.
var fieldOrderEnum = map[string]int{
	"":            avFieldUnknown,
	"progressive": avFieldProgressive,
	"tt":          avFieldTT,
	"bb":          avFieldBB,
	"tb":          avFieldTB,
	"bt":          avFieldBT,
}

// fieldOrderEnumValue returns the AVFieldOrder enum value for the given
// FieldOrder string. The empty string maps to AV_FIELD_UNKNOWN.
func fieldOrderEnumValue(s string) (int, bool) {
	v, ok := fieldOrderEnum[s]
	return v, ok
}

// ENC_TIME_BASE sentinels mirroring fftools/ffmpeg.h. The pipeline
// runtime resolves these to the upstream demuxer / buffersink TB at
// encoder-open time.
const (
	encTimeBaseDemux  = -1 // ENC_TIME_BASE_DEMUX  (num=-1, den=0)
	encTimeBaseFilter = -2 // ENC_TIME_BASE_FILTER (num=-2, den=0)
)

// parseEncoderTimeBase decodes Output.EncoderTimeBase into its
// sentinel-or-rational form. Returns (num, den, isSentinel, err).
// Empty input returns (0, 0, false, nil) so the caller can treat the
// field as unset. Mirrors fftools/ffmpeg_mux_init.c L1395-1417.
func parseEncoderTimeBase(s string) (int, int, bool, error) {
	switch s {
	case "":
		return 0, 0, false, nil
	case "demux":
		return encTimeBaseDemux, 0, true, nil
	case "filter":
		return encTimeBaseFilter, 0, true, nil
	}
	// "N/D" rational. We accept both "N/D" and "N:D" for symmetry
	// with the SAR/DAR parser (av_parse_ratio also accepts ":").
	sep := strings.IndexAny(s, "/:")
	if sep <= 0 || sep >= len(s)-1 {
		// Try plain integer (e.g. "60") -> 1/N (the FFmpeg default
		// when the user passes a single number is the framerate
		// reciprocal). We instead reject the bare integer form
		// here and require the explicit N/D so the user can't
		// accidentally invert the ratio.
		return 0, 0, false, fmt.Errorf("encoder_time_base %q: want \"demux\", \"filter\", or N/D rational", s)
	}
	n, err := strconv.Atoi(s[:sep])
	if err != nil || n <= 0 {
		return 0, 0, false, fmt.Errorf("encoder_time_base %q: numerator must be positive integer", s)
	}
	d, err := strconv.Atoi(s[sep+1:])
	if err != nil || d <= 0 {
		return 0, 0, false, fmt.Errorf("encoder_time_base %q: denominator must be positive integer", s)
	}
	return n, d, false, nil
}

// validateEncoderTiming covers the new Output.EncoderTimeBase /
// FieldOrder / InterlacedEncode triple (Wave 6 #33). Subtitles cannot
// carry encoder timing overrides (FFmpeg also rejects
// `-enc_time_base` on subtitle outputs at
// fftools/ffmpeg_mux_init.c L1392-1394).
func validateEncoderTiming(out Output) error {
	if out.EncoderTimeBase != "" {
		if out.CodecSubtitle != "" && out.CodecVideo == "" && out.CodecAudio == "" {
			return fmt.Errorf("output %q: encoder_time_base not supported for subtitle outputs", out.ID)
		}
		if out.CodecVideo == "" && out.CodecAudio == "" {
			return fmt.Errorf("output %q: encoder_time_base requires a video or audio encoder", out.ID)
		}
		if _, _, _, err := parseEncoderTimeBase(out.EncoderTimeBase); err != nil {
			return fmt.Errorf("output %q: %w", out.ID, err)
		}
	}
	if out.FieldOrder != "" {
		if _, ok := fieldOrderEnumValue(out.FieldOrder); !ok {
			return fmt.Errorf("output %q: invalid field_order %q (want one of progressive, tt, bb, tb, bt)", out.ID, out.FieldOrder)
		}
		if out.CodecVideo == "" {
			return fmt.Errorf("output %q: field_order requires a video encoder", out.ID)
		}
	}
	if out.InterlacedEncode {
		if out.CodecVideo == "" {
			return fmt.Errorf("output %q: interlaced_encode requires a video encoder", out.ID)
		}
		if out.FieldOrder == "progressive" {
			return fmt.Errorf("output %q: interlaced_encode is incompatible with field_order=progressive", out.ID)
		}
	}
	return nil
}
