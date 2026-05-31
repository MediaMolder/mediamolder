// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"strings"
	"testing"
)

// FuzzBuildFilterSpec drives buildFilterSpec with arbitrary filter
// names and parameter values to surface quoting/escaping regressions
// of the same class as commit 04f1a0c7. Phase F.5 / roadmap §5#8.
//
// Invariants asserted (the things we care about — keep tight):
//
//  1. buildFilterSpec must never panic.
//  2. When the filter name is non-empty the spec must begin with it
//     (quoting only applies to values, never to the name).
//  3. The number of unquoted ',' and ';' separators in the rendered
//     spec must be zero. avfilter_graph_parse_ptr treats both as
//     filter-chain delimiters, so any unescaped occurrence inside a
//     value string would collapse the user's value into a sibling
//     filter or chain. This is exactly the bug class 04f1a0c7 fixed.
//  4. Every emitted single-quote run is balanced (libavfilter requires
//     paired quoting; an odd count means we generated a spec that
//     would parse wrong).
//
// The fuzzer encodes (filter, posVal, namedKey, namedVal) in a single
// byte stream split on the NUL byte so the engine's mutator can twist
// every field independently.
func FuzzBuildFilterSpec(f *testing.F) {
	// Seed with the historically-painful inputs first.
	add := func(filter, posVal, namedKey, namedVal string) {
		f.Add(filter + "\x00" + posVal + "\x00" + namedKey + "\x00" + namedVal)
	}
	add("scale", "320:240", "", "")
	add("scale", "", "w", "1280")
	add("drawtext", "", "text", "Hello, world")           // comma in value
	add("drawtext", "", "text", "It's a test; really")    // single-quote + semicolon
	add("drawtext", "", "text", `back\slash and 'quote'`) // backslash + quote
	add("subtitles", "input.srt", "", "")                 // colon-bearing positional
	add("crop", "", "x", "in_w-out_w-10")                 // expression
	add("overlay", "", "x", "if(gte(t,2),W-w,0)")         // expression with comma + parens
	add("noop", "", "", "")                               // bare filter, no params

	f.Fuzz(func(t *testing.T, data string) {
		parts := strings.SplitN(data, "\x00", 4)
		for len(parts) < 4 {
			parts = append(parts, "")
		}
		filter, posVal, namedKey, namedVal := parts[0], parts[1], parts[2], parts[3]

		// Filter names and parameter keys are libavfilter identifiers,
		// not user-controlled value bytes. AVFilter.name is constrained
		// to [A-Za-z0-9_] by libavfilter itself, and `params` keys come
		// from JSON object names which we surface as identifiers in
		// the GUI / schema. The 04f1a0c7 bug class is about VALUES, so
		// scope the fuzzer to that surface — anything else is noise
		// and would only catch malicious-JSON scenarios that belong to
		// schema validation, not to buildFilterSpec.
		if filter == "" || !isFilterIdent(filter) {
			return
		}
		if namedKey != "" && !isFilterIdent(namedKey) {
			return
		}

		params := map[string]any{}
		if posVal != "" {
			params["_pos0"] = posVal
		}
		if namedKey != "" {
			params[namedKey] = namedVal
		}

		spec := buildFilterSpec(NodeDef{Filter: filter, Params: params})

		if !strings.HasPrefix(spec, filter) {
			t.Fatalf("spec %q does not start with filter name %q", spec, filter)
		}

		// Walk the spec mirroring libavfilter's grammar (see
		// docs/utils.html "Quoting and escaping"):
		//   - inside '...' single-quotes everything is literal,
		//     including backslashes;
		//   - outside single-quotes, '\X' escapes the next byte
		//     (this is what makes the bash-style "'\''" idiom work
		//     to embed a literal single-quote into a quoted run).
		// Assert no unquoted ',' / ';' (the chain separators) leak
		// through and that the single-quote runs balance.
		inSQ := false
		quoteCount := 0
		for i := 0; i < len(spec); i++ {
			c := spec[i]
			if !inSQ && c == '\\' && i+1 < len(spec) {
				i++ // skip the escaped byte
				continue
			}
			switch c {
			case '\'':
				inSQ = !inSQ
				quoteCount++
			case ',', ';':
				if !inSQ {
					t.Fatalf("unquoted %q in spec %q (filter=%q params=%v)",
						c, spec, filter, params)
				}
			}
		}
		if quoteCount%2 != 0 {
			t.Fatalf("unbalanced single quotes in spec %q (count=%d)", spec, quoteCount)
		}
	})
}

// isFilterIdent reports whether s is a libavfilter-grade identifier
// (the constraint AVFilter.name and AVOption.name actually carry).
func isFilterIdent(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r == '_' ||
			(r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}
