// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
)

// initWithTransition builds a minimal two-clip sequence_editor params object
// whose first clip carries the given transition JSON, and runs Init. The
// transition snippet is the value of the clip's "transition" key (or "" for
// no transition).
func initWithTransition(t *testing.T, transition string) error {
	t.Helper()
	trans := ""
	if transition != "" {
		trans = `, "transition": ` + transition
	}
	raw := `{
      "format": { "width": 320, "height": 240, "pix_fmt": "yuv420p", "frame_rate": 30, "time_base": [1, 90000] },
      "tracks": [
        { "id": "V1", "type": "video", "clips": [
          { "url": "a.mp4", "source_in": 0, "source_out": 3, "timeline_in": 0` + trans + ` },
          { "url": "b.mp4", "source_in": 0, "source_out": 3, "timeline_in": 3 }
        ]}
      ]
    }`
	var params map[string]any
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		t.Fatalf("test JSON invalid: %v", err)
	}
	return (&SequenceEditor{}).Init(params)
}

// An unsupported transition type must be rejected at Init (not silently
// rendered as a hard cut), pointing the user at xfade_sequence.
func TestSequenceEditor_RejectsUnsupportedTransition(t *testing.T) {
	err := initWithTransition(t, `{ "type": "notarealtransition", "duration": 0.5 }`)
	if err == nil {
		t.Fatal("expected error for unsupported transition type")
	}
	if !strings.Contains(err.Error(), "notarealtransition") {
		t.Errorf("error %q should name the bad type", err.Error())
	}
}

// A transition block without a positive duration is meaningless and must error.
func TestSequenceEditor_RejectsTransitionWithoutDuration(t *testing.T) {
	if err := initWithTransition(t, `{ "type": "dissolve" }`); err == nil {
		t.Fatal("expected error for transition with no duration")
	}
	if err := initWithTransition(t, `{ "type": "dissolve", "duration": 0 }`); err == nil {
		t.Fatal("expected error for transition with zero duration")
	}
}

// dissolve is accepted; a bare {duration} defaults to dissolve.
func TestSequenceEditor_AcceptsDissolve(t *testing.T) {
	if err := initWithTransition(t, `{ "type": "dissolve", "duration": 0.5 }`); err != nil {
		t.Fatalf("dissolve rejected: %v", err)
	}
	if err := initWithTransition(t, `{ "duration": 0.5 }`); err != nil {
		t.Fatalf("bare-duration transition rejected: %v", err)
	}
	// No transition at all is fine (hard cut between clips).
	if err := initWithTransition(t, ""); err != nil {
		t.Fatalf("no-transition sequence rejected: %v", err)
	}
}

// xfade transition names are now accepted at Init (rendered via the xfade
// graph path).
func TestSequenceEditor_AcceptsXfadeTransitions(t *testing.T) {
	for _, typ := range []string{"fade", "wipeleft", "slideright", "circleopen", "fadeblack", "hblur"} {
		if err := initWithTransition(t, `{ "type": "`+typ+`", "duration": 0.5 }`); err != nil {
			t.Errorf("xfade transition %q rejected: %v", typ, err)
		}
	}
}

// Regression: a non-dissolve transition converts two sources to the sequence
// format in the same timestep, then composites them with xfade. Each clipReader
// must own an independent scale+format converter. A single SequenceEditor-level
// converter shared across the two sources corrupted one of the two converted
// frames' chroma, producing garbage colour-difference data on the composited
// transition frame. This asserts converters are built per-source and never
// shared, so a regression back to a shared converter is caught here.
func TestSequenceEditor_PerSourceConverterIsolation(t *testing.T) {
	raw := `{
      "format": { "width": 64, "height": 64, "pix_fmt": "yuv420p", "frame_rate": 30, "time_base": [1, 90000] },
      "tracks": [
        { "id": "V1", "type": "video", "clips": [
          { "url": "a.mp4", "source_in": 0, "source_out": 3, "timeline_in": 0 },
          { "url": "b.mp4", "source_in": 0, "source_out": 3, "timeline_in": 3 }
        ]}
      ]
    }`
	var params map[string]any
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		t.Fatalf("test JSON invalid: %v", err)
	}
	se := &SequenceEditor{}
	if err := se.Init(params); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Two sources with identical StreamInfo, mirroring the two same-format clips
	// that triggered the bug. ensureReaderConverter only reads r.si, so no demux
	// or decoder is needed.
	mkReader := func(url string) *clipReader {
		return &clipReader{
			url: url,
			si: av.StreamInfo{
				Type:              av.MediaTypeVideo,
				Width:             128,
				Height:            128,
				PixFmt:            se.format.PixFmtInt,
				TimeBase:          [2]int{1, 90000},
				SampleAspectRatio: [2]int{1, 1},
			},
		}
	}
	rA := mkReader("a.mp4")
	rB := mkReader("b.mp4")
	defer rA.close()
	defer rB.close()

	se.ensureReaderConverter(rA)
	se.ensureReaderConverter(rB)

	if rA.converter == nil || rB.converter == nil {
		t.Fatalf("per-source converters not built: rA=%v rB=%v", rA.converter, rB.converter)
	}
	if rA.converter == rB.converter {
		t.Fatal("the two sources share one converter graph; a transition will corrupt chroma — each clipReader must own its converter")
	}

	// Idempotent: re-ensuring keeps the same per-source instance (no rebuild).
	prev := rA.converter
	se.ensureReaderConverter(rA)
	if rA.converter != prev {
		t.Fatal("ensureReaderConverter rebuilt an existing converter instead of reusing it")
	}
}
