// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

import (
	"fmt"
	"strings"
)

// NormalizeFilterComplex rewrites a `-filter_complex` spec into the
// canonical labelled form expected by `avfilter_graph_parse_ptr`.
//
// FFmpeg's parser (libavfilter/graphparser.c, parse_outputs) treats a
// chain whose last filter has no trailing pad label as a "dangling
// output" — the unlabelled pad becomes an open output of the whole
// graph. The same is true for unlabelled leading pads on the first
// chain. The MediaMolder importer normalises every chain so that every
// dangling pad receives an explicit synthetic label
// (`[mm_fc_out_N]` / `[mm_fc_in_N]`). The exporter half (Wave 8 #53)
// emits the same canonical labelled form, which makes the round-trip
// idempotent: NormalizeFilterComplex(Normalize(s)) == Normalize(s).
//
// The roadmap example
//
//	-filter_complex "[0:v]split=2[a][b]; [a]scale=720:-1; [b]scale=480:-1"
//
// normalises to
//
//	[0:v]split=2[a][b];[a]scale=720:-1[mm_fc_out_0];[b]scale=480:-1[mm_fc_out_1]
//
// where the trailing pads on the two `scale` chains receive synthetic
// output labels. Internal labels (`[a]`, `[b]`) — those produced by one
// chain and consumed by another within the same spec — are left
// untouched.
//
// This function is purely a string-level rewrite; it does not validate
// filter names or counts of output pads. Filters such as `split=2`
// that emit more than one pad are expected to carry the right number
// of trailing labels in the source spec.
func NormalizeFilterComplex(spec string) string {
	chains := splitChains(spec)
	if len(chains) == 0 {
		return ""
	}

	type chainParts struct {
		ins  []string // leading [label] tokens
		body string   // filter chain text (filter1[,filter2,...])
		outs []string // trailing [label] tokens
	}
	parsed := make([]chainParts, len(chains))
	for i, c := range chains {
		ins, rest := stripLabels(c, true)
		outs, body := stripLabels(rest, false)
		parsed[i] = chainParts{ins: ins, body: body, outs: outs}
	}

	var (
		nextOutIdx int
		nextInIdx  int
	)
	for i := range parsed {
		if len(parsed[i].outs) == 0 && parsed[i].body != "" {
			parsed[i].outs = []string{fmt.Sprintf("mm_fc_out_%d", nextOutIdx)}
			nextOutIdx++
		}
		if i == 0 && len(parsed[i].ins) == 0 && parsed[i].body != "" {
			parsed[i].ins = []string{fmt.Sprintf("mm_fc_in_%d", nextInIdx)}
			nextInIdx++
		}
	}

	var out strings.Builder
	for i, p := range parsed {
		if i > 0 {
			out.WriteByte(';')
		}
		for _, lbl := range p.ins {
			out.WriteByte('[')
			out.WriteString(lbl)
			out.WriteByte(']')
		}
		out.WriteString(p.body)
		for _, lbl := range p.outs {
			out.WriteByte('[')
			out.WriteString(lbl)
			out.WriteByte(']')
		}
	}
	return out.String()
}

// splitChains splits a `-filter_complex` spec on `;` separators that
// sit at the top level (i.e. not inside `[]` or escaped). Whitespace
// around each chain is trimmed.
func splitChains(spec string) []string {
	var (
		chains []string
		cur    strings.Builder
		depth  int
		esc    bool
	)
	flush := func() {
		s := strings.TrimSpace(cur.String())
		if s != "" {
			chains = append(chains, s)
		}
		cur.Reset()
	}
	for i := 0; i < len(spec); i++ {
		c := spec[i]
		switch {
		case esc:
			cur.WriteByte(c)
			esc = false
		case c == '\\':
			cur.WriteByte(c)
			esc = true
		case c == '[':
			depth++
			cur.WriteByte(c)
		case c == ']':
			if depth > 0 {
				depth--
			}
			cur.WriteByte(c)
		case c == ';' && depth == 0:
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return chains
}

// stripLabels peels `[label]` tokens from one end of a chain segment.
// When leading is true it strips from the front and returns
// (labels, remainder); otherwise it strips from the back and returns
// (labels, remainder) where labels is in the same order they appeared
// in the source.
func stripLabels(s string, leading bool) ([]string, string) {
	s = strings.TrimSpace(s)
	var labels []string
	if leading {
		for {
			s = strings.TrimLeft(s, " \t")
			if !strings.HasPrefix(s, "[") {
				break
			}
			end := strings.IndexByte(s, ']')
			if end < 0 {
				break
			}
			labels = append(labels, s[1:end])
			s = s[end+1:]
		}
		return labels, strings.TrimSpace(s)
	}
	// Trailing labels: scan from the end.
	for {
		s = strings.TrimRight(s, " \t")
		if !strings.HasSuffix(s, "]") {
			break
		}
		start := strings.LastIndexByte(s, '[')
		if start < 0 {
			break
		}
		// Ensure no `]` between start and end (would mean a nested or
		// already-consumed label boundary).
		labels = append([]string{s[start+1 : len(s)-1]}, labels...)
		s = s[:start]
	}
	return labels, strings.TrimSpace(s)
}
