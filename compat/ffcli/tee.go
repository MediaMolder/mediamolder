// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

import (
	"fmt"
	"strings"

	"github.com/MediaMolder/MediaMolder/pipeline"
)

// parseTeeSlaves parses the FFmpeg tee muxer slaves grammar
// `[opt=val:opt=val]url|[opt=val]url|url` into a slice of TeeTarget.
// Mirrors libavformat/tee.c::tee_write_header (slave splitting on `|`)
// + libavformat/tee_common.c::ff_tee_parse_slave_options (option-block
// extraction) + libavutil/avstring.c::av_get_token (escape grammar).
//
// Recognised slave options promoted to typed TeeTarget fields:
//   `f`             -> Format
//   `select`        -> Select
//   `bsfs`          -> BSFs
//   `onfail`        -> OnFail (validated: empty | abort | ignore)
//   `use_fifo`      -> UseFifo (0/1/true/false/yes/no/on/off)
//   `fifo_options`  -> FifoOptions
// Unknown keys land in Options as strings.
func parseTeeSlaves(s string) ([]pipeline.TeeTarget, error) {
	if s == "" {
		return nil, fmt.Errorf("empty slaves spec")
	}
	parts, err := splitTeeSlaves(s)
	if err != nil {
		return nil, err
	}
	out := make([]pipeline.TeeTarget, 0, len(parts))
	for i, p := range parts {
		opts, url, perr := parseTeeSlave(p)
		if perr != nil {
			return nil, fmt.Errorf("slave[%d]: %w", i, perr)
		}
		if url == "" {
			return nil, fmt.Errorf("slave[%d]: missing url", i)
		}
		t := pipeline.TeeTarget{URL: url}
		for _, kv := range opts {
			switch kv[0] {
			case "f":
				t.Format = kv[1]
			case "select":
				t.Select = kv[1]
			case "bsfs":
				t.BSFs = kv[1]
			case "onfail":
				t.OnFail = kv[1]
			case "use_fifo":
				t.UseFifo = parseTeeBool(kv[1])
			case "fifo_options":
				t.FifoOptions = kv[1]
			default:
				if t.Options == nil {
					t.Options = map[string]any{}
				}
				t.Options[kv[0]] = kv[1]
			}
		}
		out = append(out, t)
	}
	return out, nil
}

// splitTeeSlaves splits the slaves string on top-level `|` boundaries
// while respecting backslash escapes and `[...]` option blocks.
func splitTeeSlaves(s string) ([]string, error) {
	var parts []string
	var cur strings.Builder
	inBlock := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\':
			cur.WriteByte(c)
			if i+1 < len(s) {
				cur.WriteByte(s[i+1])
				i++
			}
		case '[':
			inBlock = true
			cur.WriteByte(c)
		case ']':
			inBlock = false
			cur.WriteByte(c)
		case '|':
			if inBlock {
				cur.WriteByte(c)
				continue
			}
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	if inBlock {
		return nil, fmt.Errorf("unterminated `[` option block")
	}
	parts = append(parts, cur.String())
	return parts, nil
}

// parseTeeSlave splits one slave clause into its option pairs and URL.
// A leading `[opt=val:opt=val]` block (if present) is consumed; the
// remainder is the slave URL with backslash escapes unescaped.
func parseTeeSlave(s string) ([][2]string, string, error) {
	var opts [][2]string
	if strings.HasPrefix(s, "[") {
		end := findUnescapedByte(s, ']', 1)
		if end < 0 {
			return nil, "", fmt.Errorf("unterminated `[` option block")
		}
		block := s[1:end]
		s = s[end+1:]
		if block != "" {
			pairs, err := splitTeeOptions(block)
			if err != nil {
				return nil, "", err
			}
			opts = pairs
		}
	}
	return opts, teeUnescape(s), nil
}

// splitTeeOptions splits an option block like `f=mp4:select=v,a:0` on
// top-level `:` boundaries (respecting escapes), then for each token
// splits on the first unescaped `=`.
func splitTeeOptions(block string) ([][2]string, error) {
	tokens := splitUnescaped(block, ':')
	pairs := make([][2]string, 0, len(tokens))
	for _, tok := range tokens {
		if tok == "" {
			continue
		}
		eq := findUnescapedByte(tok, '=', 0)
		if eq < 0 {
			return nil, fmt.Errorf("option %q: missing `=`", tok)
		}
		k := teeUnescape(tok[:eq])
		v := teeUnescape(tok[eq+1:])
		pairs = append(pairs, [2]string{k, v})
	}
	return pairs, nil
}

func splitUnescaped(s string, sep byte) []string {
	var out []string
	var cur strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' && i+1 < len(s) {
			cur.WriteByte(c)
			cur.WriteByte(s[i+1])
			i++
			continue
		}
		if c == sep {
			out = append(out, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteByte(c)
	}
	out = append(out, cur.String())
	return out
}

func findUnescapedByte(s string, b byte, start int) int {
	for i := start; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			continue
		}
		if s[i] == b {
			return i
		}
	}
	return -1
}

func teeUnescape(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			sb.WriteByte(s[i+1])
			i++
			continue
		}
		sb.WriteByte(s[i])
	}
	return sb.String()
}

func parseTeeBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
