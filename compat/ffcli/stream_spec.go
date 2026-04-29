// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/MediaMolder/MediaMolder/pipeline"
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
func (p *parser) streamSpecFor(typ string, idx int) *pipeline.StreamSpec {
	if p.streamSpecs == nil {
		p.streamSpecs = make(map[string]*pipeline.StreamSpec)
	}
	key := fmt.Sprintf("%s:%d", typ, idx)
	ss, ok := p.streamSpecs[key]
	if !ok {
		ss = &pipeline.StreamSpec{Type: typ, Index: idx}
		p.streamSpecs[key] = ss
	}
	return ss
}
