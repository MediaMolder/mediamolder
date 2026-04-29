// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
)

func TestParseForceKeyFrames_Empty(t *testing.T) {
	spec, err := parseForceKeyFrames("")
	if err != nil || spec != nil {
		t.Fatalf("expected (nil, nil), got (%v, %v)", spec, err)
	}
}

func TestParseForceKeyFrames_TimeListSorted(t *testing.T) {
	spec, err := parseForceKeyFrames("10,3,7.5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.kind != forceKeyFramesTimeList {
		t.Fatalf("kind = %v, want time list", spec.kind)
	}
	want := []float64{3, 7.5, 10}
	if len(spec.times) != 3 {
		t.Fatalf("times = %v, want %v", spec.times, want)
	}
	for i, v := range want {
		if spec.times[i] != v {
			t.Errorf("times[%d] = %v, want %v", i, spec.times[i], v)
		}
	}
}

func TestParseForceKeyFrames_TimeListRejectsBadInputs(t *testing.T) {
	cases := []string{"3,,5", "abc", "-1,2", "3,foo"}
	for _, c := range cases {
		if _, err := parseForceKeyFrames(c); err == nil {
			t.Errorf("parseForceKeyFrames(%q) = nil, want error", c)
		}
	}
}

func TestParseForceKeyFrames_ExprValid(t *testing.T) {
	spec, err := parseForceKeyFrames("expr:gte(t,n_forced*2)")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.kind != forceKeyFramesExpr {
		t.Fatalf("kind = %v, want expr", spec.kind)
	}
	if spec.expr != "gte(t,n_forced*2)" {
		t.Fatalf("expr = %q", spec.expr)
	}
}

func TestParseForceKeyFrames_ExprRejectsEmpty(t *testing.T) {
	if _, err := parseForceKeyFrames("expr:"); err == nil {
		t.Fatal("expected error for empty expression")
	}
}

func TestParseForceKeyFrames_ExprRejectsUnknownVar(t *testing.T) {
	_, err := parseForceKeyFrames("expr:gte(undefined_var,1)")
	if err == nil || !strings.Contains(err.Error(), "invalid expression") {
		t.Fatalf("expected invalid expression error, got %v", err)
	}
}

func TestParseForceKeyFrames_Source(t *testing.T) {
	spec, err := parseForceKeyFrames("source")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.kind != forceKeyFramesSource {
		t.Fatalf("kind = %v, want source", spec.kind)
	}
}

func TestForceKeyFramesMatcher_TimeList(t *testing.T) {
	spec, _ := parseForceKeyFrames("1.0,2.0")
	// timebase 1/1000: pts in milliseconds.
	m, err := newForceKeyFramesMatcher(spec, 1, 1000)
	if err != nil {
		t.Fatalf("matcher: %v", err)
	}
	defer m.Close()
	cases := []struct {
		pts  int64
		want bool
	}{
		{0, false},
		{500, false},
		{999, false},
		{1000, true},  // t=1.0 fires
		{1500, false},
		{2000, true},  // t=2.0 fires
		{3000, false}, // exhausted
	}
	for _, c := range cases {
		if got := m.shouldForce(c.pts, av.PictureTypeNone); got != c.want {
			t.Errorf("shouldForce(pts=%d) = %v, want %v", c.pts, got, c.want)
		}
	}
}

func TestForceKeyFramesMatcher_Expr_GTE(t *testing.T) {
	spec, _ := parseForceKeyFrames("expr:gte(t,n_forced*2)")
	m, err := newForceKeyFramesMatcher(spec, 1, 1000)
	if err != nil {
		t.Fatalf("matcher: %v", err)
	}
	defer m.Close()
	// At t=0 (n_forced=0): gte(0, 0*2)=1 → fires (n_forced becomes 1).
	// Subsequent: must reach t >= 2 → fires at t=2.0 (n_forced=2);
	// then t >= 4 → fires at t=4.0; etc.
	forcedTimes := []float64{}
	for ms := int64(0); ms <= 6000; ms += 100 {
		if m.shouldForce(ms, av.PictureTypeNone) {
			forcedTimes = append(forcedTimes, float64(ms)/1000.0)
		}
	}
	want := []float64{0.0, 2.0, 4.0, 6.0}
	if len(forcedTimes) != len(want) {
		t.Fatalf("forced at %v, want %v", forcedTimes, want)
	}
	for i, w := range want {
		if forcedTimes[i] != w {
			t.Errorf("forced[%d] = %v, want %v", i, forcedTimes[i], w)
		}
	}
}

func TestForceKeyFramesMatcher_Source(t *testing.T) {
	spec, _ := parseForceKeyFrames("source")
	m, err := newForceKeyFramesMatcher(spec, 1, 1000)
	if err != nil {
		t.Fatalf("matcher: %v", err)
	}
	defer m.Close()
	if m.shouldForce(0, av.PictureTypeP) {
		t.Error("P-frame should not force")
	}
	if !m.shouldForce(100, av.PictureTypeI) {
		t.Error("I-frame should force")
	}
	if m.shouldForce(200, av.PictureTypeB) {
		t.Error("B-frame should not force")
	}
}

func TestForceKeyFramesMatcher_NilSafe(t *testing.T) {
	var m *forceKeyFramesMatcher
	if m.shouldForce(0, 0) {
		t.Error("nil matcher should never fire")
	}
	m.Close() // must not panic
}

func TestValidateForceKeyFrames_RejectsBadSpec(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.0",
		Inputs:        []Input{{ID: "in0", URL: "in.mp4"}},
		Outputs: []Output{{
			ID:             "out0",
			URL:            "out.mp4",
			ForceKeyFrames: "expr:nonsense_var",
		}},
		Graph: GraphDef{Edges: []EdgeDef{{From: "in0:v:0", To: "out0:v", Type: "video"}}},
	}
	if err := validate(cfg); err == nil {
		t.Fatal("expected validation error for bad force_key_frames expr")
	}
}

func TestValidateForceKeyFrames_AcceptsValid(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.0",
		Inputs:        []Input{{ID: "in0", URL: "in.mp4"}},
		Outputs: []Output{{
			ID:             "out0",
			URL:            "out.mp4",
			ForceKeyFrames: "expr:gte(t,n_forced*2)",
		}},
		Graph: GraphDef{Edges: []EdgeDef{{From: "in0:v:0", To: "out0:v", Type: "video"}}},
	}
	if err := validate(cfg); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}
