// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/cbs"
)

// noPPSSlice is a non-IDR slice referencing PPS 0 with no parameter sets
// in the stream: a guaranteed layer-0 syntax violation.
var noPPSSlice = []byte{0x00, 0x00, 0x01, 0x41, 0x9A, 0x00, 0x08, 0xBF}

func TestViolationsSyntax(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, Options{Format: "json"})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.BeginStream(Source{Codec: "h264"}); err != nil {
		t.Fatal(err)
	}
	c, err := cbs.New(cbs.CodecH264, w.Tracer())
	if err != nil {
		t.Fatal(err)
	}
	w.BeginPacket(PacketInfo{Index: 0, Size: len(noPPSSlice)})
	frag, _ := c.ReadPacket(noPPSSlice)
	if err := w.EndPacket(frag, nil); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	if w.ErrorViolations() != 1 {
		t.Fatalf("ErrorViolations: %d", w.ErrorViolations())
	}
	var doc map[string]any
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	vs := doc["violations"].([]any)
	if len(vs) != 1 {
		t.Fatalf("violations: %v", vs)
	}
	v := vs[0].(map[string]any)
	if v["severity"] != "error" || v["kind"] != "syntax" || v["spec"] != "H.264" {
		t.Fatalf("violation: %v", v)
	}
	if v["packet"].(float64) != 0 || v["unit"].(float64) != 0 {
		t.Fatalf("violation location: %v", v)
	}
	if !strings.Contains(v["message"].(string), "PPS id 0 not available") {
		t.Fatalf("violation message: %q", v["message"])
	}
}

func TestViolationsCSVRows(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, Options{Format: "csv"})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.BeginStream(Source{Codec: "h264"}); err != nil {
		t.Fatal(err)
	}
	c, _ := cbs.New(cbs.CodecH264, w.Tracer())
	w.BeginPacket(PacketInfo{Index: 0})
	frag, _ := c.ReadPacket(noPPSSlice)
	if err := w.EndPacket(frag, nil); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "violation,0,") ||
		!strings.Contains(out, "PPS id 0 not available.") {
		t.Fatalf("violation row missing:\n%s", out)
	}
	if w.ErrorViolations() != 1 {
		t.Fatalf("ErrorViolations: %d", w.ErrorViolations())
	}
}

func TestChecksRejectedForText(t *testing.T) {
	if _, err := NewWriter(&bytes.Buffer{}, Options{Format: "text", Checks: []string{"default"}}); err == nil {
		t.Fatal("text format must reject structure checks")
	}
	if _, err := NewWriter(&bytes.Buffer{}, Options{Format: "json", Checks: []string{"bogus"}}); err == nil {
		t.Fatal("unknown check id must be rejected")
	}
}

// checkerEnv builds a checker with a summarizer for synthetic-unit tests.
func checkerEnv(t *testing.T, codec string, spec ...string) (*checker, *summarizer, *violationLog) {
	t.Helper()
	sum := newSummarizer()
	vlog := &violationLog{}
	ids, err := ResolveChecks(spec)
	if err != nil {
		t.Fatal(err)
	}
	c := newChecker(ids, codec, sum, vlog)
	return c, sum, vlog
}

func h264SliceUnit(nalType, refIdc uint8, frameNum uint16) *cbs.Unit {
	return &cbs.Unit{
		Type: uint32(nalType), Decomposed: true,
		Content: &cbs.H264RawSlice{Header: cbs.H264RawSliceHeader{
			NalUnitHeader: cbs.H264RawNALUnitHeader{NalUnitType: nalType, NalRefIdc: refIdc},
			FrameNum:      frameNum,
		}},
	}
}

func TestCheckVCLBeforePS(t *testing.T) {
	c, sum, vlog := checkerEnv(t, "h264", "default")
	u := h264SliceUnit(1, 2, 0)
	c.beginPacket(0)
	pic := sum.advance(u)
	c.observe(0, 0, u, pic)
	c.endPacket()
	if len(vlog.list) != 1 || vlog.list[0].Check != checkVCLBeforePS ||
		vlog.list[0].Severity != "error" {
		t.Fatalf("violations: %+v", vlog.list)
	}
	// Emitted once, not per slice.
	c.beginPacket(1)
	c.observe(1, 0, u, pic)
	c.endPacket()
	if len(vlog.list) != 1 {
		t.Fatalf("vcl_before_ps must not repeat: %+v", vlog.list)
	}
}

func TestCheckFrameNumGap(t *testing.T) {
	c, sum, vlog := checkerEnv(t, "h264", "default")
	sum.poc.h264SPS[0] = &cbs.H264RawSPS{Log2MaxFrameNumMinus4: 0} // gaps disallowed
	sum.poc.h264PPS[0] = &cbs.H264RawPPS{}

	feed := func(pktIdx int64, u *cbs.Unit) {
		c.beginPacket(pktIdx)
		pic := sum.advance(u)
		c.observe(pktIdx, 0, u, pic)
		c.endPacket()
	}
	feed(0, h264SliceUnit(5, 3, 0)) // IDR fn 0
	feed(1, h264SliceUnit(1, 2, 1)) // P fn 1: consecutive
	if len(vlog.list) != 0 {
		t.Fatalf("no gap yet: %+v", vlog.list)
	}
	feed(2, h264SliceUnit(1, 2, 3)) // fn 3 after 1: gap
	if len(vlog.list) != 1 || vlog.list[0].Check != checkFrameNumGap {
		t.Fatalf("gap not flagged: %+v", vlog.list)
	}
}

func TestCheckNoAUDAndSEIAfterVCL(t *testing.T) {
	c, sum, vlog := checkerEnv(t, "h264", "strict")
	sum.poc.h264SPS[0] = &cbs.H264RawSPS{}
	sum.poc.h264PPS[0] = &cbs.H264RawPPS{}

	c.beginPacket(0)
	u := h264SliceUnit(1, 2, 0)
	c.observe(0, 0, u, sum.advance(u))
	sei := &cbs.Unit{Type: 6, Decomposed: true, Content: &cbs.H264RawSEI{}}
	c.observe(0, 1, sei, sum.advance(sei))
	c.endPacket()

	var checks []string
	for _, v := range vlog.list {
		checks = append(checks, v.Check)
		if v.Severity != "warning" {
			t.Fatalf("strict findings are warnings: %+v", v)
		}
	}
	if len(checks) != 2 || checks[0] != checkSEIAfterVCL || checks[1] != checkNoAUD {
		t.Fatalf("checks fired: %v", checks)
	}

	// A packet with an AUD first: no findings.
	before := len(vlog.list)
	c.beginPacket(1)
	aud := &cbs.Unit{Type: 9, Decomposed: true, Content: &cbs.H264RawAUD{}}
	c.observe(1, 0, aud, sum.advance(aud))
	u2 := h264SliceUnit(1, 2, 1)
	c.observe(1, 1, u2, sum.advance(u2))
	c.endPacket()
	if len(vlog.list) != before {
		t.Fatalf("clean packet flagged: %+v", vlog.list[before:])
	}
}

// TestUnsupportedUnitsNotViolations: legal-but-undecomposed units (HEVC
// EOS/EOB, reserved types → Skip/ErrUnsupported) must not fail
// validation.
// allFormats runs a layer-0 scenario against every writer: the rules
// must be identical whether or not the format prints violations.
var allFormats = []string{"json", "jsonl", "csv", "text"}

func TestUnsupportedUnitsNotViolations(t *testing.T) {
	for _, format := range allFormats {
		t.Run(format, func(t *testing.T) {
			var buf bytes.Buffer
			w, err := NewWriter(&buf, Options{Format: format})
			if err != nil {
				t.Fatal(err)
			}
			if err := w.BeginStream(Source{Codec: "hevc"}); err != nil {
				t.Fatal(err)
			}
			c, _ := cbs.New(cbs.CodecH265, w.Tracer())
			// HEVC EOS_NUT (type 36): header 0x48 0x01 — valid, undecomposed.
			eos := []byte{0x00, 0x00, 0x01, 0x48, 0x01, 0x80}
			w.BeginPacket(PacketInfo{Index: 0})
			frag, _ := c.ReadPacket(eos)
			if err := w.EndPacket(frag, nil); err != nil {
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			if n := w.ErrorViolations(); n != 0 {
				t.Fatalf("unsupported unit counted as violation: %d\n%+v", n, w.Violations())
			}
		})
	}
}

// TestFrameNumGapMMCO5: after a reference picture with mmco5,
// PrevRefFrameNum is inferred as 0 (§7.4.3) — frame_num restarting is
// legal and must not trip frame_num_gap.
func TestFrameNumGapMMCO5(t *testing.T) {
	c, sum, vlog := checkerEnv(t, "h264", "default")
	sum.poc.h264SPS[0] = &cbs.H264RawSPS{Log2MaxFrameNumMinus4: 0}
	sum.poc.h264PPS[0] = &cbs.H264RawPPS{}

	feed := func(pktIdx int64, u *cbs.Unit) {
		c.beginPacket(pktIdx)
		pic := sum.advance(u)
		c.observe(pktIdx, 0, u, pic)
		c.endPacket()
	}
	feed(0, h264SliceUnit(5, 3, 0)) // IDR
	feed(1, h264SliceUnit(1, 2, 5)) // legal? 5 after 0 is a gap
	if len(vlog.list) != 1 {
		t.Fatalf("expected the 0→5 gap: %+v", vlog.list)
	}
	// Reference picture with mmco5 at frame_num 5.
	m5 := h264SliceUnit(1, 2, 5)
	sh := &m5.Content.(*cbs.H264RawSlice).Header
	sh.AdaptiveRefPicMarkingModeFlag = 1
	sh.Mmco[0].MemoryManagementControlOperation = 5
	feed(2, m5)
	// frame_num 1 after mmco5: PrevRefFrameNum inferred 0 → legal.
	before := len(vlog.list)
	feed(3, h264SliceUnit(1, 2, 1))
	if len(vlog.list) != before {
		t.Fatalf("post-mmco5 frame_num restart flagged: %+v", vlog.list[before:])
	}
}

// TestSplitErrorViolation: a packet whose fragment split fails (bad NALFF
// length after avcC extradata) must surface in violations.
func TestSplitErrorViolation(t *testing.T) {
	for _, format := range allFormats {
		t.Run(format, func(t *testing.T) { splitErrorScenario(t, format) })
	}
}

func splitErrorScenario(t *testing.T, format string) {
	t.Helper()
	var buf bytes.Buffer
	w, err := NewWriter(&buf, Options{Format: format})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.BeginStream(Source{Codec: "h264"}); err != nil {
		t.Fatal(err)
	}
	c, _ := cbs.New(cbs.CodecH264, w.Tracer())
	// avcC extradata: 4-byte lengths, 1 SPS, 1 PPS.
	xd := []byte{1, 0x42, 0x00, 0x1e, 0xff,
		0xe1, 0x00, 0x04, 0x67, 0x42, 0x00, 0x1e,
		0x01, 0x00, 0x02, 0x68, 0xee}
	w.BeginExtradata()
	frag, xerr := c.ReadExtradata(xd)
	if err := w.EndExtradata(frag, xerr); err != nil {
		t.Fatal(err)
	}
	// Packet with an oversized NALFF length.
	w.BeginPacket(PacketInfo{Index: 0})
	frag, perr := c.ReadPacket([]byte{0x00, 0x00, 0x00, 0xFF, 0x41, 0x9A})
	if perr == nil {
		t.Fatal("expected a split error")
	}
	if err := w.EndPacket(frag, perr); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	// Two syntax violations from the (deliberately bogus) avcC parameter
	// sets plus the split failure — and every format must agree.
	if w.ErrorViolations() != 3 {
		t.Fatalf("error violations: got %d, want 3\n%+v", w.ErrorViolations(), w.Violations())
	}
	found := false
	for _, v := range w.Violations() {
		if v.Kind == "syntax" && v.Unit == -1 && v.Packet == 0 &&
			(strings.Contains(v.Message, "Invalid NAL unit size") ||
				format == "text" && strings.Contains(v.Message, "invalid data")) {
			found = true
		}
	}
	if !found {
		t.Fatalf("split error not in violations: %+v", w.Violations())
	}
}

// TestRangeSkippedPacketAdvancesState: a packet excluded by the range
// still advances parameter sets and POC state, so later pictures derive
// correctly.
func TestRangeSkippedPacketAdvancesState(t *testing.T) {
	var buf bytes.Buffer
	w, _ := NewWriter(&buf, Options{Format: "json",
		Range: [2]int64{1, 1}, RangeSet: true})
	if err := w.BeginStream(Source{Codec: "h265"}); err != nil {
		t.Fatal(err)
	}
	sum := w.(*jsonWriter).sum
	ps := &cbs.Unit{Decomposed: true, Content: &cbs.H265RawSPS{Log2MaxPicOrderCntLsbMinus4: 0}}
	pps := &cbs.Unit{Decomposed: true, Content: &cbs.H265RawPPS{}}
	sliceU := func(lsb uint16) *cbs.Unit {
		return &cbs.Unit{Type: 1, TypeName: "TRAIL_R", Decomposed: true,
			Content: &cbs.H265RawSlice{Header: cbs.H265RawSliceHeader{
				NalUnitHeader:       cbs.H265RawNALUnitHeader{NalUnitType: 1, NuhTemporalIDPlus1: 1},
				SlicePicOrderCntLsb: lsb,
			}}}
	}
	// Packet 0 (out of range): parameter sets + a picture at lsb 8.
	w.BeginPacket(PacketInfo{Index: 0})
	if err := w.EndPacket(&cbs.Fragment{Units: []cbs.Unit{*ps, *pps, *sliceU(8)}}, nil); err != nil {
		t.Fatal(err)
	}
	// Packet 1 (in range): lsb 14 — correct only if packet 0 advanced
	// the prevTid0 state (else it wraps to -2).
	w.BeginPacket(PacketInfo{Index: 1})
	if err := w.EndPacket(&cbs.Fragment{Units: []cbs.Unit{*sliceU(14)}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	_ = sum
	var doc map[string]any
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	pkts := doc["packets"].([]any)
	if len(pkts) != 1 {
		t.Fatalf("packets in range: %d", len(pkts))
	}
	pic := pkts[0].(map[string]any)["units"].([]any)[0].(map[string]any)["picture"].(map[string]any)
	if pic["poc"].(float64) != 14 {
		t.Fatalf("poc after skipped packet: %v (state not advanced?)", pic["poc"])
	}
}
