// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package report

import (
	"bytes"
	"encoding/csv"
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
	if doc["schema"] != "mediamolder.bitstream_trace/2" {
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
		case 1, 5: // slices: typed picture record only
			if hasSections {
				t.Fatalf("slice unit %v has sections at detail=headers", typ)
			}
			if _, ok := um["picture"]; !ok {
				t.Fatalf("slice unit %v missing picture record", typ)
			}
			if _, ok := um["summary"]; ok {
				t.Fatalf("slice unit %v still has a summary blob", typ)
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
		} else if _, ok := um["picture"]; ok {
			t.Fatalf("unit %v picture not filtered", um["type"])
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
	if err := w.BeginStream(Source{Codec: "h264"}); err != nil {
		t.Fatal(err)
	}
	w.BeginPacket(PacketInfo{Index: 0})
	if err := w.EndPacket(&cbs.Fragment{}, nil); err != nil {
		t.Fatal(err)
	}
	if !w.Done() {
		t.Fatal("Done() should report true after MaxPackets")
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

// feedPackets drives n empty packets through w and returns the indices of
// the packets that appear in the JSON output.
func feedPackets(t *testing.T, opts Options, n int64) (written []int64, done int64) {
	t.Helper()
	var buf bytes.Buffer
	w, err := NewWriter(&buf, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.BeginStream(Source{Codec: "h264"}); err != nil {
		t.Fatal(err)
	}
	done = -1
	for i := int64(0); i < n; i++ {
		w.BeginPacket(PacketInfo{Index: i})
		if err := w.EndPacket(&cbs.Fragment{}, nil); err != nil {
			t.Fatal(err)
		}
		if w.Done() && done < 0 {
			done = i
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	for _, p := range doc["packets"].([]any) {
		written = append(written, int64(p.(map[string]any)["index"].(float64)))
	}
	return written, done
}

// TestRangeInclusive: packet_range / --range is a 0-based inclusive
// window at both ends (docs/bitstream-trace.md), and 0:0 is a valid
// single-packet window, not "unset".
func TestRangeInclusive(t *testing.T) {
	cases := []struct {
		lo, hi int64
		want   []int64
	}{
		{0, 2, []int64{0, 1, 2}},
		{0, 0, []int64{0}},
		{5, 5, []int64{5}},
		{3, 9, []int64{3, 4, 5, 6}}, // clipped by the 7-packet stream
	}
	for _, c := range cases {
		got, done := feedPackets(t, Options{Format: "json",
			Range: [2]int64{c.lo, c.hi}, RangeSet: true}, 7)
		if len(got) != len(c.want) {
			t.Fatalf("range %d:%d wrote %v, want %v", c.lo, c.hi, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("range %d:%d wrote %v, want %v", c.lo, c.hi, got, c.want)
			}
		}
		if c.hi < 6 && done != c.hi {
			t.Fatalf("range %d:%d: Done() first true after packet %d, want %d",
				c.lo, c.hi, done, c.hi)
		}
	}

	// Unset range: everything is written.
	got, _ := feedPackets(t, Options{Format: "json"}, 3)
	if len(got) != 3 {
		t.Fatalf("no range: wrote %v, want all 3", got)
	}
}

// TestTextRange: the range window applies to text output too — the same
// gate as JSON, suppressing both the "Packet:" lines and the trace lines
// of out-of-range packets.
func TestTextRange(t *testing.T) {
	data, err := os.ReadFile("../testdata/tiny.h264")
	if err != nil {
		t.Skipf("fixture not available: %v", err)
	}
	var buf bytes.Buffer
	w, err := NewWriter(&buf, Options{Format: "text",
		Range: [2]int64{1, 1}, RangeSet: true})
	if err != nil {
		t.Fatal(err)
	}
	c, err := cbs.New(cbs.CodecH264, w.Tracer())
	if err != nil {
		t.Fatal(err)
	}
	// Packet 0: whole fixture (out of range — must produce no output but
	// still seed SPS/PPS state). Packet 1: the same bytes again (in
	// range — slices must decompose, proving state advanced during the
	// suppressed packet).
	w.BeginPacket(PacketInfo{Index: 0, Size: len(data)})
	if _, err := c.ReadPacket(data); err != nil {
		t.Fatal(err)
	}
	if err := w.EndPacket(nil, nil); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("out-of-range packet produced output:\n%s", buf.String()[:min(buf.Len(), 300)])
	}
	if w.Done() {
		t.Fatal("Done() before the range end")
	}
	w.BeginPacket(PacketInfo{Index: 1, Size: len(data)})
	if _, err := c.ReadPacket(data); err != nil {
		t.Fatal(err)
	}
	if err := w.EndPacket(nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "Packet: ") {
		t.Fatalf("in-range packet line missing:\n%s", out[:min(len(out), 200)])
	}
	if !strings.Contains(out, "Slice Header") {
		t.Fatal("in-range packet lost its trace lines")
	}
	if !w.Done() {
		t.Fatal("Done() should be true past the range end")
	}
}

// TestUnitFilterAliases: family names from the proposal ("sei", "idr",
// "sps") must match the per-codec TypeNames (HEVC SEI_PREFIX/IDR_W_RADL,
// AV1 SEQUENCE_HEADER), not just the H.264 spellings.
func TestUnitFilterAliases(t *testing.T) {
	match := func(spec []string, typeName string) bool {
		f := newUnitFilter(spec)
		return f.match(&cbs.Unit{TypeName: typeName})
	}
	cases := []struct {
		spec     string
		typeName string
		want     bool
	}{
		{"sei", "SEI", true},
		{"sei", "SEI_PREFIX", true},
		{"sei", "SEI_SUFFIX", true},
		{"idr", "IDR", true},
		{"idr", "IDR_W_RADL", true},
		{"idr", "IDR_N_LP", true},
		{"sps", "SPS", true},
		{"sps", "SEQUENCE_HEADER", true},
		{"sei", "SPS", false},
		{"idr", "TRAIL_R", false},
	}
	for _, c := range cases {
		if got := match([]string{c.spec}, c.typeName); got != c.want {
			t.Errorf("filter %q vs %q: got %v, want %v", c.spec, c.typeName, got, c.want)
		}
	}
}

// TestCSVFormat: one row per unit, packet context + compact-JSON summary
// column, header row first.
func TestCSVFormat(t *testing.T) {
	out, _ := run(t, Options{Format: "csv"})
	rows, err := csv.NewReader(strings.NewReader(out)).ReadAll()
	if err != nil {
		t.Fatalf("output is not valid CSV: %v", err)
	}
	if len(rows) < 6 { // header + AUD, SPS, PPS, SEI, slices
		t.Fatalf("rows: %d", len(rows))
	}
	if rows[0][0] != "kind" || rows[0][len(rows[0])-1] != "summary" {
		t.Fatalf("header row: %v", rows[0])
	}
	col := map[string]int{}
	for i, name := range rows[0] {
		col[name] = i
	}
	var sps []string
	for _, r := range rows[1:] {
		if len(r) != len(rows[0]) {
			t.Fatalf("ragged row: %v", r)
		}
		if r[col["kind"]] != "packet" {
			t.Fatalf("kind: %v", r)
		}
		if r[col["type"]] == "7" {
			sps = r
		}
	}
	if sps == nil {
		t.Fatal("no SPS row")
	}
	if sps[col["packet"]] != "0" || sps[col["key_frame"]] != "true" {
		t.Fatalf("packet context: %v", sps)
	}
	var sum map[string]any
	if err := json.Unmarshal([]byte(sps[col["summary"]]), &sum); err != nil {
		t.Fatalf("summary column is not JSON: %v", err)
	}
	if sum["width"].(float64) != 64 || sum["height"].(float64) != 64 {
		t.Fatalf("summary: %v", sum)
	}
}

// TestCSVFilterAndRange: the unit filter drops rows; the packet gate
// applies as in the other formats.
func TestCSVFilterAndRange(t *testing.T) {
	out, _ := run(t, Options{Format: "csv", UnitTypes: []string{"sps"}})
	rows, err := csv.NewReader(strings.NewReader(out)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) < 2 { // header + at least one SPS
		t.Fatalf("filtered rows: %d\n%s", len(rows), out)
	}
	col := map[string]int{}
	for i, name := range rows[0] {
		col[name] = i
	}
	for _, r := range rows[1:] {
		if r[col["type"]] != "7" { // only SPS rows survive the filter
			t.Fatalf("unfiltered row: %v", r)
		}
	}

	out, _ = run(t, Options{Format: "csv", Range: [2]int64{1, 1}, RangeSet: true})
	rows, err = csv.NewReader(strings.NewReader(out)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 { // header only: the single packet (index 0) is out of range
		t.Fatalf("out-of-range rows: %d", len(rows))
	}
}

// TestCSVClassAndTime: the class column separates vcl / ps / sei rows and
// the time column carries the packet pts in seconds.
func TestCSVClassAndTime(t *testing.T) {
	out, _ := run(t, Options{Format: "csv"})
	rows, err := csv.NewReader(strings.NewReader(out)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	col := map[string]int{}
	for i, name := range rows[0] {
		col[name] = i
	}
	want := map[string]string{"7": "ps", "8": "ps", "6": "sei", "9": "other",
		"1": "vcl", "5": "vcl"}
	seen := map[string]bool{}
	for _, r := range rows[1:] {
		if w, ok := want[r[col["type"]]]; ok {
			if r[col["class"]] != w {
				t.Fatalf("type %s classified %q, want %q", r[col["type"]], r[col["class"]], w)
			}
			seen[w] = true
		}
		// pts 0 with time base 0/0 in this synthetic run: time stays
		// empty; presence is covered by the processor-level test.
		_ = r[col["time"]]
	}
	for _, c := range []string{"ps", "sei", "vcl"} {
		if !seen[c] {
			t.Fatalf("class %s never seen", c)
		}
	}
}
