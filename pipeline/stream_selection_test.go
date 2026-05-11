// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
)

// fakeStream builds a StreamInfo whose Type stringifies to typ.
// Mirrors the codec-type tags the av package uses.
func fakeStream(idx int, typ string) av.StreamInfo {
	var ct av.MediaType
	switch typ {
	case "video":
		ct = av.MediaTypeVideo
	case "audio":
		ct = av.MediaTypeAudio
	case "subtitle":
		ct = av.MediaTypeSubtitle
	case "data":
		ct = av.MediaTypeData
	case "attachment":
		ct = av.MediaTypeAttachment
	}
	return av.StreamInfo{Index: idx, Type: ct}
}

func TestResolveStreamSelection_DefaultsToFirstTrack(t *testing.T) {
	all := []av.StreamInfo{
		fakeStream(0, "video"),
		fakeStream(1, "audio"),
		fakeStream(2, "audio"),
	}
	sel, err := resolveStreamSelection([]StreamSelect{
		{Type: "video", Track: 0},
		{Type: "audio", Track: 1},
	}, all, nil)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got, want := sel, []int{0, 2}; !equalInts(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestResolveStreamSelection_AllSelectsEveryStreamOfType(t *testing.T) {
	all := []av.StreamInfo{
		fakeStream(0, "video"),
		fakeStream(1, "audio"),
		fakeStream(2, "audio"),
		fakeStream(3, "subtitle"),
	}
	sel, err := resolveStreamSelection([]StreamSelect{
		{Type: "audio", All: true},
	}, all, nil)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got, want := sel, []int{1, 2}; !equalInts(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestResolveStreamSelection_NegateRemoves(t *testing.T) {
	all := []av.StreamInfo{
		fakeStream(0, "video"),
		fakeStream(1, "subtitle"),
		fakeStream(2, "subtitle"),
	}
	sel, err := resolveStreamSelection([]StreamSelect{
		{Type: "video", All: true},
		{Type: "subtitle", All: true},
		{Type: "subtitle", Track: 0, Negate: true},
	}, all, nil)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got, want := sel, []int{0, 2}; !equalInts(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestResolveStreamSelection_OptionalSilencesMissing(t *testing.T) {
	all := []av.StreamInfo{
		fakeStream(0, "video"),
		fakeStream(1, "audio"),
	}
	sel, err := resolveStreamSelection([]StreamSelect{
		{Type: "video", Track: 0},
		{Type: "subtitle", Track: 0, Optional: true},
	}, all, nil)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got, want := sel, []int{0}; !equalInts(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestResolveStreamSelection_MissingRequiredErrors(t *testing.T) {
	all := []av.StreamInfo{fakeStream(0, "video")}
	_, err := resolveStreamSelection([]StreamSelect{
		{Type: "video", Track: 0},
		{Type: "subtitle", Track: 0},
	}, all, nil)
	if err == nil {
		t.Fatal("expected missing-stream error, got nil")
	}
	if !strings.Contains(err.Error(), "subtitle") {
		t.Errorf("error %q should mention subtitle", err)
	}
}

func TestResolveStreamSelection_ProgramFilters(t *testing.T) {
	all := []av.StreamInfo{
		fakeStream(0, "video"),
		fakeStream(1, "audio"),
		fakeStream(2, "video"),
		fakeStream(3, "audio"),
	}
	progs := []av.ProgramInfo{
		{ID: 100, StreamIndices: []int{0, 1}},
		{ID: 200, StreamIndices: []int{2, 3}},
	}
	sel, err := resolveStreamSelection([]StreamSelect{
		{Type: "video", All: true, Program: 200},
		{Type: "audio", All: true, Program: 200},
	}, all, progs)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got, want := sel, []int{2, 3}; !equalInts(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestResolveStreamSelection_TrackWithinProgram(t *testing.T) {
	all := []av.StreamInfo{
		fakeStream(0, "audio"),
		fakeStream(1, "audio"),
		fakeStream(2, "audio"),
	}
	progs := []av.ProgramInfo{
		{ID: 7, StreamIndices: []int{1, 2}},
	}
	sel, err := resolveStreamSelection([]StreamSelect{
		{Type: "audio", Track: 1, Program: 7},
	}, all, progs)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got, want := sel, []int{2}; !equalInts(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestStreamSelectValidation_NegateWithOptionalRejected(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.1",
		Inputs: []Input{{
			ID: "in", URL: "x.mp4",
			Streams: []StreamSelect{{Type: "video", All: true, Negate: true, Optional: true}},
		}},
		Outputs: []Output{{ID: "out", URL: "y.mp4"}},
		Graph:   GraphDef{Nodes: []NodeDef{}, Edges: []EdgeDef{}},
	}
	if err := validate(cfg); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected mutually-exclusive error, got %v", err)
	}
}

func TestStreamSelectValidation_BadType(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.1",
		Inputs: []Input{{
			ID: "in", URL: "x.mp4",
			Streams: []StreamSelect{{Type: "metadata", Track: 0}},
		}},
		Outputs: []Output{{ID: "out", URL: "y.mp4"}},
		Graph:   GraphDef{Nodes: []NodeDef{}, Edges: []EdgeDef{}},
	}
	if err := validate(cfg); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestResolveStreamSelection_AttachmentAllSelects(t *testing.T) {
	all := []av.StreamInfo{
		fakeStream(0, "video"),
		fakeStream(1, "attachment"),
		fakeStream(2, "attachment"),
	}
	sel, err := resolveStreamSelection([]StreamSelect{
		{Type: "video", Track: 0},
		{Type: "attachment", All: true},
	}, all, nil)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got, want := sel, []int{0, 1, 2}; !equalInts(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestStreamSelectValidation_AttachmentTypeAccepted(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.1",
		Inputs: []Input{{
			ID: "in", URL: "x.mkv",
			Streams: []StreamSelect{{Type: "attachment", All: true}},
		}},
		Outputs: []Output{{ID: "out", URL: "y.mkv"}},
		Graph:   GraphDef{Nodes: []NodeDef{}, Edges: []EdgeDef{}},
	}
	if err := validate(cfg); err != nil {
		// validation may fail for graph reasons, but NOT for "invalid type"
		if strings.Contains(err.Error(), "invalid type") {
			t.Fatalf("attachment type rejected: %v", err)
		}
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
