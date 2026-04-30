// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/MediaMolder/MediaMolder/pipeline"
)

// parsedMap is a single `-map ARG` invocation.
type parsedMap struct {
	inputIdx int
	sel      pipeline.StreamSelect
}

// parseMapArg parses a single FFmpeg `-map` argument.
//
// Grammar (mirrors fftools/ffmpeg_opt.c::map_manual +
// cmdutils.c::check_stream_specifier):
//
//	[-]INPUT_FILE_ID[:STREAM_SPECIFIER][?]
//
// Where STREAM_SPECIFIER is one of:
//
//	v | a | s | d                    (all streams of type)
//	v:N | a:N | s:N | d:N            (Nth stream of type)
//	p:PROGRAM_ID                     (every stream of program)
//	p:PROGRAM_ID:v|a|s|d             (every stream of type within program)
//	p:PROGRAM_ID:v|a|s|d:N           (Nth stream of type within program)
//
// A leading `-` negates (removes from selection); a trailing `?`
// makes a missing match a silent skip. The two are mutually exclusive
// in FFmpeg and we reject the combination at parse time.
//
// Out of scope: the bare `0` form (all streams of input 0), `M:i:N`
// (id-based stream specifier), and metadata/program filtering on
// `m:KEY[:VALUE]` — none required by the §6 Wave 2 corpus.
func parseMapArg(arg string) (parsedMap, error) {
	if arg == "" {
		return parsedMap{}, fmt.Errorf("empty -map argument")
	}
	negate := false
	if strings.HasPrefix(arg, "-") {
		negate = true
		arg = arg[1:]
	}
	optional := false
	if strings.HasSuffix(arg, "?") {
		optional = true
		arg = arg[:len(arg)-1]
	}
	if negate && optional {
		return parsedMap{}, fmt.Errorf("`?` and leading `-` are mutually exclusive in -map")
	}
	parts := strings.Split(arg, ":")
	if len(parts) == 0 || parts[0] == "" {
		return parsedMap{}, fmt.Errorf("missing input id in -map %q", arg)
	}
	inIdx, err := strconv.Atoi(parts[0])
	if err != nil || inIdx < 0 {
		return parsedMap{}, fmt.Errorf("invalid input id %q in -map", parts[0])
	}
	sel := pipeline.StreamSelect{
		InputIndex: inIdx,
		Optional:   optional,
		Negate:     negate,
		All:        true,
	}
	rest := parts[1:]
	if len(rest) > 0 && rest[0] == "p" {
		if len(rest) < 2 {
			return parsedMap{}, fmt.Errorf("missing program id in -map %q", arg)
		}
		pid, err := strconv.Atoi(rest[1])
		if err != nil || pid <= 0 {
			return parsedMap{}, fmt.Errorf("invalid program id %q in -map", rest[1])
		}
		sel.Program = pid
		rest = rest[2:]
	}
	if len(rest) == 0 {
		// Bare program-only spec selects every stream of the program.
		// When also no program (bare `0:`), we treat it as
		// "all streams of input" — collapse to type=video first as a
		// sentinel and add audio + subtitle as separate maps later.
		if sel.Program == 0 {
			return parsedMap{}, fmt.Errorf("bare `INPUT` form (no stream specifier) is not supported; use `-map 0:v -map 0:a` instead")
		}
		// Program-only: caller must expand into per-type entries; we
		// represent it with empty Type which the runtime resolver
		// won't accept. Reject here to fail fast.
		return parsedMap{}, fmt.Errorf("`-map %q` (program with no stream-type) is not supported; specify `:v`/`:a`/`:s` after the program id", arg)
	}
	typ, ok := mapStreamType(rest[0])
	if !ok {
		return parsedMap{}, fmt.Errorf("invalid stream type %q in -map (want v|a|s|d)", rest[0])
	}
	sel.Type = typ
	rest = rest[1:]
	if len(rest) == 1 {
		n, err := strconv.Atoi(rest[0])
		if err != nil || n < 0 {
			return parsedMap{}, fmt.Errorf("invalid stream index %q in -map", rest[0])
		}
		sel.Track = n
		sel.All = false
	} else if len(rest) > 1 {
		return parsedMap{}, fmt.Errorf("trailing junk %q in -map", strings.Join(rest, ":"))
	}
	return parsedMap{inputIdx: inIdx, sel: sel}, nil
}

func mapStreamType(letter string) (string, bool) {
	switch letter {
	case "v":
		return "video", true
	case "a":
		return "audio", true
	case "s":
		return "subtitle", true
	case "d":
		return "data", true
	}
	return "", false
}

// applyMapSelectors replaces each input's default Streams list with
// the maps targeting that input, when any maps were specified.
// Mirrors FFmpeg's behaviour: as soon as any `-map` is present the
// implicit "best stream of each type" selection is suppressed.
func (p *parser) applyMapSelectors() error {
	if len(p.mapSpecs) == 0 {
		return nil
	}
	touched := map[int]bool{}
	for _, m := range p.mapSpecs {
		if m.inputIdx >= len(p.inputs) {
			return fmt.Errorf("-map references input %d but only %d input(s) declared", m.inputIdx, len(p.inputs))
		}
		touched[m.inputIdx] = true
	}
	for idx := range touched {
		p.inputs[idx].Streams = nil
	}
	for _, m := range p.mapSpecs {
		p.inputs[m.inputIdx].Streams = append(p.inputs[m.inputIdx].Streams, m.sel)
	}
	return nil
}
