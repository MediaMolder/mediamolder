// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"fmt"
	"sort"
	"strings"
)

// buildTeeSlavesURL renders a slice of TeeTarget into the
// `[opt=val:opt=val]url|[opt=val]url` slaves grammar consumed by
// libavformat's tee muxer (parsed by libavformat/tee_common.c::
// ff_tee_parse_slave_options + libavformat/tee.c::open_slave). Targets
// are joined with `|`; option keys with empty string values are
// skipped; map iteration order is normalised so the rendered URL is
// stable across runs (required for round-trip / test determinism).
//
// Escaping mirrors libavutil/avstring.c::av_get_token: literal `:`,
// `]`, `\`, and `|` characters are backslash-escaped wherever they
// appear in option values or the slave URL.
func buildTeeSlavesURL(targets []TeeTarget) (string, error) {
	if len(targets) == 0 {
		return "", fmt.Errorf("tee: no targets")
	}
	parts := make([]string, 0, len(targets))
	for i, t := range targets {
		if t.URL == "" {
			return "", fmt.Errorf("tee target[%d]: missing url", i)
		}
		opts := teeTargetOptions(t)
		var sb strings.Builder
		if len(opts) > 0 {
			sb.WriteByte('[')
			for j, kv := range opts {
				if j > 0 {
					sb.WriteByte(':')
				}
				sb.WriteString(kv[0])
				sb.WriteByte('=')
				sb.WriteString(escapeTeeOptValue(kv[1]))
			}
			sb.WriteByte(']')
		}
		sb.WriteString(escapeTeeURL(t.URL))
		parts = append(parts, sb.String())
	}
	return strings.Join(parts, "|"), nil
}

// teeTargetOptions returns the ordered list of [key, value] pairs to
// render inside the slave's `[...]` block. The promoted typed fields
// (Format → "f", Select → "select", BSFs → "bsfs", OnFail → "onfail",
// UseFifo → "use_fifo", FifoOptions → "fifo_options") come first in a
// deterministic order; the free-form Options bag is appended in
// alphabetic order after those, with later writers winning over the
// promoted fields when keys collide (mirrors how FFmpeg's
// ff_tee_parse_slave_options last-write-wins on duplicate keys).
func teeTargetOptions(t TeeTarget) [][2]string {
	out := make([][2]string, 0, 8)
	add := func(k, v string) {
		if v == "" {
			return
		}
		out = append(out, [2]string{k, v})
	}
	add("f", t.Format)
	add("select", t.Select)
	add("bsfs", t.BSFs)
	add("onfail", t.OnFail)
	if t.UseFifo {
		add("use_fifo", "1")
	}
	add("fifo_options", t.FifoOptions)

	if len(t.Options) > 0 {
		keys := make([]string, 0, len(t.Options))
		for k := range t.Options {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			s := fmt.Sprintf("%v", t.Options[k])
			if s == "" || s == "<nil>" {
				continue
			}
			add(k, s)
		}
	}
	return out
}

// escapeTeeOptValue backslash-escapes characters that have special
// meaning inside a tee slave's `[...]` option block: `:` (option
// separator), `]` (block terminator), and `\` itself. `|` is also
// escaped defensively even though it would normally only appear
// outside the block, because av_get_token consumes it as a token
// terminator on the surrounding scan.
func escapeTeeOptValue(v string) string {
	return teeEscape(v, ":]|\\")
}

// escapeTeeURL backslash-escapes characters that have special meaning
// in the slave URL portion (outside the `[...]` block): `|` (slave
// separator), `[` (option-block opener), and `\` itself.
func escapeTeeURL(v string) string {
	return teeEscape(v, "|[\\")
}

func teeEscape(v, special string) string {
	if !strings.ContainsAny(v, special) {
		return v
	}
	var sb strings.Builder
	sb.Grow(len(v) + 4)
	for i := 0; i < len(v); i++ {
		c := v[i]
		if strings.IndexByte(special, c) >= 0 {
			sb.WriteByte('\\')
		}
		sb.WriteByte(c)
	}
	return sb.String()
}
