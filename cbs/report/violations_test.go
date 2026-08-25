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
