// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

import "testing"

// Wave 7 #40: round-trip test for the avfilter_graph_parse_ptr
// pad-binding quirk. A `-filter_complex` spec where a chain's last
// filter has no trailing pad label is normalised by the importer into
// the canonical labelled form. Re-running the normaliser on its own
// output is a no-op (idempotent), which is the round-trip guarantee.
func TestNormalizeFilterComplex_RoadmapExample(t *testing.T) {
	in := `[0:v]split=2[a][b]; [a]scale=720:-1; [b]scale=480:-1`
	want := `[0:v]split=2[a][b];[a]scale=720:-1[mm_fc_out_0];[b]scale=480:-1[mm_fc_out_1]`
	got := NormalizeFilterComplex(in)
	if got != want {
		t.Fatalf("normalize:\n  in:   %s\n  got:  %s\n  want: %s", in, got, want)
	}
	// Round-trip: idempotent on the canonical labelled form.
	if got2 := NormalizeFilterComplex(got); got2 != got {
		t.Fatalf("not idempotent:\n  first:  %s\n  second: %s", got, got2)
	}
}

func TestNormalizeFilterComplex_AlreadyLabelled(t *testing.T) {
	in := `[0:v]scale=720:-1[v0];[0:a]aresample=48000[a0]`
	if got := NormalizeFilterComplex(in); got != in {
		t.Fatalf("already canonical changed:\n  in:  %s\n  got: %s", in, got)
	}
}

func TestNormalizeFilterComplex_DanglingLeadingInput(t *testing.T) {
	// First chain has no leading label — synthesised as mm_fc_in_0.
	in := `scale=1280:720`
	want := `[mm_fc_in_0]scale=1280:720[mm_fc_out_0]`
	if got := NormalizeFilterComplex(in); got != want {
		t.Fatalf("dangling input:\n  in:   %s\n  got:  %s\n  want: %s", in, got, want)
	}
}

func TestNormalizeFilterComplex_MultiFilterChain(t *testing.T) {
	// Internal commas separate filters within a chain; only the
	// trailing pad of the last filter is dangling.
	in := `[0:v]scale=1280:720,format=yuv420p`
	want := `[0:v]scale=1280:720,format=yuv420p[mm_fc_out_0]`
	if got := NormalizeFilterComplex(in); got != want {
		t.Fatalf("multi-filter:\n  in:   %s\n  got:  %s\n  want: %s", in, got, want)
	}
}

func TestNormalizeFilterComplex_Empty(t *testing.T) {
	if got := NormalizeFilterComplex(""); got != "" {
		t.Fatalf("empty: got %q", got)
	}
	if got := NormalizeFilterComplex("  ;  ; "); got != "" {
		t.Fatalf("only-separators: got %q", got)
	}
}
