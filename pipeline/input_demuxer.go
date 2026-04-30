// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

// input_demuxer.go — Wave 5 #23-#28: typed input-side demuxer fields,
// force-demuxer, raw-stream / concat helpers, accurate-seek and
// thread-queue / protocol-whitelist / pattern-type plumbing. The
// runtime consumer lives in pipeline/handlers.go::openSource;
// validation here keeps the schema layer in sync with libavformat's
// expectations so a malformed JSON fails fast with a clear error
// instead of dying inside avformat_open_input.

import (
	"fmt"
	"strconv"
	"strings"
)

// rawAudioFormats is the set of FFmpeg force-demuxers that consume
// raw PCM and therefore require SampleRate + Channels (and accept an
// optional SampleFormat). Mirrors libavformat/pcmdec.c. Not exhaustive
// — extra entries fall through to the "needs video geometry" branch
// so a typo is rejected loudly rather than silently mis-validated.
var rawAudioFormats = map[string]bool{
	"s8": true, "u8": true,
	"s16le": true, "s16be": true,
	"s24le": true, "s24be": true,
	"s32le": true, "s32be": true,
	"f32le": true, "f32be": true,
	"f64le": true, "f64be": true,
	"alaw": true, "mulaw": true,
}

// rawVideoFormats is the set of FFmpeg force-demuxers that consume
// unframed video bytestreams and therefore require PixelFormat +
// VideoSize + FrameRate. Mirrors libavformat/rawvideodec.c +
// img2dec.c (the latter accepts pattern_type instead of geometry but
// still benefits from an explicit framerate).
var rawVideoFormats = map[string]bool{
	"rawvideo":     true,
	"yuv4mpegpipe": true,
}

// validPatternTypes is the closed set of `-pattern_type` values
// accepted by libavformat/img2dec.c.
var validPatternTypes = map[string]bool{
	"":              true,
	"none":          true,
	"sequence":      true,
	"glob":          true,
	"glob_sequence": true,
}

// validateInputDemuxerFields enforces the per-Input typed demuxer
// invariants introduced in Wave 5 of the parity plan. Called from
// validate() once per input.
func validateInputDemuxerFields(inp Input) error {
	if inp.FrameRate < 0 {
		return fmt.Errorf("input %q: invalid framerate %g (must be >= 0)", inp.ID, inp.FrameRate)
	}
	if inp.SampleRate < 0 {
		return fmt.Errorf("input %q: invalid sample_rate %d (must be >= 0)", inp.ID, inp.SampleRate)
	}
	if inp.Channels < 0 {
		return fmt.Errorf("input %q: invalid channels %d (must be >= 0)", inp.ID, inp.Channels)
	}
	if inp.ThreadQueueSize < 0 {
		return fmt.Errorf("input %q: invalid thread_queue_size %d (must be >= 0)", inp.ID, inp.ThreadQueueSize)
	}
	if !validPatternTypes[inp.PatternType] {
		return fmt.Errorf("input %q: invalid pattern_type %q (want one of \"\", \"none\", \"sequence\", \"glob\", \"glob_sequence\")", inp.ID, inp.PatternType)
	}
	for _, p := range inp.ProtocolWhitelist {
		if strings.TrimSpace(p) == "" {
			return fmt.Errorf("input %q: protocol_whitelist contains empty entry", inp.ID)
		}
		if strings.ContainsAny(p, " ,\t\n") {
			return fmt.Errorf("input %q: protocol_whitelist entry %q contains whitespace or comma (one protocol per slice element)", inp.ID, p)
		}
	}
	if inp.VideoSize != "" {
		if err := validateVideoSize(inp.VideoSize); err != nil {
			return fmt.Errorf("input %q: video_size: %w", inp.ID, err)
		}
	}
	switch inp.Kind {
	case "raw":
		if inp.Format == "" {
			return fmt.Errorf("input %q: kind=\"raw\" requires format (e.g. \"rawvideo\", \"s16le\")", inp.ID)
		}
		switch {
		case rawAudioFormats[inp.Format]:
			if inp.SampleRate == 0 {
				return fmt.Errorf("input %q: kind=\"raw\" format=%q requires sample_rate", inp.ID, inp.Format)
			}
			if inp.Channels == 0 {
				return fmt.Errorf("input %q: kind=\"raw\" format=%q requires channels", inp.ID, inp.Format)
			}
		case rawVideoFormats[inp.Format]:
			if inp.PixelFormat == "" {
				return fmt.Errorf("input %q: kind=\"raw\" format=%q requires pixel_format", inp.ID, inp.Format)
			}
			if inp.VideoSize == "" {
				return fmt.Errorf("input %q: kind=\"raw\" format=%q requires video_size", inp.ID, inp.Format)
			}
			if inp.FrameRate == 0 {
				return fmt.Errorf("input %q: kind=\"raw\" format=%q requires framerate", inp.ID, inp.Format)
			}
		default:
			return fmt.Errorf("input %q: kind=\"raw\" format %q is not a recognised raw demuxer (want one of rawvideo / yuv4mpegpipe / s16le / s24le / s32le / f32le / f64le / u8 / s8 / alaw / mulaw)", inp.ID, inp.Format)
		}
	case "concat":
		if len(inp.ConcatList) == 0 && inp.Format == "" {
			// Allow URL-pointing-at-existing-listfile; require Format=concat
			// be explicit so the user opts in.
			return fmt.Errorf("input %q: kind=\"concat\" requires concat_list entries (or set format=\"concat\" and point url at an existing listfile)", inp.ID)
		}
		for i, ent := range inp.ConcatList {
			if ent.File == "" {
				return fmt.Errorf("input %q: concat_list[%d] missing file", inp.ID, i)
			}
			if strings.Contains(ent.File, "'") {
				return fmt.Errorf("input %q: concat_list[%d] file %q contains a single quote (not supported by the concat demuxer's listfile grammar)", inp.ID, i, ent.File)
			}
			if strings.ContainsAny(ent.File, "\r\n") {
				return fmt.Errorf("input %q: concat_list[%d] file contains a newline", inp.ID, i)
			}
			if ent.Duration < 0 {
				return fmt.Errorf("input %q: concat_list[%d] duration %g must be >= 0", inp.ID, i, ent.Duration)
			}
			if ent.InPoint < 0 {
				return fmt.Errorf("input %q: concat_list[%d] inpoint %g must be >= 0", inp.ID, i, ent.InPoint)
			}
			if ent.OutPoint < 0 {
				return fmt.Errorf("input %q: concat_list[%d] outpoint %g must be >= 0", inp.ID, i, ent.OutPoint)
			}
			if ent.OutPoint > 0 && ent.OutPoint <= ent.InPoint {
				return fmt.Errorf("input %q: concat_list[%d] outpoint %g must be > inpoint %g", inp.ID, i, ent.OutPoint, ent.InPoint)
			}
		}
	default:
		// "", "file", "lavfi" — geometry/raw fields are advisory only;
		// the underlying demuxer ignores them.
		if len(inp.ConcatList) > 0 {
			return fmt.Errorf("input %q: concat_list is only valid when kind=\"concat\"", inp.ID)
		}
	}
	return nil
}

// validateVideoSize accepts either a named libavutil preset
// (resolved by av_parse_video_size at open time) or an explicit
// WxH form. We don't carry a preset table here — the runtime
// delegates the final check to libavutil — but we reject obviously
// malformed strings so a typo fails at parse time rather than
// inside avformat_open_input.
func validateVideoSize(s string) error {
	if s == "" {
		return nil
	}
	// Lowercase WxH (libavutil also accepts whitespace; we keep it strict).
	if i := strings.IndexAny(s, "xX"); i > 0 && i < len(s)-1 {
		w, errW := strconv.Atoi(s[:i])
		h, errH := strconv.Atoi(s[i+1:])
		if errW == nil && errH == nil {
			if w <= 0 || h <= 0 {
				return fmt.Errorf("invalid size %q (width and height must be positive)", s)
			}
			return nil
		}
	}
	// Named preset (hd720, vga, ntsc, …) — require at least one
	// non-x/X alphabetic character so that obvious WxH typos like
	// "1920x" or "x1080" don't slip through as a "preset name".
	hasNamedAlpha := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z' && r != 'x', r >= 'A' && r <= 'Z' && r != 'X':
			hasNamedAlpha = true
		case r >= '0' && r <= '9', r == '_', r == '-', r == 'x', r == 'X':
		default:
			return fmt.Errorf("invalid size %q (want WxH or a libavutil named preset like \"hd720\")", s)
		}
	}
	if !hasNamedAlpha {
		return fmt.Errorf("invalid size %q (want WxH or a libavutil named preset like \"hd720\")", s)
	}
	return nil
}
