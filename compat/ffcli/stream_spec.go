// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/MediaMolder/MediaMolder/job"
)

// parseStreamSpec parses an FFmpeg-style `<type>[:<idx>]` stream
// specifier into a normalised letter (`v`/`a`/`s`/`d`) and 0-based
// index. The bare-letter form (e.g. `a`) is treated as `a:0` to
// match the most common CLI shorthand.
func parseStreamSpec(spec string) (string, int, error) {
	parts := strings.SplitN(spec, ":", 2)
	typ := parts[0]
	switch typ {
	case "v", "a", "s", "d":
	default:
		return "", 0, fmt.Errorf("invalid stream type %q (want v|a|s|d)", typ)
	}
	idx := 0
	if len(parts) == 2 {
		n, err := strconv.Atoi(parts[1])
		if err != nil || n < 0 {
			return "", 0, fmt.Errorf("invalid stream index %q", parts[1])
		}
		idx = n
	}
	return typ, idx, nil
}

// streamSpecFor returns a draft StreamSpec for the given media type
// and index, creating it on first reference. Subsequent calls with
// the same key return the same draft so that multiple
// `-metadata:s:...` flags accumulate into one Metadata map and
// `-disposition:s:...` lands on the same record.
func (p *parser) streamSpecFor(typ string, idx int) *job.StreamSpec {
	if p.streamSpecs == nil {
		p.streamSpecs = make(map[string]*job.StreamSpec)
	}
	key := fmt.Sprintf("%s:%d", typ, idx)
	ss, ok := p.streamSpecs[key]
	if !ok {
		ss = &job.StreamSpec{Type: typ, Index: idx}
		p.streamSpecs[key] = ss
	}
	return ss
}

// perStreamEncoderFlag is the parsed shape of an FFmpeg
// `-<key>:<type>:<idx>` per-stream encoder option (Wave 6 #30).
type perStreamEncoderFlag struct {
	key string
	typ string
	idx int
}

// perStreamEncoderKeys enumerates the encoder AVOption names that
// the importer recognises in their per-stream `-<key>:<type>:<idx>`
// form. Mirrors the most common subset of FFmpeg's encoder option
// table (libavcodec/options_table.h + per-encoder AVOption arrays
// in libavcodec/libx264.c, libavcodec/aacenc.c, etc.). Adding a
// new key here is a one-line change.
var perStreamEncoderKeys = map[string]bool{
	"b":             true, // bitrate
	"minrate":       true,
	"maxrate":       true,
	"bufsize":       true,
	"crf":           true,
	"qp":            true,
	"qmin":          true,
	"qmax":          true,
	"qscale":        true,
	"preset":        true,
	"tune":          true,
	"profile":       true,
	"level":         true,
	"g":             true, // gop_size
	"bf":            true, // b-frames
	"refs":          true,
	"keyint_min":    true,
	"sc_threshold":  true,
	"x264-params":   true,
	"x264opts":      true,
	"x265-params":   true,
	"svtav1-params": true,
	"aq-mode":       true,
	"threads":       true,
	"aspect":        true,
	"ar":            true,
	"ac":            true,
	"sample_fmt":    true,
}

// parsePerStreamEncoderFlag returns a populated
// perStreamEncoderFlag if arg matches `-<key>:<type>:<idx>` where
// key is in perStreamEncoderKeys; otherwise returns nil. Used as
// the predicate of a switch case so the case body can run a second
// parse pass without paying the cost twice (the parsed result is
// recomputed on entry — perStreamEncoderKeys lookup is O(1)).
func parsePerStreamEncoderFlag(arg string) *perStreamEncoderFlag {
	if len(arg) < 2 || arg[0] != '-' {
		return nil
	}
	parts := strings.Split(arg[1:], ":")
	if len(parts) != 3 {
		return nil
	}
	key, typ := parts[0], parts[1]
	if !perStreamEncoderKeys[key] {
		return nil
	}
	if typ != "v" && typ != "a" && typ != "s" && typ != "d" {
		return nil
	}
	n, err := strconv.Atoi(parts[2])
	if err != nil || n < 0 {
		return nil
	}
	return &perStreamEncoderFlag{key: key, typ: typ, idx: n}
}
