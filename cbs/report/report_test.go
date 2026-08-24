// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package report

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/cbs"
)

func parseFixture(t *testing.T, w Writer) *cbs.Fragment {
	t.Helper()
	data, err := os.ReadFile("../testdata/tiny.h264")
	if err != nil {
		t.Skipf("fixture not available: %v", err)
	}
	c, err := cbs.New(cbs.CodecH264, w.Tracer())
	if err != nil {
		t.Fatal(err)
	}
	frag, err := c.ReadPacket(data)
	if err != nil {
		t.Fatal(err)
	}
	return frag
}

func run(t *testing.T, opts Options) (string, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	w, err := NewWriter(&buf, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.BeginStream(Source{Codec: "h264", StreamIndex: 0}); err != nil {
		t.Fatal(err)
	}
	pi := PacketInfo{Index: 0, Size: 1234, PTS: 0, HasPTS: true, DTS: -512,
		HasDTS: true, Duration: 512, KeyFrame: true, Pos: 48}
	w.BeginPacket(pi)
	frag := parseFixture(t, w)
	if err := w.EndPacket(frag, nil); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	var doc map[string]any
	if opts.Format == "" || opts.Format == "json" {
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("output is not valid JSON: %v\n%s", err, out[:min(len(out), 2000)])
		}
	}
	return out, doc
}

func TestJSONDocument(t *testing.T) {
	_, doc := run(t, Options{Format: "json", Detail: "elements"})
	if doc["schema"] != "mediamolder.bitstream_trace/1" {
		t.Fatalf("schema: %v", doc["schema"])
	}
	pkts := doc["packets"].([]any)
	if len(pkts) != 1 {
		t.Fatalf("packets: %d", len(pkts))
	}
	pkt := pkts[0].(map[string]any)
	units := pkt["units"].([]any)
	if len(units) < 5 { // AUD, SPS, PPS, SEI, slices
		t.Fatalf("units: %d", len(units))
	}
	// Find the SPS: must carry a summary with dimensions and sections
	// with elements.
	var sps map[string]any
	for _, u := range units {
		um := u.(map[string]any)
		if um["type"].(float64) == 7 {
			sps = um
			break
		}
	}
	if sps == nil {
		t.Fatal("no SPS unit found")
	}
	sum := sps["summary"].(map[string]any)
	if sum["width"].(float64) != 64 || sum["height"].(float64) != 64 {
		t.Fatalf("sps summary dimensions: %v x %v", sum["width"], sum["height"])
	}
	secs := sps["sections"].([]any)
	if len(secs) == 0 {
		t.Fatal("sps has no sections at detail=elements")
	}
	el := secs[0].(map[string]any)["elements"].([]any)[0].(map[string]any)
	if el["name"] != "forbidden_zero_bit" || el["pos"].(float64) != 0 {
		t.Fatalf("first element: %v", el)
	}
	st := doc["stats"].(map[string]any)
	if st["packets"].(float64) != 1 || st["errors"].(float64) != 0 {
		t.Fatalf("stats: %v", st)
	}
}

func TestDetailHeadersOmitsSliceSections(t *testing.T) {
	_, doc := run(t, Options{Format: "json", Detail: "headers"})
	pkt := doc["packets"].([]any)[0].(map[string]any)
	for _, u := range pkt["units"].([]any) {
		um := u.(map[string]any)
		typ := um["type"].(float64)
		_, hasSections := um["sections"]
		switch typ {
		case 1, 5: // slices: summary only
			if hasSections {
				t.Fatalf("slice unit %v has sections at detail=headers", typ)
			}
			if _, ok := um["summary"]; !ok {
				t.Fatalf("slice unit %v missing summary", typ)
			}
		case 7: // SPS keeps elements
			if !hasSections {
				t.Fatal("SPS lost its sections at detail=headers")
			}
		}
	}
}

func TestDetailSummaryOmitsAllSections(t *testing.T) {
	out, doc := run(t, Options{Format: "json", Detail: "summary"})
	if strings.Contains(out, `"sections"`) {
		t.Fatal("summary detail still contains sections")
	}
	_ = doc
}

func TestUnitTypeFilter(t *testing.T) {
	_, doc := run(t, Options{Format: "json", Detail: "elements", UnitTypes: []string{"sps"}})
	pkt := doc["packets"].([]any)[0].(map[string]any)
	for _, u := range pkt["units"].([]any) {
		um := u.(map[string]any)
		if um["type"].(float64) == 7 {
			if _, ok := um["summary"]; !ok {
				t.Fatal("SPS filtered out despite matching filter")
			}
		} else if _, ok := um["summary"]; ok {
			t.Fatalf("unit %v not filtered", um["type"])
		}
	}
}

func TestJSONLines(t *testing.T) {
	out, _ := run(t, Options{Format: "jsonl", Detail: "summary"})
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 { // header, packet, stats
		t.Fatalf("jsonl lines: %d", len(lines))
	}
	for i, l := range lines {
		var v map[string]any
		if err := json.Unmarshal([]byte(l), &v); err != nil {
			t.Fatalf("line %d not JSON: %v", i, err)
		}
	}
}

func TestTextPacketLine(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, Options{Format: "text"})
	if err != nil {
		t.Fatal(err)
	}
	w.BeginPacket(PacketInfo{Size: 1587, KeyFrame: true, HasPTS: true, PTS: 0,
		HasDTS: true, DTS: 0, Duration: 1})
	if got, want := strings.TrimSpace(buf.String()),
		"Packet: 1587 bytes, key frame, pts 0, dts 0, duration 1."; got != want {
		t.Fatalf("packet line:\ngot  %q\nwant %q", got, want)
	}

	buf.Reset()
	w.BeginPacket(PacketInfo{Size: 9})
	if got, want := strings.TrimSpace(buf.String()),
		"Packet: 9 bytes, no pts, no dts."; got != want {
		t.Fatalf("packet line:\ngot  %q\nwant %q", got, want)
	}
}

func TestMaxPacketsDone(t *testing.T) {
	var buf bytes.Buffer
	w, _ := NewWriter(&buf, Options{Format: "json", MaxPackets: 1})
	w.BeginStream(Source{Codec: "h264"})
	w.BeginPacket(PacketInfo{Index: 0})
	w.EndPacket(&cbs.Fragment{}, nil)
	if !w.Done() {
		t.Fatal("Done() should report true after MaxPackets")
	}
	w.Close()
}
