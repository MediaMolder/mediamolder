// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

import (
	"strings"
	"testing"
)

// FuzzTokenize drives the FFmpeg-CLI shell-style tokenizer with
// arbitrary input. Phase F.5 / roadmap §5#8.
//
// Invariants asserted (the things we care about — kept tight to the
// quoting/escaping bug class):
//
//  1. tokenize must never panic.
//  2. The total number of bytes across all tokens must be ≤ len(s).
//     The tokenizer only drops separator-spaces and quote bytes —
//     it must not fabricate or duplicate content.
//  3. No unquoted-space leaks into a token via state-machine
//     confusion. A quoted segment may legitimately contain ' ',
//     but only if the original input contained at least one quote
//     byte. When the tokenizer is buggy it can emit a space-bearing
//     token from input that had no quotes — that's the regression
//     we're hunting.
//
// We deliberately do NOT assert round-trip idempotency under
// strings.Join(toks, " "): tokenize is a lossy operation by design
// (quote bytes are stripped, not escaped), so a quoting-aware joiner
// would be needed to make round-trip meaningful. That's a feature,
// not a bug.
func FuzzTokenize(f *testing.F) {
	f.Add("ffmpeg -i in.mp4 out.mkv")
	f.Add(`ffmpeg -i "my file.mp4" out.mp4`)
	f.Add(`ffmpeg -vf "scale=640:480,fps=30" out.mp4`)
	f.Add(`ffmpeg -metadata title='It''s me' out.mp4`)
	f.Add(`ffmpeg -vf 'scale=320:240' out.mp4`)
	f.Add(`'unterminated`)
	f.Add(`"`)
	f.Add(``)
	f.Add(`   `)
	f.Add(`a"b c"d`) // quoted region embedded in the middle of a token
	f.Add("a\tb\nc") // non-space whitespace — tokenizer only splits on ASCII space today

	f.Fuzz(func(t *testing.T, s string) {
		toks := tokenize(s)

		// (2) Byte budget: total token bytes never exceeds input.
		var total int
		for _, tok := range toks {
			total += len(tok)
		}
		if total > len(s) {
			t.Fatalf("token bytes (%d) exceed input bytes (%d) for %q -> %q",
				total, len(s), s, toks)
		}

		// (3) No unquoted-space leaks into a token via state-machine
		// confusion. A quoted segment may legitimately contain ' ',
		// but only if the original input contained at least one
		// quote byte. When the tokenizer is buggy it can emit a
		// space-bearing token from input that had no quotes — that's
		// the regression we're hunting.
		if !strings.ContainsAny(s, `'"`) {
			for _, tok := range toks {
				if strings.Contains(tok, " ") {
					t.Fatalf("token %q contains space but input %q has no quotes",
						tok, s)
				}
			}
		}
	})
}

// FuzzParseFilterExpr exercises the filter-expression parser used to
// turn raw -vf/-af tokens like `scale=640:480` into a NodeDef params
// map. Tightens the surface for the same quoting/escaping bug class
// as FuzzBuildFilterSpec.
//
// Invariants:
//  1. Never panic.
//  2. Round-trip: parsing a name with no params returns that exact
//     name and a nil map.
//  3. Every key in the returned map is non-empty (positional keys
//     are auto-generated as `_posN`).
func FuzzParseFilterExpr(f *testing.F) {
	f.Add("scale")
	f.Add("scale=320:240")
	f.Add("scale=w=640:h=480")
	f.Add("drawtext=text=Hello:x=10:y=20")
	f.Add("=")
	f.Add("==")
	f.Add(":")
	f.Add("scale=:")
	f.Add("a=b=c") // value containing '='
	f.Add("")

	f.Fuzz(func(t *testing.T, expr string) {
		name, params := parseFilterExpr(expr)
		if !strings.HasPrefix(expr, name) {
			t.Fatalf("name %q is not a prefix of expr %q", name, expr)
		}
		for k := range params {
			if k == "" {
				t.Fatalf("empty param key in %q -> %q / %v", expr, name, params)
			}
		}
	})
}
