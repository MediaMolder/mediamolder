// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package cbs

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"
)

// filterGolden strips the parts of a trace_headers capture that depend on
// packetisation: the leading extradata section (everything before the
// first "Packet:" line) and the per-packet "Packet: ..." lines themselves.
// What remains is the in-band unit trace, which parsing the whole
// elementary stream as one fragment must reproduce exactly.
func filterGolden(golden string) []string {
	var out []string
	seenPacket := false
	for _, line := range strings.Split(golden, "\n") {
		if strings.HasPrefix(line, "Packet: ") {
			seenPacket = true
			continue
		}
		if !seenPacket || line == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

func diffLines(t *testing.T, got, want []string) {
	t.Helper()
	n := min(len(got), len(want))
	for i := 0; i < n; i++ {
		if got[i] != want[i] {
			start := max(0, i-3)
			var ctx strings.Builder
			for j := start; j < i; j++ {
				fmt.Fprintf(&ctx, "  %s\n", want[j])
			}
			t.Fatalf("line %d differs:\ncontext:\n%sgot:  %q\nwant: %q",
				i+1, ctx.String(), got[i], want[i])
		}
	}
	if len(got) != len(want) {
		tail := ""
		if len(got) > len(want) {
			tail = fmt.Sprintf("first extra line: %q", got[len(want)])
		} else {
			tail = fmt.Sprintf("first missing line: %q", want[len(got)])
		}
		t.Fatalf("line count differs: got %d, want %d\n%s", len(got), len(want), tail)
	}
}

// goldenRawStream parses a raw elementary stream as a single fragment and
// diffs the rendered trace against the (packet-independent part of the)
// trace_headers golden.
func goldenRawStream(t *testing.T, codec CodecID, fixture, golden string) {
	t.Helper()
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Skipf("fixture not available: %v", err)
	}
	goldenText, err := os.ReadFile(golden)
	if err != nil {
		t.Skipf("golden not available: %v", err)
	}
	want := filterGolden(string(goldenText))

	var buf bytes.Buffer
	tt := NewTextTracer(&buf)
	c, err := New(codec, tt)
	if err != nil {
		t.Fatal(err)
	}
	frag, err := c.ReadPacket(data)
	if err != nil {
		t.Fatalf("ReadPacket: %v", err)
	}
	for i := range frag.Units {
		if u := &frag.Units[i]; u.Err != nil {
			t.Errorf("unit %d (type %d %s): %v", i, u.Type, u.TypeName, u.Err)
		}
	}

	got := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	diffLines(t, got, want)
}

func TestGoldenH264RawStream(t *testing.T) {
	goldenRawStream(t, CodecH264, "testdata/tiny.h264", "testdata/golden/tiny.h264.txt")
}
