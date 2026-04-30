// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

// input_demuxer.go — Wave 5 #23-#28: drain per-file flags latched
// into pendingFileOpts onto the typed Input/Output fields after the
// `-i` / output URL marker is seen. Mirrors fftools/ffmpeg_opt.c
// where the per-file OptionsContext bag is consumed by either
// open_input_file or open_output_file depending on which file
// specifier comes next.

import (
	"strconv"
	"strings"

	"github.com/MediaMolder/MediaMolder/pipeline"
)

// drainTypedInputDemuxer extracts the input-side typed keys
// (`__format`, `__r`, `framerate`, `pix_fmt`, `pixel_format`,
// `video_size`, `ar`, `ac`, `sample_fmt`, `thread_queue_size`,
// `protocol_whitelist`, `pattern_type`, `accurate_seek`,
// `seek_timestamp`) out of opts and into the matching typed Input
// field, deleting the key from opts on success. Unknown keys stay
// in opts so legacy AVDict pass-through still works.
func drainTypedInputDemuxer(in *pipeline.Input, opts map[string]any) {
	if opts == nil {
		return
	}
	if v, ok := opts["__format"]; ok {
		in.Format = anyToString(v)
		delete(opts, "__format")
		// kind="raw" auto-detected from rawvideo / PCM format names so
		// the typed field set up by `-f rawvideo -i …` validates without
		// the user also writing `-kind raw` (no such ffmpeg flag).
		if isRawFormat(in.Format) {
			in.Kind = "raw"
		} else if in.Format == "concat" {
			in.Kind = "concat"
		} else if in.Format == "lavfi" {
			in.Kind = "lavfi"
		}
	}
	if v, ok := opts["__r"]; ok {
		if f, err := strconv.ParseFloat(anyToString(v), 64); err == nil {
			in.FrameRate = f
		}
		delete(opts, "__r")
	}
	if v, ok := opts["framerate"]; ok {
		if f, err := strconv.ParseFloat(anyToString(v), 64); err == nil {
			in.FrameRate = f
		}
		delete(opts, "framerate")
	}
	if v, ok := opts["pix_fmt"]; ok {
		in.PixelFormat = anyToString(v)
		delete(opts, "pix_fmt")
	}
	if v, ok := opts["pixel_format"]; ok {
		in.PixelFormat = anyToString(v)
		delete(opts, "pixel_format")
	}
	if v, ok := opts["video_size"]; ok {
		in.VideoSize = anyToString(v)
		delete(opts, "video_size")
	}
	if v, ok := opts["ar"]; ok {
		if n, err := strconv.Atoi(anyToString(v)); err == nil {
			in.SampleRate = n
		}
		delete(opts, "ar")
	}
	if v, ok := opts["ac"]; ok {
		if n, err := strconv.Atoi(anyToString(v)); err == nil {
			in.Channels = n
		}
		delete(opts, "ac")
	}
	if v, ok := opts["sample_fmt"]; ok {
		in.SampleFormat = anyToString(v)
		delete(opts, "sample_fmt")
	}
	if v, ok := opts["thread_queue_size"]; ok {
		if n, err := strconv.Atoi(anyToString(v)); err == nil {
			in.ThreadQueueSize = n
		}
		delete(opts, "thread_queue_size")
	}
	if v, ok := opts["protocol_whitelist"]; ok {
		s := anyToString(v)
		parts := strings.Split(s, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		in.ProtocolWhitelist = out
		delete(opts, "protocol_whitelist")
	}
	if v, ok := opts["pattern_type"]; ok {
		in.PatternType = anyToString(v)
		delete(opts, "pattern_type")
	}
	if v, ok := opts["accurate_seek"]; ok {
		s := anyToString(v)
		b := s == "1" || strings.EqualFold(s, "true")
		in.AccurateSeek = &b
		delete(opts, "accurate_seek")
	}
	if v, ok := opts["seek_timestamp"]; ok {
		s := anyToString(v)
		in.SeekTimestamp = s == "1" || strings.EqualFold(s, "true")
		delete(opts, "seek_timestamp")
	}
	if len(opts) == 0 {
		in.Options = nil
	}
}

// drainTypedOutputDemuxer extracts the output-side meanings of the
// per-file flags from opts and routes them onto the canonical
// destination: `__format` → Output.Format, `__r` / `pix_fmt` →
// videoEncOpts, `ar` / `ac` → audioEncOpts. Input-only keys
// (framerate, pixel_format, video_size, thread_queue_size,
// protocol_whitelist, pattern_type, accurate_seek, seek_timestamp,
// sample_fmt) that landed in opts because the user wrote them after
// the last `-i` are silently dropped — they don't have an
// output-side meaning in our schema.
func drainTypedOutputDemuxer(out *pipeline.Output, vEnc, aEnc map[string]any, opts map[string]any) {
	if opts == nil {
		return
	}
	if v, ok := opts["__format"]; ok {
		out.Format = anyToString(v)
		delete(opts, "__format")
	}
	if v, ok := opts["__r"]; ok {
		vEnc["r"] = anyToString(v)
		delete(opts, "__r")
	}
	if v, ok := opts["pix_fmt"]; ok {
		vEnc["pix_fmt"] = anyToString(v)
		delete(opts, "pix_fmt")
	}
	if v, ok := opts["ar"]; ok {
		aEnc["ar"] = anyToString(v)
		delete(opts, "ar")
	}
	if v, ok := opts["ac"]; ok {
		aEnc["ac"] = anyToString(v)
		delete(opts, "ac")
	}
	// Input-only leftovers — drop silently.
	for _, k := range []string{
		"framerate", "pixel_format", "video_size", "sample_fmt",
		"thread_queue_size", "protocol_whitelist", "pattern_type",
		"accurate_seek", "seek_timestamp",
	} {
		delete(opts, k)
	}
	if len(opts) == 0 {
		out.Options = nil
	}
}

func anyToString(v any) string {
	switch s := v.(type) {
	case string:
		return s
	default:
		return ""
	}
}

func isRawFormat(name string) bool {
	switch name {
	case "rawvideo", "yuv4mpegpipe",
		"s8", "u8",
		"s16le", "s16be",
		"s24le", "s24be",
		"s32le", "s32be",
		"f32le", "f32be",
		"f64le", "f64be",
		"alaw", "mulaw":
		return true
	}
	return false
}
